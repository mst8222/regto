-- migrations/000021_agent_session_write.up.sql
-- 范围：把 agent_sessions 的写权限交给应用账号，让「会话运维」能走正常接口
-- 依据：SPEC-AGENT-002 REQ-10 / DESIGN-AGENT-002#DEC-09
-- 执行者：本迁移必须以**超级用户**连接执行（REGTO_MIGRATION_URL）。
--
-- 背景：迁移 000020 对 agent_sessions 做了 REVOKE ALL + GRANT SELECT，
-- 于是「人类管会话」实际变成了「DBA 手工 INSERT」——与 SPEC-AGENT-001 的
-- 本意（人类专属动作）正好相反。这里补齐 INSERT/UPDATE（**仍不授 DELETE**，
-- 物理删除只归 DBA/迁移；吊销是 status='revoked' 的软删，不需要 DELETE）。
--
-- 主键 id 是 BIGINT 且当初没有默认值（由 DBA 脚本给号），应用写入需要号源，
-- 故建序列并设为默认值。

CREATE SEQUENCE IF NOT EXISTS agent_sessions_id_seq;

ALTER TABLE agent_sessions
  ALTER COLUMN id SET DEFAULT nextval('agent_sessions_id_seq');

-- 序列需与表数据对齐，避免应用写入时撞上 DBA 已用过的号。
SELECT setval('agent_sessions_id_seq',
              COALESCE((SELECT max(id) FROM agent_sessions), 0) + 1,
              false);

-- ---------- 最小权限（与 000010/000019/000020 同一模型） ----------
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        EXECUTE 'GRANT SELECT, INSERT, UPDATE ON public.agent_sessions TO regto_app';
        EXECUTE 'GRANT USAGE, SELECT ON SEQUENCE public.agent_sessions_id_seq TO regto_app';
    END IF;
END $$;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 21)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
