-- migrations/000014_permissions_newtables.down.sql
-- 回滚授权：撤销 regto_app 对 M2-A 新表的权限（表本身由 000012/000013 的 down 删除）。
-- 顺序：先于 000013/000012 的 down 执行（golang-migrate 按版本号逆序，天然保证）。

DO $$
DECLARE
    tbl text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        RETURN;
    END IF;
    FOREACH tbl IN ARRAY ARRAY[
        'test_suites', 'suite_cases',
        'test_tasks', 'test_task_cases', 'test_results'
    ]
    LOOP
        EXECUTE format('REVOKE ALL ON public.%I FROM regto_app', tbl);
    END LOOP;
END
$$;

INSERT INTO schema_meta (singleton, version)
VALUES (true, 13)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
