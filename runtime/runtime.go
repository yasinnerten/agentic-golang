package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type RetryPolicy struct {
	MaxAttempts   int
	BackoffMillis int
}

type AgentSpec struct {
	Name                string
	SystemPrompt        string
	RetryPolicy         RetryPolicy
	SemanticThreshold   float64
	PromptPrefix        string
	ObservabilityPolicy string
}

type CompletionRequest struct {
	AgentName string
	Prompt    string
}

type CompletionResponse struct {
	Text    string
	CostUSD float64
}

type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

type Providers []Provider

type cacheResult struct {
	Text      string
	Provider  string
	CostUSD   float64
	CacheTier string
}

type routeResult struct {
	cacheResult
	Attempts  int
	LatencyMs int64
}

type Runtime struct {
	db     *sql.DB
	router *Router
	now    func() time.Time
}

func New(db *sql.DB, providers Providers) *Runtime {
	return &Runtime{
		db:     db,
		router: NewRouter(db, providers),
		now:    time.Now,
	}
}

func (r *Runtime) InitSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agents (
name TEXT PRIMARY KEY,
system_prompt TEXT NOT NULL DEFAULT '',
prompt_prefix TEXT NOT NULL DEFAULT '',
max_attempts INTEGER NOT NULL DEFAULT 1,
backoff_millis INTEGER NOT NULL DEFAULT 0,
semantic_threshold REAL NOT NULL DEFAULT 0.8,
observability_policy TEXT NOT NULL DEFAULT 'default'
)`,
		`CREATE TABLE IF NOT EXISTS tasks (
id INTEGER PRIMARY KEY AUTOINCREMENT,
agent_name TEXT NOT NULL,
input TEXT NOT NULL,
status TEXT NOT NULL DEFAULT 'pending',
output TEXT NOT NULL DEFAULT '',
error TEXT NOT NULL DEFAULT '',
attempts INTEGER NOT NULL DEFAULT 0,
provider TEXT NOT NULL DEFAULT '',
cache_tier TEXT NOT NULL DEFAULT 'none',
cost_usd REAL NOT NULL DEFAULT 0,
latency_ms INTEGER NOT NULL DEFAULT 0,
created_at TEXT NOT NULL,
updated_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS semantic_cache (
id INTEGER PRIMARY KEY AUTOINCREMENT,
agent_name TEXT NOT NULL,
prompt TEXT NOT NULL,
normalized_prompt TEXT NOT NULL,
response_text TEXT NOT NULL,
provider TEXT NOT NULL,
cost_usd REAL NOT NULL DEFAULT 0,
created_at TEXT NOT NULL
)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status_id ON tasks(status, id)`,
		`CREATE INDEX IF NOT EXISTS idx_semantic_cache_agent ON semantic_cache(agent_name)`,
	}
	for _, stmt := range stmts {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) RegisterAgent(ctx context.Context, spec AgentSpec) error {
	if spec.Name == "" {
		return errors.New("agent name is required")
	}
	if spec.RetryPolicy.MaxAttempts <= 0 {
		spec.RetryPolicy.MaxAttempts = 1
	}
	if spec.SemanticThreshold <= 0 || spec.SemanticThreshold > 1 {
		spec.SemanticThreshold = 0.80
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO agents(name, system_prompt, prompt_prefix, max_attempts, backoff_millis, semantic_threshold, observability_policy)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
system_prompt = excluded.system_prompt,
prompt_prefix = excluded.prompt_prefix,
max_attempts = excluded.max_attempts,
backoff_millis = excluded.backoff_millis,
semantic_threshold = excluded.semantic_threshold,
observability_policy = excluded.observability_policy
`, spec.Name, spec.SystemPrompt, spec.PromptPrefix, spec.RetryPolicy.MaxAttempts, spec.RetryPolicy.BackoffMillis, spec.SemanticThreshold, spec.ObservabilityPolicy)
	return err
}

