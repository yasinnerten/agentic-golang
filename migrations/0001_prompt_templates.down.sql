-- 0001_prompt_templates.down.sql
DROP INDEX IF EXISTS idx_prompt_templates_by_agent;
DROP INDEX IF EXISTS idx_prompt_templates_lookup;
DROP TABLE IF EXISTS prompt_templates;
