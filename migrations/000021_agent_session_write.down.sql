-- migrations/000021_agent_session_write.down.sql
-- 回滚：撤销 agent_sessions 的写权限，恢复「应用只读、会话由 DBA 管」的状态。
--
-- 注意：**不 DROP 序列**。序列可能已被应用取过号，删掉会让下次 up 时
-- setval 从 max(id) 重新对齐——看似无害，但如果期间有会话被物理删除，
-- 号会回退并可能撞主键。保留一个空转的序列比冒这个风险便宜。

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        EXECUTE 'REVOKE INSERT, UPDATE ON public.agent_sessions FROM regto_app';
        EXECUTE 'REVOKE USAGE, SELECT ON SEQUENCE public.agent_sessions_id_seq FROM regto_app';
    END IF;
END $$;

ALTER TABLE agent_sessions ALTER COLUMN id DROP DEFAULT;

-- 回退 schema 版本契约到上一版本（与 up 成对）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 20)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
