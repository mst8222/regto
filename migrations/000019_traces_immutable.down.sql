-- migrations/000019_traces_immutable.down.sql
-- 回滚：把 regto_app 对 traces 的权限恢复到 000018 时的状态（SELECT, INSERT, UPDATE）。
-- 注意：回滚后 traces 不再具备物理防篡改能力，TEST-TRACE-001#TC-04 会重新失败——
-- 这是预期的（down 只是结构回滚，不是安全建议）。
--
-- regto_trace 的 SELECT/INSERT 是 000010 的既有约定，不随本迁移回滚；
-- PUBLIC 的零权限同样保留（撤销 PUBLIC 权限不可逆地属于"更安全"一侧，无需回退）。

DO $$
BEGIN
    IF to_regclass('public.traces') IS NULL THEN
        RETURN;
    END IF;

    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        EXECUTE 'GRANT SELECT, INSERT, UPDATE ON public.traces TO regto_app';
        RAISE NOTICE '已恢复 regto_app 对 traces 的 UPDATE（防篡改失效）';
    END IF;
END $$;

-- 回退 schema 版本契约到上一版本（与 up 成对，保证 down 后启动校验仍自洽）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 18)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
