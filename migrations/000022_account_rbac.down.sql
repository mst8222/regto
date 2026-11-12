-- migrations/000022_account_rbac.down.sql
-- 回滚：撤销授权并删除两张表；schema 版本回到 21。
--
-- ⚠️ 这会丢弃全部角色绑定数据（user_roles）。down 只用于「本次升级要撤回」，
-- 不用于日常清理——绑定关系是审计事实，删了就再也答不出「当时谁有权」。

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        EXECUTE 'REVOKE ALL ON public.user_roles FROM regto_app';
        EXECUTE 'REVOKE ALL ON public.roles FROM regto_app';
    END IF;
END $$;

DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;

INSERT INTO schema_meta (singleton, version)
VALUES (true, 21)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
