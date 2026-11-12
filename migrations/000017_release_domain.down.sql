-- migrations/000017_release_domain.down.sql
-- 回滚：删除发布域四表（与 000012/000013 同模式：整表 DROP）。
-- 注意：plan_stories 先删（无物理外键，顺序仅表意）；traces 不引用本域。
DROP TABLE IF EXISTS builds;
DROP TABLE IF EXISTS releases;
DROP TABLE IF EXISTS plan_stories;
DROP TABLE IF EXISTS plans;

INSERT INTO schema_meta (singleton, version)
VALUES (true, 16)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
