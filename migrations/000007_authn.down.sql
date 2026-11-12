-- migrations/000007_authn.down.sql
-- 回滚：删除认证域两张表（顺序与 up 相反，外键先行）。

DROP INDEX IF EXISTS idx_refresh_tokens_family;
DROP INDEX IF EXISTS idx_refresh_tokens_user;
DROP TABLE IF EXISTS refresh_tokens;

DROP INDEX IF EXISTS idx_api_keys_active;
DROP INDEX IF EXISTS idx_api_keys_prefix;
DROP TABLE IF EXISTS api_keys;

-- 回退 schema 版本契约到上一版本（与 up 成对，保证 down 后启动校验仍自洽）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 6)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
