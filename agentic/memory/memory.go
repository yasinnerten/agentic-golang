package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yasinnerten/agentic-golang/shared/types"
)

// MemoryType categorizes memory items.
type MemoryType string

const (
	MemoryShortTerm       MemoryType = "short_term_execution_memory"
	MemorySemantic        MemoryType = "semantic_memory"
	MemoryDeterministic   MemoryType = "deterministic_memory"
	MemoryCompressedState MemoryType = "compressed_state_memory"
	MemoryHumanOverride   MemoryType = "human_override_memory"
	MemorySession         MemoryType = "session_memory"
)

// Store persists and retrieves agent memory items.
type Store struct {
	db *sql.DB
}

// NewStore creates a memory store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Put inserts a memory item.
func (s *Store) Put(ctx context.Context, tenantID, sessionID, agentInstanceID string, memType MemoryType, key string, value any, sourceType, sourceID string, confidence float64, ttl time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var expires *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		expires = &t
	}
	// Supersede any prior current value for this (session, type, key) so a
	// re-written key (e.g. a re-executed loop node) versions cleanly instead of
	// colliding on a deterministic primary key. is_current then points only at
	// the latest write, matching what Get/Query filter on.
	if _, err = s.db.ExecContext(ctx,
		`UPDATE public.agent_memory_items SET is_current = false, updated_at = NOW()
		WHERE session_id = $1 AND memory_type = $2 AND key = $3 AND is_current = true`,
		sessionID, string(memType), key); err != nil {
		return fmt.Errorf("supersede prior memory: %w", err)
	}
	query := `INSERT INTO public.agent_memory_items
		(memory_item_id, session_id, agent_instance_id, tenant_id, memory_type, key, value_json, value_text, source_type, source_id, confidence, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())`
	id := "mem_" + uuid.New().String()
	_, err = s.db.ExecContext(ctx, query, id, sessionID, agentInstanceID, tenantID, string(memType), key, b, string(b), sourceType, sourceID, confidence, expires)
	return err
}

// Get loads a single memory item by session + key + type.
func (s *Store) Get(ctx context.Context, sessionID string, memType MemoryType, key string) (any, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT value_json FROM public.agent_memory_items
		WHERE session_id = $1 AND memory_type = $2 AND key = $3 AND is_current = true
		AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC LIMIT 1`,
		sessionID, string(memType), key).Scan(&b)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Query searches memory items scoped to a session, assessment, or tenant.
func (s *Store) Query(ctx context.Context, scope types.MemoryScope) ([]types.MemoryItem, error) {
	query := `SELECT memory_item_id, session_id, agent_instance_id, tenant_id, memory_type, key,
		value_json, source_type, source_id, confidence, expires_at, created_at
		FROM public.agent_memory_items
		WHERE is_current = true AND (expires_at IS NULL OR expires_at > NOW())`
	args := []any{}
	argIdx := 1

	if scope.SessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, scope.SessionID)
		argIdx++
	}
	if scope.TenantID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", argIdx)
		args = append(args, scope.TenantID)
		argIdx++
	}
	if scope.MemoryType != "" {
		query += fmt.Sprintf(" AND memory_type = $%d", argIdx)
		args = append(args, string(scope.MemoryType))
		argIdx++
	}
	query += " ORDER BY created_at DESC"
	if scope.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, scope.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []types.MemoryItem
	for rows.Next() {
		var mi types.MemoryItem
		var expires sql.NullTime
		if err := rows.Scan(&mi.MemoryItemID, &mi.SessionID, &mi.AgentInstanceID, &mi.TenantID, &mi.MemoryType, &mi.Key,
			&mi.ValueJSON, &mi.SourceType, &mi.SourceID, &mi.Confidence, &expires, &mi.CreatedAt); err != nil {
			return nil, err
		}
		if expires.Valid {
			mi.ExpiresAt = &expires.Time
		}
		items = append(items, mi)
	}
	return items, rows.Err()
}

// Invalidate marks memory items as not current.
func (s *Store) Invalidate(ctx context.Context, sessionID string, memType MemoryType, key string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE public.agent_memory_items SET is_current = false, updated_at = NOW()
		WHERE session_id = $1 AND memory_type = $2 AND key = $3`,
		sessionID, string(memType), key)
	return err
}
