-- migrations/000012_casedomain.down.sql
-- 回滚用例域扩展。注意（DESIGN-CASE-001「迁移与回滚方案」）：
-- source_ref 收紧回 NOT NULL 前必须处理业务用例（source_ref 为空的行），
-- 否则 ALTER 失败。这里按约定填占位串 manual:<id>——这是数据损失点，
-- 仅允许在无业务用例时执行；有业务用例时应先人工处理。

UPDATE cases SET source_ref = 'manual:' || id::text WHERE source_ref IS NULL;

DROP INDEX IF EXISTS uniq_suite_case;
DROP INDEX IF EXISTS idx_suite_cases_suite;
DROP TABLE IF EXISTS suite_cases;

DROP INDEX IF EXISTS uniq_suite_product_name;
DROP INDEX IF EXISTS idx_test_suites_product;
DROP TABLE IF EXISTS test_suites;

ALTER TABLE case_steps DROP COLUMN IF EXISTS step_type;

DROP INDEX IF EXISTS idx_cases_story;
DROP INDEX IF EXISTS idx_cases_product_external;
DROP INDEX IF EXISTS idx_cases_product_status;
DROP INDEX IF EXISTS uniq_cases_source_ref;
ALTER TABLE cases
  DROP COLUMN IF EXISTS last_run_at,
  DROP COLUMN IF EXISTS last_run_result,
  DROP COLUMN IF EXISTS last_runner,
  DROP COLUMN IF EXISTS opened_by_id,
  DROP COLUMN IF EXISTS external_id,
  DROP COLUMN IF EXISTS version,
  DROP COLUMN IF EXISTS keywords,
  DROP COLUMN IF EXISTS precondition,
  DROP COLUMN IF EXISTS status,
  DROP COLUMN IF EXISTS type,
  DROP COLUMN IF EXISTS pri,
  DROP COLUMN IF EXISTS branch_id,
  DROP COLUMN IF EXISTS story_version,
  DROP COLUMN IF EXISTS story_id,
  DROP COLUMN IF EXISTS module_id,
  DROP COLUMN IF EXISTS product_id;
-- 恢复 source_ref NOT NULL + 原唯一约束（与 000008 一致：表级约束，非裸索引）。
DROP INDEX IF EXISTS uniq_cases_source_ref;
ALTER TABLE cases ALTER COLUMN source_ref SET NOT NULL;
ALTER TABLE cases ADD CONSTRAINT uniq_cases_source_ref UNIQUE (source_ref);

INSERT INTO schema_meta (singleton, version)
VALUES (true, 11)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
