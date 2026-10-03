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
	"errors"
	"fmt"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

// DefinitionSource loads agent definitions and model policies.
// *agents.Registry implements it.
type DefinitionSource interface {
	GetDefinition(ctx context.Context, id string) (*types.AgentDefinition, error)
	GetModelPolicy(ctx context.Context, id string) (*types.ModelPolicy, error)
}

// Completer sends one request to a model. *modelrouter.Router implements it.
type Completer interface {
	Complete(ctx context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error)
}

// Defaults applied when neither the node config nor the model policy sets a value.
const (
	DefaultTemperature = 0.1
	DefaultMaxTokens   = 1024
)

// LLMExecutor handles "agent" and "decision" workflow nodes.
type LLMExecutor struct {
	registry DefinitionSource
	router   Completer
	model    string
}

// Option configures an LLMExecutor.
type Option func(*LLMExecutor)

// NewLLMExecutor wires the executor to the agent registry and model router.
// model is the fallback used when neither the node nor the agent's model
// policy names one.
func NewLLMExecutor(registry DefinitionSource, router Completer, model string, opts ...Option) *LLMExecutor {
	if model == "" {
		model = "llama3.1:8b"
	}
	e := &LLMExecutor{registry: registry, router: router, model: model}
	for _, opt := range opts {
		opt(e)
	}
	return e
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
	cfg, err := types.ParseNodeConfig(node.ConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", node.WorkflowNodeID, err)
	}
	spec, err := e.resolveModel(ctx, def, cfg)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", node.WorkflowNodeID, err)
	}
	return e.runLLM(ctx, node, def, spec, input)
}

// ModelSpec is the resolved model choice for one node: the models to try in
// order, and the sampling settings.
type ModelSpec struct {
	Models      []string
	Temperature float64
	MaxTokens   int
}

// resolveModel picks the models for a node. Precedence: the node's
// config_json "model" (with its "fallback_models"), then the agent's model
// policy, then the executor default. Temperature and max_tokens follow the
// same order, falling back to package defaults.
func (e *LLMExecutor) resolveModel(ctx context.Context, def *types.AgentDefinition, cfg types.NodeConfig) (ModelSpec, error) {
	spec := ModelSpec{Temperature: DefaultTemperature, MaxTokens: DefaultMaxTokens}

	var policy *types.ModelPolicy
	if def.DefaultModelPolicyID != nil && *def.DefaultModelPolicyID != "" {
		p, err := e.registry.GetModelPolicy(ctx, *def.DefaultModelPolicyID)
		if err != nil {
			return spec, fmt.Errorf("agent %q: %w", def.AgentDefinitionID, err)
		}
		policy = p
	}

	switch {
	case cfg.Model != "":
		spec.Models = append([]string{cfg.Model}, cfg.FallbackModels...)
	case policy != nil:
		spec.Models = append([]string{policy.PrimaryModel}, policy.FallbackModels...)
	default:
		spec.Models = []string{e.model}
	}

	if policy != nil {
		if policy.Temperature != nil {
			spec.Temperature = *policy.Temperature
		}
		if policy.MaxTokens > 0 {
			spec.MaxTokens = policy.MaxTokens
		}
	}
	if cfg.Temperature != nil {
		spec.Temperature = *cfg.Temperature
	}
	if cfg.MaxTokens > 0 {
		spec.MaxTokens = cfg.MaxTokens
	}
	return spec, nil
}

// complete sends req to each model in spec.Models in turn and returns the
// first success. Errors from every attempted model are joined.
func (e *LLMExecutor) complete(ctx context.Context, spec ModelSpec, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	var errs []error
	for _, m := range spec.Models {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(append(errs, err)...)
		}
		req.Model = m
		resp, err := e.router.Complete(ctx, req)
		if err == nil {
			if resp.Meta.Model == "" {
				resp.Meta.Model = m
			}
			return resp, nil
		}
		errs = append(errs, fmt.Errorf("model %s: %w", m, err))
	}
	return nil, errors.Join(errs...)
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
func (e *LLMExecutor) runLLM(ctx context.Context, node types.WorkflowNode, def *types.AgentDefinition, spec ModelSpec, input map[string]any) (map[string]any, error) {
	inputJSON, _ := json.Marshal(input)

	sys := fmt.Sprintf(
		"You are %s, an EU AI Act compliance agent. %s\n"+
			"Respond with ONLY a single JSON object, no prose or markdown, matching this schema (keys and value types): %s",
		def.AgentName, def.Description, string(def.OutputSchemaJSON),
	)
	if bytes.Contains(def.OutputSchemaJSON, []byte("classification")) {
		sys += "\nThe \"classification\" field MUST be exactly one of: prohibited, high_risk, limited_risk, minimal_risk."
	}

	resp, err := e.complete(ctx, spec, modelrouter.LLMRequest{
		Messages:    []modelrouter.Message{{Role: "system", Content: sys}, {Role: "user", Content: "Inputs:\n" + string(inputJSON)}},
		MaxTokens:   spec.MaxTokens,
		Temperature: spec.Temperature,
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
