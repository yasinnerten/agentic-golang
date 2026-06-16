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

// AzureOpenAIProvider calls Azure OpenAI.
type AzureOpenAIProvider struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

// NewAzureOpenAIProvider creates an Azure OpenAI provider.
func NewAzureOpenAIProvider(endpoint, apiKey string) *AzureOpenAIProvider {
	return &AzureOpenAIProvider{
		endpoint: endpoint,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (a *AzureOpenAIProvider) Name() string     { return "azure_openai" }
func (a *AzureOpenAIProvider) Models() []string { return []string{"gpt-4o", "gpt-4o-mini"} }

func (a *AzureOpenAIProvider) HealthCheck(ctx context.Context) error {
	if a.endpoint == "" {
		return fmt.Errorf("no endpoint configured")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.endpoint+"/openai/models?api-version=2024-06-01", nil)
	if err != nil {
		return err
	}
	req.Header.Set("api-key", a.apiKey)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (a *AzureOpenAIProvider) Complete(ctx context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	messages := make([]map[string]string, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = map[string]string{"role": m.Role, "content": m.Content}
	}
	body, err := json.Marshal(map[string]any{
		"messages":    messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=2024-06-01", a.endpoint, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("api-key", a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	return &modelrouter.LLMResponse{
		Content: result.Choices[0].Message.Content,
		Meta: modelrouter.LLMMeta{
			Model:        req.Model,
			Provider:     a.Name(),
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
			TotalTokens:  result.Usage.TotalTokens,
		},
	}, nil
}
