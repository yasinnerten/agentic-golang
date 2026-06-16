package semanticcache

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yasinnerten/agentic-golang/services/embeddings"
)

// Cache provides exact hash + embedding similarity caching.
type Cache struct {
	db      *sql.DB
	service *embeddings.Service
}

// NewCache creates a semantic cache backed by Postgres.
func NewCache(db *sql.DB, svc *embeddings.Service) *Cache {
	return &Cache{db: db, service: svc}
}

// Lookup checks exact hash first, then semantic similarity.
func (c *Cache) Lookup(ctx context.Context, tenantID, taskType, text string, similarityThreshold float64) (*Result, error) {
	hash := hashInput(text)

	// 1. Exact match
	var cachedID, outputJSON string
	var hitCount int
	err := c.db.QueryRowContext(ctx,
		`SELECT cache_id, output_json, hit_count FROM public.semantic_cache
		WHERE tenant_id = $1 AND task_type = $2 AND input_hash = $3
		AND (expires_at IS NULL OR expires_at > NOW())`,
		tenantID, taskType, hash).Scan(&cachedID, &outputJSON, &hitCount)
	if err == nil {
		_, _ = c.db.ExecContext(ctx,
			`UPDATE public.semantic_cache SET hit_count = $1, last_hit_at = NOW() WHERE cache_id = $2`,
			hitCount+1, cachedID)
		var out any
		_ = json.Unmarshal([]byte(outputJSON), &out)
		return &Result{Matched: true, ExactHit: true, CacheID: cachedID, Output: out}, nil
	}

	// 2. Semantic similarity (if embedding service available)
	if c.service != nil {
		results, err := c.service.Search(ctx, tenantID, text, 3)
		if err == nil && len(results) > 0 {
			best := results[0]
			if best.Score >= similarityThreshold {
				// Load the actual cached output for the matched embedding
				var semOutput string
				err := c.db.QueryRowContext(ctx,
					`SELECT cache_id, output_json FROM public.semantic_cache
					WHERE tenant_id = $1 AND task_type = $2
					AND input_hash LIKE $3 || '%'
					AND (expires_at IS NULL OR expires_at > NOW())
					LIMIT 1`,
					tenantID, taskType, hash[:16]).Scan(&cachedID, &semOutput)
				if err == nil {
					var out any
					_ = json.Unmarshal([]byte(semOutput), &out)
					return &Result{Matched: true, ExactHit: false, CacheID: cachedID, Score: best.Score, Output: out}, nil
				}
			}
		}
	}

	return &Result{Matched: false}, nil
}

// Store saves a result into the semantic cache.
func (c *Cache) Store(ctx context.Context, tenantID, taskType, text string, output any, modelUsed string, ttl time.Duration) error {
	hash := hashInput(text)
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return err
	}
	var expires *time.Time
	if ttl > 0 {
		t := time.Now().Add(ttl)
		expires = &t
	}
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO public.semantic_cache (cache_id, tenant_id, task_type, input_hash, normalized_input_summary, output_json, model_used, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), $8)
		ON CONFLICT (cache_id) DO UPDATE SET
			output_json = EXCLUDED.output_json,
			normalized_input_summary = EXCLUDED.normalized_input_summary,
			expires_at = EXCLUDED.expires_at`,
		fmt.Sprintf("cache_%s_%s", tenantID, hash[:16]), tenantID, taskType, hash, truncate(text, 200), string(outputJSON), modelUsed, expires)
	return err
}

// Result is a cache lookup result.
type Result struct {
	Matched  bool    `json:"matched"`
	ExactHit bool    `json:"exact_hit"`
	CacheID  string  `json:"cache_id"`
	Score    float64 `json:"score,omitempty"`
	Output   any     `json:"output,omitempty"`
}

func hashInput(text string) string {
	h := sha256.New()
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
