-- migrations/000015_cases_opened_at.up.sql
-- 范围：补 cases.opened_at 列（M2-A 用例域的领域模型字段，000012 扩展时漏建）。
-- 依据：internal/domain/testcase.Case.OpenedAtMs（对应禅道 zt_case.openedDate）。
-- 说明：000008 建表时只有 created_at/updated_at；000012 加了 opened_by_id 却漏了
-- opened_at，仓储 INSERT/SELECT 均引用该列，故本迁移补齐（不改已应用的 000012——
-- 迁移一旦应用即不可变，只能增量修补）。
-- 方言：PostgreSQL（本机 WSL2）。命名规范见 AGENTS.md §5。

-- 可空：SpecDD 用例（spec sync 写入，无业务「创办」语义）允许为 NULL；
-- 业务用例由 usecase 统一写入 OpenedAtMs。
ALTER TABLE cases
  ADD COLUMN IF NOT EXISTS opened_at TIMESTAMPTZ(3) NULL;

-- 存量 SpecDD 用例回填：以 created_at 近似（同步入库时间即「打开」时间），
-- 避免展示层出现 NULL 与 created_at 不一致的割裂感。
UPDATE cases SET opened_at = created_at WHERE opened_at IS NULL;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 15)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
