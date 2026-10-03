package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

var chatRequest = modelrouter.LLMRequest{
	Model: "test-model",
	Messages: []modelrouter.Message{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "again"},
	},
	MaxTokens:   64,
	Temperature: 0.1,
}

// captureServer records the decoded JSON body and headers of the last request
// and replies with the given canned response.
func captureServer(t *testing.T, reply string, body *map[string]any, hdr *http.Header, path *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hdr = r.Header.Clone()
		*path = r.URL.String()
		if r.Body != nil && r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
		}
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAnthropicSendsSystemAsTopLevelField(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var path string
	srv := captureServer(t, `{"content":[{"text":"ok"}],"usage":{"input_tokens":3,"output_tokens":2}}`, &body, &hdr, &path)

	p := NewAnthropicProvider("sk-test")
	p.baseURL = srv.URL
	resp, err := p.Complete(context.Background(), chatRequest)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "ok" || resp.Meta.TotalTokens != 5 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if body["system"] != "be terse" {
		t.Fatalf("system = %v, want %q", body["system"], "be terse")
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 non-system messages, got %d: %v", len(msgs), msgs)
	}
	for _, m := range msgs {
		if role := m.(map[string]any)["role"]; role == "system" {
			t.Fatalf("system role leaked into messages: %v", msgs)
		}
	}
	if body["temperature"] != 0.1 {
		t.Fatalf("temperature = %v, want 0.1", body["temperature"])
	}
	if hdr.Get("x-api-key") != "sk-test" {
		t.Fatalf("missing x-api-key header")
	}
}

func TestAnthropicOmitsSystemWhenAbsent(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var path string
	srv := captureServer(t, `{"content":[{"text":"ok"}]}`, &body, &hdr, &path)

	p := NewAnthropicProvider("sk-test")
	p.baseURL = srv.URL
	_, err := p.Complete(context.Background(), modelrouter.LLMRequest{
		Model:    "test-model",
		Messages: []modelrouter.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := body["system"]; ok {
		t.Fatalf("system field should be omitted, got %v", body["system"])
	}
}

func TestGeminiMapsRolesAndSystemInstruction(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var path string
	srv := captureServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`, &body, &hdr, &path)

	p := NewGeminiProvider("g-secret")
	p.baseURL = srv.URL
	resp, err := p.Complete(context.Background(), chatRequest)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "ok" || resp.Meta.TotalTokens != 5 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	si, ok := body["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("missing systemInstruction: %v", body)
	}
	if text := si["parts"].([]any)[0].(map[string]any)["text"]; text != "be terse" {
		t.Fatalf("systemInstruction text = %v", text)
	}

	var roles []string
	for _, c := range body["contents"].([]any) {
		roles = append(roles, c.(map[string]any)["role"].(string))
	}
	if got := strings.Join(roles, ","); got != "user,model,user" {
		t.Fatalf("roles = %s, want user,model,user", got)
	}
}

func TestGeminiKeepsKeyOutOfURL(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var path string
	srv := captureServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`, &body, &hdr, &path)

	p := NewGeminiProvider("g-secret")
	p.baseURL = srv.URL

	if _, err := p.Complete(context.Background(), chatRequest); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.Contains(path, "g-secret") {
		t.Fatalf("API key leaked into request URL: %s", path)
	}
	if hdr.Get("x-goog-api-key") != "g-secret" {
		t.Fatalf("missing x-goog-api-key header")
	}

	if err := p.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if strings.Contains(path, "g-secret") {
		t.Fatalf("API key leaked into health-check URL: %s", path)
	}

	// Transport errors include the request URL; the key must not appear there.
	p.baseURL = "http://127.0.0.1:1"
	_, err := p.Complete(context.Background(), chatRequest)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "g-secret") {
		t.Fatalf("API key leaked into error: %v", err)
	}
}

func captureServerStatus(t *testing.T, status int, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}
