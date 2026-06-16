package modelrouter

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// LLMRequest is the unified request format for all providers.
type LLMRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	JSONMode    bool      `json:"json_mode,omitempty"`
}

// Message represents a single turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LLMResponse is the unified response format.
type LLMResponse struct {
	Content string  `json:"content"`
	Meta    LLMMeta `json:"meta"`
}

// LLMMeta contains metadata about the response.
type LLMMeta struct {
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	LatencyMs    int64  `json:"latency_ms"`
}

// Provider is the interface every LLM provider must implement.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req LLMRequest) (*LLMResponse, error)
	Models() []string
	HealthCheck(ctx context.Context) error
}

// Router dispatches LLM calls to the appropriate provider with health-aware failover.
type Router struct {
	mu        sync.RWMutex
	providers map[string]Provider        // name → provider
	modelMap  map[string]Provider        // model → provider
	health    map[string]*ProviderHealth // name → health status
}

// ProviderHealth tracks the health of a provider.
type ProviderHealth struct {
	Status              string    `json:"status"` // "healthy", "degraded", "offline"
	LastCheck           time.Time `json:"last_check"`
	ErrorMsg            string    `json:"error_msg,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

// NewRouter creates a new model router.
func NewRouter() *Router {
	return &Router{
		providers: make(map[string]Provider),
		modelMap:  make(map[string]Provider),
		health:    make(map[string]*ProviderHealth),
	}
}

// Register adds a provider and its supported models.
func (r *Router) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
	r.health[p.Name()] = &ProviderHealth{Status: "unknown", LastCheck: time.Now()}
	for _, m := range p.Models() {
		r.modelMap[m] = p
	}
	log.Printf("[modelrouter] registered provider=%s models=%v", p.Name(), p.Models())
}

// Complete routes a request to the best available provider for the requested model.
func (r *Router) Complete(ctx context.Context, req LLMRequest) (*LLMResponse, error) {
	start := time.Now()
	p, err := r.resolveProvider(req.Model)
	if err != nil {
		return nil, err
	}
	resp, err := p.Complete(ctx, req)
	if err != nil {
		// Mark provider as potentially degraded
		r.recordFailure(p.Name(), err)
		return nil, fmt.Errorf("provider %s: %w", p.Name(), err)
	}
	r.recordSuccess(p.Name())
	resp.Meta.LatencyMs = time.Since(start).Milliseconds()
	resp.Meta.Provider = p.Name()
	return resp, nil
}

// resolveProvider finds the provider for a model, with failover.
func (r *Router) resolveProvider(model string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Try primary provider for model
	if p, ok := r.modelMap[model]; ok {
		if h := r.health[p.Name()]; h != nil && h.Status != "offline" {
			return p, nil
		}
	}

	// Failover: prefer a non-offline provider that advertises this model (so we
	// don't route, say, llama3.1 to a remote whose model set doesn't include it);
	// otherwise fall back to any healthy provider as a last resort.
	var anyHealthy Provider
	for name, p := range r.providers {
		h := r.health[name]
		if h == nil || h.Status == "offline" {
			continue
		}
		if providerServes(p, model) {
			return p, nil
		}
		if anyHealthy == nil {
			anyHealthy = p
		}
	}
	if anyHealthy != nil {
		return anyHealthy, nil
	}
	return nil, fmt.Errorf("no healthy provider available for model %q", model)
}

// providerServes reports whether p advertises support for model.
func providerServes(p Provider, model string) bool {
	for _, m := range p.Models() {
		if m == model {
			return true
		}
	}
	return false
}

// HealthStatuses returns the current health of all registered providers.
func (r *Router) HealthStatuses() map[string]*ProviderHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*ProviderHealth, len(r.health))
	for k, v := range r.health {
		out[k] = v
	}
	return out
}

// ProviderModels returns the models registered for each provider, keyed by
// provider name. Used to enrich the health response for the chat model picker.
func (r *Router) ProviderModels() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string][]string, len(r.providers))
	for name, p := range r.providers {
		out[name] = p.Models()
	}
	return out
}

// RunHealthChecks performs health checks on all providers.
func (r *Router) RunHealthChecks(ctx context.Context) {
	var wg sync.WaitGroup
	for name, p := range r.providers {
		wg.Add(1)
		go func(n string, prov Provider) {
			defer wg.Done()
			if err := prov.HealthCheck(ctx); err != nil {
				r.recordFailure(n, err)
				log.Printf("[modelrouter] health FAIL provider=%s err=%v", n, err)
			} else {
				r.recordSuccess(n)
				log.Printf("[modelrouter] health OK provider=%s", n)
			}
		}(name, p)
	}
	wg.Wait()
}

func (r *Router) recordFailure(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.health[name]
	if h == nil {
		return
	}
	h.LastCheck = time.Now()
	h.ConsecutiveFailures++
	h.ErrorMsg = err.Error()
	if h.ConsecutiveFailures >= 3 {
		h.Status = "offline"
	} else if h.ConsecutiveFailures >= 1 {
		h.Status = "degraded"
	}
}

func (r *Router) recordSuccess(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.health[name]
	if h == nil {
		return
	}
	h.LastCheck = time.Now()
	h.ConsecutiveFailures = 0
	h.Status = "healthy"
	h.ErrorMsg = ""
}
