-- migrations/000016_cases_source_hash_nullable.down.sql
-- 回滚 000016：source_hash 恢复 NOT NULL。
-- ⚠️ 若存在业务用例（source_hash 为 NULL），本语句会失败——
-- 属预期保护：有业务数据时不允许回退该约束。
ALTER TABLE cases
  ALTER COLUMN source_hash SET NOT NULL;

-- schema_meta 回退到 15（000015 的版本）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 15)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
