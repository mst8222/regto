package httputil

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mst8222/regto/internal/domain/account"
	dauthn "github.com/mst8222/regto/internal/domain/authn"
	dtrace "github.com/mst8222/regto/internal/domain/trace"
)

// ACL 运行模式（SPEC-ACCOUNT-001 REQ-04）。
//
// 默认 enforce，而不是 warn：M3 的教训是「守卫写好了但没接线」等于没有，
// 而默认 warn 的 ACL 会安静地记一堆日志，所有人都以为权限已经生效。
// 失败方向安全的做法是「默认拒绝」，把没配角色的问题在升级当天就暴露出来。
const (
	ACLModeEnforce = "enforce"
	ACLModeWarn    = "warn"
	ACLModeOff     = "off"
)

// NormalizeACLMode 校验并规范化模式字符串；空值视为 enforce。
func NormalizeACLMode(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", ACLModeEnforce:
		return ACLModeEnforce, nil
	case ACLModeWarn:
		return ACLModeWarn, nil
	case ACLModeOff:
		return ACLModeOff, nil
	}
	return "", &InvalidACLModeError{Value: s}
}

// InvalidACLModeError 表示配置了不认识的模式（启动即失败，不要静默回退成 off）。
type InvalidACLModeError struct{ Value string }

func (e *InvalidACLModeError) Error() string {
	return "REGTO_ACL_MODE 非法: " + e.Value + "（允许 enforce|warn|off）"
}

// 路由归属类型，与 http.ACLRouteOf 的返回值对应（用字符串避免反向依赖 http 包）。
const (
	ACLRouteMapped  = "mapped"
	ACLRouteExempt  = "exempt"
	ACLRouteUnknown = "unknown"
)

// ACLLookup 判定某个用户能否对某资源执行某动作（由 usecase/account 实现）。
type ACLLookup func(ctx context.Context, userID int64, res account.Resource, act account.Action) (account.Decision, error)

// ACLRouteMapper 把方法 + 路由模式映射为判定输入（由 http.ACLRouteOf 实现）。
type ACLRouteMapper func(method, route string) (account.Resource, account.Action, string)

// ACLGuard 是**人类**主体的授权守卫。
//
// 与 AgentGuard 按主体类型分流（DESIGN-ACCOUNT-001#DEC-06）：人类看角色矩阵，
// AI 看会话能力与三档策略。两者依据不同，串联只会产生互相误导的拒绝理由
// （看到 ACL_DENIED 却是因为 AI 会话没能力）。同一请求只会被其中一个判定。
type ACLGuard struct {
	mode   string
	decide ACLLookup
	route  ACLRouteMapper
	tracer Tracer
	logger func(ctx context.Context, msg string, kv ...any)
}

// NewACLGuard 构造人类侧守卫。
//
// tracer 可为 nil（此时拒绝不带 trace_id）：留痕是「越权尝试必须有记录」的实现，
// 生产必须注入；nil 只用于单测与最小装配。
func NewACLGuard(mode string, decide ACLLookup, route ACLRouteMapper,
	tracer Tracer, logger func(ctx context.Context, msg string, kv ...any)) *ACLGuard {
	if logger == nil {
		logger = func(context.Context, string, ...any) {}
	}
	return &ACLGuard{mode: mode, decide: decide, route: route, tracer: tracer, logger: logger}
}

// Mode 返回当前模式（供启动日志与健康检查展示，避免「以为开着其实关了」）。
func (g *ACLGuard) Mode() string { return g.mode }

