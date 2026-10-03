// Package tools lets agents call Go functions. A Tool describes itself to the
// model with a JSON Schema and is invoked with the JSON arguments the model
// produced. NewFunc builds a Tool from a typed Go function and derives the
// schema from its argument struct, so the schema and the code cannot drift.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

// Tool is something an agent can call.
type Tool interface {
	// Name is how the model refers to the tool: 1-64 letters, digits, '_' or '-'.
	Name() string
	// Description tells the model when and how to use the tool.
	Description() string
	// Parameters is the JSON Schema (an object schema) for the arguments.
	Parameters() json.RawMessage
	// Call runs the tool. args is the JSON object the model produced. The
	// result is JSON-encoded and handed back to the model.
	Call(ctx context.Context, args json.RawMessage) (any, error)
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidateName reports whether name is acceptable to every supported provider.
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid tool name %q: use 1-64 letters, digits, '_' or '-'", name)
	}
	return nil
}

// Spec converts a Tool into the router's provider-neutral description.
func Spec(t Tool) modelrouter.ToolSpec {
	return modelrouter.ToolSpec{Name: t.Name(), Description: t.Description(), Parameters: t.Parameters()}
}

// Registry holds the tools available to an executor. It is safe for
// concurrent use.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns a registry containing the given tools.
func NewRegistry(ts ...Tool) (*Registry, error) {
	r := &Registry{tools: make(map[string]Tool)}
	for _, t := range ts {
		if err := r.Register(t); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds a tool. Names must be valid and unique.
func (r *Registry) Register(t Tool) error {
	if err := ValidateName(t.Name()); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	if _, dup := r.tools[t.Name()]; dup {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

// Get returns the named tool.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns every registered tool, sorted by name.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Select returns the named tools in the given order. An unknown name is an
// error, so a typo in a node's tool list fails loudly instead of silently
// leaving the agent without the tool.
func (r *Registry) Select(names []string) ([]Tool, error) {
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		t, ok := r.Get(n)
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", n)
		}
		out = append(out, t)
	}
	return out, nil
}

type ctxKey int

const (
	sessionIDKey ctxKey = iota
	nodeIDKey
)

// WithCallContext records which session and workflow node a tool call belongs
// to. The executor sets it before calling tools.
func WithCallContext(ctx context.Context, sessionID, nodeID string) context.Context {
	ctx = context.WithValue(ctx, sessionIDKey, sessionID)
	return context.WithValue(ctx, nodeIDKey, nodeID)
}

// SessionID returns the session the current tool call belongs to, if any.
func SessionID(ctx context.Context) string {
	s, _ := ctx.Value(sessionIDKey).(string)
	return s
}

// NodeID returns the workflow node the current tool call belongs to, if any.
func NodeID(ctx context.Context) string {
	s, _ := ctx.Value(nodeIDKey).(string)
	return s
}
