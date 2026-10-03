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
	"sync"

	"github.com/yasinnerten/agentic-golang/agentic/tools"
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

// DefaultMaxToolRounds bounds how many model -> tools -> model cycles one node
// may run before it must answer.
const DefaultMaxToolRounds = 8

// LLMExecutor handles "agent" and "decision" workflow nodes.
type LLMExecutor struct {
	registry      DefinitionSource
	router        Completer
	model         string
	tools         *tools.Registry
	maxToolRounds int
}

// Option configures an LLMExecutor.
type Option func(*LLMExecutor)

// WithTools makes the registry's tools available to nodes that allow tool use
// (node tool_allowed and agent can_use_tools).
func WithTools(r *tools.Registry) Option {
	return func(e *LLMExecutor) { e.tools = r }
}

// WithMaxToolRounds changes the default tool-round limit. A node's config
// "max_tool_rounds" still overrides it.
func WithMaxToolRounds(n int) Option {
	return func(e *LLMExecutor) { e.maxToolRounds = n }
}

// NewLLMExecutor wires the executor to the agent registry and model router.
// model is the fallback used when neither the node nor the agent's model
// policy names one.
func NewLLMExecutor(registry DefinitionSource, router Completer, model string, opts ...Option) *LLMExecutor {
	if model == "" {
		model = "llama3.1:8b"
	}
	e := &LLMExecutor{registry: registry, router: router, model: model, maxToolRounds: DefaultMaxToolRounds}
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
	return e.runLLM(ctx, node, def, cfg, spec, input, sessionID)
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
func (e *LLMExecutor) runLLM(ctx context.Context, node types.WorkflowNode, def *types.AgentDefinition, cfg types.NodeConfig, spec ModelSpec, input map[string]any, sessionID string) (map[string]any, error) {
	inputJSON, _ := json.Marshal(input)

	sys := fmt.Sprintf(
		"You are %s, an EU AI Act compliance agent. %s\n"+
			"Respond with ONLY a single JSON object, no prose or markdown, matching this schema (keys and value types): %s",
		def.AgentName, def.Description, string(def.OutputSchemaJSON),
	)
	if bytes.Contains(def.OutputSchemaJSON, []byte("classification")) {
		sys += "\nThe \"classification\" field MUST be exactly one of: prohibited, high_risk, limited_risk, minimal_risk."
	}

	toolset, err := e.toolsFor(node, def, cfg)
	if err != nil {
		return nil, fmt.Errorf("llm node %q: %w", node.WorkflowNodeID, err)
	}
	maxRounds := e.maxToolRounds
	if cfg.MaxToolRounds > 0 {
		maxRounds = cfg.MaxToolRounds
	}

	msgs := []modelrouter.Message{{Role: "system", Content: sys}, {Role: "user", Content: "Inputs:\n" + string(inputJSON)}}
	var specs []modelrouter.ToolSpec
	for _, t := range toolset {
		specs = append(specs, tools.Spec(t))
	}
	var trace []map[string]any
	var usage modelrouter.LLMMeta

	// The model may call tools any number of times (bounded by maxRounds)
	// before giving its final JSON answer.
	for round := 0; ; round++ {
		resp, err := e.complete(ctx, spec, modelrouter.LLMRequest{
			Messages:    msgs,
			MaxTokens:   spec.MaxTokens,
			Temperature: spec.Temperature,
			JSONMode:    true,
			Tools:       specs,
		})
		if err != nil {
			return nil, fmt.Errorf("llm node %q: %w", node.WorkflowNodeID, err)
		}
		usage.InputTokens += resp.Meta.InputTokens
		usage.OutputTokens += resp.Meta.OutputTokens
		usage.TotalTokens += resp.Meta.TotalTokens

		if len(resp.ToolCalls) == 0 {
			out, err := parseJSONObject(resp.Content)
			if err != nil {
				return nil, fmt.Errorf("llm node %q returned unparseable output: %w", node.WorkflowNodeID, err)
			}
			out["_model"] = resp.Meta.Model
			out["_provider"] = resp.Meta.Provider
			out["_usage"] = map[string]any{
				"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens,
			}
			if len(trace) > 0 {
				out["_tool_calls"] = trace
			}
			return out, nil
		}
		if len(specs) == 0 {
			return nil, fmt.Errorf("llm node %q: model requested tools but none are enabled for this node", node.WorkflowNodeID)
		}
		if round >= maxRounds {
			return nil, fmt.Errorf("llm node %q: still calling tools after %d rounds", node.WorkflowNodeID, maxRounds)
		}

		msgs = append(msgs, modelrouter.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		results, calls := e.runTools(tools.WithCallContext(ctx, sessionID, node.WorkflowNodeID), toolset, resp.ToolCalls)
		msgs = append(msgs, results...)
		trace = append(trace, calls...)
	}
}

// toolsFor returns the tools a node may call. Tools are offered only when the
// executor has a registry, the node allows tools and the agent may use them.
// The node's config "tools" list narrows the set.
func (e *LLMExecutor) toolsFor(node types.WorkflowNode, def *types.AgentDefinition, cfg types.NodeConfig) ([]tools.Tool, error) {
	if e.tools == nil || !node.ToolAllowed || !def.CanUseTools {
		return nil, nil
	}
	if len(cfg.Tools) > 0 {
		return e.tools.Select(cfg.Tools)
	}
	return e.tools.All(), nil
}

// runTools executes one round of tool calls concurrently. Results come back in
// call order. A failing, panicking or unknown tool produces an error result
// for the model to read and react to; it does not fail the node.
func (e *LLMExecutor) runTools(ctx context.Context, toolset []tools.Tool, calls []modelrouter.ToolCall) ([]modelrouter.Message, []map[string]any) {
	byName := make(map[string]tools.Tool, len(toolset))
	for _, t := range toolset {
		byName[t.Name()] = t
	}

	results := make([]modelrouter.Message, len(calls))
	trace := make([]map[string]any, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call modelrouter.ToolCall) {
			defer wg.Done()
			value, err := callTool(ctx, byName[call.Name], call)

			entry := map[string]any{"id": call.ID, "name": call.Name, "arguments": decodeOrRaw(call.Arguments)}
			var content []byte
			if err != nil {
				entry["error"] = err.Error()
				content, _ = json.Marshal(map[string]string{"error": err.Error()})
			} else {
				entry["result"] = value
				if content, err = json.Marshal(value); err != nil {
					content, _ = json.Marshal(map[string]string{"error": "tool result is not JSON-encodable: " + err.Error()})
				}
			}
			results[i] = modelrouter.Message{Role: "tool", ToolCallID: call.ID, ToolName: call.Name, Content: string(content)}
			trace[i] = entry
		}(i, call)
	}
	wg.Wait()
	return results, trace
}

func callTool(ctx context.Context, t tools.Tool, call modelrouter.ToolCall) (value any, err error) {
	if t == nil {
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool %q panicked: %v", call.Name, r)
		}
	}()
	return t.Call(ctx, call.Arguments)
}

func decodeOrRaw(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		return v
	}
	return string(raw)
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
