package embeddings

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// VectorStore persists embeddings and runs nearest-neighbor searches.
type VectorStore interface {
	EnsureTable(ctx context.Context) error
	Insert(ctx context.Context, id, tenantID, sourceType, sourceID, model, hash string, vector []float32) error
	Search(ctx context.Context, tenantID string, vector []float32, limit int) ([]SearchResult, error)
}

// PGVectorStore provides pgvector operations for embeddings.
type PGVectorStore struct {
	db *sql.DB
}

// NewVectorStore creates a Postgres-backed vector store.
func NewVectorStore(db *sql.DB) *PGVectorStore {
	return &PGVectorStore{db: db}
}

// EnsureTable creates the embeddings table in the public schema.
func (vs *PGVectorStore) EnsureTable(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS public.embeddings (
		embedding_id TEXT PRIMARY KEY,
		tenant_id TEXT NOT NULL DEFAULT 'global',
		source_type TEXT,
		source_id TEXT,
		embedding_model TEXT,
		embedding_vector VECTOR(768),
		embedding_hash TEXT,
		created_at TIMESTAMP DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_embeddings_tenant ON public.embeddings(tenant_id);
	CREATE INDEX IF NOT EXISTS idx_embeddings_source ON public.embeddings(source_type, source_id);
	`
	_, err := vs.db.ExecContext(ctx, query)
	return err
}

// Insert stores a new embedding vector.
func (vs *PGVectorStore) Insert(ctx context.Context, id, tenantID, sourceType, sourceID, model, hash string, vector []float32) error {
	query := `
		INSERT INTO public.embeddings (embedding_id, tenant_id, source_type, source_id, embedding_model, embedding_vector, embedding_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := vs.db.ExecContext(ctx, query, id, tenantID, sourceType, sourceID, model, pgVector(vector), hash)
	return err
}

// Search finds nearest neighbors by cosine similarity.
func (vs *PGVectorStore) Search(ctx context.Context, tenantID string, vector []float32, limit int) ([]SearchResult, error) {
	query := `
		SELECT embedding_id, source_type, source_id, embedding_model, embedding_vector <=> $1 as distance
		FROM public.embeddings
		WHERE tenant_id = $2
		ORDER BY embedding_vector <=> $1
		LIMIT $3
	`
	rows, err := vs.db.QueryContext(ctx, query, pgVector(vector), tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.SourceType, &r.SourceID, &r.Model, &r.Distance); err != nil {
			return nil, err
		}
		r.Score = 1 - r.Distance
		results = append(results, r)
	}
	return results, rows.Err()
}

// SearchResult is a single similarity search result.
type SearchResult struct {
	ID         string  `json:"embedding_id"`
	SourceType string  `json:"source_type"`
	SourceID   string  `json:"source_id"`
	Model      string  `json:"embedding_model"`
	Distance   float64 `json:"distance"`
	Score      float64 `json:"score"`
}

func pgVector(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fmt.Sprintf("%f", value))
	}
	b.WriteByte(']')
	return b.String()
}
