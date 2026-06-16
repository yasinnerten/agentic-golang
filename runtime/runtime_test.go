package runtime

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

type flakyProvider struct {
	name     string
	failures int
	calls    int
	response string
}

func (f *flakyProvider) Name() string { return f.name }

func (f *flakyProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
	f.calls++
	if f.calls <= f.failures {
		return CompletionResponse{}, errors.New("temporary failure")
	}
	return CompletionResponse{Text: f.response, CostUSD: 0.001}, nil
}

func newTestRuntime(t *testing.T, providers Providers) *Runtime {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := New(db, providers)
	if err := r.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAgent(context.Background(), AgentSpec{Name: "a1", RetryPolicy: RetryPolicy{MaxAttempts: 2}, SemanticThreshold: 0.3}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunNextTask_ProviderFailover(t *testing.T) {
	r := newTestRuntime(t, Providers{
		NewStaticProvider("p1", true, ""),
		NewStaticProvider("p2", false, "ok"),
	})
	if _, err := r.EnqueueTask(context.Background(), "a1", "hello there"); err != nil {
		t.Fatal(err)
	}
	res, err := r.RunNextTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Status != "completed" {
		t.Fatalf("expected completed task, got %+v", res)
	}
	if res.Provider != "p2" {
		t.Fatalf("expected failover provider p2, got %q", res.Provider)
	}
}

func TestRunNextTask_RetryPolicy(t *testing.T) {
	flaky := &flakyProvider{name: "flaky", failures: 1, response: "retry-ok"}
	r := newTestRuntime(t, Providers{flaky})
	if _, err := r.EnqueueTask(context.Background(), "a1", "retry me"); err != nil {
		t.Fatal(err)
	}
	res, err := r.RunNextTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "completed" || res.Output != "retry-ok" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Attempts != 2 {
		t.Fatalf("expected 2 attempts due to retry policy, got %d", res.Attempts)
	}
}

func TestTwoTierSemanticCache(t *testing.T) {
	flaky := &flakyProvider{name: "p", failures: 0, response: "cached"}
	r := newTestRuntime(t, Providers{flaky})
	ctx := context.Background()
	if _, err := r.EnqueueTask(ctx, "a1", "book me a flight to paris"); err != nil {
		t.Fatal(err)
	}
	res1, err := r.RunNextTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res1.CacheTier != "miss" {
		t.Fatalf("expected first call cache miss, got %s", res1.CacheTier)
	}

	if _, err := r.EnqueueTask(ctx, "a1", "book flight paris please"); err != nil {
		t.Fatal(err)
	}
	res2, err := r.RunNextTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res2.CacheTier != "l1" {
		t.Fatalf("expected l1 cache hit, got %s", res2.CacheTier)
	}

	r2 := New(r.db, Providers{&flakyProvider{name: "never", failures: 100, response: "x"}})
	r2.router.l1 = newMemorySemanticCache(1)
	if _, err := r2.EnqueueTask(ctx, "a1", "please flight booking to paris"); err != nil {
		t.Fatal(err)
	}
	res3, err := r2.RunNextTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res3.CacheTier != "l2" {
		t.Fatalf("expected l2 cache hit, got %s", res3.CacheTier)
	}
}
