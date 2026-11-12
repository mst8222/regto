-- 000006：schema 版本契约表
--
-- 目的：让应用能在**不接触迁移工具元数据表**的前提下知道自己连的库是哪个版本。
--
-- 背景（DEC-08）：
--   - golang-migrate 的 schema_migrations 是**工具元数据**，应用账号被授予零权限
--     （防应用误改迁移历史，见 docs/deploy/02-init-permissions.sql）。
--   - 但服务启动必须校验「代码期望版本 == 库实际版本」（AGENTS.md §5：禁止自动迁移）。
--
-- 方案：新建业务可读的 schema_meta 契约表，由迁移文件自己维护版本号。
-- 纪律：每个新增迁移文件末尾必须把 version 更新为自身版本号
--      （由 cmd/migrate validate 断言 version == 最新迁移号，漏改即报错）。

CREATE TABLE IF NOT EXISTS schema_meta (
  singleton  BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  version    INTEGER      NOT NULL,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT now()
);

COMMENT ON TABLE schema_meta IS 'schema 版本契约：应用只读，由迁移文件维护（详见文件头注释）';

-- 版本写入（幂等，便于重跑）
INSERT INTO schema_meta (singleton, version)
VALUES (true, 6)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
