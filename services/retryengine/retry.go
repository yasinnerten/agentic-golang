package retryengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Engine executes retry policies from the database.
type Engine struct {
	db *sql.DB
}

// NewEngine creates a retry engine.
func NewEngine(db *sql.DB) *Engine {
	return &Engine{db: db}
}

// Policy is a loaded retry policy.
type Policy struct {
	PolicyID        string
	MaxAttempts     int
	RetryOnErrors   []string
	BackoffStrategy string // "exponential", "linear", "fixed"
	InitialDelayMs  int
	MaxDelayMs      int
	JitterEnabled   bool
}

// LoadPolicy fetches a retry policy by ID.
func (e *Engine) LoadPolicy(ctx context.Context, policyID string) (*Policy, error) {
	var p Policy
	var errorsJSON []byte
	err := e.db.QueryRowContext(ctx,
		`SELECT retry_policy_id, max_attempts, retry_on_errors_json, backoff_strategy, initial_delay_ms, max_delay_ms, jitter_enabled
		FROM public.retry_policies WHERE retry_policy_id = $1`, policyID).Scan(
		&p.PolicyID, &p.MaxAttempts, &errorsJSON, &p.BackoffStrategy, &p.InitialDelayMs, &p.MaxDelayMs, &p.JitterEnabled)
	if err != nil {
		return nil, fmt.Errorf("load retry policy: %w", err)
	}
	if len(errorsJSON) > 0 {
		_ = json.Unmarshal(errorsJSON, &p.RetryOnErrors)
	}
	return &p, nil
}

// DefaultPolicy returns a built-in default retry policy.
func DefaultPolicy() *Policy {
	return &Policy{
		PolicyID:        "default",
		MaxAttempts:     3,
		RetryOnErrors:   []string{"timeout_error", "rate_limit_error", "llm_error"},
		BackoffStrategy: "exponential",
		InitialDelayMs:  1000,
		MaxDelayMs:      30000,
		JitterEnabled:   true,
	}
}

// Execute runs the given function with retry logic.
func (e *Engine) Execute(ctx context.Context, policy *Policy, fn func() error) error {
	if policy == nil {
		policy = DefaultPolicy()
	}
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if attempt > 1 {
			delay := calculateDelay(policy, attempt-1)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetry(err, policy.RetryOnErrors) {
			return err
		}
	}
	return fmt.Errorf("retry exhausted after %d attempts: %w", policy.MaxAttempts, lastErr)
}

func calculateDelay(policy *Policy, attempt int) time.Duration {
	var delay time.Duration
	switch policy.BackoffStrategy {
	case "exponential":
		delay = time.Duration(policy.InitialDelayMs) * time.Millisecond * time.Duration(math.Pow(2, float64(attempt-1)))
	case "linear":
		delay = time.Duration(policy.InitialDelayMs*attempt) * time.Millisecond
	default:
		delay = time.Duration(policy.InitialDelayMs) * time.Millisecond
	}
	if delay > time.Duration(policy.MaxDelayMs)*time.Millisecond {
		delay = time.Duration(policy.MaxDelayMs) * time.Millisecond
	}
	if policy.JitterEnabled {
		jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
		delay += jitter
	}
	return delay
}

func shouldRetry(err error, retryOn []string) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	for _, e := range retryOn {
		if containsSubstring(errStr, e) {
			return true
		}
	}
	return false
}

func containsSubstring(s, substr string) bool {
	if substr == "" {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
