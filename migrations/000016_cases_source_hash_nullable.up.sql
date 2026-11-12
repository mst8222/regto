-- migrations/000016_cases_source_hash_nullable.up.sql
-- 范围：cases.source_hash 放宽为可空（与 000012 的 source_ref 可空化对齐，REQ-07）。
-- 依据：source_ref/source_hash 是**成对**的 SpecDD 概念（指向 TEST yaml 及其内容哈希）；
-- 业务用例（手工/ZTF 导入）两者皆空——000012 放开了 source_ref 却漏了 source_hash，
-- 导致用例域 INSERT 写 NULL 时违反 NOT NULL（SQLSTATE 23502）。
-- 说明：spec sync 对该列照常写入非空哈希，约束放宽不影响 SpecDD 侧不变量。
-- 方言：PostgreSQL（本机 WSL2）。命名规范见 AGENTS.md §5。

ALTER TABLE cases
  ALTER COLUMN source_hash DROP NOT NULL;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 16)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
