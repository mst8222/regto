-- migrations/000019_traces_immutable.up.sql
-- 范围：把 traces 从"业务表统一授权"中摘出，收敛为只读副本（物理防篡改落地）
-- 依据：DESIGN-TRACE-001#DEC-04 / SPEC-TRACE-001#REQ-04,#AC-04 / TEST-TRACE-001#TC-04
-- 执行者：本迁移必须以**超级用户**连接执行（REGTO_MIGRATION_URL），普通应用账号无权 REVOKE。
-- 方言：PostgreSQL（本机 WSL2）。命名规范见 AGENTS.md §5。
--
-- 背景（本迁移要修的缺口）：
--   000010 §1 的"逐表收敛"循环对 public 下**所有**表统一 GRANT SELECT, INSERT, UPDATE，
--   traces 也在其中；而 DEC-04 只给**独立角色 regto_trace** 授了 SELECT/INSERT。
--   服务端实际连接的是 regto_app（REGTO_DATABASE_URL），于是"应用角色不可改删"
--   在生产路径上并未真正生效——TC-04 因此无法通过。
--   本迁移补上这一层：对 regto_app 也只保留 SELECT/INSERT。
--
-- 权限模型（最终态）：
--   regto_app  ：业务表 SELECT/INSERT/UPDATE；traces **仅 SELECT/INSERT**（❌ UPDATE ❌ DELETE ❌ TRUNCATE）
--   regto_trace：traces 仅 SELECT/INSERT（000010 已授予，此处重断言以防后续迁移漂移）
--   PUBLIC     ：对 traces 零权限（防任何"顺手 GRANT TO PUBLIC"把防篡改捅穿）
--
-- 幂等：REVOKE 对未授予的权限无副作用，可重复执行；表/角色不存在时跳过并 NOTICE。

DO $$
BEGIN
    IF to_regclass('public.traces') IS NULL THEN
        RAISE NOTICE '表 public.traces 不存在，跳过留痕权限收敛';
        RETURN;
    END IF;

    -- ---------- 1. 应用角色：只读副本（可追加、可查询，不可改删） ----------
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        EXECUTE 'REVOKE UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON public.traces FROM regto_app';
        EXECUTE 'GRANT SELECT, INSERT ON public.traces TO regto_app';
        RAISE NOTICE '已收敛 regto_app 对 traces 的权限为 SELECT, INSERT';
    ELSE
        RAISE NOTICE '角色 regto_app 不存在，跳过（请先执行 docs/deploy/01-init-database.sql）';
    END IF;

    -- ---------- 2. 留痕专用角色：重断言（DEC-04 的原有约束，防后续迁移漂移） ----------
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_trace') THEN
        EXECUTE 'REVOKE ALL ON public.traces FROM regto_trace';
        EXECUTE 'GRANT SELECT, INSERT ON public.traces TO regto_trace';
        RAISE NOTICE '已重断言 regto_trace 对 traces 的权限为 SELECT, INSERT';
    END IF;

    -- ---------- 3. PUBLIC：对 traces 零权限 ----------
    EXECUTE 'REVOKE ALL ON public.traces FROM PUBLIC';
END $$;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号，
-- 漏改会让 regto-server 启动校验读到旧版本而拒绝启动）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 19)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
