package rag

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yasinnerten/agentic-golang/services/embeddings"
)

type DocumentRecord struct {
	DocumentID  string    `json:"document_id"`
	TenantID    string    `json:"tenant_id"`
	Filename    string    `json:"filename"`
	ContentHash string    `json:"content_hash"`
	ChunkCount  int       `json:"chunk_count"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type SearchResult struct {
	DocumentID string  `json:"document_id"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	Model      string  `json:"model"`
}

type Embedder interface {
	Generate(ctx context.Context, texts []string) ([][]float32, error)
	ModelName() string
}

type Service struct {
	db      *sql.DB
	store   embeddings.VectorStore
	embed   Embedder
	chunker *Chunker
}

func NewService(database *sql.DB, store embeddings.VectorStore, embed Embedder) *Service {
	return &Service{
		db:      database,
		store:   store,
		embed:   embed,
		chunker: NewChunker(500, 100),
	}
}

func (s *Service) EnsureTables(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS public.rag_documents (
			document_id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			filename TEXT,
			content_hash TEXT,
			chunk_count INTEGER DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			framework TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_rag_docs_tenant ON public.rag_documents(tenant_id);
		CREATE INDEX IF NOT EXISTS idx_rag_docs_status ON public.rag_documents(status);

		CREATE TABLE IF NOT EXISTS public.rag_chunks (
			chunk_id TEXT PRIMARY KEY,
			document_id TEXT NOT NULL REFERENCES public.rag_documents(document_id) ON DELETE CASCADE,
			tenant_id TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			content TEXT NOT NULL,
			content_hash TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_rag_chunks_doc ON public.rag_chunks(document_id);
		CREATE INDEX IF NOT EXISTS idx_rag_chunks_tenant ON public.rag_chunks(tenant_id);
	`)
	if err != nil {
		return fmt.Errorf("create rag tables: %w", err)
	}
	return s.store.EnsureTable(ctx)
}

func (s *Service) Ingest(ctx context.Context, tenantID, filename, content, framework string) (*DocumentRecord, error) {
	contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	docID := "doc_" + uuid.New().String()[:20]

	chunks := s.chunker.ChunkText(content)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no content to ingest")
	}

	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO public.rag_documents (document_id, tenant_id, filename, content_hash, chunk_count, status, framework, created_at)
		VALUES ($1, $2, $3, $4, $5, 'processing', $6, $7)
	`, docID, tenantID, filename, contentHash, len(chunks), framework, now)
	if err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}

	batchSize := 10
	for batchStart := 0; batchStart < len(chunks); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(chunks) {
			batchEnd = len(chunks)
		}
		batch := chunks[batchStart:batchEnd]

		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Content
		}

		vecs, err := s.embed.Generate(ctx, texts)
		if err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE public.rag_documents SET status = 'failed' WHERE document_id = $1`, docID)
			return nil, fmt.Errorf("generate embeddings: %w", err)
		}

		for i, c := range batch {
			if i >= len(vecs) {
				break
			}
			chunkID := fmt.Sprintf("chk_%s_%d", docID, c.Index)
			chunkHash := fmt.Sprintf("%x", sha256.Sum256([]byte(c.Content)))

			_, err := s.db.ExecContext(ctx, `
				INSERT INTO public.rag_chunks (chunk_id, document_id, tenant_id, chunk_index, content, content_hash, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, chunkID, docID, tenantID, c.Index, c.Content, chunkHash, now)
			if err != nil {
				continue
			}

			_ = s.store.Insert(ctx, chunkID, tenantID, "rag_chunk", chunkID, s.embed.ModelName(), chunkHash, vecs[i])
		}
	}

	_, err = s.db.ExecContext(ctx, `UPDATE public.rag_documents SET status = 'indexed' WHERE document_id = $1`, docID)
	if err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}

	return &DocumentRecord{
		DocumentID:  docID,
		TenantID:    tenantID,
		Filename:    filename,
		ContentHash: contentHash,
		ChunkCount:  len(chunks),
		Status:      "indexed",
		CreatedAt:   now,
	}, nil
}

