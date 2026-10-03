package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

// toolConversation is a full tool round trip: the model asked for two tools in
// parallel and both results are being sent back.
var toolConversation = modelrouter.LLMRequest{
	Model: "m",
	Messages: []modelrouter.Message{
		{Role: "system", Content: "use tools"},
		{Role: "user", Content: "weather in Paris and Rome?"},
		{Role: "assistant", ToolCalls: []modelrouter.ToolCall{
			{ID: "c1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Paris"}`)},
			{ID: "c2", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Rome"}`)},
		}},
		{Role: "tool", ToolCallID: "c1", ToolName: "get_weather", Content: `{"temp_c":18}`},
		{Role: "tool", ToolCallID: "c2", ToolName: "get_weather", Content: `"sunny"`},
	},
	Tools: []modelrouter.ToolSpec{{
		Name:        "get_weather",
		Description: "Current weather for a city",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
	}},
	MaxTokens: 100,
}

// path walks decoded JSON: strings index objects, ints index arrays.
func path(t *testing.T, v any, keys ...any) any {
	t.Helper()
	for _, k := range keys {
		switch k := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("expected object at %v, got %T", k, v)
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				t.Fatalf("expected array with index %d, got %v", k, v)
			}
			v = a[k]
		}
	}
	return v
}

func assertToolCall(t *testing.T, resp *modelrouter.LLMResponse, name, args string) {
	t.Helper()
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %+v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.Name != name || tc.ID == "" {
		t.Fatalf("unexpected tool call %+v", tc)
	}
	var got, want any
	_ = json.Unmarshal(tc.Arguments, &got)
	_ = json.Unmarshal([]byte(args), &want)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("arguments = %s, want %s", tc.Arguments, args)
	}
}

func TestOpenAIToolCalling(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var url string
	srv := captureServer(t, `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_9","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}}]}}]}`, &body, &hdr, &url)

	p := NewOpenAIProvider("k")
	p.baseURL = srv.URL
	req := toolConversation
	req.JSONMode = true
	resp, err := p.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertToolCall(t, resp, "get_weather", `{"city":"Oslo"}`)
	if resp.ToolCalls[0].ID != "call_9" {
		t.Fatalf("provider call ID not kept: %+v", resp.ToolCalls[0])
	}

	if path(t, body, "tools", 0, "function", "name") != "get_weather" {
		t.Fatalf("tools not encoded: %v", body["tools"])
	}
	if path(t, body, "response_format", "type") != "json_object" {
		t.Fatalf("JSON mode not sent: %v", body["response_format"])
	}
	if v := path(t, body, "messages", 2, "content"); v != nil {
		t.Fatalf("assistant tool-call turn should have null content, got %v", v)
	}
	if path(t, body, "messages", 2, "tool_calls", 1, "function", "arguments") != `{"city":"Rome"}` {
		t.Fatalf("arguments must be a JSON string: %v", path(t, body, "messages", 2, "tool_calls"))
	}
	if path(t, body, "messages", 3, "role") != "tool" || path(t, body, "messages", 3, "tool_call_id") != "c1" {
		t.Fatalf("tool result not encoded: %v", path(t, body, "messages", 3))
	}
}

func TestAzureToolCallingRoutesByDeployment(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var url string
	srv := captureServer(t, `{"choices":[{"message":{"tool_calls":[{"id":"x","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}}]}`, &body, &hdr, &url)

	p := NewAzureOpenAIProvider(srv.URL, "k")
	resp, err := p.Complete(context.Background(), toolConversation)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertToolCall(t, resp, "get_weather", `{}`)
	if !strings.Contains(url, "/openai/deployments/m/chat/completions") {
		t.Fatalf("unexpected URL %s", url)
	}
	if _, ok := body["model"]; ok {
		t.Fatal("Azure body must not carry a model field")
	}
	if hdr.Get("api-key") != "k" {
		t.Fatal("missing api-key header")
	}
}

