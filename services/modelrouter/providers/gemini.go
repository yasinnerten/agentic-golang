package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

// GeminiProvider calls the Google Gemini API.
type GeminiProvider struct {
	apiKey string
	client *http.Client
}

// NewGeminiProvider creates a Gemini provider.
func NewGeminiProvider(apiKey string) *GeminiProvider {
	return &GeminiProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (g *GeminiProvider) Name() string     { return "gemini" }
func (g *GeminiProvider) Models() []string { return []string{"gemini-2.5-flash", "gemini-pro"} }

func (g *GeminiProvider) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (g *GeminiProvider) Complete(ctx context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	contents := make([]map[string]any, len(req.Messages))
	for i, m := range req.Messages {
		contents[i] = map[string]any{
			"role":  m.Role,
			"parts": []map[string]string{{"text": m.Content}},
		}
	}
	body, err := json.Marshal(map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": req.MaxTokens,
			"temperature":     req.Temperature,
		},
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", req.Model, g.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini status %d", resp.StatusCode)
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no candidates returned")
	}

	return &modelrouter.LLMResponse{
		Content: result.Candidates[0].Content.Parts[0].Text,
		Meta: modelrouter.LLMMeta{
			Model:        req.Model,
			Provider:     g.Name(),
			InputTokens:  result.UsageMetadata.PromptTokenCount,
			OutputTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  result.UsageMetadata.TotalTokenCount,
		},
	}, nil
}