func (s *Service) Search(ctx context.Context, tenantID, query string, topK int, framework string) ([]SearchResult, error) {
	if topK <= 0 {
		topK = 10
	}

	vecs, err := s.embed.Generate(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no query embedding generated")
	}

	results, err := s.store.Search(ctx, tenantID, vecs[0], topK)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	var searchResults []SearchResult
	for _, r := range results {
		if r.SourceType != "rag_chunk" {
			continue
		}
		var content string
		var chunkIndex int
		var docID string
		err := s.db.QueryRowContext(ctx, `
			SELECT content, chunk_index, document_id FROM public.rag_chunks WHERE chunk_id = $1
		`, r.SourceID).Scan(&content, &chunkIndex, &docID)
		if err != nil {
			continue
		}
		searchResults = append(searchResults, SearchResult{
			DocumentID: docID,
			ChunkIndex: chunkIndex,
			Content:    content,
			Score:      r.Score,
			Model:      r.Model,
		})
	}

	return searchResults, nil
}

func (s *Service) Status(ctx context.Context) (map[string]any, error) {
	var docCount, chunkCount int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM public.rag_documents WHERE status = 'indexed'`).Scan(&docCount)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM public.rag_chunks`).Scan(&chunkCount)

	return map[string]any{
		"status":            "operational",
		"documents_indexed": docCount,
		"chunks_indexed":    chunkCount,
		"last_updated":      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (s *Service) ListDocuments(ctx context.Context, tenantID string) ([]DocumentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT document_id, tenant_id, filename, content_hash, chunk_count, status, created_at
		FROM public.rag_documents
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []DocumentRecord
	for rows.Next() {
		var d DocumentRecord
		if err := rows.Scan(&d.DocumentID, &d.TenantID, &d.Filename, &d.ContentHash, &d.ChunkCount, &d.Status, &d.CreatedAt); err != nil {
			continue
		}
		docs = append(docs, d)
	}
	return docs, nil
}

func (s *Service) DeleteDocument(ctx context.Context, tenantID, documentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM public.rag_documents WHERE document_id = $1 AND tenant_id = $2`, documentID, tenantID)
	return err
}

func (s *Service) IngestFile(ctx context.Context, tenantID, filename string, data []byte, framework string) (*DocumentRecord, *ExtractedContent, error) {
	ext := ""
	if idx := strings.LastIndex(filename, "."); idx >= 0 {
		ext = filename[idx:]
	}
	extracted, err := ExtractFromBytes(data, ext)
	if err != nil {
		return nil, nil, fmt.Errorf("extract text: %w", err)
	}
	if strings.TrimSpace(extracted.Text) == "" {
		return nil, extracted, fmt.Errorf("no extractable text content")
	}
	doc, err := s.Ingest(ctx, tenantID, filename, extracted.Text, framework)
	if err != nil {
		return nil, extracted, err
	}
	return doc, extracted, nil
}

func (s *Service) AnalyzeWithLLM(ctx context.Context, tenantID, query string, topK int, framework string, llmComplete func(ctx context.Context, prompt string) (string, error)) (map[string]any, error) {
	results, err := s.Search(ctx, tenantID, query, topK, framework)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	var sources []map[string]any
	var contextBuilder strings.Builder
	for i, r := range results {
		sources = append(sources, map[string]any{
			"documentId": r.DocumentID,
			"chunkIndex": r.ChunkIndex,
			"excerpt":    truncate(r.Content, 200),
			"score":      r.Score,
		})
		contextBuilder.WriteString(fmt.Sprintf("[Source %d] (score: %.2f)\n%s\n\n", i+1, r.Score, r.Content))
	}
	if sources == nil {
		sources = []map[string]any{}
	}

	analysis := "No relevant documents found for analysis."
	if len(results) > 0 && llmComplete != nil {
		prompt := fmt.Sprintf(`Based on the following retrieved document excerpts, provide a concise analysis answering the user's question.

Question: %s

Retrieved context:
%s

Provide a clear, well-structured analysis. Cite specific sources where relevant.`, query, contextBuilder.String())
		if llmResp, err := llmComplete(ctx, prompt); err == nil && llmResp != "" {
			analysis = llmResp
		}
	}

	return map[string]any{
		"analysis":        analysis,
		"sources":         sources,
		"sourceCount":     len(sources),
		"recommendations": []any{},
	}, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
