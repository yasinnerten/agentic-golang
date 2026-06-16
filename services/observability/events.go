package observability

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Service records and queries observability events.
type Service struct {
	db *sql.DB
}

// NewService creates an observability service.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// Event is a single observability record.
type Event struct {
	ObservabilityEventID string    `json:"observability_event_id"`
	SessionID            string    `json:"session_id"`
	TenantID             string    `json:"tenant_id"`
	AgentRunID           string    `json:"agent_run_id"`
	WorkflowNodeID       string    `json:"workflow_node_id"`
	ModelUsed            string    `json:"model_used"`
	InputTokens          int       `json:"input_tokens"`
	OutputTokens         int       `json:"output_tokens"`
	TotalTokens          int       `json:"total_tokens"`
	TokenCost            float64   `json:"token_cost"`
	LatencyMs            int       `json:"latency_ms"`
	RetryCount           int       `json:"retry_count"`
	CacheHit             bool      `json:"cache_hit"`
	SemanticCacheHit     bool      `json:"semantic_cache_hit"`
	Confidence           float64   `json:"confidence"`
	HallucinationRisk    float64   `json:"hallucination_risk"`
	ManualReviewRequired bool      `json:"manual_review_required"`
	UserOverrideCount    int       `json:"user_override_count"`
	EventType            string    `json:"event_type"`
	CreatedAt            time.Time `json:"created_at"`
}

// Record inserts an observability event.
func (s *Service) Record(ctx context.Context, e Event) error {
	query := `INSERT INTO public.observability_events
		(observability_event_id, session_id, tenant_id, agent_run_id, workflow_node_id,
		 model_used, input_tokens, output_tokens, total_tokens, token_cost, latency_ms, retry_count,
		 cache_hit, semantic_cache_hit, confidence, hallucination_risk, manual_review_required,
		 user_override_count, event_type, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, NOW())`
	_, err := s.db.ExecContext(ctx, query,
		e.ObservabilityEventID, e.SessionID, e.TenantID, e.AgentRunID, e.WorkflowNodeID,
		e.ModelUsed, e.InputTokens, e.OutputTokens, e.TotalTokens, e.TokenCost, e.LatencyMs, e.RetryCount,
		e.CacheHit, e.SemanticCacheHit, e.Confidence, e.HallucinationRisk, e.ManualReviewRequired,
		e.UserOverrideCount, e.EventType)
	return err
}

// List returns observability events, optionally filtered.
func (s *Service) List(ctx context.Context, tenantID, sessionID string, limit int) ([]Event, error) {
	query := `SELECT observability_event_id, session_id, tenant_id, agent_run_id, workflow_node_id,
		model_used, input_tokens, output_tokens, total_tokens, token_cost, latency_ms, retry_count,
		cache_hit, semantic_cache_hit, confidence, hallucination_risk, manual_review_required,
		user_override_count, event_type, created_at
		FROM public.observability_events WHERE 1=1`
	args := []any{}
	argIdx := 1
	if tenantID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", argIdx)
		args = append(args, tenantID)
		argIdx++
	}
	if sessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, sessionID)
		argIdx++
	}
	query += " ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var agentRunID, workflowNodeID, modelUsed sql.NullString
		var tokenCost, confidence, hallucinationRisk sql.NullFloat64
		if err := rows.Scan(&e.ObservabilityEventID, &e.SessionID, &e.TenantID, &agentRunID, &workflowNodeID,
			&modelUsed, &e.InputTokens, &e.OutputTokens, &e.TotalTokens, &tokenCost, &e.LatencyMs, &e.RetryCount,
			&e.CacheHit, &e.SemanticCacheHit, &confidence, &hallucinationRisk, &e.ManualReviewRequired,
			&e.UserOverrideCount, &e.EventType, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.AgentRunID = agentRunID.String
		e.WorkflowNodeID = workflowNodeID.String
		e.ModelUsed = modelUsed.String
		e.TokenCost = tokenCost.Float64
		e.Confidence = confidence.Float64
		e.HallucinationRisk = hallucinationRisk.Float64
		events = append(events, e)
	}
	return events, rows.Err()
}

// CalculateHallucinationProxy computes a proxy score from event metadata.
func (s *Service) CalculateHallucinationProxy(ctx context.Context, sessionID string) (float64, error) {
	events, err := s.List(ctx, "", sessionID, 100)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	var score float64
	for _, e := range events {
		if e.ManualReviewRequired {
			score += 0.2
		}
		if e.HallucinationRisk > 0 {
			score += e.HallucinationRisk * 0.3
		}
		if e.RetryCount > 2 {
			score += 0.1
		}
	}
	avg := score / float64(len(events))
	if avg > 1 {
		avg = 1
	}
	return avg, nil
}

// RecordNodeExecution is called by the loopcontroller after each node runs.
// It extracts token counts and model info from the node output (if available)
// and writes a single observability event.
func (s *Service) RecordNodeExecution(ctx context.Context, sessionID, tenantID, loopRunID, nodeID, agentType, model string, latencyMs int) {
	eventID := uuid.New().String()
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO public.observability_events
		(observability_event_id, session_id, tenant_id, agent_run_id, workflow_node_id,
		 model_used, latency_ms, event_type, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'node_executed', NOW())`,
		eventID, sessionID, tenantID, loopRunID, nodeID, model, latencyMs)
}
