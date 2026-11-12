package httputil

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mst8222/regto/internal/domain/agent"
	dtrace "github.com/mst8222/regto/internal/domain/trace"
)

// 审批令牌请求头（ROADMAP §5.4 越权流程 / SPEC-AGENT-001 REQ-06）。
const HeaderApproval = "X-Regto-Approval"

// SessionResolver 按 agent_id 取会话的授权视图。
// 由 usecase 注入：httputil 不反向依赖 usecase 与持久层。
type SessionResolver func(ctx context.Context, agentID string) (agent.Session, error)

// ApprovalLookup 按 trace_id 取审批状态快照（四要素，SPEC-AGENT-001 REQ-06）。
type ApprovalLookup func(ctx context.Context, traceID string) (agent.Approval, error)

// PendingLookup 查找同 Agent+动作+对象引用下可复用的 pending 记录（NFR-02）。
// 返回空串表示无。
type PendingLookup func(ctx context.Context, actorID, action, objectRef string) (string, error)

// Tracer 记录一条留痕并返回 trace_id（真源是 jsonl 文件，DB 是同步过去的只读副本）。
type Tracer func(ctx context.Context, rec dtrace.Record) (string, error)

// AgentGuard 是 AI 主体的授权守卫（SPEC-AGENT-001）。
//
// 只对 actor_type=ai 生效：人类与系统主体直接放行（REQ-07）。
type AgentGuard struct {
	sessions SessionResolver
	approval ApprovalLookup
	pending  PendingLookup
	tracer   Tracer
	action   func(method, route string) (agent.Action, bool)
	logger   func(ctx context.Context, msg string, kv ...any)
}

// NewAgentGuard 构造守卫。actionOf 把「方法 + 路由模式」映射为动作，
// 返回 false 表示未知路由（按最严格的 human_only 处理，DEC-01）。
func NewAgentGuard(sessions SessionResolver, approval ApprovalLookup,
	pending PendingLookup, tracer Tracer,
	actionOf func(method, route string) (agent.Action, bool)) *AgentGuard {
	return &AgentGuard{sessions: sessions, approval: approval,
		pending: pending, tracer: tracer, action: actionOf}
}

// Middleware 返回授权守卫中间件（必须挂在认证中间件之后）。
func (g *AgentGuard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, ok := SubjectFromContext(r.Context())
		if !ok || !sub.IsAI() {
			next.ServeHTTP(w, r) // 人类 / 系统直通（REQ-07）
			return
		}

		action, known := g.resolveAction(r)
		if !known {
			// 未知路由：按人类专属拒绝，但不入队——连动作都认不出来的请求
			// 不配占用审批队列（否则能被用来刷队列）。
			g.deny(w, r, agent.VerdictHumanOnly, "", action)
			return
		}

		session, err := g.sessions(r.Context(), sub.AgentID)
		if err != nil {
			// 取不到会话 = 无法证明有授权 → 按最保守处理：白名单外。
			session = agent.Session{AgentID: sub.AgentID}
		}

		var ap *agent.Approval
		if token := strings.TrimSpace(r.Header.Get(HeaderApproval)); token != "" {
			st, lerr := g.approval(r.Context(), token)
			if lerr != nil {
				st = agent.Approval{} // 查不到 = 批文无效
			}
			ap = &st
		}

		// nowMillis 每请求现取：过期判定以请求时刻为准（SPEC-COLLAB-001#REQ-07）。
		v := agent.Evaluate(action, session, ap, nowMillis())
		if v == agent.VerdictAllow {
			next.ServeHTTP(w, r)
			return
		}
		g.deny(w, r, v, r.Header.Get(HeaderApproval), action)
	})
}

// resolveAction 从 chi 路由模式解析动作；无法解析时返回 known=false。
func (g *AgentGuard) resolveAction(r *http.Request) (agent.Action, bool) {
	route := ""
	if rc := chi.RouteContext(r.Context()); rc != nil {
		route = rc.RoutePattern()
	}
	if g.action == nil {
		return "", false
	}
	return g.action(r.Method, route)
}

// deny 生成越权响应；需要入队时先写 pending 留痕，把 trace_id 回给调用方。
//
// 入队与报错是同一次动作的两面（DEC-03）：要么既有 403 又有 pending 留痕，
// 要么都没有（写留痕失败时返回 503 拒绝，绝不「越权但没留痕」）。
func (g *AgentGuard) deny(w http.ResponseWriter, r *http.Request,
	v agent.Verdict, token string, action agent.Action) {
	traceID := token
	if agent.ShouldEnqueue(v) && g.tracer != nil && g.pending != nil {
		sub, _ := SubjectFromContext(r.Context())
		ref := r.Method + " " + r.URL.Path
		// NFR-02：同一 Agent + 动作 + 对象已有 pending 时复用，不重复入队。
		if exist, err := g.pending(r.Context(), sub.AgentID, string(action), ref); err == nil && exist != "" {
			traceID = exist
		} else if id, err := g.tracer(r.Context(), dtrace.Record{
			Action:           string(action),
			ObjectType:       "action",
			ObjectID:         string(action),
			ObjectRef:        ref,
			ActorType:        dtrace.ActorAI,
			ActorID:          sub.AgentID,
			Summary:          "AI 越权被拦截：" + string(action) + " → " + string(v),
			Outcome:          dtrace.OutcomeBlocked,
			NeedsHumanReview: true,
			ReviewStatus:     "pending",
			OccurredAtMs:     nowMillis(),
			RecordedAtMs:     nowMillis(),
		}); err == nil {
			traceID = id
		} else {
			writeAgentProblem(w, r, http.StatusServiceUnavailable,
				"TRACE_WRITE_FAILED", "越权留痕写入失败，请求已拒绝", "")
			return
		}
	}
	writeAgentProblem(w, r, http.StatusForbidden, agent.CodeOf(v), detailFor(v, action), traceID)
}

// detailFor 生成人类可读的拒绝原因（中文，便于审批人一眼看懂）。
func detailFor(v agent.Verdict, action agent.Action) string {
	switch v {
	case agent.VerdictNeedApproval:
		return "动作 " + string(action) + " 需要人类审批，已生成待审批留痕"
	case agent.VerdictApprovalInvalid:
		return "动作 " + string(action) + " 的审批令牌无效（动作/属主不符、未批准或已驳回）"
	case agent.VerdictHumanOnly:
		return "动作 " + string(action) + " 为人类专属，AI 不可执行"
	case agent.VerdictCapabilityDenied:
		return "动作 " + string(action) + " 不在该 Agent 的能力白名单内"
	case agent.VerdictSessionRevoked:
		return "Agent 会话已吊销或已过期（expires_at 已到）"
	}
	return "动作 " + string(action) + " 被拒绝"
}

// writeAgentProblem 直接写 RFC 7807 响应。
//
// 不走 WriteProblem：守卫的错误码（AI_ACTION_REQUIRES_APPROVAL 等）是 403，
// 而 statusFor 对未登记码的 DomainError 一律映射 422，语义不符。
func writeAgentProblem(w http.ResponseWriter, r *http.Request,
	status int, code, detail, traceID string) {
	p := Problem{
		Type:      "https://regto.dev/problems/" + strings.ToLower(code),
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Code:      code,
		RequestID: RequestID(r.Context()),
	}
	if traceID != "" {
		p.Meta = map[string]any{"trace_id": traceID}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// nowMillis 返回当前 UTC 毫秒（留痕时间字段）。
func nowMillis() int64 { return time.Now().UTC().UnixMilli() }
