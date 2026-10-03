package agents

import (
	"context"
	"testing"

	"github.com/yasinnerten/agentic-golang/internal/testdb"
)

func TestModelPolicyRoundTrip(t *testing.T) {
	db := testdb.Open(t)
	testdb.MustExec(t, db, `INSERT INTO public.model_policies (model_policy_id, primary_model, fallback_models, temperature, max_tokens)
		VALUES ('fast', 'gpt-4o-mini', '["llama3.1:8b"]', 0.3, 512), ('bare', 'gpt-4o', '[]', NULL, NULL)`)
	testdb.MustExec(t, db, `INSERT INTO public.agent_definitions (agent_definition_id, agent_name, agent_type, default_model_policy_id, can_use_llm)
		VALUES ('summarizer', 'Summarizer', 'worker', 'fast', TRUE)`)

	r := NewRegistry(db)
	ctx := context.Background()

	def, err := r.GetDefinition(ctx, "summarizer")
	if err != nil {
		t.Fatalf("GetDefinition: %v", err)
	}
	if def.DefaultModelPolicyID == nil || *def.DefaultModelPolicyID != "fast" {
		t.Fatalf("policy id = %v", def.DefaultModelPolicyID)
	}

	p, err := r.GetModelPolicy(ctx, "fast")
	if err != nil {
		t.Fatalf("GetModelPolicy: %v", err)
	}
	if p.PrimaryModel != "gpt-4o-mini" || len(p.FallbackModels) != 1 || p.FallbackModels[0] != "llama3.1:8b" {
		t.Fatalf("unexpected policy: %+v", p)
	}
	if p.Temperature == nil || *p.Temperature != 0.3 || p.MaxTokens != 512 {
		t.Fatalf("unexpected sampling settings: %+v", p)
	}

	bare, err := r.GetModelPolicy(ctx, "bare")
	if err != nil {
		t.Fatalf("GetModelPolicy(bare): %v", err)
	}
	if bare.Temperature != nil || bare.MaxTokens != 0 || len(bare.FallbackModels) != 0 {
		t.Fatalf("NULL columns should decode to zero values: %+v", bare)
	}

	if _, err := r.GetModelPolicy(ctx, "missing"); err == nil {
		t.Fatal("expected an error for a missing policy")
	}
}
