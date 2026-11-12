package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/agent"
	"github.com/mst8222/regto/internal/domain/shared"
)

// AgentSessionRepo 读取 Agent 会话的授权视图（migrations/000020）。
//
// 只提供只读方法：修改会话（创建/吊销/改能力）是人类专属动作（SPEC-AGENT-001 REQ-01），
// 且 regto_app 对本表只有 SELECT（迁移 000020 已授权）。
type AgentSessionRepo struct {
	pool *pgxpool.Pool
}

// NewAgentSessionRepo 构造 Agent 会话仓储。
func NewAgentSessionRepo(pool *pgxpool.Pool) *AgentSessionRepo { return &AgentSessionRepo{pool: pool} }

// ByAgentID 按 agent_id 取会话；不存在或已吊销都返回零值会话（守卫按「无授权」处理）。
//
// 约定：查不到不算错误——会话缺失等价于能力白名单为空，是最保守的结果。
// expires_at 必须随判定视图带出（SPEC-COLLAB-001#REQ-07 必修）：
// 只在这里读而不在守卫里补查，保证「判定输入」单一来源。
func (r *AgentSessionRepo) ByAgentID(ctx context.Context, agentID string) (agent.Session, error) {
	const q = `SELECT agent_id, capabilities, approval_policy, status, expires_at
	             FROM agent_sessions WHERE agent_id = $1`
	var (
		id      string
		caps    []byte
		pol     []byte
		st      string
		expires pgtype.Timestamptz
	)
	err := r.pool.QueryRow(ctx, q, agentID).Scan(&id, &caps, &pol, &st, &expires)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agent.Session{AgentID: agentID}, nil // 空白名单
		}
		return agent.Session{}, fmt.Errorf("查询 agent_sessions 失败: %w", err)
	}
	s := agent.Session{AgentID: id, Status: agent.SessionStatus(st)}
	if expires.Valid {
		s.ExpiresAtMs = expires.Time.UTC().UnixMilli()
	}
	if len(caps) > 0 {
		_ = json.Unmarshal(caps, &s.Capabilities)
	}
	if len(pol) > 0 {
		_ = json.Unmarshal(pol, &s.ApprovalPolicy)
	}
	return s, nil
}

// CreateSession 登记一个 Agent 会话（SPEC-AGENT-002 REQ-10）。
//
// 校验由领域层负责（ValidateSessionConfig），本方法只负责落库：
// 仓储不该重复实现业务规则，否则 CLI / HTTP / 未来的 UI 会各有一套判定。
// agent_id 唯一冲突映射为 CONFLICT（409），而不是把 pq 错误裸露给调用方。
func (r *AgentSessionRepo) CreateSession(ctx context.Context, in agent.CreateSessionInput) (agent.RegisteredSession, error) {
	const q = `INSERT INTO agent_sessions
	             (agent_id, display_name, model, capabilities, approval_policy, owner_id, status, expires_at)
	           VALUES ($1, $2, NULLIF($3,''), $4, $5, NULLIF($6,0), 'active', $7)
	           RETURNING id, agent_id, display_name, COALESCE(model,''), capabilities,
	                     approval_policy, COALESCE(owner_id,0), status, created_at, expires_at`
	sess, err := r.scanSession(r.pool.QueryRow(ctx, q,
		in.AgentID, in.DisplayName, in.Model, capsJSON(in.Capabilities),
		policyJSON(in.ApprovalPolicy), in.OwnerID, msOrNilTrace(in.ExpiresAtMs)))
	if err != nil {
		if isUniqueViolation(err) {
			return agent.RegisteredSession{}, shared.New("CONFLICT",
				"该 agent_id 已存在会话: "+in.AgentID, map[string]any{"agent_id": in.AgentID})
		}
		return agent.RegisteredSession{}, fmt.Errorf("登记 Agent 会话失败: %w", err)
	}
	return sess, nil
}

// ListSessions 列出全部会话（按 id 升序，稳定的分页前提）。
func (r *AgentSessionRepo) ListSessions(ctx context.Context) ([]agent.RegisteredSession, error) {
	const q = `SELECT id, agent_id, display_name, COALESCE(model,''), capabilities,
	                  approval_policy, COALESCE(owner_id,0), status, created_at, expires_at
	             FROM agent_sessions ORDER BY id`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("查询 Agent 会话列表失败: %w", err)
	}
	defer rows.Close()

	var out []agent.RegisteredSession
	for rows.Next() {
		s, err := r.scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RevokeSession 吊销会话（软删：status='revoked'）。
//
// 不做物理删除：AGENTS.md 规定物理删除只归 DBA/迁移，
// 且删掉行会丢失「这个 Agent 曾被授权过」这一审计事实。
func (r *AgentSessionRepo) RevokeSession(ctx context.Context, agentID string) error {
	const q = `UPDATE agent_sessions SET status = 'revoked'
	            WHERE agent_id = $1 AND status = 'active'`
	ct, err := r.pool.Exec(ctx, q, agentID)
	if err != nil {
		return fmt.Errorf("吊销 Agent 会话失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		// 不存在与「已吊销」对外都表现为找不到可吊销的活跃会话。
		return shared.New("NOT_FOUND", "没有可吊销的活跃会话: "+agentID,
			map[string]any{"agent_id": agentID})
	}
	return nil
}

// rowScanner 抽象 pgx.Row 与 pgx.Rows 的共同能力，便于列表与单条复用同一扫描逻辑。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSession 把一行 agent_sessions 扫描成领域视图。
func (r *AgentSessionRepo) scanSession(row rowScanner) (agent.RegisteredSession, error) {
	var (
		sess    agent.RegisteredSession
		caps    []byte
		pol     []byte
		status  string
		created time.Time
		expires pgtype.Timestamptz
	)
	if err := row.Scan(&sess.ID, &sess.AgentID, &sess.DisplayName, &sess.Model, &caps,
		&pol, &sess.OwnerID, &status, &created, &expires); err != nil {
		return agent.RegisteredSession{}, fmt.Errorf("扫描 Agent 会话失败: %w", err)
	}
	sess.Status = agent.SessionStatus(status)
	sess.CreatedAtMs = created.UTC().UnixMilli()
	if expires.Valid {
		sess.ExpiresAtMs = expires.Time.UTC().UnixMilli()
	}
	if len(caps) > 0 {
		_ = json.Unmarshal(caps, &sess.Capabilities)
	}
	if len(pol) > 0 {
		_ = json.Unmarshal(pol, &sess.ApprovalPolicy)
	}
	return sess, nil
}

// capsJSON 把能力列表序列化为 JSONB 参数；nil 写成空数组（列有 NOT NULL DEFAULT）。
func capsJSON(caps []string) string {
	if len(caps) == 0 {
		return "[]"
	}
	b, err := json.Marshal(caps)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// policyJSON 把策略覆盖序列化为 JSONB 参数。
func policyJSON(p map[string]string) string {
	if len(p) == 0 {
		return "{}"
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(b)
}
