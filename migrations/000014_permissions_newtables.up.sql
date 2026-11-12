-- migrations/000014_permissions_newtables.up.sql
-- 范围：为 M2-A 新增表授权（test_suites / suite_cases / test_tasks / test_task_cases / test_results）
-- 依据：ROADMAP §5.3「迁移脚本显式 GRANT」；000010 的动态 DO 块只在其执行时点生效，
--       之后新建的表必须由新迁移授权，否则应用账号对新表无权限（插入即报错）。
-- 执行者：必须以超级用户连接执行（REGTO_MIGRATION_URL）。
-- 方言：PostgreSQL（本机 WSL2）。

DO $$
DECLARE
    tbl text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        RAISE NOTICE '角色 regto_app 不存在，跳过授权（请先执行 docs/deploy/01-init-database.sql）';
        RETURN;
    END IF;

    -- 只收敛本迁移范围的新表（不重复 000010 的全表扫描，意图更清晰）。
    FOR tbl IN
        SELECT unnest(ARRAY[
            'test_suites', 'suite_cases',
            'test_tasks', 'test_task_cases', 'test_results'
        ])
    LOOP
        EXECUTE format('REVOKE ALL ON public.%I FROM regto_app', tbl);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON public.%I TO regto_app', tbl);
        RAISE NOTICE '已授权 regto_app: %', tbl;
    END LOOP;
END
$$;

INSERT INTO schema_meta (singleton, version)
VALUES (true, 14)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
