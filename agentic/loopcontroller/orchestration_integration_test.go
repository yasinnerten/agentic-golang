package loopcontroller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yasinnerten/agentic-golang/agentic/tools"
	"github.com/yasinnerten/agentic-golang/internal/testdb"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

func startSession(t *testing.T, c *Controller, workflowID string, input map[string]any) *RunResult {
	t.Helper()
	ctx := context.Background()
	sess, err := c.sessions.Create(ctx, "t1", "test", "", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	res, err := c.RunWorkflow(ctx, sess.SessionID, workflowID, input, LoopConfig{})
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	return res
}

func noopExecutor(nodeType string) funcExecutor {
	return funcExecutor{nodeType: nodeType, fn: func(context.Context, types.WorkflowNode, map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	}}
}

func TestSubagentNodeRunsChildWorkflow(t *testing.T) {
	db := testdb.Open(t)

	// Child: doubles x into y.
	seedWorkflow(t, db, "wf_child", "double", []testNode{{id: "double", nodeType: "double"}}, nil)
	// Parent: research (subagent) -> check, which only proceeds when the
	// child's result is visible to edge conditions.
	seedWorkflow(t, db, "wf_parent", "research",
		[]testNode{
			{id: "research", nodeType: NodeTypeSubagent, config: `{"workflow_id":"wf_child"}`},
			{id: "ok", nodeType: "noop"},
		},
		[]testEdge{{from: "research", to: "ok", cond: "research.y == 4"}})

	c, sm, _ := newTestController(db)
	c.RegisterExecutor(noopExecutor("noop"))
	c.RegisterExecutor(funcExecutor{nodeType: "double", fn: func(_ context.Context, _ types.WorkflowNode, in map[string]any) (map[string]any, error) {
		x, _ := in["accumulated"].(map[string]any)["x"].(float64)
		return map[string]any{"y": x * 2}, nil
	}})

	res := startSession(t, c, "wf_parent", map[string]any{"x": 2})
	if res.Status != "completed" {
		t.Fatalf("status = %s (%s)", res.Status, res.Reason)
	}
	research, ok := res.Output["research"].(map[string]any)
	if !ok || research["y"] != 4.0 {
		t.Fatalf("child result not stored under output key: %v", res.Output)
	}
	if _, leaked := research["x"]; leaked {
		t.Fatalf("unchanged input keys should not be copied back: %v", research)
	}
	childID, _ := research["_session_id"].(string)
	child, err := sm.Get(context.Background(), childID)
	if err != nil {
		t.Fatalf("child session: %v", err)
	}
	if child.ParentSessionID != res.SessionID || child.SessionType != string(types.SessionTypeSubagent) || child.Status != "completed" {
		t.Fatalf("child session not linked/finished: %+v", child)
	}
	// The edge into "ok" depends on research.y, so reaching it proves the
	// condition saw the child's result.
	var reachedOK int
	_ = db.QueryRow(`SELECT COUNT(*) FROM public.try_run_events WHERE session_id = $1 AND workflow_node_id = 'wf_parent/ok'`, res.SessionID).Scan(&reachedOK)
	if reachedOK != 1 {
		t.Fatal("edge on the subagent's output was not taken")
	}
}

func TestSubagentFailurePropagates(t *testing.T) {
	db := testdb.Open(t)
	seedWorkflow(t, db, "wf_bad_child", "boom", []testNode{{id: "boom", nodeType: "boom"}}, nil)
	seedWorkflow(t, db, "wf_p", "sub", []testNode{{id: "sub", nodeType: NodeTypeSubagent, config: `{"workflow_id":"wf_bad_child","output_key":"r"}`}}, nil)

	c, _, _ := newTestController(db)
	c.RegisterExecutor(funcExecutor{nodeType: "boom", fn: func(context.Context, types.WorkflowNode, map[string]any) (map[string]any, error) {
		return nil, errors.New("child exploded")
	}})
	res := startSession(t, c, "wf_p", nil)
	if res.Status != "failed" || !strings.Contains(res.Reason, "child exploded") {
		t.Fatalf("want failed with child's reason, got %s: %s", res.Status, res.Reason)
	}
}

func TestSubagentDepthLimit(t *testing.T) {
	db := testdb.Open(t)
	// A workflow that calls itself forever.
	seedWorkflow(t, db, "wf_loop", "again", []testNode{{id: "again", nodeType: NodeTypeSubagent, config: `{"workflow_id":"wf_loop"}`}}, nil)

	c, _, _ := newTestController(db)
	c.SetMaxSubagentDepth(3)
	res := startSession(t, c, "wf_loop", nil)
	if res.Status != "failed" || !strings.Contains(res.Reason, "deeper than 3") {
		t.Fatalf("want depth-limit failure, got %s: %s", res.Status, res.Reason)
	}
	var sessions int
	_ = db.QueryRow(`SELECT COUNT(*) FROM public.agent_sessions`).Scan(&sessions)
	if sessions != 4 { // the root plus three nested children
		t.Fatalf("expected 4 sessions, got %d", sessions)
	}
}

func TestParallelNodeRunsBranchesConcurrentlyWithOwnModels(t *testing.T) {
	db := testdb.Open(t)
	seedWorkflow(t, db, "wf_review", "summarize", []testNode{{id: "summarize", nodeType: "summarize"}}, nil)
	seedWorkflow(t, db, "wf_par", "fan", []testNode{
		{id: "fan", nodeType: NodeTypeParallel, config: `{"branches":[
			{"key":"fast","agent_definition_id":"writer","model":"small-model"},
			{"key":"careful","agent_definition_id":"writer","model":"big-model","config":{"temperature":0}},
			{"key":"review","workflow_id":"wf_review"}
		]}`},
		{id: "pick", nodeType: "noop"},
	}, []testEdge{{from: "fan", to: "pick", cond: "careful.score > fast.score && review.summary == 'ok'"}})

	c, _, _ := newTestController(db)
	c.RegisterExecutor(noopExecutor("noop"))
	c.RegisterExecutor(funcExecutor{nodeType: "summarize", fn: func(context.Context, types.WorkflowNode, map[string]any) (map[string]any, error) {
		return map[string]any{"summary": "ok"}, nil
	}})

	// The two agent branches must overlap in time to pass the barrier.
	var barrier sync.WaitGroup
	barrier.Add(2)
	c.RegisterExecutor(funcExecutor{nodeType: "agent", fn: func(_ context.Context, node types.WorkflowNode, in map[string]any) (map[string]any, error) {
		cfg, _ := types.ParseNodeConfig(node.ConfigJSON)
		barrier.Done()
		done := make(chan struct{})
		go func() { barrier.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return nil, errors.New("branches did not run concurrently")
		}
		score := 1.0
		if cfg.Model == "big-model" {
			score = 2.0
		}
		return map[string]any{"model": cfg.Model, "score": score, "temp_set": cfg.Temperature != nil, "node": node.WorkflowNodeID, "input_x": in["accumulated"].(map[string]any)["x"]}, nil
	}})

	res := startSession(t, c, "wf_par", map[string]any{"x": "shared"})
	if res.Status != "completed" {
		t.Fatalf("status = %s (%s)", res.Status, res.Reason)
	}
	fast := res.Output["fast"].(map[string]any)
	careful := res.Output["careful"].(map[string]any)
	if fast["model"] != "small-model" || careful["model"] != "big-model" {
		t.Fatalf("branches did not get their own models: fast=%v careful=%v", fast, careful)
	}
	if careful["temp_set"] != true || fast["temp_set"] != false {
		t.Fatal("per-branch config not passed through")
	}
	if fast["node"] != "wf_par/fan/fast" || fast["input_x"] != "shared" {
		t.Fatalf("branch node identity/input wrong: %v", fast)
	}
	if review, _ := res.Output["review"].(map[string]any); review["summary"] != "ok" {
		t.Fatalf("workflow branch result missing: %v", res.Output["review"])
	}

	var branchRuns int
	_ = db.QueryRow(`SELECT COUNT(*) FROM public.try_run_events WHERE session_id = $1 AND workflow_node_id LIKE 'wf_par/fan/%' AND status = 'completed'`, res.SessionID).Scan(&branchRuns)
	if branchRuns != 2 {
		t.Fatalf("each agent branch should be recorded as its own try-run, got %d", branchRuns)
	}
	var reachedPick int
	_ = db.QueryRow(`SELECT COUNT(*) FROM public.try_run_events WHERE session_id = $1 AND workflow_node_id = 'wf_par/pick'`, res.SessionID).Scan(&reachedPick)
	if reachedPick != 1 {
		t.Fatal("edge comparing branch outputs was not taken")
	}
}

func parallelWithFailure(t *testing.T, failFast bool) *RunResult {
	t.Helper()
	db := testdb.Open(t)
	cfg, _ := json.Marshal(types.NodeConfig{FailFast: failFast, Branches: []types.BranchConfig{
		{Key: "good", AgentDefinitionID: "good"},
		{Key: "bad", AgentDefinitionID: "bad"},
	}})
	seedWorkflow(t, db, "wf_pf", "fan", []testNode{{id: "fan", nodeType: NodeTypeParallel, config: string(cfg)}}, nil)
	c, _, _ := newTestController(db)
	c.RegisterExecutor(funcExecutor{nodeType: "agent", fn: func(ctx context.Context, node types.WorkflowNode, _ map[string]any) (map[string]any, error) {
		if node.AgentDefinitionID == "bad" {
			return nil, errors.New("bad branch failed")
		}
		if failFast {
			<-ctx.Done() // only finishes when fail-fast cancels it
			return nil, ctx.Err()
		}
		return map[string]any{"ok": true}, nil
	}})
	return startSession(t, c, "wf_pf", nil)
}

func TestParallelNodeCollectsBranchErrors(t *testing.T) {
	res := parallelWithFailure(t, false)
	if res.Status != "completed" {
		t.Fatalf("partial failure should not fail the node: %s (%s)", res.Status, res.Reason)
	}
	errs, _ := res.Output["branch_errors"].(map[string]any)
	if !strings.Contains(errs["bad"].(string), "bad branch failed") {
		t.Fatalf("branch_errors = %v", res.Output["branch_errors"])
	}
	if good, _ := res.Output["good"].(map[string]any); good["ok"] != true {
		t.Fatalf("successful branch output missing: %v", res.Output)
	}
}

func TestParallelNodeFailFastCancelsOtherBranches(t *testing.T) {
	res := parallelWithFailure(t, true)
	if res.Status != "failed" || !strings.Contains(res.Reason, "bad branch failed") {
		t.Fatalf("want fail-fast failure, got %s: %s", res.Status, res.Reason)
	}
	if strings.Contains(res.Reason, "branch good") {
		t.Fatalf("cancelled branch should not be reported as the cause: %s", res.Reason)
	}
}

func TestAgentToolRunsWorkflowAsSubagent(t *testing.T) {
	db := testdb.Open(t)
	seedWorkflow(t, db, "wf_translator", "translate", []testNode{{id: "translate", nodeType: "translate"}}, nil)
	seedWorkflow(t, db, "wf_main", "delegate", []testNode{{id: "delegate", nodeType: "delegate"}}, nil)

	c, sm, _ := newTestController(db)
	c.RegisterExecutor(funcExecutor{nodeType: "translate", fn: func(_ context.Context, _ types.WorkflowNode, in map[string]any) (map[string]any, error) {
		acc := in["accumulated"].(map[string]any)
		return map[string]any{"translation": strings.ToUpper(acc["task"].(string)) + "/" + acc["lang"].(string)}, nil
	}})
	translator, err := c.AgentTool("translator", "Translates text", "wf_translator")
	if err != nil {
		t.Fatal(err)
	}
	// Stand-in for a model deciding to call the tool.
	c.RegisterExecutor(funcExecutor{nodeType: "delegate", fn: func(ctx context.Context, node types.WorkflowNode, _ map[string]any) (map[string]any, error) {
		out, err := translator.Call(tools.WithCallContext(ctx, testSession(ctx), node.WorkflowNodeID),
			json.RawMessage(`{"task":"hello","context":{"lang":"fr"}}`))
		if err != nil {
			return nil, err
		}
		return map[string]any{"delegated": out}, nil
	}})

	res := startSession(t, c, "wf_main", nil)
	if res.Status != "completed" {
		t.Fatalf("status = %s (%s)", res.Status, res.Reason)
	}
	got := res.Output["delegated"].(map[string]any)
	if got["translation"] != "HELLO/fr" {
		t.Fatalf("tool result = %v", got)
	}
	child, err := sm.Get(context.Background(), got["_session_id"].(string))
	if err != nil || child.ParentSessionID != res.SessionID {
		t.Fatalf("agent tool should run as a child session: %+v %v", child, err)
	}

	if _, err := translator.Call(context.Background(), json.RawMessage(`{"task":"x"}`)); err == nil {
		t.Fatal("calling an agent tool outside a session should fail")
	}
}
