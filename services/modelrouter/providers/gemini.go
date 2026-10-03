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

const geminiBaseURL = "https://generativelanguage.googleapis.com"

// GeminiProvider calls the Google Gemini API.
type GeminiProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewGeminiProvider creates a Gemini provider.
func NewGeminiProvider(apiKey string) *GeminiProvider {
	return &GeminiProvider{
		apiKey:  apiKey,
		baseURL: geminiBaseURL,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (g *GeminiProvider) Name() string     { return "gemini" }
func (g *GeminiProvider) Models() []string { return []string{"gemini-2.5-flash", "gemini-pro"} }

func (g *GeminiProvider) HealthCheck(ctx context.Context) error {
	// The key goes in a header, not the query string: http.Client errors embed
	// the full request URL, which would leak the key into logs.
	req, err := http.NewRequestWithContext(ctx, "GET", g.baseURL+"/v1beta/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-api-key", g.apiKey)
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
	// Gemini only accepts the roles "user" and "model" in contents; system
	// prompts go in systemInstruction.
	var system []map[string]string
	contents := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			system = append(system, map[string]string{"text": m.Content})
			continue
		case "assistant":
			m.Role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  m.Role,
			"parts": []map[string]string{{"text": m.Content}},
		})
	}
	payload := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": req.MaxTokens,
			"temperature":     req.Temperature,
		},
	}
	if len(system) > 0 {
		payload["systemInstruction"] = map[string]any{"parts": system}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", g.baseURL, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", g.apiKey)

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
