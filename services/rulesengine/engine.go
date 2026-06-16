package rulesengine

import (
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// Engine evaluates CEL expressions against a typed environment.
type Engine struct {
	options []cel.EnvOption
}

// NewEngine creates a rules engine with standard declarations.
func NewEngine() *Engine {
	return &Engine{
		options: []cel.EnvOption{
			cel.Declarations(
				decls.NewVar("env", decls.NewMapType(decls.String, decls.Dyn)),
				decls.NewVar("input", decls.NewMapType(decls.String, decls.Dyn)),
				decls.NewVar("state", decls.NewMapType(decls.String, decls.Dyn)),
				decls.NewVar("answers", decls.NewMapType(decls.String, decls.Dyn)),
				decls.NewVar("evidence", decls.NewMapType(decls.String, decls.Dyn)),
				decls.NewVar("route", decls.String),
				decls.NewVar("risk_level", decls.String),
				decls.NewVar("high_risk_status", decls.String),
				decls.NewVar("prohibited_status", decls.String),
				decls.NewVar("transparency_status", decls.String),
				decls.NewVar("systemic_risk_status", decls.String),
				decls.NewVar("gpai_status", decls.String),
				decls.NewVar("object_type", decls.String),
				decls.NewVar("actor_role", decls.String),
				decls.NewVar("confidence", decls.Double),
			),
		},
	}
}

// EvaluateBool parses and evaluates a CEL expression against the given bindings.
// Returns the boolean result, or false if the expression evaluates to a non-bool / fails.
func (e *Engine) EvaluateBool(expression string, bindings map[string]any) (bool, error) {
	if expression == "" {
		return true, nil // empty condition means always true
	}

	env, err := cel.NewEnv(e.options...)
	if err != nil {
		return false, fmt.Errorf("cel env: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("cel compile: %w", issues.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("cel program: %w", err)
	}

	out, _, err := prg.Eval(bindings)
	if err != nil {
		return false, fmt.Errorf("cel eval: %w", err)
	}

	if out.Type() == cel.BoolType {
		return out.Value().(bool), nil
	}
	return false, fmt.Errorf("cel result is not bool: %v", out.Value())
}

// EvaluateAny evaluates a CEL expression and returns the raw CEL value.
func (e *Engine) EvaluateAny(expression string, bindings map[string]any) (any, error) {
	if expression == "" {
		return nil, nil
	}

	env, err := cel.NewEnv(e.options...)
	if err != nil {
		return nil, fmt.Errorf("cel env: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("cel compile: %w", issues.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel program: %w", err)
	}

	out, _, err := prg.Eval(bindings)
	if err != nil {
		return nil, fmt.Errorf("cel eval: %w", err)
	}
	return out.Value(), nil
}

// EvaluateBoolWithVars parses and evaluates a CEL expression with dynamically declared variables from bindings.
func (e *Engine) EvaluateBoolWithVars(expression string, bindings map[string]any) (bool, error) {
	if expression == "" {
		return true, nil
	}

	varDcls := make([]*exprpb.Decl, 0, len(bindings))
	for k, v := range bindings {
		varDcls = append(varDcls, decls.NewVar(k, celTypeOf(v)))
	}

	allOpts := append([]cel.EnvOption{}, e.options...)
	allOpts = append(allOpts, cel.Declarations(varDcls...))

	env, err := cel.NewEnv(allOpts...)
	if err != nil {
		return false, fmt.Errorf("cel env: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("cel compile: %w", issues.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return false, fmt.Errorf("cel program: %w", err)
	}

	out, _, err := prg.Eval(bindings)
	if err != nil {
		return false, fmt.Errorf("cel eval: %w", err)
	}

	if out.Type() == cel.BoolType {
		return out.Value().(bool), nil
	}
	return false, fmt.Errorf("cel result is not bool: %v", out.Value())
}

func celTypeOf(v any) *exprpb.Type {
	switch v.(type) {
	case bool:
		return decls.Bool
	case int, int32, int64:
		return decls.Int
	case uint, uint32, uint64:
		return decls.Uint
	case float32, float64:
		return decls.Double
	case string:
		return decls.String
	case []byte:
		return decls.Bytes
	default:
		return decls.Dyn
	}
}
