package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yasinnerten/agentic-golang/shared/types"
)

// SaveCompressedState stores a compressed snapshot for a session.
func (m *Manager) SaveCompressedState(ctx context.Context, sessionID, tenantID string, state *types.CompressedState) (string, error) {
	stateID := "state_" + sessionID + "_" + fmt.Sprintf("%d", time.Now().Unix())
	b, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	query := `INSERT INTO public.state_snapshots (state_snapshot_id, session_id, tenant_id, state_type, state_json, compression_level, token_estimate, created_at)
		VALUES ($1, $2, $3, 'compressed_state', $4, 'medium', $5, NOW())`
	// Rough token estimate: ~4 chars per token
	tokenEst := len(b) / 4
	_, err = m.db.ExecContext(ctx, query, stateID, sessionID, tenantID, b, tokenEst)
	if err != nil {
		return "", fmt.Errorf("save state snapshot: %w", err)
	}
	return stateID, nil
}

// LoadLatestCompressedState restores the most recent compressed state for a session.
func (m *Manager) LoadLatestCompressedState(ctx context.Context, sessionID string) (*types.CompressedState, error) {
	var b []byte
	err := m.db.QueryRowContext(ctx,
		`SELECT state_json FROM public.state_snapshots WHERE session_id = $1 AND state_type = 'compressed_state' ORDER BY created_at DESC LIMIT 1`,
		sessionID).Scan(&b)
	if err != nil {
		return nil, fmt.Errorf("load compressed state: %w", err)
	}
	var state types.CompressedState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
