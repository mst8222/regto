-- migrations/000009_trace.up.sql
-- 范围：留痕持久化——traces（jsonl 的只读副本）
-- 依据：DESIGN-TRACE-001 数据模型（ROADMAP §5.3 三重落地载体）
-- 方言：PostgreSQL（本机 WSL2）。命名规范见 AGENTS.md §5。
-- 注意：本文件合入 main 后禁止修改；变更必须新增 000010_*.up.sql。
--
-- 物理防篡改：应用角色 regto_trace 仅授予 INSERT/SELECT（不授予 UPDATE/DELETE），
-- 见 docs/deploy/02-init-permissions.sql。主键为 seq，即链的物理顺序（DEC-01）。

CREATE TABLE IF NOT EXISTS traces (
  seq              BIGINT PRIMARY KEY,
  trace_id         CHAR(26) NOT NULL,
  schema_version   SMALLINT NOT NULL DEFAULT 1,
  prev_hash        CHAR(64) NOT NULL,
  hash             CHAR(64) NOT NULL,
  occurred_at      TIMESTAMPTZ(3) NOT NULL,
  recorded_at      TIMESTAMPTZ(3) NOT NULL,
  duration_ms      INTEGER NULL,

  action           VARCHAR(24) NOT NULL,
  outcome          VARCHAR(12) NULL,

  actor_type       VARCHAR(8) NOT NULL,          -- human | ai | system
  actor_id         VARCHAR(64) NOT NULL,
  actor_model      VARCHAR(64) NULL,
  actor_version    VARCHAR(32) NULL,
  actor_session_id VARCHAR(64) NULL,

  object_type      VARCHAR(16) NOT NULL,
  object_id        VARCHAR(128) NOT NULL,
  object_ref       VARCHAR(512) NULL,
  related          JSONB NULL,
  from_state       VARCHAR(32) NULL,
  to_state         VARCHAR(32) NULL,

  summary          VARCHAR(512) NOT NULL,
  detail           TEXT NULL,
  evidence         JSONB NULL,
  prompt_digest    CHAR(64) NULL,

  confidence       SMALLINT NULL,
  needs_human_review SMALLINT NOT NULL DEFAULT 0,
  review_status    VARCHAR(12) NULL,
  reviewed_by      VARCHAR(64) NULL,
  reviewed_at      TIMESTAMPTZ(3) NULL,

  repo             VARCHAR(255) NULL,
  commit_sha       VARCHAR(40) NULL,
  pr               VARCHAR(64) NULL,
  env              VARCHAR(16) NULL,
  tags             JSONB NULL,

  CONSTRAINT uniq_trace_id UNIQUE (trace_id),
  CONSTRAINT uniq_trace_hash UNIQUE (hash)
);

CREATE INDEX IF NOT EXISTS idx_traces_actor ON traces (actor_type, actor_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_traces_action ON traces (action, occurred_at);
CREATE INDEX IF NOT EXISTS idx_traces_object ON traces (object_type, object_id);
CREATE INDEX IF NOT EXISTS idx_traces_review ON traces (needs_human_review, review_status);
CREATE INDEX IF NOT EXISTS idx_traces_commit ON traces (commit_sha) WHERE commit_sha IS NOT NULL;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号，
-- 漏改会让 regto-server 启动校验读到旧版本而拒绝启动）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 9)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