// Middleware 返回人类授权中间件（必须挂在认证中间件之后，且用 r.With 挂端点链）。
func (g *ACLGuard) Middleware(next http.Handler) http.Handler {
	if g.mode == ACLModeOff {
		return next // 完全关闭：行为与接入 ACL 之前一致
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, ok := SubjectFromContext(r.Context())
		if !ok || sub.Kind != dauthn.KindHuman {
			next.ServeHTTP(w, r) // AI / 系统主体：交给 AgentGuard，此处不判定
			return
		}

		res, act, kind := g.resolve(r)
		switch kind {
		case ACLRouteExempt:
			next.ServeHTTP(w, r) // 行级鉴权（本人 / admin）在 handler
			return
		case ACLRouteUnknown:
			// 未登记 = 无法证明有授权 = 拒绝。新增路由必须登记，否则上线即 403。
			if g.rejectOrWarn(w, r, sub, res, act, account.Denied(account.ReasonUnknownResource)) {
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		d, err := g.decide(r.Context(), sub.ID, res, act)
		if err != nil {
			// 查库失败时不能猜：既不放行，也不谎称「你没权限」，而是明确 503。
			g.logger(r.Context(), "ACL 判定失败，请求已拒绝",
				"user_id", sub.ID, "resource", string(res), "action", act.String(), "error", err.Error())
			writeAgentProblem(w, r, http.StatusServiceUnavailable,
				"ACL_CHECK_FAILED", "权限判定失败，请求已拒绝", "")
			return
		}
		if d.Allowed {
			next.ServeHTTP(w, r)
			return
		}
		if g.rejectOrWarn(w, r, sub, res, act, d) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resolve 取路由模式并映射。
//
// chi 的 RoutePattern 在**端点链**上才是完整模式；挂在组中间件上只给 /api/v1/*，
// 那样所有路由都会落进 ACLRouteUnknown 而全量 403。故本中间件必须用 r.With 挂到端点。
func (g *ACLGuard) resolve(r *http.Request) (account.Resource, account.Action, string) {
	route := ""
	if rc := chi.RouteContext(r.Context()); rc != nil {
		route = rc.RoutePattern()
	}
	if g.route == nil {
		return "", 0, ACLRouteUnknown
	}
	return g.route(r.Method, route)
}

// rejectOrWarn 处理一次拒绝：enforce 下写留痕 + 403；warn 下只记录并返回 false（继续链路）。
//
// 返回值 reported=true 表示「已经写出响应，调用方不要再继续」，false 表示「warn 放行」。
// 刻意用返回值而不是把 next 存到结构体上：结构体字段在并发请求间会被互相覆盖。
func (g *ACLGuard) rejectOrWarn(w http.ResponseWriter, r *http.Request, sub dauthn.Subject,
	res account.Resource, act account.Action, d account.Decision) (reported bool) {
	if g.mode == ACLModeWarn {
		g.logger(r.Context(), "ACL(warn) 越权请求已放行（当前未启用 enforce）",
			"user_id", sub.ID, "resource", string(res), "action", act.String(), "reason", d.Reason)
		w.Header().Set("X-Regto-ACL", "warn:"+d.Reason)
		return false
	}

	action := string(res) + "." + act.String()
	traceID := ""
	if g.tracer != nil {
		id, err := g.tracer(r.Context(), dtrace.Record{
			Action:           action,
			ObjectType:       "action",
			ObjectID:         action,
			ObjectRef:        r.Method + " " + r.URL.Path,
			ActorType:        dtrace.ActorHuman,
			ActorID:          humanActorID(sub),
			Summary:          "人类越权被拦截：" + action + " → " + d.Reason,
			Outcome:          dtrace.OutcomeBlocked,
			NeedsHumanReview: false, // 人类越权不是「等人批」，而是「直接拒」，不进审批队列
			OccurredAtMs:     nowMillis(),
			RecordedAtMs:     nowMillis(),
		})
		if err != nil {
			// 「越权但没留痕」不可接受：宁可 503，也不静默通过或静默拒绝。
			writeAgentProblem(w, r, http.StatusServiceUnavailable,
				"TRACE_WRITE_FAILED", "越权留痕写入失败，请求已拒绝", "")
			return true
		}
		traceID = id
	}
	writeAgentProblem(w, r, http.StatusForbidden, account.CodeACLDenied,
		"角色权限不足："+action+"（"+d.Reason+"）", traceID)
	return true
}

// HumanActorID 取人类主体的留痕 actor_id（优先账号名，退回 id 字符串）。
//
// 导出供 handler 复用：审批、会话运维、角色绑定都要往留痕里写「谁做的」，
// 三处各写一遍容易出现 "1" 与 "admin" 混用，后续按 actor 检索就废了。
func HumanActorID(sub dauthn.Subject) string {
	if strings.TrimSpace(sub.Account) != "" {
		return sub.Account
	}
	return strconv.FormatInt(sub.ID, 10)
}

// humanActorID 是包内别名，保持既有调用点简短。
func humanActorID(sub dauthn.Subject) string { return HumanActorID(sub) }
