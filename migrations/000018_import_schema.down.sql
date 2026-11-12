-- migrations/000018_import_schema.down.sql
-- 回滚：删除导入记账 schema（业务表不受影响；导入元数据可弃）。
DROP TABLE IF EXISTS regto_import.import_errors;
DROP TABLE IF EXISTS regto_import.import_id_map;
DROP TABLE IF EXISTS regto_import.import_runs;
DROP SCHEMA IF EXISTS regto_import;

INSERT INTO schema_meta (singleton, version)
VALUES (true, 17)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
