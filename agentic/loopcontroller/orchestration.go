package loopcontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yasinnerten/agentic-golang/agentic/tools"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

// Built-in orchestration node types. A registered NodeExecutor that handles
// the same type takes precedence.
const (
	// NodeTypeSubagent runs a child workflow in a child session and stores its
	// result under the node's output key.
	NodeTypeSubagent = "subagent"
	// NodeTypeParallel runs several branches (agents or child workflows)
	// concurrently and stores each result under its branch key.
	NodeTypeParallel = "parallel"

	// DefaultMaxSubagentDepth bounds how deeply subagents may nest, so a
	// workflow that (directly or indirectly) calls itself cannot recurse
	// forever.
	DefaultMaxSubagentDepth = 5
)

// RunResult reports how a run ended.
type RunResult struct {
	SessionID string
	LoopRunID string
	// Status is the final session status: completed, failed, timed_out,
	// paused or waiting_for_user.
	Status string
	// Reason says why the run stopped.
	Reason string
	// Output is the accumulated state: the seeded input plus every node's
	// output.
	Output map[string]any
}

// SetMaxSubagentDepth changes how deeply subagents may nest. Zero or less
// restores the default.
func (c *Controller) SetMaxSubagentDepth(n int) {
	c.maxSubagentDepth = n
}

func (c *Controller) subagentDepthLimit() int {
	if c.maxSubagentDepth > 0 {
		return c.maxSubagentDepth
	}
	return DefaultMaxSubagentDepth
}

// runInfo is what nested runs inherit from the run that started them.
type runInfo struct {
	cfg       LoopConfig
	depth     int
	loopRunID string
}

type runInfoKey struct{}

func withRunInfo(ctx context.Context, ri runInfo) context.Context {
	return context.WithValue(ctx, runInfoKey{}, ri)
}

func runInfoFrom(ctx context.Context) runInfo {
	ri, _ := ctx.Value(runInfoKey{}).(runInfo)
	return ri
}

func runDepth(ctx context.Context) int { return runInfoFrom(ctx).depth }

// RunChild runs workflowID as a subagent of parentSessionID: in a new child
// session (linked through parent_session_id) of the same tenant, with the
// parent run's loop limits. It returns an error unless the child completes.
func (c *Controller) RunChild(ctx context.Context, parentSessionID, workflowID string, input map[string]any) (*RunResult, error) {
	ri := runInfoFrom(ctx)
	if limit := c.subagentDepthLimit(); ri.depth+1 > limit {
		return nil, fmt.Errorf("subagent %q: nesting deeper than %d levels", workflowID, limit)
	}
	parent, err := c.sessions.Get(ctx, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("subagent %q: load parent session: %w", workflowID, err)
	}
	child, err := c.sessions.Create(ctx, parent.TenantID, string(types.SessionTypeSubagent), parent.FrameworkID, &parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("subagent %q: create child session: %w", workflowID, err)
	}

	c.emit(parent.TenantID, parentSessionID, "subagent_started", map[string]any{
		"workflow_id": workflowID, "child_session_id": child.SessionID, "depth": ri.depth + 1,
	})
	childCtx := withRunInfo(ctx, runInfo{cfg: ri.cfg, depth: ri.depth + 1})
	res, err := c.RunWorkflow(childCtx, child.SessionID, workflowID, input, ri.cfg)
	if err != nil {
		return nil, fmt.Errorf("subagent %q (session %s): %w", workflowID, child.SessionID, err)
	}
	c.emit(parent.TenantID, parentSessionID, "subagent_finished", map[string]any{
		"workflow_id": workflowID, "child_session_id": child.SessionID, "status": res.Status,
	})
	if res.Status != "completed" {
		return res, fmt.Errorf("subagent %q (session %s) ended %s: %s", workflowID, child.SessionID, res.Status, res.Reason)
	}
	return res, nil
}

