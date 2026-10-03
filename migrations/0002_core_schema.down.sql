-- 0002_core_schema.down.sql
DROP TABLE IF EXISTS public.semantic_cache;
DROP TABLE IF EXISTS public.observability_events;
DROP TABLE IF EXISTS public.state_snapshots;
DROP TABLE IF EXISTS public.agent_memory_items;
DROP TABLE IF EXISTS public.try_run_events;
DROP TABLE IF EXISTS public.agentic_loop_runs;
DROP TABLE IF EXISTS public.agent_instances;
DROP TABLE IF EXISTS public.agent_sessions;
DROP TABLE IF EXISTS public.workflow_edges;
DROP TABLE IF EXISTS public.workflow_nodes;
DROP TABLE IF EXISTS public.workflow_definitions;
DROP TABLE IF EXISTS public.agent_definitions;
DROP TABLE IF EXISTS public.retry_policies;
DROP TABLE IF EXISTS public.model_policies;
