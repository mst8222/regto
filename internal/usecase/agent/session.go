package agent

import (
	"context"
	"strings"

	dagent "github.com/mst8222/regto/internal/domain/agent"
	"github.com/mst8222/regto/internal/domain/shared"
)

// 会话校验错误码（与领域层的 INVALID_SESSION_CONFIG 同义，用于「入参本身不成立」）。
const (
	CodeAgentIDRequired  = "INVALID_SESSION_CONFIG"
	maxAgentIDLength     = 64  // 与 agent_sessions.agent_id VARCHAR(64) 对齐
	maxDisplayNameLength = 128 // 与 display_name VARCHAR(128) 对齐
)

// sessionRepo 是会话运维需要的写能力；由 postgres.AgentSessionRepo 实现。
type sessionRepo interface {
	CreateSession(ctx context.Context, in dagent.CreateSessionInput) (dagent.RegisteredSession, error)
	ListSessions(ctx context.Context) ([]dagent.RegisteredSession, error)
	RevokeSession(ctx context.Context, agentID string) error
}

// SessionService 处理 Agent 会话的登记 / 吊销 / 列表。
//
// 权限边界不在本服务里（本服务不判断调用者是谁）：那是 HTTP 守卫的事，
// 动作 agent.session_manage 已归 human_only 档（SPEC-AGENT-002 REQ-10）。
// 本服务只保证「配置本身合法」——两条防线缺一不可：
// 守卫保证 AI 调不到，ValidateSessionConfig 保证人类也开不出越界会话。
type SessionService struct {
	repo sessionRepo
}

// NewSessionService 构造会话服务。
func NewSessionService(repo sessionRepo) *SessionService {
	return &SessionService{repo: repo}
}

// List 返回全部会话。
func (s *SessionService) List(ctx context.Context) ([]dagent.RegisteredSession, error) {
	return s.repo.ListSessions(ctx)
}

// Create 登记一个 Agent 会话。
func (s *SessionService) Create(ctx context.Context, in dagent.CreateSessionInput) (dagent.RegisteredSession, error) {
	in.AgentID = strings.TrimSpace(in.AgentID)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Model = strings.TrimSpace(in.Model)
	if in.AgentID == "" {
		return dagent.RegisteredSession{}, shared.New(CodeAgentIDRequired,
			"agent_id 不可为空", map[string]any{"field": "agent_id"})
	}
	if len(in.AgentID) > maxAgentIDLength {
		return dagent.RegisteredSession{}, shared.New(CodeAgentIDRequired,
			"agent_id 过长（上限 64 字符）", map[string]any{"field": "agent_id", "max": maxAgentIDLength})
	}
	if in.DisplayName == "" {
		in.DisplayName = in.AgentID // 展示名缺省用 agent_id，避免前端拿到空标题
	}
	if len(in.DisplayName) > maxDisplayNameLength {
		return dagent.RegisteredSession{}, shared.New(CodeAgentIDRequired,
			"display_name 过长（上限 128 字符）",
			map[string]any{"field": "display_name", "max": maxDisplayNameLength})
	}
	// 自举防护：能力 ⊆ 已知动作、不得含 human_only、策略只允许收紧。
	if err := dagent.ValidateSessionConfig(in.Capabilities, in.ApprovalPolicy); err != nil {
		return dagent.RegisteredSession{}, err
	}
	return s.repo.CreateSession(ctx, in)
}

// Revoke 吊销会话（软删）。
func (s *SessionService) Revoke(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return shared.New(CodeAgentIDRequired, "agent_id 不可为空",
			map[string]any{"field": "agent_id"})
	}
	return s.repo.RevokeSession(ctx, agentID)
}
