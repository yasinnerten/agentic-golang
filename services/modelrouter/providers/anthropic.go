package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

const anthropicBaseURL = "https://api.anthropic.com"

// AnthropicProvider calls the Anthropic API.
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewAnthropicProvider creates an Anthropic provider.
func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey:  apiKey,
		baseURL: anthropicBaseURL,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (a *AnthropicProvider) Name() string     { return "anthropic" }
func (a *AnthropicProvider) Models() []string { return []string{"claude-sonnet-4", "claude-opus-4"} }

func (a *AnthropicProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.baseURL+"/v1/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
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

func (a *AnthropicProvider) Complete(ctx context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	system, messages := toAnthropicMessages(req.Messages)
	payload := map[string]any{
		"model":       req.Model,
		"messages":    messages,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}
	if system != "" {
		payload["system"] = system
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = map[string]any{"name": t.Name, "description": t.Description, "input_schema": schemaOrEmpty(t.Parameters)}
		}
		payload["tools"] = tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError("anthropic", resp)
	}

	var result struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Content) == 0 {
		return nil, fmt.Errorf("no content returned")
	}

	out := &modelrouter.LLMResponse{
		Meta: modelrouter.LLMMeta{
			Model:        req.Model,
			Provider:     a.Name(),
			InputTokens:  result.Usage.InputTokens,
			OutputTokens: result.Usage.OutputTokens,
			TotalTokens:  result.Usage.InputTokens + result.Usage.OutputTokens,
		},
	}
	var text []string
	for _, block := range result.Content {
		switch block.Type {
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, modelrouter.ToolCall{ID: block.ID, Name: block.Name, Arguments: argsOrEmpty(block.Input)})
		case "text", "":
			text = append(text, block.Text)
		}
	}
	out.Content = strings.Join(text, "")
	return out, nil
}

// toAnthropicMessages converts unified messages to the Messages API shape.
// System prompts move to the top-level "system" field (the API rejects role
// "system" in messages). Tool calls become tool_use blocks on the assistant
// turn; tool results become tool_result blocks, and consecutive results are
// grouped into one user turn as the API requires.
func toAnthropicMessages(msgs []modelrouter.Message) (string, []map[string]any) {
	var system []string
	var out []map[string]any
	for _, m := range msgs {
		switch {
		case m.Role == "system":
			system = append(system, m.Content)

		case m.Role == "tool":
			block := map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content}
			if n := len(out); n > 0 && out[n-1]["role"] == "user" {
				if blocks, ok := out[n-1]["content"].([]map[string]any); ok && isToolResults(blocks) {
					out[n-1]["content"] = append(blocks, block)
					continue
				}
			}
			out = append(out, map[string]any{"role": "user", "content": []map[string]any{block}})

		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			var blocks []map[string]any
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": argsOrEmpty(tc.Arguments)})
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})

		default:
			out = append(out, map[string]any{"role": m.Role, "content": m.Content})
		}
	}
	return strings.Join(system, "\n\n"), out
}

func isToolResults(blocks []map[string]any) bool {
	for _, b := range blocks {
		if b["type"] != "tool_result" {
			return false
		}
	}
	return true
}