func (r *Runtime) EnqueueTask(ctx context.Context, agentName, input string) (int64, error) {
	now := r.now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.ExecContext(ctx, `
INSERT INTO tasks(agent_name, input, status, created_at, updated_at)
VALUES(?, ?, 'pending', ?, ?)
`, agentName, input, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type TaskResult struct {
	TaskID    int64
	Status    string
	Output    string
	Error     string
	Provider  string
	CacheTier string
	Attempts  int
	LatencyMs int64
	CostUSD   float64
	AgentName string
	Input     string
}

func (r *Runtime) RunNextTask(ctx context.Context) (*TaskResult, error) {
	var (
		taskID       int64
		agentName    string
		input        string
		systemPrompt string
		promptPrefix string
		maxAttempts  int
		backoffMs    int
		threshold    float64
	)

	err := r.db.QueryRowContext(ctx, `
SELECT id, agent_name, input
FROM tasks
WHERE status = 'pending'
ORDER BY id ASC
LIMIT 1
`).Scan(&taskID, &agentName, &input)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	err = r.db.QueryRowContext(ctx, `
SELECT system_prompt, prompt_prefix, max_attempts, backoff_millis, semantic_threshold
FROM agents
WHERE name = ?
`, agentName).Scan(&systemPrompt, &promptPrefix, &maxAttempts, &backoffMs, &threshold)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("agent %q not found", agentName)
		}
		return nil, err
	}

	fullPrompt := strings.TrimSpace(strings.TrimSpace(systemPrompt+"\n"+promptPrefix) + "\n" + strings.TrimSpace(input))
	res, routeErr := r.router.Route(ctx, CompletionRequest{AgentName: agentName, Prompt: fullPrompt}, RetryPolicy{MaxAttempts: maxAttempts, BackoffMillis: backoffMs}, threshold)

	now := r.now().UTC().Format(time.RFC3339Nano)
	if routeErr != nil {
		_, updErr := r.db.ExecContext(ctx, `
UPDATE tasks
SET status = 'failed', error = ?, attempts = ?, latency_ms = ?, updated_at = ?
WHERE id = ?
`, routeErr.Error(), res.Attempts, res.LatencyMs, now, taskID)
		if updErr != nil {
			return nil, updErr
		}
		return &TaskResult{TaskID: taskID, Status: "failed", Error: routeErr.Error(), Attempts: res.Attempts, LatencyMs: res.LatencyMs, AgentName: agentName, Input: input}, nil
	}

	_, err = r.db.ExecContext(ctx, `
UPDATE tasks
SET status = 'completed', output = ?, attempts = ?, provider = ?, cache_tier = ?, cost_usd = ?, latency_ms = ?, updated_at = ?
WHERE id = ?
`, res.Text, res.Attempts, res.Provider, res.CacheTier, res.CostUSD, res.LatencyMs, now, taskID)
	if err != nil {
		return nil, err
	}

	return &TaskResult{
		TaskID:    taskID,
		Status:    "completed",
		Output:    res.Text,
		Provider:  res.Provider,
		CacheTier: res.CacheTier,
		Attempts:  res.Attempts,
		LatencyMs: res.LatencyMs,
		CostUSD:   res.CostUSD,
		AgentName: agentName,
		Input:     input,
	}, nil
}

type Router struct {
	db        *sql.DB
	providers Providers
	l1        *memorySemanticCache
	mu        sync.Mutex
}

func NewRouter(db *sql.DB, providers Providers) *Router {
	return &Router{db: db, providers: providers, l1: newMemorySemanticCache(128)}
}

