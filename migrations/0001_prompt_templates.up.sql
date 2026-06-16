-- 0001_prompt_templates.up.sql
-- Generic per-agent prompt template store. The engine ships the mechanism;
-- downstream products seed their own domain-specific expert prompts.

CREATE TABLE IF NOT EXISTS prompt_templates (
    prompt_template_id   TEXT PRIMARY KEY,
    agent_type           TEXT,
    agent_definition_id  TEXT,
    domain_id            TEXT,
    framework_id         TEXT,
    system_prompt        TEXT NOT NULL,
    few_shot_json        JSONB,
    variables_json       JSONB,
    version              INT  NOT NULL DEFAULT 1,
    is_active            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prompt_templates_lookup
    ON prompt_templates (agent_type, domain_id, framework_id)
    WHERE is_active;

CREATE INDEX IF NOT EXISTS idx_prompt_templates_by_agent
    ON prompt_templates (agent_definition_id)
    WHERE is_active;

-- One neutral, generic example so the engine runs out of the box.
INSERT INTO prompt_templates
    (prompt_template_id, agent_type, system_prompt, few_shot_json, variables_json, version, is_active)
VALUES
    ('tpl_generic_evaluator', 'evaluator',
     'You are a careful evaluator. Given the inputs for {{subject}}, assess them against the stated criteria. Reason step by step, then respond with ONLY a single JSON object matching the provided output schema. Do not include prose or markdown outside the JSON.',
     '[]'::jsonb,
     '{"subject": "the item under review"}'::jsonb,
     1, TRUE)
ON CONFLICT (prompt_template_id) DO NOTHING;
