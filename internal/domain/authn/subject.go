// Package authn 定义认证域的领域概念：执行主体（Subject）。
//
// 本包零外部依赖（AGENTS.md §0 铁律 3）：密码学实现在 platform/authn，
// 存储实现在 adapter/persistence/postgres，本包只持有值语义。
//
// 规格来源：spec/authn/authentication/SPEC-AUTHN-001.md。
package authn

// 主体类型（与留痕的 actor_type 一致：AGENTS.md §3.1）。
const (
	KindHuman  = "human"
	KindAI     = "ai"
	KindSystem = "system"
)

// Subject 是认证通过的执行主体。
// 它由服务端推导，客户端无法通过请求头自报（DESIGN-AUTHN-001 DEC-02）。
type Subject struct {
	Kind    string // human | ai | system
	ID      int64  // 人类 = users.id；AI = 机器账号 users.id
	Account string // 登录名（AI 主体为 agent 的账号）
	AgentID string // AI 主体的 agent_id；人类为空
}

// IsValid 报告主体是否可用（未认证主体为零值）。
func (s Subject) IsValid() bool {
	switch s.Kind {
	case KindHuman, KindAI, KindSystem:
		return s.ID > 0
	}
	return false
}

// IsAI 报告是否 AI 主体。
func (s *Subject) IsAI() bool { return s != nil && s.Kind == KindAI }

// Label 返回可打印的主体标识（日志与留痕摘要用）。
func (s Subject) Label() string {
	if s.Kind == KindAI && s.AgentID != "" {
		return KindAI + ":" + s.AgentID
	}
	return s.Kind + ":" + itoa(s.ID)
}

// itoa 是轻量整数转字符串（避免引入 strconv 之外的依赖，保持领域包纯净）。
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
