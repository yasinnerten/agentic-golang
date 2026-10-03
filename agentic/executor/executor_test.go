package executor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

type fakeDefs struct {
	defs     map[string]*types.AgentDefinition
	policies map[string]*types.ModelPolicy
}

func (f *fakeDefs) GetDefinition(_ context.Context, id string) (*types.AgentDefinition, error) {
	if d, ok := f.defs[id]; ok {
		return d, nil
	}
	return nil, errors.New("no such definition")
}

func (f *fakeDefs) GetModelPolicy(_ context.Context, id string) (*types.ModelPolicy, error) {
	if p, ok := f.policies[id]; ok {
		return p, nil
	}
	return nil, errors.New("no such policy")
}

// fakeLLM answers with a scripted function and records every request.
type fakeLLM struct {
	mu      sync.Mutex
	reqs    []modelrouter.LLMRequest
	failing map[string]bool
	reply   func(req modelrouter.LLMRequest) *modelrouter.LLMResponse
}

func (f *fakeLLM) Complete(_ context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if f.failing[req.Model] {
		return nil, errors.New("unavailable")
	}
	if f.reply != nil {
		return f.reply(req), nil
	}
	return &modelrouter.LLMResponse{Content: `{"answer":"ok"}`}, nil
}

func (f *fakeLLM) models() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.reqs))
	for i, r := range f.reqs {
		out[i] = r.Model
	}
	return out
}

func strPtr(s string) *string { return &s }

func llmAgent(id string, policy *string) *types.AgentDefinition {
	return &types.AgentDefinition{AgentDefinitionID: id, AgentName: id, AgentType: "worker", CanUseLLM: true, DefaultModelPolicyID: policy}
}

func llmNode(agentID, config string) types.WorkflowNode {
	return types.WorkflowNode{WorkflowNodeID: "n1", NodeType: "agent", AgentDefinitionID: agentID, LLMAllowed: true, ConfigJSON: []byte(config)}
}

func TestModelSelection(t *testing.T) {
	temp := 0.7
	defs := &fakeDefs{
		defs: map[string]*types.AgentDefinition{
			"plain":    llmAgent("plain", nil),
			"policied": llmAgent("policied", strPtr("pol")),
		},
		policies: map[string]*types.ModelPolicy{
			"pol": {ModelPolicyID: "pol", PrimaryModel: "policy-primary", FallbackModels: []string{"policy-backup"}, Temperature: &temp, MaxTokens: 300},
		},
	}

	cases := []struct {
		name       string
		agent      string
		config     string
		failing    []string
		wantCalls  []string
		wantModel  string
		wantTemp   float64
		wantTokens int
	}{
		{name: "executor default", agent: "plain", wantCalls: []string{"default-model"}, wantModel: "default-model", wantTemp: DefaultTemperature, wantTokens: DefaultMaxTokens},
		{name: "agent policy", agent: "policied", wantCalls: []string{"policy-primary"}, wantModel: "policy-primary", wantTemp: 0.7, wantTokens: 300},
		{name: "policy fallback", agent: "policied", failing: []string{"policy-primary"}, wantCalls: []string{"policy-primary", "policy-backup"}, wantModel: "policy-backup", wantTemp: 0.7, wantTokens: 300},
		{name: "node override beats policy", agent: "policied", config: `{"model":"node-model","temperature":0.2,"max_tokens":50}`, wantCalls: []string{"node-model"}, wantModel: "node-model", wantTemp: 0.2, wantTokens: 50},
		{name: "node fallbacks", agent: "plain", config: `{"model":"a","fallback_models":["b","c"]}`, failing: []string{"a", "b"}, wantCalls: []string{"a", "b", "c"}, wantModel: "c", wantTemp: DefaultTemperature, wantTokens: DefaultMaxTokens},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := &fakeLLM{failing: map[string]bool{}}
			for _, m := range tc.failing {
				llm.failing[m] = true
			}
			e := NewLLMExecutor(defs, llm, "default-model")
			out, err := e.Execute(context.Background(), llmNode(tc.agent, tc.config), map[string]any{}, "s1")
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := strings.Join(llm.models(), ","); got != strings.Join(tc.wantCalls, ",") {
				t.Fatalf("models tried = %s, want %s", got, strings.Join(tc.wantCalls, ","))
			}
			if out["_model"] != tc.wantModel {
				t.Fatalf("_model = %v, want %s", out["_model"], tc.wantModel)
			}
			last := llm.reqs[len(llm.reqs)-1]
			if last.Temperature != tc.wantTemp || last.MaxTokens != tc.wantTokens {
				t.Fatalf("temperature/max_tokens = %v/%d, want %v/%d", last.Temperature, last.MaxTokens, tc.wantTemp, tc.wantTokens)
			}
		})
	}
}

func TestAllModelsFailingReportsEachError(t *testing.T) {
	defs := &fakeDefs{defs: map[string]*types.AgentDefinition{"plain": llmAgent("plain", nil)}}
	llm := &fakeLLM{failing: map[string]bool{"a": true, "b": true}}
	e := NewLLMExecutor(defs, llm, "unused")
	_, err := e.Execute(context.Background(), llmNode("plain", `{"model":"a","fallback_models":["b"]}`), map[string]any{}, "s1")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "model a") || !strings.Contains(err.Error(), "model b") {
		t.Fatalf("error should name both models: %v", err)
	}
}

func TestMissingModelPolicyIsAnError(t *testing.T) {
	defs := &fakeDefs{defs: map[string]*types.AgentDefinition{"x": llmAgent("x", strPtr("missing"))}}
	e := NewLLMExecutor(defs, &fakeLLM{}, "m")
	if _, err := e.Execute(context.Background(), llmNode("x", ""), map[string]any{}, "s1"); err == nil {
		t.Fatal("expected an error for a missing model policy")
	}
}

func TestDeterministicNodeNeverCallsModel(t *testing.T) {
	defs := &fakeDefs{defs: map[string]*types.AgentDefinition{"r": {AgentDefinitionID: "r", AgentType: "router"}}}
	llm := &fakeLLM{}
	e := NewLLMExecutor(defs, llm, "m")
	out, err := e.Execute(context.Background(), types.WorkflowNode{NodeType: "agent", AgentDefinitionID: "r"}, nil, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if out["resolved_by"] != "rules" || len(llm.reqs) != 0 {
		t.Fatalf("expected a rules-only result with no model calls, got %v and %d calls", out, len(llm.reqs))
	}
}
