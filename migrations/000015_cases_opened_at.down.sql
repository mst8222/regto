-- migrations/000015_cases_opened_at.down.sql
-- 回滚 000015：移除 cases.opened_at（数据随列丢失，仅允许在无业务用例依赖时执行）。
ALTER TABLE cases
  DROP COLUMN IF EXISTS opened_at;

-- schema_meta 回退到 14（000014 的版本）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 14)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
