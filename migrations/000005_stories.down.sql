-- migrations/000005_stories.down.sql
-- 回滚 000005：删除 story 域三张表（先删从表）。
DROP TABLE IF EXISTS story_reviews;
DROP TABLE IF EXISTS story_specs;
DROP TABLE IF EXISTS stories;
