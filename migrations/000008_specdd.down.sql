-- migrations/000008_specdd.down.sql
-- 回滚：删除 SpecDD 索引表（顺序与 up 相反，依赖表先行）。

DROP INDEX IF EXISTS idx_trace_links_target;
DROP TABLE IF EXISTS trace_links;
DROP TABLE IF EXISTS case_steps;
DROP TABLE IF EXISTS cases;

DROP INDEX IF EXISTS idx_spec_items_doc;
DROP TABLE IF EXISTS spec_items;

DROP INDEX IF EXISTS idx_spec_documents_doc_id;
DROP TABLE IF EXISTS spec_documents;

-- 回退 schema 版本契约到上一版本（与 up 成对，保证 down 后启动校验仍自洽）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 7)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
