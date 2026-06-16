package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OllamaClient calls Ollama embedding endpoints.
type OllamaClient struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllamaClient creates an Ollama embedding client.
func NewOllamaClient(baseURL, model string) *OllamaClient {
	return &OllamaClient{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (o *OllamaClient) ModelName() string {
	return o.model
}

func (o *OllamaClient) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", o.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama health status: %d", resp.StatusCode)
	}
	return nil
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"prompt"`
}

type embedResponse struct {
	Embedding []float32 `json:"embedding"`
}

func (o *OllamaClient) Generate(ctx context.Context, texts []string) ([][]float32, error) {
	var results [][]float32
	for _, text := range texts {
		body, err := json.Marshal(embedRequest{Model: o.model, Input: text})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := o.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ollama request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("ollama status %d", resp.StatusCode)
		}

		var er embedResponse
		err = json.NewDecoder(resp.Body).Decode(&er)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		results = append(results, er.Embedding)
	}
	return results, nil
}

// MultiOllama tries local then remote Ollama.
type MultiOllama struct {
	local   *OllamaClient
	remote  *OllamaClient
	primary *OllamaClient
}

// NewMultiOllama creates a multi-source Ollama client.
func NewMultiOllama(localURL, remoteURL, model string) *MultiOllama {
	return &MultiOllama{
		local:  NewOllamaClient(localURL, model),
		remote: NewOllamaClient(remoteURL, model),
	}
}

func (m *MultiOllama) ModelName() string {
	return m.primary.ModelName()
}

func (m *MultiOllama) HealthCheck(ctx context.Context) error {
	if err := m.local.HealthCheck(ctx); err == nil {
		m.primary = m.local
		return nil
	}
	if err := m.remote.HealthCheck(ctx); err == nil {
		m.primary = m.remote
		return nil
	}
	return fmt.Errorf("no ollama instance available")
}

func (m *MultiOllama) Generate(ctx context.Context, texts []string) ([][]float32, error) {
	if m.primary == nil {
		if err := m.HealthCheck(ctx); err != nil {
			return nil, err
		}
	}
	vecs, err := m.primary.Generate(ctx, texts)
	if err != nil && m.primary == m.local {
		m.primary = m.remote
		vecs, err = m.primary.Generate(ctx, texts)
	}
	return vecs, err
}
