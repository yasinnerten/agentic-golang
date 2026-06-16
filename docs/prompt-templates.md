---
title: Prompt templates
---

# Prompt templates — making agents experts on their task

By default an agent's prompt is generic: its name, a one-line description, and its
raw output schema. That's enough to function, but not enough to be *expert*. The
`prompt_templates` system lets you attach a rich, versioned, **domain-specific**
system prompt (persona + rubric + few-shot examples) to each agent — while keeping
the engine itself domain-agnostic.

The key design rule for this open-source engine:

> **The engine ships a generic prompt-template mechanism. Your domain ships the
> actual expert prompts as seed data.**

So `agentic-golang` provides the table, the loader, the executor wiring, and one
small *generic* example. A downstream product (e.g. a compliance platform) seeds its
own specific prompts — "the Article 10 data-governance evaluator", "the underwriting
risk classifier", etc. — without forking the engine.

## Schema (generic)

```sql
CREATE TABLE prompt_templates (
    prompt_template_id   TEXT PRIMARY KEY,
    agent_type           TEXT,            -- bind by role (classifier, evaluator, …)
    agent_definition_id  TEXT,            -- or bind to a specific agent (optional)
    domain_id            TEXT,            -- optional scoping
    framework_id         TEXT,            -- optional scoping
    system_prompt        TEXT NOT NULL,   -- persona + rubric, may contain {{placeholders}}
    few_shot_json        JSONB,           -- [{ "input": …, "output": … }, …]
    variables_json       JSONB,           -- declared template variables + defaults
    version              INT  NOT NULL DEFAULT 1,
    is_active            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Resolution order when the executor builds a prompt for a node:

1. the agent's `default_prompt_template_id`, if set; else
2. the most specific active template matching `agent_definition_id`; else
3. an active template matching `agent_type` (+ domain/framework if present); else
4. the built-in generic fallback (current behavior).

## Rendering

The chosen template's `system_prompt` is rendered with **session context** injected
via `{{placeholders}}` (e.g. `{{framework}}`, `{{chapter}}`, `{{classification}}`),
then the agent's output schema is appended, then `few_shot_json` examples are added
as alternating user/assistant turns. This is what lets one "evaluator" agent become
the *specific* evaluator for whatever the current session is about.

## Phases

- **P0 — Schema & loader.** Create the table (migration `0001_prompt_templates`);
  the registry loads the resolved template for a node.
- **P1 — Executor wiring.** Replace the inline system-prompt construction with the
  rendered template; inject session context.
- **P2 — Generic example.** Ship one neutral example template so the engine runs
  out of the box and contributors see the shape.
- **P3 — Validation gate.** Combine with the structured-output validation gate so
  a better prompt *and* a structural guarantee remove the malformed-output failure
  class.
- **P4 — Iteration.** Use `observability_events` (confidence, hallucination proxy,
  retry count, manual-review rate) to A/B prompt `version`s.

## For downstream products

Seed your expert prompts in **your own** migrations/seeds, not here. Keep regulated
or proprietary prompt content in the private repo. The engine never needs to know
your domain — it only needs `default_prompt_template_id` to resolve.
