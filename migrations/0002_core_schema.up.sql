-- 0002_core_schema.up.sql
-- Every table the engine reads or writes. Columns that the Go code scans into
-- plain strings are NOT NULL DEFAULT '' so a missing value never breaks a scan.

-- Model policies: which model an agent uses, plus ordered fallbacks.
CREATE TABLE IF NOT EXISTS public.model_policies (
    model_policy_id   TEXT PRIMARY KEY,
    primary_model     TEXT NOT NULL,
    fallback_models   JSONB NOT NULL DEFAULT '[]'::jsonb,
    temperature       DOUBLE PRECISION,
    max_tokens        INT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.retry_policies (
    retry_policy_id       TEXT PRIMARY KEY,
    max_attempts          INT NOT NULL DEFAULT 3,
    retry_on_errors_json  JSONB NOT NULL DEFAULT '[]'::jsonb,
    backoff_strategy      TEXT NOT NULL DEFAULT 'exponential',
    initial_delay_ms      INT NOT NULL DEFAULT 1000,
    max_delay_ms          INT NOT NULL DEFAULT 30000,
    jitter_enabled        BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS public.agent_definitions (
    agent_definition_id         TEXT PRIMARY KEY,
    agent_name                  TEXT NOT NULL,
    agent_type                  TEXT NOT NULL,
    domain_id                   TEXT NOT NULL DEFAULT '',
    framework_id                TEXT NOT NULL DEFAULT '',
    description                 TEXT NOT NULL DEFAULT '',
    input_schema_json           JSONB,
    output_schema_json          JSONB,
    default_model_policy_id     TEXT REFERENCES public.model_policies(model_policy_id),
    default_prompt_template_id  TEXT,
    can_use_llm                 BOOLEAN NOT NULL DEFAULT FALSE,
    can_use_tools               BOOLEAN NOT NULL DEFAULT FALSE,
    can_spawn_subagents         BOOLEAN NOT NULL DEFAULT FALSE,
    can_run_parallel            BOOLEAN NOT NULL DEFAULT FALSE,
    max_iterations              INT NOT NULL DEFAULT 0,
    max_runtime_ms              INT NOT NULL DEFAULT 0,
    timeout_ms                  INT NOT NULL DEFAULT 0,
    retry_policy_id             TEXT,
    error_policy_id             TEXT,
    cache_policy_id             TEXT,
    memory_policy_id            TEXT,
    is_active                   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.workflow_definitions (
    workflow_id     TEXT PRIMARY KEY,
    domain_id       TEXT NOT NULL DEFAULT '',
    framework_id    TEXT NOT NULL DEFAULT '',
    workflow_name   TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    entry_node_id   TEXT NOT NULL,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- config_json carries node-type-specific settings: a model override, the
-- child workflow of a subagent node, the branches of a parallel node.
CREATE TABLE IF NOT EXISTS public.workflow_nodes (
    workflow_node_id      TEXT PRIMARY KEY,
    workflow_id           TEXT NOT NULL REFERENCES public.workflow_definitions(workflow_id) ON DELETE CASCADE,
    node_name             TEXT NOT NULL DEFAULT '',
    node_type             TEXT NOT NULL,
    agent_definition_id   TEXT REFERENCES public.agent_definitions(agent_definition_id),
    input_contract_json   JSONB,
    output_contract_json  JSONB,
    config_json           JSONB NOT NULL DEFAULT '{}'::jsonb,
    rules_first           BOOLEAN NOT NULL DEFAULT TRUE,
    llm_allowed           BOOLEAN NOT NULL DEFAULT FALSE,
    tool_allowed          BOOLEAN NOT NULL DEFAULT FALSE,
    retry_policy_id       TEXT,
    error_policy_id       TEXT,
    cache_policy_id       TEXT,
    is_idempotent         BOOLEAN NOT NULL DEFAULT FALSE,
    timeout_ms            INT NOT NULL DEFAULT 0,
    max_runtime_ms        INT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_workflow_nodes_workflow ON public.workflow_nodes(workflow_id);

CREATE TABLE IF NOT EXISTS public.workflow_edges (
    workflow_edge_id      TEXT PRIMARY KEY,
    workflow_id           TEXT NOT NULL REFERENCES public.workflow_definitions(workflow_id) ON DELETE CASCADE,
    from_node_id          TEXT NOT NULL,
    to_node_id            TEXT NOT NULL,
    condition_expression  TEXT NOT NULL DEFAULT '',
    priority              INT NOT NULL DEFAULT 0,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_workflow_edges_from ON public.workflow_edges(workflow_id, from_node_id);

CREATE TABLE IF NOT EXISTS public.agent_sessions (
    session_id           TEXT PRIMARY KEY,
    parent_session_id    TEXT REFERENCES public.agent_sessions(session_id),
    domain_id            TEXT,
    framework_id         TEXT NOT NULL DEFAULT '',
    tenant_id            TEXT NOT NULL,
    session_type         TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'pending',
    started_by_user_id   TEXT,
    assigned_agent_id    TEXT,
    current_workflow_id  TEXT,
    current_node_id      TEXT,
    compressed_state_id  TEXT,
    raw_context_policy   TEXT NOT NULL DEFAULT 'minimal',
    max_runtime_ms       INT NOT NULL DEFAULT 300000,
    timeout_ms           INT NOT NULL DEFAULT 30000,
    max_agent_runs       INT NOT NULL DEFAULT 50,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_activity_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at            TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_tenant ON public.agent_sessions(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_parent ON public.agent_sessions(parent_session_id);

CREATE TABLE IF NOT EXISTS public.agent_instances (
    agent_instance_id    TEXT PRIMARY KEY,
    agent_definition_id  TEXT NOT NULL,
    session_id           TEXT NOT NULL,
    instance_name        TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'pending',
    current_task_id      TEXT NOT NULL DEFAULT '',
    current_node_id      TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at            TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS public.agentic_loop_runs (
    loop_run_id      TEXT PRIMARY KEY,
    session_id       TEXT NOT NULL,
    tenant_id        TEXT NOT NULL,
    status           TEXT NOT NULL,
    max_iterations   INT NOT NULL,
    max_runtime_ms   INT NOT NULL,
    timeout_ms       INT NOT NULL,
    iteration_count  INT NOT NULL DEFAULT 0,
    current_node_id  TEXT,
    stop_reason      TEXT,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_loop_runs_session ON public.agentic_loop_runs(session_id);

CREATE TABLE IF NOT EXISTS public.try_run_events (
    try_run_id        TEXT PRIMARY KEY,
    loop_run_id       TEXT NOT NULL,
    session_id        TEXT NOT NULL,
    tenant_id         TEXT NOT NULL,
    workflow_node_id  TEXT NOT NULL,
    attempt_number    INT NOT NULL DEFAULT 1,
    status            TEXT NOT NULL,
    input_json        JSONB,
    output_json       JSONB,
    latency_ms        INT,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_try_run_events_session ON public.try_run_events(session_id);

CREATE TABLE IF NOT EXISTS public.agent_memory_items (
    memory_item_id     TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL,
    agent_instance_id  TEXT NOT NULL DEFAULT '',
    tenant_id          TEXT NOT NULL,
    memory_type        TEXT NOT NULL,
    key                TEXT NOT NULL,
    value_json         JSONB,
    value_text         TEXT,
    source_type        TEXT NOT NULL DEFAULT '',
    source_id          TEXT NOT NULL DEFAULT '',
    confidence         DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    is_current         BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_lookup ON public.agent_memory_items(session_id, memory_type, key) WHERE is_current;

CREATE TABLE IF NOT EXISTS public.state_snapshots (
    state_snapshot_id  TEXT PRIMARY KEY,
    session_id         TEXT NOT NULL,
    tenant_id          TEXT NOT NULL,
    state_type         TEXT NOT NULL,
    state_json         JSONB NOT NULL,
    compression_level  TEXT,
    token_estimate     INT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_state_snapshots_session ON public.state_snapshots(session_id, state_type, created_at DESC);

CREATE TABLE IF NOT EXISTS public.observability_events (
    observability_event_id  TEXT PRIMARY KEY,
    session_id              TEXT NOT NULL,
    tenant_id               TEXT NOT NULL,
    agent_run_id            TEXT,
    workflow_node_id        TEXT,
    model_used              TEXT,
    input_tokens            INT NOT NULL DEFAULT 0,
    output_tokens           INT NOT NULL DEFAULT 0,
    total_tokens            INT NOT NULL DEFAULT 0,
    token_cost              DOUBLE PRECISION,
    latency_ms              INT NOT NULL DEFAULT 0,
    retry_count             INT NOT NULL DEFAULT 0,
    cache_hit               BOOLEAN NOT NULL DEFAULT FALSE,
    semantic_cache_hit      BOOLEAN NOT NULL DEFAULT FALSE,
    confidence              DOUBLE PRECISION,
    hallucination_risk      DOUBLE PRECISION,
    manual_review_required  BOOLEAN NOT NULL DEFAULT FALSE,
    user_override_count     INT NOT NULL DEFAULT 0,
    event_type              TEXT NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_observability_session ON public.observability_events(session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS public.semantic_cache (
    cache_id                  TEXT PRIMARY KEY,
    tenant_id                 TEXT NOT NULL,
    task_type                 TEXT NOT NULL,
    input_hash                TEXT NOT NULL,
    normalized_input_summary  TEXT,
    output_json               TEXT NOT NULL,
    model_used                TEXT,
    hit_count                 INT NOT NULL DEFAULT 0,
    last_hit_at               TIMESTAMPTZ,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at                TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_semantic_cache_lookup ON public.semantic_cache(tenant_id, task_type, input_hash);
