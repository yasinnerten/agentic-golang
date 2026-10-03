package loopcontroller

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yasinnerten/agentic-golang/shared/types"
)

func TestValidateBranches(t *testing.T) {
	ok := []types.BranchConfig{{Key: "a", AgentDefinitionID: "x"}, {Key: "b_2", WorkflowID: "wf"}}
	if err := validateBranches(ok); err != nil {
		t.Fatalf("valid branches rejected: %v", err)
	}
	bad := map[string][]types.BranchConfig{
		"empty":         nil,
		"bad key":       {{Key: "a-b", AgentDefinitionID: "x"}},
		"reserved":      {{Key: "branch_errors", AgentDefinitionID: "x"}},
		"duplicate":     {{Key: "a", AgentDefinitionID: "x"}, {Key: "a", AgentDefinitionID: "y"}},
		"neither":       {{Key: "a"}},
		"both":          {{Key: "a", AgentDefinitionID: "x", WorkflowID: "wf"}},
		"leading digit": {{Key: "1a", AgentDefinitionID: "x"}},
	}
	for name, b := range bad {
		if err := validateBranches(b); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDefaultOutputKey(t *testing.T) {
	for in, want := range map[string]string{
		"wf_main/research": "research",
		"wf/research-step": "research_step",
		"plain":            "plain",
		"wf/2nd pass":      "_2nd_pass",
		"wf/a/b/c.d":       "c_d",
		"wf/":              "_",
	} {
		if got := defaultOutputKey(in); got != want {
			t.Errorf("defaultOutputKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChildResultKeepsOnlyNewOrChangedKeys(t *testing.T) {
	input := map[string]any{"x": 1, "same": "v", "changed": "old"}
	res := &RunResult{SessionID: "child-1", Output: map[string]any{
		"x": 1.0, "same": "v", "changed": "new", "added": true, "error": "",
	}}
	got := childResult(input, res)
	want := map[string]any{"_session_id": "child-1", "changed": "new", "added": true}
	if len(got) != len(want) {
		t.Fatalf("childResult = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("childResult[%s] = %v, want %v (full: %v)", k, got[k], v, got)
		}
	}
}

func TestJoinBranchErrorsPrefersRealErrors(t *testing.T) {
	err := joinBranchErrors(map[string]error{"b": context.Canceled, "a": errors.New("boom")})
	if !strings.Contains(err.Error(), "branch a: boom") || strings.Contains(err.Error(), "branch b") {
		t.Fatalf("cancellations caused by fail-fast should be hidden: %v", err)
	}
	only := joinBranchErrors(map[string]error{"b": context.Canceled})
	if !errors.Is(only, context.Canceled) {
		t.Fatalf("all-cancelled should still report: %v", only)
	}
}

func TestRunDepthFromContext(t *testing.T) {
	ctx := context.Background()
	if runDepth(ctx) != 0 {
		t.Fatal("bare context should be depth 0")
	}
	ctx = withRunInfo(ctx, runInfo{depth: 3})
	if runDepth(ctx) != 3 {
		t.Fatal("depth not carried")
	}
}
