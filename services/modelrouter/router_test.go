package modelrouter

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockProvider is a test double for the Provider interface.
type mockProvider struct {
	name        string
	models      []string
	healthy     bool
	completeErr error
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Complete(ctx context.Context, req LLMRequest) (*LLMResponse, error) {
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return &LLMResponse{Content: "mock response", Meta: LLMMeta{Provider: m.name}}, nil
}
func (m *mockProvider) Models() []string { return m.models }
func (m *mockProvider) HealthCheck(ctx context.Context) error {
	if !m.healthy {
		return errors.New("unhealthy")
	}
	return nil
}

func TestNewRouter(t *testing.T) {
	r := NewRouter()
	if r == nil {
		t.Fatal("NewRouter returned nil")
	}
	if len(r.providers) != 0 {
		t.Fatalf("Expected 0 providers, got %d", len(r.providers))
	}
}

func TestRegisterAndResolve(t *testing.T) {
	r := NewRouter()
	p := &mockProvider{name: "test_provider", models: []string{"model-a", "model-b"}, healthy: true}
	r.Register(p)

	if len(r.providers) != 1 {
		t.Fatalf("Expected 1 provider, got %d", len(r.providers))
	}

	// Resolve for registered model
	resolved, err := r.resolveProvider("model-a")
	if err != nil {
		t.Fatalf("resolveProvider failed: %v", err)
	}
	if resolved.Name() != "test_provider" {
		t.Errorf("Expected provider 'test_provider', got %q", resolved.Name())
	}

	// Resolve for unregistered model should still find the healthy provider
	resolved, err = r.resolveProvider("unknown-model")
	if err != nil {
		t.Fatalf("resolveProvider failed for unknown model: %v", err)
	}
	if resolved.Name() != "test_provider" {
		t.Errorf("Expected fallback to 'test_provider', got %q", resolved.Name())
	}
}

func TestResolveProviderNoHealthy(t *testing.T) {
	r := NewRouter()
	p := &mockProvider{name: "unhealthy", models: []string{"model-x"}, healthy: false}
	r.Register(p)

	// Mark as offline manually
	r.recordFailure("unhealthy", errors.New("down"))
	r.recordFailure("unhealthy", errors.New("down"))
	r.recordFailure("unhealthy", errors.New("down"))

	_, err := r.resolveProvider("model-x")
	if err == nil {
		t.Fatal("Expected error when no healthy providers available")
	}
}

func TestCompleteSuccess(t *testing.T) {
	r := NewRouter()
	p := &mockProvider{name: "openai", models: []string{"gpt-4"}, healthy: true}
	r.Register(p)

	resp, err := r.Complete(context.Background(), LLMRequest{Model: "gpt-4"})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != "mock response" {
		t.Errorf("Expected 'mock response', got %q", resp.Content)
	}
	if resp.Meta.Provider != "openai" {
		t.Errorf("Expected provider 'openai', got %q", resp.Meta.Provider)
	}
}

func TestCompleteFailure(t *testing.T) {
	r := NewRouter()
	p := &mockProvider{name: "failing", models: []string{"model"}, healthy: true, completeErr: errors.New("boom")}
	r.Register(p)

	_, err := r.Complete(context.Background(), LLMRequest{Model: "model"})
	if err == nil {
		t.Fatal("Expected error from failing provider")
	}

	// Provider should be marked as degraded
	r.mu.RLock()
	h := r.health["failing"]
	r.mu.RUnlock()
	if h == nil {
		t.Fatal("Health status not recorded")
	}
	if h.ConsecutiveFailures != 1 {
		t.Errorf("Expected 1 failure, got %d", h.ConsecutiveFailures)
	}
}

func TestHealthStatuses(t *testing.T) {
	r := NewRouter()
	p1 := &mockProvider{name: "healthy", models: []string{"m1"}, healthy: true}
	p2 := &mockProvider{name: "degraded", models: []string{"m2"}, healthy: true}
	r.Register(p1)
	r.Register(p2)

	// Initial status is "unknown" until first check
	// Run health checks first
	r.RunHealthChecks(context.Background())

	statuses := r.HealthStatuses()
	if len(statuses) != 2 {
		t.Fatalf("Expected 2 health statuses, got %d", len(statuses))
	}

	if statuses["healthy"].Status != "healthy" {
		t.Errorf("Expected 'healthy' status, got %q", statuses["healthy"].Status)
	}

	// Now degrade p2
	r.recordFailure("degraded", errors.New("slow"))

	statuses = r.HealthStatuses()
	if statuses["degraded"].Status != "degraded" {
		t.Errorf("Expected 'degraded' status, got %q", statuses["degraded"].Status)
	}
}

func TestRunHealthChecks(t *testing.T) {
	r := NewRouter()
	p := &mockProvider{name: "checker", models: []string{"m1"}, healthy: true}
	r.Register(p)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r.RunHealthChecks(ctx)

	statuses := r.HealthStatuses()
	if statuses["checker"].Status != "healthy" {
		t.Errorf("Expected healthy after check, got %q", statuses["checker"].Status)
	}
}

func TestMultipleProvidersFailover(t *testing.T) {
	r := NewRouter()
	p1 := &mockProvider{name: "primary", models: []string{"gpt-4"}, healthy: true, completeErr: errors.New("primary down")}
	p2 := &mockProvider{name: "backup", models: []string{"gpt-3.5"}, healthy: true}
	r.Register(p1)
	r.Register(p2)

	// Simulate primary going offline
	r.recordFailure("primary", errors.New("down"))
	r.recordFailure("primary", errors.New("down"))
	r.recordFailure("primary", errors.New("down"))

	// Request for primary model should failover to backup
	resolved, err := r.resolveProvider("gpt-4")
	if err != nil {
		t.Fatalf("resolveProvider failed: %v", err)
	}
	// gpt-4 maps to primary, but primary is offline, so we failover to any healthy provider
	if resolved.Name() != "backup" {
		t.Errorf("Expected failover to 'backup', got %q", resolved.Name())
	}
}

func TestRecordSuccess(t *testing.T) {
	r := NewRouter()
	p := &mockProvider{name: "recovery", models: []string{"m1"}, healthy: true}
	r.Register(p)

	// Fail a few times
	r.recordFailure("recovery", errors.New("err"))
	r.recordFailure("recovery", errors.New("err"))

	// Then succeed
	r.recordSuccess("recovery")

	statuses := r.HealthStatuses()
	if statuses["recovery"].Status != "healthy" {
		t.Errorf("Expected healthy after success, got %q", statuses["recovery"].Status)
	}
	if statuses["recovery"].ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 failures after success, got %d", statuses["recovery"].ConsecutiveFailures)
	}
}
