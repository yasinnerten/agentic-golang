package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yasinnerten/agentic-golang/agentic/tools"
	"github.com/yasinnerten/agentic-golang/services/modelrouter"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

// scriptedLLM returns the next scripted reply on each call.
func scriptedLLM(replies ...*modelrouter.LLMResponse) *fakeLLM {
	var mu sync.Mutex
	i := 0
	return &fakeLLM{reply: func(modelrouter.LLMRequest) *modelrouter.LLMResponse {
		mu.Lock()
		defer mu.Unlock()
		r := replies[i]
		if i < len(replies)-1 {
			i++
		}
		return r
	}}
}

func callTools(calls ...modelrouter.ToolCall) *modelrouter.LLMResponse {
	return &modelrouter.LLMResponse{ToolCalls: calls, Meta: modelrouter.LLMMeta{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
}

func answer(content string) *modelrouter.LLMResponse {
	return &modelrouter.LLMResponse{Content: content, Meta: modelrouter.LLMMeta{InputTokens: 20, OutputTokens: 3, TotalTokens: 23}}
}

func call(id, name, args string) modelrouter.ToolCall {
	return modelrouter.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func toolAgent() *fakeDefs {
	return &fakeDefs{defs: map[string]*types.AgentDefinition{
		"worker":   {AgentDefinitionID: "worker", AgentName: "worker", CanUseLLM: true, CanUseTools: true},
		"no_tools": {AgentDefinitionID: "no_tools", AgentName: "no_tools", CanUseLLM: true},
	}}
}

func toolNode(agent, config string) types.WorkflowNode {
	n := llmNode(agent, config)
	n.ToolAllowed = true
	return n
}

type cityArgs struct {
	City string `json:"city"`
}

func mustRegistry(t *testing.T, ts ...tools.Tool) *tools.Registry {
	t.Helper()
	r, err := tools.NewRegistry(ts...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestToolLoopRunsParallelCallsAndFeedsResultsBack(t *testing.T) {
	// Both calls must be running at once to get past the barrier.
	var barrier sync.WaitGroup
	barrier.Add(2)
	var mu sync.Mutex
	var seenSession string
	weather := tools.MustFunc("get_weather", "weather", func(ctx context.Context, a cityArgs) (map[string]any, error) {
		mu.Lock()
		seenSession = tools.SessionID(ctx)
		mu.Unlock()
		barrier.Done()
		done := make(chan struct{})
		go func() { barrier.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return nil, errors.New("tool calls did not run concurrently")
		}
		return map[string]any{"city": a.City, "temp_c": len(a.City)}, nil
	})

	llm := scriptedLLM(
		callTools(call("c1", "get_weather", `{"city":"Paris"}`), call("c2", "get_weather", `{"city":"Rome"}`)),
		answer(`{"summary":"done"}`),
	)
	e := NewLLMExecutor(toolAgent(), llm, "m", WithTools(mustRegistry(t, weather)))
	out, err := e.Execute(context.Background(), toolNode("worker", ""), map[string]any{}, "sess-42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["summary"] != "done" {
		t.Fatalf("final answer lost: %v", out)
	}
	if seenSession != "sess-42" {
		t.Fatalf("tool saw session %q", seenSession)
	}

	if len(llm.reqs) != 2 {
		t.Fatalf("expected 2 model calls, got %d", len(llm.reqs))
	}
	if len(llm.reqs[0].Tools) != 1 || llm.reqs[0].Tools[0].Name != "get_weather" {
		t.Fatalf("tools not offered: %+v", llm.reqs[0].Tools)
	}
	second := llm.reqs[1].Messages
	if len(second) != 5 {
		t.Fatalf("second request should hold system, user, assistant, 2 results; got %d", len(second))
	}
	if second[2].Role != "assistant" || len(second[2].ToolCalls) != 2 {
		t.Fatalf("assistant tool-call turn missing: %+v", second[2])
	}
	for i, want := range []struct{ id, content string }{{"c1", `{"city":"Paris","temp_c":5}`}, {"c2", `{"city":"Rome","temp_c":4}`}} {
		got := second[3+i]
		if got.Role != "tool" || got.ToolCallID != want.id || got.ToolName != "get_weather" || got.Content != want.content {
			t.Fatalf("result %d = %+v, want id %s content %s", i, got, want.id, want.content)
		}
	}

	trace, _ := out["_tool_calls"].([]map[string]any)
	if len(trace) != 2 || trace[0]["name"] != "get_weather" || trace[1]["id"] != "c2" {
		t.Fatalf("tool trace wrong: %v", out["_tool_calls"])
	}
	usage := out["_usage"].(map[string]any)
	if usage["total_tokens"] != 38 {
		t.Fatalf("usage should sum both calls: %v", usage)
	}
}

func TestToolFailuresGoBackToTheModel(t *testing.T) {
	failing := tools.MustFunc("failing", "", func(context.Context, struct{}) (int, error) { return 0, errors.New("db down") })
	panicky := tools.MustFunc("panicky", "", func(context.Context, struct{}) (int, error) { panic("boom") })

	llm := scriptedLLM(
		callTools(call("a", "failing", `{}`), call("b", "panicky", `{}`), call("c", "missing", `{}`)),
		answer(`{"ok":false}`),
	)
	e := NewLLMExecutor(toolAgent(), llm, "m", WithTools(mustRegistry(t, failing, panicky)))
	if _, err := e.Execute(context.Background(), toolNode("worker", ""), map[string]any{}, "s"); err != nil {
		t.Fatalf("tool failures must not fail the node: %v", err)
	}
	results := llm.reqs[1].Messages[3:]
	for i, want := range []string{"db down", "panicked", "unknown tool"} {
		if !strings.Contains(results[i].Content, want) || !strings.HasPrefix(results[i].Content, `{"error":`) {
			t.Fatalf("result %d = %s, want an error mentioning %q", i, results[i].Content, want)
		}
	}
}

func TestToolRoundLimit(t *testing.T) {
	loop := tools.MustFunc("again", "", func(context.Context, struct{}) (int, error) { return 1, nil })
	llm := scriptedLLM(callTools(call("x", "again", `{}`)))

	e := NewLLMExecutor(toolAgent(), llm, "m", WithTools(mustRegistry(t, loop)))
	_, err := e.Execute(context.Background(), toolNode("worker", `{"max_tool_rounds":2}`), map[string]any{}, "s")
	if err == nil || !strings.Contains(err.Error(), "after 2 rounds") {
		t.Fatalf("expected round limit error, got %v", err)
	}
	if len(llm.reqs) != 3 {
		t.Fatalf("expected 3 model calls (2 tool rounds + the over-limit one), got %d", len(llm.reqs))
	}
}

func TestToolSelection(t *testing.T) {
	a := tools.MustFunc("a", "", func(context.Context, struct{}) (int, error) { return 1, nil })
	b := tools.MustFunc("b", "", func(context.Context, struct{}) (int, error) { return 2, nil })
	reg := mustRegistry(t, a, b)

	offered := func(agent string, node types.WorkflowNode) ([]string, error) {
		llm := scriptedLLM(answer(`{}`))
		e := NewLLMExecutor(toolAgent(), llm, "m", WithTools(reg))
		node.AgentDefinitionID = agent
		if _, err := e.Execute(context.Background(), node, map[string]any{}, "s"); err != nil {
			return nil, err
		}
		var names []string
		for _, s := range llm.reqs[0].Tools {
			names = append(names, s.Name)
		}
		return names, nil
	}

	if got, _ := offered("worker", toolNode("worker", "")); strings.Join(got, ",") != "a,b" {
		t.Fatalf("all tools should be offered by default, got %v", got)
	}
	if got, _ := offered("worker", toolNode("worker", `{"tools":["b"]}`)); strings.Join(got, ",") != "b" {
		t.Fatalf("config should narrow tools, got %v", got)
	}
	if got, _ := offered("worker", llmNode("worker", "")); len(got) != 0 {
		t.Fatalf("node without tool_allowed must get no tools, got %v", got)
	}
	if got, _ := offered("no_tools", toolNode("no_tools", "")); len(got) != 0 {
		t.Fatalf("agent without can_use_tools must get no tools, got %v", got)
	}
	if _, err := offered("worker", toolNode("worker", `{"tools":["nope"]}`)); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown tool in config should fail, got %v", err)
	}
}

func TestToolCallWithoutToolsFails(t *testing.T) {
	llm := scriptedLLM(callTools(call("x", "anything", `{}`)))
	e := NewLLMExecutor(toolAgent(), llm, "m")
	if _, err := e.Execute(context.Background(), llmNode("worker", ""), map[string]any{}, "s"); err == nil {
		t.Fatal("expected an error when the model calls tools that were never offered")
	}
}
