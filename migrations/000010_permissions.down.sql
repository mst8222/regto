-- migrations/000010_permissions.down.sql
-- 回滚：撤销本迁移授予的权限（不删角色，避免连带删除用户自建对象）。

REVOKE ALL ON traces FROM regto_trace;
REVOKE USAGE ON SCHEMA public FROM regto_trace;
REVOKE CONNECT ON DATABASE regto FROM regto_trace;

DO $$
DECLARE
    tbl text;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        FOR tbl IN
            SELECT tablename FROM pg_tables WHERE schemaname = 'public'
        LOOP
            EXECUTE format('REVOKE ALL ON public.%I FROM regto_app', tbl);
        END LOOP;
    END IF;
END $$;

-- 回退 schema 版本契约到上一版本（与 up 成对，保证 down 后启动校验仍自洽）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 9)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
