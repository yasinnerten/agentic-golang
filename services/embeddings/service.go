package embeddings

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// Service handles vector generation and storage.
type Service struct {
	generator Generator
	store     VectorStore
}

// Generator produces float32 vectors from text.
type Generator interface {
	Generate(ctx context.Context, texts []string) ([][]float32, error)
	ModelName() string
	HealthCheck(ctx context.Context) error
}

// NewService creates an embedding service.
func NewService(gen Generator, store VectorStore) *Service {
	return &Service{generator: gen, store: store}
}

// Embed generates and stores an embedding.
func (s *Service) Embed(ctx context.Context, tenantID, sourceType, sourceID string, text string) (string, error) {
	vecs, err := s.generator.Generate(ctx, []string{text})
	if err != nil {
		return "", fmt.Errorf("generate embedding: %w", err)
	}
	if len(vecs) == 0 {
		return "", fmt.Errorf("no embedding generated")
	}

	id := fmt.Sprintf("emb_%s_%s_%s", tenantID, sourceType, sourceID)
	hash := hashText(text)

	if err := s.store.Insert(ctx, id, tenantID, sourceType, sourceID, s.generator.ModelName(), hash, vecs[0]); err != nil {
		return "", fmt.Errorf("store embedding: %w", err)
	}
	return id, nil
}

// Search finds similar embeddings.
func (s *Service) Search(ctx context.Context, tenantID, text string, limit int) ([]SearchResult, error) {
	vecs, err := s.generator.Generate(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no query embedding generated")
	}
	return s.store.Search(ctx, tenantID, vecs[0], limit)
}

func hashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h[:])
}
