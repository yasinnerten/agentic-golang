package reviews

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Review struct {
	ReviewID            string          `json:"review_id"`
	TenantID            string          `json:"tenant_id,omitempty"`
	SessionID           string          `json:"session_id,omitempty"`
	TaskID              string          `json:"task_id,omitempty"`
	ChecklistItemID     string          `json:"checklist_item_id,omitempty"`
	ReviewType          string          `json:"review_type"`
	Status              string          `json:"status"`
	Priority            string          `json:"priority"`
	ContextJSON         json.RawMessage `json:"context_json,omitempty"`
	AgentRecommendation string          `json:"agent_recommendation,omitempty"`
	AssignedTo          string          `json:"assigned_to,omitempty"`
	ResolvedBy          string          `json:"resolved_by,omitempty"`
	ResolutionJSON      json.RawMessage `json:"resolution_json,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	ResolvedAt          *time.Time      `json:"resolved_at,omitempty"`
}

type CreateReviewInput struct {
	TenantID            string          `json:"tenant_id,omitempty"`
	SessionID           string          `json:"session_id,omitempty"`
	TaskID              string          `json:"task_id,omitempty"`
	ChecklistItemID     string          `json:"checklist_item_id,omitempty"`
	ReviewType          string          `json:"review_type"`
	Priority            string          `json:"priority,omitempty"`
	ContextJSON         json.RawMessage `json:"context_json,omitempty"`
	AgentRecommendation string          `json:"agent_recommendation,omitempty"`
}

type Manager struct {
	db *sql.DB
}

func NewManager(db *sql.DB) *Manager {
	return &Manager{db: db}
}

func (m *Manager) EnsureTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS public.human_reviews (
			review_id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT '',
			session_id TEXT,
			task_id TEXT,
			checklist_item_id TEXT,
			review_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			priority TEXT NOT NULL DEFAULT 'medium',
			context_json JSONB,
			agent_recommendation TEXT,
			assigned_to TEXT,
			resolved_by TEXT,
			resolution_json JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			resolved_at TIMESTAMP
		)
	`)
	return err
}

func (m *Manager) Create(ctx context.Context, input CreateReviewInput) (*Review, error) {
	r := &Review{
		ReviewID:            "rev_" + uuid.New().String()[:20],
		TenantID:            input.TenantID,
		SessionID:           input.SessionID,
		TaskID:              input.TaskID,
		ChecklistItemID:     input.ChecklistItemID,
		ReviewType:          input.ReviewType,
		Status:              "pending",
		Priority:            input.Priority,
		ContextJSON:         input.ContextJSON,
		AgentRecommendation: input.AgentRecommendation,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if r.Priority == "" {
		r.Priority = "medium"
	}

	_, err := m.db.ExecContext(ctx, `
		INSERT INTO public.human_reviews (review_id, tenant_id, session_id, task_id, checklist_item_id, review_type, status, priority, context_json, agent_recommendation, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, r.ReviewID, r.TenantID, r.SessionID, r.TaskID, r.ChecklistItemID,
		r.ReviewType, r.Status, r.Priority, r.ContextJSON, r.AgentRecommendation,
		r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert review: %w", err)
	}
	return r, nil
}

func (m *Manager) List(ctx context.Context, tenantID, status string) ([]Review, error) {
	query := `SELECT review_id, tenant_id, session_id, task_id, checklist_item_id, review_type, status, priority, context_json, agent_recommendation, assigned_to, resolved_by, resolution_json, created_at, updated_at, resolved_at FROM public.human_reviews WHERE 1=1`
	args := []any{}
	argN := 1
	if tenantID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", argN)
		args = append(args, tenantID)
		argN++
	}
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, status)
		argN++
	}
	query += " ORDER BY created_at DESC LIMIT 100"

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []Review
	for rows.Next() {
		var r Review
		var ctxJSON, resJSON []byte
		if err := rows.Scan(&r.ReviewID, &r.TenantID, &r.SessionID, &r.TaskID, &r.ChecklistItemID,
			&r.ReviewType, &r.Status, &r.Priority, &ctxJSON, &r.AgentRecommendation,
			&r.AssignedTo, &r.ResolvedBy, &resJSON, &r.CreatedAt, &r.UpdatedAt, &r.ResolvedAt); err != nil {
			continue
		}
		r.ContextJSON = ctxJSON
		r.ResolutionJSON = resJSON
		reviews = append(reviews, r)
	}
	return reviews, nil
}

func (m *Manager) Get(ctx context.Context, reviewID string) (*Review, error) {
	var r Review
	var ctxJSON, resJSON []byte
	err := m.db.QueryRowContext(ctx, `
		SELECT review_id, tenant_id, session_id, task_id, checklist_item_id, review_type, status, priority, context_json, agent_recommendation, assigned_to, resolved_by, resolution_json, created_at, updated_at, resolved_at
		FROM public.human_reviews WHERE review_id = $1
	`, reviewID).Scan(&r.ReviewID, &r.TenantID, &r.SessionID, &r.TaskID, &r.ChecklistItemID,
		&r.ReviewType, &r.Status, &r.Priority, &ctxJSON, &r.AgentRecommendation,
		&r.AssignedTo, &r.ResolvedBy, &resJSON, &r.CreatedAt, &r.UpdatedAt, &r.ResolvedAt)
	if err != nil {
		return nil, err
	}
	r.ContextJSON = ctxJSON
	r.ResolutionJSON = resJSON
	return &r, nil
}

func (m *Manager) Assign(ctx context.Context, reviewID, assignedTo string) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE public.human_reviews SET assigned_to = $1, status = 'assigned', updated_at = NOW() WHERE review_id = $2
	`, assignedTo, reviewID)
	return err
}

func (m *Manager) Resolve(ctx context.Context, reviewID, resolvedBy string, resolution json.RawMessage) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE public.human_reviews SET status = 'resolved', resolved_by = $1, resolution_json = $2, resolved_at = NOW(), updated_at = NOW() WHERE review_id = $3
	`, resolvedBy, resolution, reviewID)
	return err
}

func (m *Manager) Dismiss(ctx context.Context, reviewID string) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE public.human_reviews SET status = 'dismissed', resolved_at = NOW(), updated_at = NOW() WHERE review_id = $1
	`, reviewID)
	return err
}
