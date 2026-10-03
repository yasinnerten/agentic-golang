package loopcontroller

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/yasinnerten/agentic-golang/agentic/memory"
	"github.com/yasinnerten/agentic-golang/agentic/sessions"
	"github.com/yasinnerten/agentic-golang/agentic/workflow"
	"github.com/yasinnerten/agentic-golang/internal/testdb"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

// funcExecutor adapts a function to NodeExecutor for tests.
type funcExecutor struct {
	nodeType string
	fn       func(ctx context.Context, node types.WorkflowNode, input map[string]any) (map[string]any, error)
}

func (f funcExecutor) CanHandle(t string) bool { return t == f.nodeType }
func (f funcExecutor) Execute(ctx context.Context, node types.WorkflowNode, input map[string]any, sessionID string) (map[string]any, error) {
	return f.fn(withTestSession(ctx, sessionID), node, input)
}

type testSessionKey struct{}

func withTestSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, testSessionKey{}, id)
}

func testSession(ctx context.Context) string {
	s, _ := ctx.Value(testSessionKey{}).(string)
	return s
}

type testNode struct{ id, nodeType, config string }
type testEdge struct {
	from, to, cond string
	priority       int
}

func seedWorkflow(t *testing.T, db *sql.DB, wfID, entry string, nodes []testNode, edges []testEdge) {
	t.Helper()
	testdb.MustExec(t, db, `INSERT INTO public.workflow_definitions (workflow_id, workflow_name, entry_node_id) VALUES ($1, $1, $2)`, wfID, entry)
	for _, n := range nodes {
		cfg := n.config
		if cfg == "" {
			cfg = "{}"
		}
		testdb.MustExec(t, db, `INSERT INTO public.workflow_nodes (workflow_node_id, workflow_id, node_name, node_type, config_json) VALUES ($1, $2, $1, $3, $4)`,
			wfID+"/"+n.id, wfID, n.nodeType, cfg)
	}
	for i, e := range edges {
		testdb.MustExec(t, db, `INSERT INTO public.workflow_edges (workflow_edge_id, workflow_id, from_node_id, to_node_id, condition_expression, priority) VALUES ($1, $2, $3, $4, $5, $6)`,
			fmt.Sprintf("%s/e%d", wfID, i), wfID, wfID+"/"+e.from, wfID+"/"+e.to, e.cond, e.priority)
	}
	// Entry node IDs are stored fully qualified.
	testdb.MustExec(t, db, `UPDATE public.workflow_definitions SET entry_node_id = $1 WHERE workflow_id = $2`, wfID+"/"+entry, wfID)
}

func newTestController(db *sql.DB) (*Controller, *sessions.Manager, *memory.Store) {
	sm := sessions.NewManager(db)
	ms := memory.NewStore(db)
	return NewController(db, sm, ms, workflow.NewBuilder(db)), sm, ms
}

func TestLoopRunsCyclicWorkflowAgainstPostgres(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()

	// work -> check; check loops back to work until count reaches 3, then done.
	seedWorkflow(t, db, "wf_cycle", "work",
		[]testNode{{id: "work", nodeType: "step"}, {id: "check", nodeType: "noop"}, {id: "done", nodeType: "noop"}},
		[]testEdge{
			{from: "work", to: "check"},
			{from: "check", to: "work", cond: "count < 3", priority: 20},
			{from: "check", to: "done", cond: "count >= 3", priority: 10},
		})

	c, sm, _ := newTestController(db)
	c.RegisterExecutor(funcExecutor{nodeType: "step", fn: func(_ context.Context, _ types.WorkflowNode, in map[string]any) (map[string]any, error) {
		acc := in["accumulated"].(map[string]any)
		n, _ := acc["count"].(float64)
		return map[string]any{"count": n + 1}, nil
	}})
	c.RegisterExecutor(funcExecutor{nodeType: "noop", fn: func(context.Context, types.WorkflowNode, map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	}})

	sess, err := sm.Create(ctx, "t1", "test", "", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := c.StartWorkflow(ctx, sess.SessionID, "wf_cycle", map[string]any{"count": 0.0}, LoopConfig{}); err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}

	got, err := sm.Get(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	var steps int
	if err := db.QueryRow(`SELECT COUNT(*) FROM public.try_run_events WHERE session_id = $1 AND workflow_node_id = 'wf_cycle/work'`, sess.SessionID).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if steps != 3 {
		t.Fatalf("work node ran %d times, want 3", steps)
	}
}