// runSubagentNode implements NodeTypeSubagent. The child workflow receives the
// parent's accumulated state as its input; the keys it adds or changes are
// stored under the node's output key, plus "_session_id" naming the child
// session.
func (c *Controller) runSubagentNode(ctx context.Context, node types.WorkflowNode, input map[string]any, sessionID string) (map[string]any, error) {
	cfg, err := types.ParseNodeConfig(node.ConfigJSON)
	if err != nil {
		return nil, err
	}
	if cfg.WorkflowID == "" {
		return nil, fmt.Errorf("subagent node %q: config needs a workflow_id", node.WorkflowNodeID)
	}
	key := cfg.OutputKey
	if key == "" {
		key = defaultOutputKey(node.WorkflowNodeID)
	}
	childIn := accumulatedCopy(input)
	res, err := c.RunChild(ctx, sessionID, cfg.WorkflowID, childIn)
	if err != nil {
		return nil, err
	}
	return map[string]any{key: childResult(childIn, res)}, nil
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// runParallelNode implements NodeTypeParallel: every branch gets the same
// input and runs concurrently (at most max_concurrency at a time). Each
// branch's result is stored under its key, so edge conditions can compare
// them (e.g. "fast.score > careful.score").
//
// With fail_fast, the first branch error cancels the remaining branches and
// fails the node. Without it, failed branches are listed in "branch_errors"
// (key -> message) and the node succeeds as long as one branch did, so edges
// can route on partial results.
func (c *Controller) runParallelNode(ctx context.Context, node types.WorkflowNode, input map[string]any, sessionID string) (map[string]any, error) {
	cfg, err := types.ParseNodeConfig(node.ConfigJSON)
	if err != nil {
		return nil, err
	}
	if err := validateBranches(cfg.Branches); err != nil {
		return nil, fmt.Errorf("parallel node %q: %w", node.WorkflowNodeID, err)
	}

	limit := cfg.MaxConcurrency
	if limit <= 0 || limit > len(cfg.Branches) {
		limit = len(cfg.Branches)
	}
	sem := make(chan struct{}, limit)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	outputs := map[string]any{}
	errs := map[string]error{}
	var wg sync.WaitGroup
	for _, b := range cfg.Branches {
		wg.Add(1)
		go func(b types.BranchConfig) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				errs[b.Key] = ctx.Err()
				mu.Unlock()
				return
			}
			out, err := c.runBranch(ctx, node, b, input, sessionID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[b.Key] = err
				if cfg.FailFast {
					cancel()
				}
				return
			}
			outputs[b.Key] = out
		}(b)
	}
	wg.Wait()

	if len(errs) == 0 {
		return outputs, nil
	}
	if cfg.FailFast || len(outputs) == 0 {
		return nil, fmt.Errorf("parallel node %q: %w", node.WorkflowNodeID, joinBranchErrors(errs))
	}
	branchErrs := map[string]any{}
	for k, e := range errs {
		branchErrs[k] = e.Error()
	}
	outputs["branch_errors"] = branchErrs
	return outputs, nil
}

func validateBranches(branches []types.BranchConfig) error {
	if len(branches) == 0 {
		return errors.New("config needs at least one branch")
	}
	seen := map[string]bool{}
	for _, b := range branches {
		if !identifier.MatchString(b.Key) {
			return fmt.Errorf("branch key %q must be an identifier (letters, digits, _)", b.Key)
		}
		if b.Key == "branch_errors" {
			return errors.New(`branch key "branch_errors" is reserved`)
		}
		if seen[b.Key] {
			return fmt.Errorf("duplicate branch key %q", b.Key)
		}
		seen[b.Key] = true
		if (b.AgentDefinitionID == "") == (b.WorkflowID == "") {
			return fmt.Errorf("branch %q needs exactly one of agent_definition_id or workflow_id", b.Key)
		}
	}
	return nil
}

// joinBranchErrors joins branch errors in key order, leaving out cancellations
// caused by fail-fast unless nothing else failed.
func joinBranchErrors(errs map[string]error) error {
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var real, all []error
	for _, k := range keys {
		e := fmt.Errorf("branch %s: %w", k, errs[k])
		all = append(all, e)
		if !errors.Is(errs[k], context.Canceled) {
			real = append(real, e)
		}
	}
	if len(real) > 0 {
		return errors.Join(real...)
	}
	return errors.Join(all...)
}

