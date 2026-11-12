-- migrations/000020_agent.down.sql
-- 回滚：删除 agent_sessions（授权载体）。删除后守卫读不到任何会话，
-- 全部 AI 动作按「能力白名单为空」拒绝——失败方向是安全的。

DROP INDEX IF EXISTS idx_agent_sessions_owner;
DROP INDEX IF EXISTS idx_agent_sessions_status;
DROP TABLE IF EXISTS agent_sessions;

-- 回退 schema 版本契约到上一版本（与 up 成对）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 19)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
