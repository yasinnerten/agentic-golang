// Package executor implements the agentic loop's NodeExecutor: it turns a
// workflow node into a concrete result following the blueprint's rules-first,
// LLM-second principle (§2.1). Nodes whose definition disallows the LLM resolve
// deterministically; LLM-allowed nodes call the model router and parse a
// structured JSON result that downstream edge conditions can route on.
package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/yasinnerten/agentic-golang/agentic/agents"
	"github.com/yasinnerten/agentic-golang/services/modelrouter"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

// LLMExecutor handles "agent" and "decision" workflow nodes.
type LLMExecutor struct {
	registry *agents.Registry
	router   *modelrouter.Router
	model    string
}

// NewLLMExecutor wires the executor to the agent registry and model router.
func NewLLMExecutor(registry *agents.Registry, router *modelrouter.Router, model string) *LLMExecutor {
	if model == "" {
		model = "llama3.1:8b"
	}
	return &LLMExecutor{registry: registry, router: router, model: model}
}

// CanHandle reports whether this executor runs the given node type.
func (e *LLMExecutor) CanHandle(nodeType string) bool {
	switch nodeType {
	case "agent", "decision":
		return true
	default:
		return false
	}
}

// Execute resolves a node. The node's agent definition decides whether the LLM
// is consulted; when it isn't (router/decision, tool-only collectors, human
// review), the node resolves deterministically so the loop can keep flowing.
func (e *LLMExecutor) Execute(ctx context.Context, node types.WorkflowNode, input map[string]any, sessionID string) (map[string]any, error) {
	def, err := e.registry.GetDefinition(ctx, node.AgentDefinitionID)
	if err != nil {
		return nil, fmt.Errorf("load agent definition %q: %w", node.AgentDefinitionID, err)
	}

	if !(node.LLMAllowed && def.CanUseLLM) {
		return deterministic(def), nil
	}
	return e.runLLM(ctx, node, def, input)
}

// deterministic returns a rules-first result for nodes that must not call the LLM.
func deterministic(def *types.AgentDefinition) map[string]any {
	switch def.AgentType {
	case "router":
		return map[string]any{"routed": true, "resolved_by": "rules"}
	case "collector":
		// Evidence collection needs tool/connector integration (not yet wired);
		// resolve to an explicit pending result rather than fabricating evidence.
		return map[string]any{"evidence_items": []any{}, "evidence_status": "pending_tool_integration", "resolved_by": "rules"}
	case "human_review":
		return map[string]any{"status": "pending_human_review", "queued": true, "resolved_by": "rules"}
	default:
		return map[string]any{"status": "completed", "resolved_by": "rules", "agent_type": def.AgentType}
	}
}

// runLLM prompts the model for a structured JSON result matching the agent's
// output schema and parses it into a map the loop can route on.
func (e *LLMExecutor) runLLM(ctx context.Context, node types.WorkflowNode, def *types.AgentDefinition, input map[string]any) (map[string]any, error) {
	inputJSON, _ := json.Marshal(input)

	sys := fmt.Sprintf(
		"You are %s, an EU AI Act compliance agent. %s\n"+
			"Respond with ONLY a single JSON object, no prose or markdown, matching this schema (keys and value types): %s",
		def.AgentName, def.Description, string(def.OutputSchemaJSON),
	)
	if bytes.Contains(def.OutputSchemaJSON, []byte("classification")) {
		sys += "\nThe \"classification\" field MUST be exactly one of: prohibited, high_risk, limited_risk, minimal_risk."
	}

	resp, err := e.router.Complete(ctx, modelrouter.LLMRequest{
		Model:       e.model,
		Messages:    []modelrouter.Message{{Role: "system", Content: sys}, {Role: "user", Content: "Inputs:\n" + string(inputJSON)}},
		MaxTokens:   1024,
		Temperature: 0.1,
		JSONMode:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("llm node %q: %w", node.WorkflowNodeID, err)
	}

	out, err := parseJSONObject(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("llm node %q returned unparseable output: %w", node.WorkflowNodeID, err)
	}
	out["_model"] = resp.Meta.Model
	out["_provider"] = resp.Meta.Provider
	return out, nil
}

// parseJSONObject decodes a JSON object, tolerating leading/trailing noise by
// falling back to the outermost { ... } span.
func parseJSONObject(s string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		return m, nil
	}
	start := bytes.IndexByte([]byte(s), '{')
	end := bytes.LastIndexByte([]byte(s), '}')
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(s[start:end+1]), &m); err == nil {
			return m, nil
		}
	}
	return nil, fmt.Errorf("no JSON object found")
}
