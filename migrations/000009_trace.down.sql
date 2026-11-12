-- migrations/000009_trace.down.sql
-- 回滚：删除留痕只读副本（jsonl 文件仍是真源，不受影响）。

DROP INDEX IF EXISTS idx_traces_commit;
DROP INDEX IF EXISTS idx_traces_review;
DROP INDEX IF EXISTS idx_traces_object;
DROP INDEX IF EXISTS idx_traces_action;
DROP INDEX IF EXISTS idx_traces_actor;
DROP TABLE IF EXISTS traces;

-- 回退 schema 版本契约到上一版本（与 up 成对，保证 down 后启动校验仍自洽）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 8)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
