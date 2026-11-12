-- migrations/000010_permissions.up.sql
-- 范围：权限收敛——业务表最小权限 + traces 只读副本角色
-- 依据：ROADMAP §5.3「迁移脚本显式 GRANT」、DESIGN-TRACE-001 DEC-04
-- 执行者：本迁移必须以**超级用户**连接执行（REGTO_MIGRATION_URL），普通应用账号无权 GRANT。
-- 方言：PostgreSQL（本机 WSL2）。命名规范见 AGENTS.md §5。
--
-- 权限模型（Regto 是审计型系统，删除一律软删 deleted_at）：
--   regto_app  ：SELECT / INSERT / UPDATE；❌ DELETE ❌ TRUNCATE ❌ CREATE
--                ❌ 对 schema_migrations 无任何权限（迁移元数据与业务隔离）
--   regto_trace：对 traces 仅 SELECT / INSERT；❌ UPDATE ❌ DELETE（物理防篡改）
--
-- 幂等：角色不存在时跳过（新环境应先跑 docs/deploy/01-init-database.sql 建角色）。

-- ---------- 1. 业务表：逐表收敛到最小权限 ----------
DO $$
DECLARE
    tbl text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        RAISE NOTICE '角色 regto_app 不存在，跳过业务表授权（请先执行 docs/deploy/01-init-database.sql）';
        RETURN;
    END IF;

    FOR tbl IN
        SELECT tablename FROM pg_tables
         WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
         ORDER BY tablename
    LOOP
        EXECUTE format('REVOKE ALL ON public.%I FROM regto_app', tbl);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON public.%I TO regto_app', tbl);
    END LOOP;

    -- 序列（IDENTITY 列依赖）：只给 USAGE/SELECT
    EXECUTE 'ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public '
         || 'GRANT SELECT, INSERT, UPDATE ON TABLES TO regto_app';
    EXECUTE 'ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public '
         || 'GRANT USAGE, SELECT ON SEQUENCES TO regto_app';
END $$;

-- ---------- 2. 留痕只读副本角色 ----------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_trace') THEN
        -- 口令仅为占位：部署时必须 ALTER ROLE regto_trace PASSWORD '<强口令>'。
        EXECUTE 'CREATE ROLE regto_trace LOGIN PASSWORD ''change-me-before-deploy''';
    END IF;
END $$;

GRANT CONNECT ON DATABASE regto TO regto_trace;
GRANT USAGE ON SCHEMA public TO regto_trace;

-- 关键：只授予 SELECT/INSERT，UPDATE/DELETE 一律不授予（物理防篡改，DEC-04）。
REVOKE ALL ON traces FROM regto_trace;
GRANT SELECT, INSERT ON traces TO regto_trace;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号，
-- 漏改会让 regto-server 启动校验读到旧版本而拒绝启动）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 10)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
