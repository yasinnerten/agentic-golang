package rulesengine

import (
	"testing"
)

func TestEvaluateBoolBasic(t *testing.T) {
	eng := NewEngine()

	// Simple boolean expression
	result, err := eng.EvaluateBoolWithVars("a == true", map[string]any{"a": true})
	if err != nil {
		t.Fatalf("EvaluateBoolWithVars failed: %v", err)
	}
	if !result {
		t.Fatal("Expected true for a == true")
	}

	// Numeric comparison
	result, err = eng.EvaluateBoolWithVars("x > 5", map[string]any{"x": 10})
	if err != nil {
		t.Fatalf("EvaluateBoolWithVars failed: %v", err)
	}
	if !result {
		t.Fatal("Expected true for x > 5")
	}

	// String equality
	result, err = eng.EvaluateBoolWithVars("status == 'active'", map[string]any{"status": "active"})
	if err != nil {
		t.Fatalf("EvaluateBoolWithVars failed: %v", err)
	}
	if !result {
		t.Fatal("Expected true for status == 'active'")
	}
}

func TestEvaluateBoolCompound(t *testing.T) {
	eng := NewEngine()

	// AND condition
	result, err := eng.EvaluateBoolWithVars(
		"classification == 'high_risk' && confidence > 0.8",
		map[string]any{"classification": "high_risk", "confidence": 0.95},
	)
	if err != nil {
		t.Fatalf("EvaluateBoolWithVars failed: %v", err)
	}
	if !result {
		t.Fatal("Expected true for compound AND condition")
	}

	// OR condition
	result, err = eng.EvaluateBoolWithVars(
		"route == 'prohibited' || route == 'high_risk'",
		map[string]any{"route": "high_risk"},
	)
	if err != nil {
		t.Fatalf("EvaluateBoolWithVars failed: %v", err)
	}
	if !result {
		t.Fatal("Expected true for OR condition")
	}

	// OR with neither true
	result, err = eng.EvaluateBoolWithVars(
		"route == 'prohibited' || route == 'high_risk'",
		map[string]any{"route": "minimal_risk"},
	)
	if err != nil {
		t.Fatalf("EvaluateBoolWithVars failed: %v", err)
	}
	if result {
		t.Fatal("Expected false for OR with neither true")
	}
}

func TestEvaluateBoolEdgeCases(t *testing.T) {
	eng := NewEngine()

	// Empty expression should return true
	result, err := eng.EvaluateBoolWithVars("", map[string]any{})
	if err != nil {
		t.Fatalf("Empty expression failed: %v", err)
	}
	if !result {
		t.Fatal("Empty expression should return true")
	}

	// Missing variable should fail
	_, err = eng.EvaluateBoolWithVars("missing_var == 'test'", map[string]any{})
	if err == nil {
		t.Fatal("Expected error for missing variable")
	}

	// Type mismatch
	_, err = eng.EvaluateBoolWithVars("x > 5", map[string]any{"x": "not_a_number"})
	if err == nil {
		t.Fatal("Expected error for type mismatch")
	}
}

func TestEvaluateBoolClassificationScenarios(t *testing.T) {
	eng := NewEngine()

	scenarios := []struct {
		name       string
		expr       string
		bindings   map[string]any
		wantResult bool
		wantErr    bool
	}{
		{
			name:       "high_risk_with_high_confidence",
			expr:       "classification == 'high_risk' && confidence > 0.8",
			bindings:   map[string]any{"classification": "high_risk", "confidence": 0.95},
			wantResult: true,
		},
		{
			name:       "high_risk_with_low_confidence",
			expr:       "classification == 'high_risk' && confidence > 0.8",
			bindings:   map[string]any{"classification": "high_risk", "confidence": 0.5},
			wantResult: false,
		},
		{
			name:       "prohibited_practice",
			expr:       "classification == 'prohibited'",
			bindings:   map[string]any{"classification": "prohibited"},
			wantResult: true,
		},
		{
			name:       "limited_risk_route",
			expr:       "classification == 'limited_risk' || classification == 'minimal_risk'",
			bindings:   map[string]any{"classification": "limited_risk"},
			wantResult: true,
		},
		{
			name:       "risk_score_threshold",
			expr:       "risk_score >= 70",
			bindings:   map[string]any{"risk_score": 85},
			wantResult: true,
		},
		{
			name:       "risk_score_below_threshold",
			expr:       "risk_score >= 70",
			bindings:   map[string]any{"risk_score": 45},
			wantResult: false,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			result, err := eng.EvaluateBoolWithVars(sc.expr, sc.bindings)
			if sc.wantErr {
				if err == nil {
					t.Fatal("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if result != sc.wantResult {
				t.Errorf("Got %v, want %v for expression: %s", result, sc.wantResult, sc.expr)
			}
		})
	}
}

func TestEvaluateAny(t *testing.T) {
	eng := NewEngine()

	// String result (no variables needed)
	result, err := eng.EvaluateAny("'hello' + ' ' + 'world'", map[string]any{})
	if err != nil {
		t.Fatalf("EvaluateAny failed: %v", err)
	}
	if result != "hello world" {
		t.Errorf("Expected 'hello world', got %v", result)
	}

	// Numeric result using pre-declared variables (env, input etc. are pre-declared)
	result, err = eng.EvaluateAny("env.size() == 0", map[string]any{"env": map[string]any{}})
	if err != nil {
		t.Fatalf("EvaluateAny failed: %v", err)
	}
	if result != true {
		t.Errorf("Expected true, got %v", result)
	}
}