func TestAnthropicToolCalling(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var url string
	srv := captureServer(t, `{"content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Oslo"}}]}`, &body, &hdr, &url)

	p := NewAnthropicProvider("k")
	p.baseURL = srv.URL
	resp, err := p.Complete(context.Background(), toolConversation)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertToolCall(t, resp, "get_weather", `{"city":"Oslo"}`)
	if resp.Content != "checking" {
		t.Fatalf("text alongside tool use lost: %q", resp.Content)
	}

	if path(t, body, "tools", 0, "input_schema", "type") != "object" {
		t.Fatalf("tools not encoded: %v", body["tools"])
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("want user, assistant, grouped tool results; got %d messages: %v", len(msgs), msgs)
	}
	if path(t, msgs, 1, "content", 1, "type") != "tool_use" || path(t, msgs, 1, "content", 1, "input", "city") != "Rome" {
		t.Fatalf("tool_use blocks wrong: %v", msgs[1])
	}
	results := path(t, msgs, 2, "content").([]any)
	if path(t, msgs, 2, "role") != "user" || len(results) != 2 {
		t.Fatalf("both tool results must share one user turn: %v", msgs[2])
	}
	if path(t, results, 1, "tool_use_id") != "c2" {
		t.Fatalf("tool_result id wrong: %v", results[1])
	}
}

func TestGeminiToolCalling(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var url string
	srv := captureServer(t, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"Oslo"}}}]}}]}`, &body, &hdr, &url)

	p := NewGeminiProvider("k")
	p.baseURL = srv.URL
	req := toolConversation
	req.JSONMode = true
	resp, err := p.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertToolCall(t, resp, "get_weather", `{"city":"Oslo"}`)

	if path(t, body, "tools", 0, "functionDeclarations", 0, "name") != "get_weather" {
		t.Fatalf("tools not encoded: %v", body["tools"])
	}
	if _, ok := body["generationConfig"].(map[string]any)["responseMimeType"]; ok {
		t.Fatal("responseMimeType must not be combined with tools")
	}
	contents := body["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("want user, model, grouped responses; got %v", contents)
	}
	if path(t, contents, 1, "role") != "model" || path(t, contents, 1, "parts", 1, "functionCall", "args", "city") != "Rome" {
		t.Fatalf("functionCall parts wrong: %v", contents[1])
	}
	if path(t, contents, 2, "parts", 0, "functionResponse", "response", "temp_c") != 18.0 {
		t.Fatalf("object result should pass through: %v", contents[2])
	}
	if path(t, contents, 2, "parts", 1, "functionResponse", "response", "result") != "sunny" {
		t.Fatalf("non-object result should be wrapped: %v", contents[2])
	}
}

func TestOllamaToolCalling(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	var url string
	srv := captureServer(t, `{"message":{"content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Oslo"}}}]}}`, &body, &hdr, &url)

	p := NewOllamaProvider("ollama", srv.URL, []string{"m"})
	resp, err := p.Complete(context.Background(), toolConversation)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	assertToolCall(t, resp, "get_weather", `{"city":"Oslo"}`)

	if path(t, body, "tools", 0, "function", "name") != "get_weather" {
		t.Fatalf("tools not encoded: %v", body["tools"])
	}
	if path(t, body, "messages", 2, "tool_calls", 0, "function", "arguments", "city") != "Paris" {
		t.Fatalf("arguments must be an object: %v", path(t, body, "messages", 2))
	}
	if path(t, body, "messages", 3, "tool_name") != "get_weather" {
		t.Fatalf("tool result must name the tool: %v", path(t, body, "messages", 3))
	}
}

func TestStatusErrorIncludesBody(t *testing.T) {
	srv := captureServerStatus(t, http.StatusBadRequest, `{"error":{"message":"bad tool schema"}}`)
	p := NewOpenAIProvider("k")
	p.baseURL = srv.URL
	_, err := p.Complete(context.Background(), toolConversation)
	if err == nil || !strings.Contains(err.Error(), "bad tool schema") {
		t.Fatalf("error should include the API's message, got %v", err)
	}
}
