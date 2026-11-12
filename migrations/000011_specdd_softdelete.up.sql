-- migrations/000011_specdd_softdelete.up.sql
-- 范围：规格索引表补齐软删列
-- 缘由：同步是「文件整体替换」，而权限模型禁止 regto_app 执行 DELETE
--       （审计型系统，物理删除只由 DBA 或迁移执行）。改用软删 + 复活：
--       文件里消失的条目标 deleted_at，重新出现时复活（deleted_at = NULL）。
-- 方言：PostgreSQL（本机 WSL2）。命名规范见 AGENTS.md §5。
-- 注意：本文件合入 main 后禁止修改；变更必须新增 000012_*.up.sql。

ALTER TABLE spec_items ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ(3) NULL;
ALTER TABLE case_steps ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ(3) NULL;

-- 软删后的唯一键必须让位：同一 (case_id, seq) 历史上可能出现多条软删记录，
-- 故把唯一约束改为「仅对未删记录唯一」的部分唯一索引。
-- 注意：必须用 DROP CONSTRAINT（约束索引不能直接 DROP INDEX）。
ALTER TABLE case_steps DROP CONSTRAINT IF EXISTS uniq_case_step_seq;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_case_step_active
    ON case_steps (case_id, seq) WHERE deleted_at IS NULL;

-- spec_items 的 ref 唯一键同理：删除的条目保留历史，活跃引用唯一。
ALTER TABLE spec_items DROP CONSTRAINT IF EXISTS uniq_spec_item_ref;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_spec_item_active
    ON spec_items (ref) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_spec_items_doc_active ON spec_items (doc_id) WHERE deleted_at IS NULL;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号，
-- 漏改会让 regto-server 启动校验读到旧版本而拒绝启动）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 11)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
