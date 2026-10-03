package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

// OllamaProvider calls local or remote Ollama instances.
type OllamaProvider struct {
	baseURL string
	name    string
	client  *http.Client
	models  []string
}

// NewOllamaProvider creates an Ollama provider.
func NewOllamaProvider(name, baseURL string, models []string) *OllamaProvider {
	return &OllamaProvider{
		baseURL: baseURL,
		name:    name,
		client:  &http.Client{Timeout: 120 * time.Second},
		models:  models,
	}
}

func (o *OllamaProvider) Name() string     { return o.name }
func (o *OllamaProvider) Models() []string { return o.models }

func (o *OllamaProvider) HealthCheck(ctx context.Context) error {
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
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (o *OllamaProvider) Complete(ctx context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	// Convert messages to Ollama chat format. Tool-call arguments are JSON
	// objects (not strings) and results are role "tool" turns naming the tool.
	messages := make([]map[string]any, len(req.Messages))
	for i, m := range req.Messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if m.Role == "tool" && m.ToolName != "" {
			msg["tool_name"] = m.ToolName
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				calls[j] = map[string]any{"function": map[string]any{"name": tc.Name, "arguments": argsOrEmpty(tc.Arguments)}}
			}
			msg["tool_calls"] = calls
		}
		messages[i] = msg
	}
	options := map[string]any{"temperature": req.Temperature}
	if req.MaxTokens > 0 {
		options["num_predict"] = req.MaxTokens
	}
	payload := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   false,
		"options":  options,
	}
	if req.JSONMode {
		// Constrain Ollama to emit valid JSON so structured node output parses.
		payload["format"] = "json"
	}
	if len(req.Tools) > 0 {
		payload["tools"] = toOpenAITools(req.Tools)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError("ollama", resp)
	}

	var result struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		PromptEvalCount int `json:"prompt_eval_count"`
		EvalCount       int `json:"eval_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	out := &modelrouter.LLMResponse{
		Content: result.Message.Content,
		Meta: modelrouter.LLMMeta{
			Model:        req.Model,
			Provider:     o.name,
			InputTokens:  result.PromptEvalCount,
			OutputTokens: result.EvalCount,
			TotalTokens:  result.PromptEvalCount + result.EvalCount,
		},
	}
	for _, tc := range result.Message.ToolCalls {
		// Ollama does not assign call IDs; generate them.
		out.ToolCalls = append(out.ToolCalls, modelrouter.ToolCall{
			ID: "call_" + uuid.NewString(), Name: tc.Function.Name, Arguments: argsOrEmpty(tc.Function.Arguments),
		})
	}
	return out, nil
}
