package sessions

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yasinnerten/agentic-golang/shared/types"
)

// Manager handles CRUD and lifecycle for agent sessions.
type Manager struct {
	db *sql.DB
}

// NewManager creates a session manager.
func NewManager(db *sql.DB) *Manager {
	return &Manager{db: db}
}

// Create inserts a new session and returns it.
func (m *Manager) Create(ctx context.Context, tenantID, sessionType, frameworkID string, parentSessionID *string) (*types.AgentSession, error) {
	s := &types.AgentSession{
		SessionID:      uuid.New().String(),
		TenantID:       tenantID,
		SessionType:    sessionType,
		FrameworkID:    frameworkID,
		Status:         "pending",
		MaxRuntimeMs:   300000,
		TimeoutMs:      30000,
		MaxAgentRuns:   50,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}
	if parentSessionID != nil {
		s.ParentSessionID = *parentSessionID
	}

	query := `INSERT INTO public.agent_sessions
		(session_id, parent_session_id, tenant_id, session_type, framework_id, status,
		 max_runtime_ms, timeout_ms, max_agent_runs, created_at, updated_at, last_activity_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	_, err := m.db.ExecContext(ctx, query, s.SessionID, nullString(s.ParentSessionID), s.TenantID,
		s.SessionType, s.FrameworkID, s.Status, s.MaxRuntimeMs, s.TimeoutMs, s.MaxAgentRuns,
		s.CreatedAt, s.UpdatedAt, s.LastActivityAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return s, nil
}

// Get loads a session by ID.
func (m *Manager) Get(ctx context.Context, sessionID string) (*types.AgentSession, error) {
	query := `SELECT session_id, COALESCE(parent_session_id,''), COALESCE(domain_id,''), framework_id, tenant_id,
		session_type, status, COALESCE(started_by_user_id,''), COALESCE(assigned_agent_id,''), COALESCE(current_workflow_id,''), COALESCE(current_node_id,''),
		COALESCE(compressed_state_id,''), raw_context_policy, max_runtime_ms, timeout_ms, max_agent_runs,
		created_at, updated_at, last_activity_at, closed_at
		FROM public.agent_sessions WHERE session_id = $1`
	var s types.AgentSession
	var closedAt sql.NullTime
	err := m.db.QueryRowContext(ctx, query, sessionID).Scan(
		&s.SessionID, &s.ParentSessionID, &s.DomainID, &s.FrameworkID, &s.TenantID,
		&s.SessionType, &s.Status, &s.StartedByUserID, &s.AssignedAgentID, &s.CurrentWorkflowID, &s.CurrentNodeID,
		&s.CompressedStateID, &s.RawContextPolicy, &s.MaxRuntimeMs, &s.TimeoutMs, &s.MaxAgentRuns,
		&s.CreatedAt, &s.UpdatedAt, &s.LastActivityAt, &closedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if closedAt.Valid {
		s.ClosedAt = &closedAt.Time
	}
	return &s, nil
}

// ListByTenant returns sessions for a tenant, optionally filtered by status.
func (m *Manager) ListByTenant(ctx context.Context, tenantID, status string) ([]types.AgentSession, error) {
	query := `SELECT session_id, COALESCE(parent_session_id,''), COALESCE(domain_id,''), framework_id, tenant_id,
		session_type, status, COALESCE(started_by_user_id,''), COALESCE(assigned_agent_id,''), COALESCE(current_workflow_id,''), COALESCE(current_node_id,''),
		COALESCE(compressed_state_id,''), raw_context_policy, max_runtime_ms, timeout_ms, max_agent_runs,
		created_at, updated_at, last_activity_at, closed_at
		FROM public.agent_sessions WHERE tenant_id = $1`
	args := []any{tenantID}
	if status != "" {
		query += " AND status = $2"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC"

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []types.AgentSession
	for rows.Next() {
		var s types.AgentSession
		var closedAt sql.NullTime
		if err := rows.Scan(
			&s.SessionID, &s.ParentSessionID, &s.DomainID, &s.FrameworkID, &s.TenantID,
			&s.SessionType, &s.Status, &s.StartedByUserID, &s.AssignedAgentID, &s.CurrentWorkflowID, &s.CurrentNodeID,
			&s.CompressedStateID, &s.RawContextPolicy, &s.MaxRuntimeMs, &s.TimeoutMs, &s.MaxAgentRuns,
			&s.CreatedAt, &s.UpdatedAt, &s.LastActivityAt, &closedAt,
		); err != nil {
			return nil, err
		}
		if closedAt.Valid {
			s.ClosedAt = &closedAt.Time
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// UpdateStatus sets the session status and bumps timestamps.
func (m *Manager) UpdateStatus(ctx context.Context, sessionID, status string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE public.agent_sessions SET status = $1, updated_at = NOW(), last_activity_at = NOW() WHERE session_id = $2`,
		status, sessionID)
	return err
}

// UpdateCurrentNode sets the current workflow node for a session.
func (m *Manager) UpdateCurrentNode(ctx context.Context, sessionID, workflowID, nodeID string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE public.agent_sessions SET current_workflow_id = $1, current_node_id = $2, updated_at = NOW(), last_activity_at = NOW() WHERE session_id = $3`,
		workflowID, nodeID, sessionID)
	return err
}

// AttachWorkflow binds a workflow to a session so the loop can load its graph.
// The current node is left empty; the loop starts from the workflow's entry node.
func (m *Manager) AttachWorkflow(ctx context.Context, sessionID, workflowID string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE public.agent_sessions SET current_workflow_id = $1, updated_at = NOW(), last_activity_at = NOW() WHERE session_id = $2`,
		workflowID, sessionID)
	return err
}

// Close marks a session as closed.
func (m *Manager) Close(ctx context.Context, sessionID string) error {
	_, err := m.db.ExecContext(ctx,
		`UPDATE public.agent_sessions SET status = 'closed', closed_at = NOW(), updated_at = NOW() WHERE session_id = $1`,
		sessionID)
	return err
}

// ListExpired returns sessions that have been inactive longer than their timeout.
func (m *Manager) ListExpired(ctx context.Context, inactiveThreshold time.Duration) ([]types.AgentSession, error) {
	cutoff := time.Now().Add(-inactiveThreshold)
	query := `SELECT session_id, COALESCE(parent_session_id,''), COALESCE(domain_id,''), framework_id, tenant_id,
		session_type, status, COALESCE(started_by_user_id,''), COALESCE(assigned_agent_id,''), COALESCE(current_workflow_id,''), COALESCE(current_node_id,''),
		COALESCE(compressed_state_id,''), raw_context_policy, max_runtime_ms, timeout_ms, max_agent_runs,
		created_at, updated_at, last_activity_at, closed_at
		FROM public.agent_sessions
		WHERE status IN ('pending', 'running') AND last_activity_at < $1`
	rows, err := m.db.QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []types.AgentSession
	for rows.Next() {
		var s types.AgentSession
		var closedAt sql.NullTime
		if err := rows.Scan(
			&s.SessionID, &s.ParentSessionID, &s.DomainID, &s.FrameworkID, &s.TenantID,
			&s.SessionType, &s.Status, &s.StartedByUserID, &s.AssignedAgentID, &s.CurrentWorkflowID, &s.CurrentNodeID,
			&s.CompressedStateID, &s.RawContextPolicy, &s.MaxRuntimeMs, &s.TimeoutMs, &s.MaxAgentRuns,
			&s.CreatedAt, &s.UpdatedAt, &s.LastActivityAt, &closedAt,
		); err != nil {
			return nil, err
		}
		if closedAt.Valid {
			s.ClosedAt = &closedAt.Time
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
