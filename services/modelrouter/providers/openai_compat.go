package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

// The OpenAI chat-completions wire format, shared by OpenAI and Azure OpenAI.

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"` // null for an assistant turn that only calls tools
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // a JSON object encoded as a string
	} `json:"function"`
}

func toOpenAIMessages(msgs []modelrouter.Message) []oaMessage {
	out := make([]oaMessage, 0, len(msgs))
	for _, m := range msgs {
		content := m.Content
		om := oaMessage{Role: m.Role, Content: &content, ToolCallID: m.ToolCallID}
		if len(m.ToolCalls) > 0 {
			if content == "" {
				om.Content = nil
			}
			for _, tc := range m.ToolCalls {
				var c oaToolCall
				c.ID, c.Type = tc.ID, "function"
				c.Function.Name = tc.Name
				c.Function.Arguments = string(argsOrEmpty(tc.Arguments))
				om.ToolCalls = append(om.ToolCalls, c)
			}
		}
		out = append(out, om)
	}
	return out
}

func toOpenAITools(specs []modelrouter.ToolSpec) []map[string]any {
	out := make([]map[string]any, len(specs))
	for i, s := range specs {
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        s.Name,
				"description": s.Description,
				"parameters":  schemaOrEmpty(s.Parameters),
			},
		}
	}
	return out
}

// openAICompatComplete sends a chat-completions request to url and decodes the
// reply. Model goes in the body unless the provider routes by URL (Azure).
func openAICompatComplete(ctx context.Context, client *http.Client, provider, url string, headers map[string]string, req modelrouter.LLMRequest, modelInBody bool) (*modelrouter.LLMResponse, error) {
	payload := map[string]any{
		"messages":    toOpenAIMessages(req.Messages),
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}
	if modelInBody {
		payload["model"] = req.Model
	}
	if len(req.Tools) > 0 {
		payload["tools"] = toOpenAITools(req.Tools)
	}
	if req.JSONMode {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(provider, resp)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content   *string      `json:"content"`
				ToolCalls []oaToolCall `json:"tool_calls"`
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

	msg := result.Choices[0].Message
	out := &modelrouter.LLMResponse{
		Meta: modelrouter.LLMMeta{
			Model:        req.Model,
			Provider:     provider,
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
			TotalTokens:  result.Usage.TotalTokens,
		},
	}
	if msg.Content != nil {
		out.Content = *msg.Content
	}
	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, modelrouter.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: argsOrEmpty(json.RawMessage(tc.Function.Arguments)),
		})
	}
	return out, nil
}

// statusError reports a non-200 reply with the start of its body, which
// usually says what was wrong with the request.
func statusError(provider string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if len(snippet) == 0 {
		return fmt.Errorf("%s status %d", provider, resp.StatusCode)
	}
	return fmt.Errorf("%s status %d: %s", provider, resp.StatusCode, bytes.TrimSpace(snippet))
}

// argsOrEmpty returns raw, or an empty JSON object when raw is empty or not
// valid JSON. Tool arguments must always be an object on the wire.
func argsOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || !json.Valid(raw) {
		return json.RawMessage(`{}`)
	}
	return raw
}

// schemaOrEmpty returns the tool's parameter schema, or an empty object schema
// for tools that take no arguments.
func schemaOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return raw
}