func (r *Router) Route(ctx context.Context, req CompletionRequest, policy RetryPolicy, threshold float64) (routeResult, error) {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 1
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.80
	}
	start := time.Now()

	if entry, ok := r.l1.Lookup(req.AgentName, req.Prompt, threshold); ok {
		return routeResult{cacheResult: cacheResult{Text: entry.Text, Provider: entry.Provider, CostUSD: 0, CacheTier: "l1"}, Attempts: 0, LatencyMs: time.Since(start).Milliseconds()}, nil
	}

	if entry, ok, err := r.lookupL2(ctx, req.AgentName, req.Prompt, threshold); err != nil {
		return routeResult{Attempts: 0, LatencyMs: time.Since(start).Milliseconds()}, err
	} else if ok {
		r.l1.Store(req.AgentName, req.Prompt, entry.Text, entry.Provider)
		return routeResult{cacheResult: cacheResult{Text: entry.Text, Provider: entry.Provider, CostUSD: 0, CacheTier: "l2"}, Attempts: 0, LatencyMs: time.Since(start).Milliseconds()}, nil
	}

	if len(r.providers) == 0 {
		return routeResult{Attempts: policy.MaxAttempts, LatencyMs: time.Since(start).Milliseconds()}, errors.New("no providers configured")
	}

	attempts := 0
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		attempts = attempt
		for _, provider := range r.providers {
			resp, err := provider.Complete(ctx, req)
			if err != nil {
				lastErr = err
				continue
			}
			r.l1.Store(req.AgentName, req.Prompt, resp.Text, provider.Name())
			if err := r.storeL2(ctx, req.AgentName, req.Prompt, resp.Text, provider.Name(), resp.CostUSD); err != nil {
				return routeResult{Attempts: attempt, LatencyMs: time.Since(start).Milliseconds()}, err
			}
			return routeResult{cacheResult: cacheResult{Text: resp.Text, Provider: provider.Name(), CostUSD: resp.CostUSD, CacheTier: "miss"}, Attempts: attempt, LatencyMs: time.Since(start).Milliseconds()}, nil
		}
		if attempt < policy.MaxAttempts && policy.BackoffMillis > 0 {
			select {
			case <-ctx.Done():
				return routeResult{Attempts: attempts, LatencyMs: time.Since(start).Milliseconds()}, ctx.Err()
			case <-time.After(time.Duration(policy.BackoffMillis) * time.Millisecond):
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("all providers failed")
	}
	return routeResult{Attempts: attempts, LatencyMs: time.Since(start).Milliseconds()}, fmt.Errorf("routing failed after %d attempt(s): %w", attempts, lastErr)
}

type l2CacheEntry struct {
	Text     string
	Provider string
}

func (r *Router) lookupL2(ctx context.Context, agentName, prompt string, threshold float64) (l2CacheEntry, bool, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT normalized_prompt, response_text, provider
FROM semantic_cache
WHERE agent_name = ?
ORDER BY id DESC
LIMIT 256
`, agentName)
	if err != nil {
		return l2CacheEntry{}, false, err
	}
	defer rows.Close()

	target := normalize(prompt)
	bestScore := 0.0
	best := l2CacheEntry{}
	for rows.Next() {
		var normalized, text, provider string
		if err := rows.Scan(&normalized, &text, &provider); err != nil {
			return l2CacheEntry{}, false, err
		}
		score := jaccard(normalized, target)
		if score >= threshold && score > bestScore {
			bestScore = score
			best = l2CacheEntry{Text: text, Provider: provider}
		}
	}
	if err := rows.Err(); err != nil {
		return l2CacheEntry{}, false, err
	}
	if bestScore == 0 {
		return l2CacheEntry{}, false, nil
	}
	return best, true, nil
}

func (r *Router) storeL2(ctx context.Context, agentName, prompt, responseText, provider string, cost float64) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO semantic_cache(agent_name, prompt, normalized_prompt, response_text, provider, cost_usd, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
`, agentName, prompt, normalize(prompt), responseText, provider, cost, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type StaticProvider struct {
	name       string
	alwaysFail bool
	response   string
}

func NewStaticProvider(name string, alwaysFail bool, response string) *StaticProvider {
	return &StaticProvider{name: name, alwaysFail: alwaysFail, response: response}
}

func (s *StaticProvider) Name() string { return s.name }

func (s *StaticProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
	if s.alwaysFail {
		return CompletionResponse{}, errors.New("provider failure")
	}
	return CompletionResponse{Text: s.response, CostUSD: 0.0005}, nil
}
