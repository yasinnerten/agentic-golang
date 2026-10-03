package providers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

const openAIBaseURL = "https://api.openai.com"

// OpenAIProvider calls the OpenAI API.
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewOpenAIProvider creates an OpenAI provider.
func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: openAIBaseURL,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (o *OpenAIProvider) Name() string { return "openai" }
func (o *OpenAIProvider) Models() []string {
	return []string{"gpt-4o", "gpt-4o-mini", "gpt-4", "gpt-3.5-turbo"}
}

func (o *OpenAIProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", o.baseURL+"/v1/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (o *OpenAIProvider) Complete(ctx context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	return openAICompatComplete(ctx, o.client, o.Name(), o.baseURL+"/v1/chat/completions",
		map[string]string{"Authorization": "Bearer " + o.apiKey}, req, true)
}
