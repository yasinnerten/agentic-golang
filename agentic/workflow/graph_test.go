package workflow

import (
	"strings"
	"testing"

	"github.com/yasinnerten/agentic-golang/services/rulesengine"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

func graphWithEdges(edges ...types.WorkflowEdge) *types.WorkflowGraph {
	return &types.WorkflowGraph{Edges: edges}
}

func edge(id, cond string, priority int) types.WorkflowEdge {
	return types.WorkflowEdge{WorkflowEdgeID: id, FromNodeID: "a", ToNodeID: "to_" + id, ConditionExpression: cond, Priority: priority}
}

func TestSelectNextEdgePicksFirstMatch(t *testing.T) {
	g := graphWithEdges(edge("hi", "x > 5", 20), edge("default", "", 10))
	e, err := SelectNextEdge(rulesengine.NewEngine(), g, "a", map[string]any{"x": 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e == nil || e.WorkflowEdgeID != "hi" {
		t.Fatalf("expected edge hi, got %+v", e)
	}
}

func TestSelectNextEdgeReportsBrokenConditionButStillRoutes(t *testing.T) {
	g := graphWithEdges(edge("broken", "missing_var > 5", 20), edge("default", "", 10))
	e, err := SelectNextEdge(rulesengine.NewEngine(), g, "a", map[string]any{})
	if e == nil || e.WorkflowEdgeID != "default" {
		t.Fatalf("expected fallback to default edge, got %+v", e)
	}
	if err == nil || !strings.Contains(err.Error(), "edge broken") {
		t.Fatalf("expected error naming the broken edge, got %v", err)
	}
}

func TestSelectNextEdgeNoMatchWithErrors(t *testing.T) {
	g := graphWithEdges(edge("broken", "missing_var > 5", 20), edge("false", "1 == 2", 10))
	e, err := SelectNextEdge(rulesengine.NewEngine(), g, "a", map[string]any{})
	if e != nil {
		t.Fatalf("expected no edge, got %+v", e)
	}
	if err == nil {
		t.Fatal("expected the broken condition to be reported")
	}
}

func TestSelectNextEdgeTerminalNode(t *testing.T) {
	e, err := SelectNextEdge(rulesengine.NewEngine(), graphWithEdges(), "a", map[string]any{})
	if e != nil || err != nil {
		t.Fatalf("terminal node: expected nil, nil; got %+v, %v", e, err)
	}
}

func TestSelectNextEdgeFalseConditionsAreNotErrors(t *testing.T) {
	g := graphWithEdges(edge("no", "x > 5", 10))
	e, err := SelectNextEdge(rulesengine.NewEngine(), g, "a", map[string]any{"x": 1})
	if e != nil || err != nil {
		t.Fatalf("expected nil, nil; got %+v, %v", e, err)
	}
}
