package agent

import "github.com/mst8222/regto/internal/domain/shared"

// SessionStatus 是 Agent 会话状态。
type SessionStatus string

// 会话状态常量。
const (
	StatusActive  SessionStatus = "active"
	StatusRevoked SessionStatus = "revoked"
)

// ValidStatus 判断会话状态是否合法。
func ValidStatus(s SessionStatus) bool {
	return s == StatusActive || s == StatusRevoked
}

// Session 是 Agent 会话的授权视图（持久化见 migrations/000020 agent_sessions）。
//
// 领域层只关心四个字段：能力白名单、策略覆盖、状态、过期时间。
type Session struct {
	AgentID string
	// Capabilities 是动作白名单；不在名单内的动作一律拒绝（DEC-02）。
	// 空或 nil 表示「无授权」——最保守，会话缺失时正是这种形态。
	Capabilities []string
	// ApprovalPolicy 是 per-agent 的策略覆盖（动作 → 策略）。
	// 只允许**收紧**（auto→approval→human_only），放宽项会被 EffectivePolicy 忽略。
	ApprovalPolicy map[string]string
	Status         SessionStatus
	// ExpiresAtMs 是会话过期时刻（UTC 毫秒）；0 表示不过期。
	// SPEC-COLLAB-001#REQ-07 必修项：过期会话必须被拒——HTTP 短请求下危害有限，
	// 但 WS 一次握手存活数小时，不判过期等于过期会话在长连接里全程有效。
	ExpiresAtMs int64
}

// Expired 报告会话在给定时刻（UTC 毫秒）是否已过期。
//
// 边界语义：nowMs == ExpiresAtMs 即视为过期（到期那一刻就不该再放行）；
// ExpiresAtMs 为 0 表示永不过期。时间由调用方注入而非取墙钟，
// 保持本函数（及 Evaluate）为纯函数，WS 层动作级复检才能零成本复用。
func (s Session) Expired(nowMs int64) bool {
	return s.ExpiresAtMs > 0 && nowMs >= s.ExpiresAtMs
}

// Allows 报告该会话的能力白名单是否包含动作。
func (s Session) Allows(a Action) bool {
	for _, c := range s.Capabilities {
		if c == string(a) {
			return true
		}
	}
	return false
}

// policyRank 给出策略的严格程度序（用于「只允许收紧」的比较）。
func policyRank(p Policy) int {
	switch p {
	case PolicyAuto:
		return 0
	case PolicyApproval:
		return 1
	case PolicyHumanOnly:
		return 2
	}
	return 2 // 非法值按最严格处理
}

// EffectivePolicy 返回该会话下动作的实际策略。
//
// 覆盖规则：仅当 per-agent 策略**严于或等于**默认策略时生效；
// 试图放宽（如把 human_only 降成 auto）的覆盖项被忽略，
// 防止运营误操作把人类专属动作开给 AI。
func (s Session) EffectivePolicy(a Action) Policy {
	def := PolicyOf(a)
	raw, ok := s.ApprovalPolicy[string(a)]
	if !ok {
		return def
	}
	over := Policy(raw)
	if !ValidPolicy(over) {
		return def
	}
	if policyRank(over) < policyRank(def) {
		return def // 放宽无效
	}
	return over
}

// CodeInvalidSessionConfig 是会话配置非法的错误码（SPEC-AGENT-002 REQ-10）。
const CodeInvalidSessionConfig = "INVALID_SESSION_CONFIG"

// ValidateSessionConfig 校验「人类要登记 / 更新的 Agent 会话配置」。
//
// 这是**自举防护**的落点（DESIGN-AGENT-002#DEC-07）：中间件只保证「AI 调不到
// 会话接口」，本函数保证「人类也开不出越界的会话」——否则只要把 spec.approve
// 写进某个 Agent 的 capabilities，就等于造出一个「能批自己」的 AI。
//
// 三条规则：
//  1. 能力必须 ⊆ 已登记动作。拼错的动作不是「暂时无效」而是配置错误：
//     它会让 Agent 在生产里静默少一个能力，且没人会去查。
//  2. 能力不得包含 human_only 档动作。人类专属意味着「AI 不该做」，
//     批一下也不行，所以它连进白名单的资格都没有。
//  3. 策略覆盖只允许收紧。放宽项当场拒绝，而不是存进去之后被 EffectivePolicy 静默忽略——
//     静默忽略会让运维以为自己改成功了。
func ValidateSessionConfig(caps []string, policy map[string]string) error {
	for _, c := range caps {
		a := Action(c)
		if !IsKnownAction(a) {
			return shared.New(CodeInvalidSessionConfig,
				"能力白名单含未登记动作: "+c,
				map[string]any{"capability": c, "reason": "unknown_action"})
		}
		if PolicyOf(a) == PolicyHumanOnly {
			return shared.New(CodeInvalidSessionConfig,
				"人类专属动作不得授予 Agent: "+c,
				map[string]any{"capability": c, "policy": string(PolicyHumanOnly),
					"reason": "human_only_capability"})
		}
	}
	for act, raw := range policy {
		a := Action(act)
		if !IsKnownAction(a) {
			return shared.New(CodeInvalidSessionConfig,
				"策略覆盖含未登记动作: "+act,
				map[string]any{"action": act, "reason": "unknown_action"})
		}
		over := Policy(raw)
		if !ValidPolicy(over) {
			return shared.New(CodeInvalidSessionConfig,
				"策略取值非法: "+act+"="+raw,
				map[string]any{"action": act, "policy": raw, "reason": "invalid_policy"})
		}
		if policyRank(over) < policyRank(PolicyOf(a)) {
			return shared.New(CodeInvalidSessionConfig,
				"策略覆盖只能收紧，不能放宽: "+act+"="+raw,
				map[string]any{"action": act, "policy": raw,
					"default": string(PolicyOf(a)), "reason": "policy_loosens"})
		}
	}
	return nil
}

// RegisteredSession 是会话的完整视图：比判定用的 Session 多出展示与归属字段。
//
// 为什么单独一个类型：判定只看「能力 + 策略 + 状态」，而运维接口要展示
// 「谁负责、什么时候建的、什么时候过期」。把两者混成一个结构，
// 判定路径就会被迫携带一堆无用的展示字段（也更容易误改）。
type RegisteredSession struct {
	ID             int64
	AgentID        string
	DisplayName    string
	Model          string
	Capabilities   []string
	ApprovalPolicy map[string]string
	OwnerID        int64
	Status         SessionStatus
	CreatedAtMs    int64
	ExpiresAtMs    int64 // 0 表示不过期
}

// CreateSessionInput 是登记 Agent 会话的入参。
type CreateSessionInput struct {
	AgentID        string
	DisplayName    string
	Model          string
	Capabilities   []string
	ApprovalPolicy map[string]string
	OwnerID        int64
	ExpiresAtMs    int64
}
