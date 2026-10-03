package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

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
	system, contents := toGeminiContents(req.Messages)
	genConfig := map[string]any{
		"maxOutputTokens": req.MaxTokens,
		"temperature":     req.Temperature,
	}
	// Gemini does not combine a JSON response MIME type with function calling.
	if req.JSONMode && len(req.Tools) == 0 {
		genConfig["responseMimeType"] = "application/json"
	}
	payload := map[string]any{
		"contents":         contents,
		"generationConfig": genConfig,
	}
	if len(system) > 0 {
		payload["systemInstruction"] = map[string]any{"parts": system}
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			decls[i] = map[string]any{"name": t.Name, "description": t.Description, "parameters": schemaOrEmpty(t.Parameters)}
		}
		payload["tools"] = []map[string]any{{"functionDeclarations": decls}}
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
		return nil, statusError("gemini", resp)
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall"`
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

	out := &modelrouter.LLMResponse{
		Meta: modelrouter.LLMMeta{
			Model:        req.Model,
			Provider:     g.Name(),
			InputTokens:  result.UsageMetadata.PromptTokenCount,
			OutputTokens: result.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  result.UsageMetadata.TotalTokenCount,
		},
	}
	var text []string
	for _, part := range result.Candidates[0].Content.Parts {
		if fc := part.FunctionCall; fc != nil {
			// Gemini does not assign call IDs; generate them so results can be
			// matched to calls.
			out.ToolCalls = append(out.ToolCalls, modelrouter.ToolCall{
				ID: "call_" + uuid.NewString(), Name: fc.Name, Arguments: argsOrEmpty(fc.Args),
			})
			continue
		}
		text = append(text, part.Text)
	}
	out.Content = strings.Join(text, "")
	return out, nil
}

// toGeminiContents converts unified messages to Gemini contents. Gemini only
// accepts the roles "user" and "model": system prompts go to
// systemInstruction, tool calls become functionCall parts on the model turn,
// and tool results become functionResponse parts on a user turn (consecutive
// results share one turn).
func toGeminiContents(msgs []modelrouter.Message) ([]map[string]any, []map[string]any) {
	var system, contents []map[string]any
	for _, m := range msgs {
		switch {
		case m.Role == "system":
			system = append(system, map[string]any{"text": m.Content})

		case m.Role == "tool":
			part := map[string]any{"functionResponse": map[string]any{"name": m.ToolName, "response": geminiResponseObject(m.Content)}}
			if n := len(contents); n > 0 && contents[n-1]["role"] == "user" && isFunctionResponses(contents[n-1]["parts"]) {
				contents[n-1]["parts"] = append(contents[n-1]["parts"].([]map[string]any), part)
				continue
			}
			contents = append(contents, map[string]any{"role": "user", "parts": []map[string]any{part}})

		default:
			role := m.Role
			if role == "assistant" {
				role = "model"
			}
			var parts []map[string]any
			if m.Content != "" || len(m.ToolCalls) == 0 {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": tc.Name, "args": argsOrEmpty(tc.Arguments)}})
			}
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	return system, contents
}

func isFunctionResponses(v any) bool {
	parts, ok := v.([]map[string]any)
	if !ok || len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if _, ok := p["functionResponse"]; !ok {
			return false
		}
	}
	return true
}

// geminiResponseObject wraps a tool result so it is always a JSON object, as
// functionResponse.response requires.
func geminiResponseObject(content string) any {
	var obj map[string]any
	if json.Unmarshal([]byte(content), &obj) == nil && obj != nil {
		return obj
	}
	var v any
	if json.Unmarshal([]byte(content), &v) == nil {
		return map[string]any{"result": v}
	}
	return map[string]any{"result": content}
}
