package agent

// Verdict 是守卫的判定结果。
type Verdict string

// 判定结果常量。
const (
	VerdictAllow            Verdict = "allow"             // 放行
	VerdictNeedApproval     Verdict = "need_approval"     // 需审批：403 + 入队
	VerdictHumanOnly        Verdict = "human_only"        // 人类专属：403，不入队
	VerdictCapabilityDenied Verdict = "capability_denied" // 白名单外：403，不入队
	VerdictSessionRevoked   Verdict = "session_revoked"   // 会话已吊销：403，不入队
	VerdictApprovalInvalid  Verdict = "approval_invalid"  // 批文无效：403 + 入队（重新申请）
)

// 错误码（响应体 code 字段，SPEC-AGENT-001 REQ-03/04、REQ-02）。
const (
	CodeRequiresApproval = "AI_ACTION_REQUIRES_APPROVAL"
	CodeHumanOnly        = "AGENT_ACTION_HUMAN_ONLY"
	CodeCapabilityDenied = "AGENT_CAPABILITY_DENIED"
	CodeSessionRevoked   = "AGENT_SESSION_REVOKED"
	CodeApprovalInvalid  = "AI_APPROVAL_INVALID"
)

// CodeOf 返回判定对应的错误码；放行返回空串。
func CodeOf(v Verdict) string {
	switch v {
	case VerdictNeedApproval:
		return CodeRequiresApproval
	case VerdictHumanOnly:
		return CodeHumanOnly
	case VerdictCapabilityDenied:
		return CodeCapabilityDenied
	case VerdictSessionRevoked:
		return CodeSessionRevoked
	case VerdictApprovalInvalid:
		return CodeApprovalInvalid
	}
	return ""
}

// ShouldEnqueue 报告该判定是否需要生成 pending 审批留痕。
//
// 只有「需审批」与「批文无效」入队：前者是首次撞墙，后者是批文不对需要重新申请。
// 白名单外 / 人类专属 / 会话吊销一律不入队——它们不是「人类批一下就行」的情形
// （SPEC-AGENT-001 REQ-04、AC-02）。
func ShouldEnqueue(v Verdict) bool {
	return v == VerdictNeedApproval || v == VerdictApprovalInvalid
}

// Approval 是审批令牌指向的留痕状态快照。
//
// 由守卫从库内查出后传入：领域层不碰 IO，只做四要素匹配（DEC-05）。
// traces 是只读副本（不可 UPDATE），故「已批准」表现为
// 存在一条 action=approve 且 object_id 指向该 trace 的新记录（REQ-05）。
type Approval struct {
	Found    bool   // 该 trace_id 是否存在
	Pending  bool   // review_status 是否为 pending
	Action   string // 留痕记录的 action（须与本次动作一致）
	ActorID  string // 留痕记录的 actor_id（须与当前 Agent 一致）
	Approved bool   // 是否已有 approve 记录
	Rejected bool   // 是否已有 reject 记录
}

// TokenValid 校验审批令牌的四要素（DEC-05）：
// 存在 且 pending 且 已批准 且 未驳回 且 动作一致 且 属主一致。
func TokenValid(a Action, agentID string, ap Approval) bool {
	return ap.Found &&
		ap.Pending &&
		ap.Approved &&
		!ap.Rejected &&
		ap.Action == string(a) &&
		ap.ActorID == agentID
}

// Evaluate 对一次 AI 调用做出判定。
//
// nowMs 是当前时刻（UTC 毫秒），由调用方注入：领域层不取墙钟，
// 保持纯函数（DESIGN-COLLAB-001#DEC-07：WS 层动作级复检直接复用本函数）。
//
// 判定顺序（过期/吊销最先行，白名单其次，DEC-02）：
//  1. 会话已吊销或已过期（SPEC-COLLAB-001#REQ-07 必修）→ VerdictSessionRevoked
//  2. 动作不在能力白名单 → VerdictCapabilityDenied
//  3. 策略 = auto → VerdictAllow
//  4. 策略 = human_only → VerdictHumanOnly
//  5. 策略 = approval：
//     - 无令牌 → VerdictNeedApproval
//     - 令牌四要素全匹配 → VerdictAllow
//     - 否则 → VerdictApprovalInvalid（需重新申请，故也入队）
//
// 过期与吊销共用同一判定：二者对调用方的含义一致（会话授权已终止），
// 且复用既有错误码 AGENT_SESSION_REVOKED，前端与 SDK 无需新增分支。
// 过期优先于白名单：过期会话连「看白名单」的资格都没有——
// 否则一个过期 Agent 仍可通过带合法批文走 approval 档放行。
func Evaluate(a Action, s Session, ap *Approval, nowMs int64) Verdict {
	if s.Status != "" && s.Status != StatusActive {
		return VerdictSessionRevoked
	}
	if s.Expired(nowMs) {
		return VerdictSessionRevoked
	}
	if !s.Allows(a) {
		return VerdictCapabilityDenied
	}
	switch s.EffectivePolicy(a) {
	case PolicyAuto:
		return VerdictAllow
	case PolicyHumanOnly:
		return VerdictHumanOnly
	}
	// approval 档
	if ap == nil {
		return VerdictNeedApproval
	}
	if TokenValid(a, s.AgentID, *ap) {
		return VerdictAllow
	}
	return VerdictApprovalInvalid
}
