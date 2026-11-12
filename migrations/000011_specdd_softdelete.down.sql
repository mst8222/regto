-- migrations/000011_specdd_softdelete.down.sql
-- 回滚：移除软删列并恢复原唯一键（隐含：软删记录需人工先清理）。

DROP INDEX IF EXISTS idx_spec_items_doc_active;
DROP INDEX IF EXISTS uniq_spec_item_active;
DROP INDEX IF EXISTS uniq_case_step_active;

DELETE FROM case_steps WHERE deleted_at IS NOT NULL;
DELETE FROM spec_items WHERE deleted_at IS NOT NULL;

ALTER TABLE case_steps DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE spec_items DROP COLUMN IF EXISTS deleted_at;

ALTER TABLE case_steps ADD CONSTRAINT uniq_case_step_seq UNIQUE (case_id, seq);
ALTER TABLE spec_items ADD CONSTRAINT uniq_spec_item_ref UNIQUE (ref);

-- 回退 schema 版本契约到上一版本（与 up 成对，保证 down 后启动校验仍自洽）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 10)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