// runBranch runs one parallel branch: a child workflow, or a single agent
// executed as a synthetic node through the registered executors. Agent
// branches inherit the parallel node's llm_allowed/tool_allowed flags and are
// recorded as their own try-run (node ID "<parallel node>/<key>").
func (c *Controller) runBranch(ctx context.Context, node types.WorkflowNode, b types.BranchConfig, input map[string]any, sessionID string) (map[string]any, error) {
	if b.WorkflowID != "" {
		childIn := accumulatedCopy(input)
		res, err := c.RunChild(ctx, sessionID, b.WorkflowID, childIn)
		if err != nil {
			return nil, err
		}
		return childResult(childIn, res), nil
	}

	branchCfg := map[string]any{}
	for k, v := range b.Config {
		branchCfg[k] = v
	}
	if b.Model != "" {
		branchCfg["model"] = b.Model
	}
	cfgJSON, err := json.Marshal(branchCfg)
	if err != nil {
		return nil, fmt.Errorf("branch %s config: %w", b.Key, err)
	}
	bnode := types.WorkflowNode{
		WorkflowNodeID:    node.WorkflowNodeID + "/" + b.Key,
		WorkflowID:        node.WorkflowID,
		NodeName:          b.Key,
		NodeType:          "agent",
		AgentDefinitionID: b.AgentDefinitionID,
		ConfigJSON:        cfgJSON,
		RulesFirst:        node.RulesFirst,
		LLMAllowed:        node.LLMAllowed,
		ToolAllowed:       node.ToolAllowed,
	}
	branchIn := make(map[string]any, len(input))
	for k, v := range input {
		branchIn[k] = v
	}
	branchIn["node_id"] = bnode.WorkflowNodeID
	branchIn["node_name"] = bnode.NodeName
	branchIn["node_type"] = bnode.NodeType
	branchIn["accumulated"] = accumulatedCopy(input)

	session, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	tr := &TryRun{
		TryRunID:       uuid.New().String(),
		LoopRunID:      runInfoFrom(ctx).loopRunID,
		SessionID:      sessionID,
		TenantID:       session.TenantID,
		WorkflowNodeID: bnode.WorkflowNodeID,
		AttemptNumber:  1,
		Status:         "started",
		StartedAt:      time.Now(),
	}
	return c.executeNodeWithTryRun(ctx, tr, bnode, branchIn, sessionID)
}

// agentToolArgs is what a model passes when it delegates to an agent tool.
type agentToolArgs struct {
	Task    string         `json:"task" desc:"What the agent should do, in plain language"`
	Context map[string]any `json:"context,omitempty" desc:"Extra structured input for the agent"`
}

// AgentTool exposes a workflow as a tool, so a model can decide to delegate
// work to another agent (the "agents as tools" pattern). Each call runs
// workflowID as a subagent of the calling session, with input {"task": ...}
// plus any "context" keys, and returns the keys the subagent produced.
func (c *Controller) AgentTool(name, description, workflowID string) (tools.Tool, error) {
	return tools.NewFunc(name, description, func(ctx context.Context, a agentToolArgs) (map[string]any, error) {
		parent := tools.SessionID(ctx)
		if parent == "" {
			return nil, fmt.Errorf("agent tool %q must be called from within a workflow session", name)
		}
		in := map[string]any{"task": a.Task}
		for k, v := range a.Context {
			if k != "task" {
				in[k] = v
			}
		}
		res, err := c.RunChild(ctx, parent, workflowID, in)
		if err != nil {
			return nil, err
		}
		return childResult(in, res), nil
	})
}

// accumulatedCopy returns a shallow copy of the parent's accumulated state,
// minus the loop's internal "error" seed.
func accumulatedCopy(input map[string]any) map[string]any {
	acc, _ := input["accumulated"].(map[string]any)
	out := make(map[string]any, len(acc))
	for k, v := range acc {
		if k == "error" && v == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// childResult returns the keys a child run added or changed relative to its
// input, plus "_session_id". Input values are compared after a JSON round
// trip because the child sees its input that way.
func childResult(input map[string]any, res *RunResult) map[string]any {
	normalized := map[string]any{}
	if b, err := json.Marshal(input); err == nil {
		_ = json.Unmarshal(b, &normalized)
	}
	out := map[string]any{"_session_id": res.SessionID}
	for k, v := range res.Output {
		if k == "error" && v == "" {
			continue
		}
		if prev, ok := normalized[k]; ok && reflect.DeepEqual(prev, v) {
			continue
		}
		out[k] = v
	}
	return out
}

// defaultOutputKey turns a node ID like "wf_main/research-step" into a CEL
// identifier ("research_step") for use as an output key.
func defaultOutputKey(nodeID string) string {
	if i := strings.LastIndexByte(nodeID, '/'); i >= 0 {
		nodeID = nodeID[i+1:]
	}
	key := strings.Map(func(r rune) rune {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			return r
		}
		return '_'
	}, nodeID)
	if key == "" || ('0' <= key[0] && key[0] <= '9') {
		key = "_" + key
	}
	return key
}
