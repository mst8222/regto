-- migrations/000020_agent.up.sql
-- 范围：AI 执行主体的授权载体——agent_sessions（能力白名单 + 策略覆盖）
-- 依据：SPEC-AGENT-001 REQ-02 / DESIGN-AGENT-001 数据模型（ROADMAP §5.4 落地机制）
-- 执行者：本迁移必须以**超级用户**连接执行（REGTO_MIGRATION_URL）。
-- 方言：PostgreSQL（本机 WSL2）。命名规范见 AGENTS.md §5。
--
-- 说明：本迁移只建「授权载体」表；Agent 会话的 CRUD 属 M4 的 ACL 细化，
-- 本期守卫通过注入的解析器读取本表，读不到一律按「无授权」处理（最保守）。

CREATE TABLE IF NOT EXISTS agent_sessions (
  id              BIGINT PRIMARY KEY,
  agent_id        VARCHAR(64) NOT NULL,
  display_name    VARCHAR(128) NOT NULL,
  model           VARCHAR(64) NULL,
  version         VARCHAR(32) NULL,
  api_key_id      BIGINT NULL,
  -- 动作白名单：不在名单内的动作一律拒绝（DESIGN-AGENT-001#DEC-02）。
  capabilities    JSONB NOT NULL DEFAULT '[]'::jsonb,
  -- per-agent 策略覆盖（动作 → auto|approval|human_only）；只允许收紧，放宽无效。
  approval_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
  owner_id        BIGINT NULL,                 -- 人类责任人
  status          VARCHAR(12) NOT NULL DEFAULT 'active',  -- active | revoked
  created_at      TIMESTAMPTZ(3) NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ(3) NULL,
  CONSTRAINT uniq_agent_sessions_agent_id UNIQUE (agent_id),
  CONSTRAINT chk_agent_sessions_status CHECK (status IN ('active', 'revoked'))
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_status ON agent_sessions (status);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_owner ON agent_sessions (owner_id) WHERE owner_id IS NOT NULL;

-- ---------- 最小权限（与 000010/000019 同一模型） ----------
-- regto_app 需要读会话（守卫判定用），但不得自行改会话——改会话是人类专属动作。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'regto_app') THEN
        EXECUTE 'REVOKE ALL ON public.agent_sessions FROM regto_app';
        EXECUTE 'GRANT SELECT ON public.agent_sessions TO regto_app';
    END IF;
END $$;

-- 维护 schema 版本契约（文件头纪律：每个迁移末尾必须把 version 更新为自身版本号）。
INSERT INTO schema_meta (singleton, version)
VALUES (true, 20)
ON CONFLICT (singleton) DO UPDATE
  SET version = EXCLUDED.version, updated_at = now();
