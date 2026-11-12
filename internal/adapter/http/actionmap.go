package http

import (
	"strings"

	"github.com/mst8222/regto/internal/domain/account"
	"github.com/mst8222/regto/internal/domain/agent"
)

// ActionFor 把「HTTP 方法 + chi 路由模式」映射为动作（DESIGN-AGENT-001#DEC-01）。
//
// 设计要点：
//  1. 映射集中在一处，新增路由忘了登记 → 返回 known=false → 守卫按最严格的
//     human_only 处理（失败方向安全，DESIGN-AGENT-001#DEC-01）。
//  2. 用路由**模式**（如 /api/v1/releases/{id}）而不是具体路径，避免 ID 干扰。
//  3. 删除类动作统一走 resource.delete（需审批），与 ROADMAP §5.4 一致。
//
// 未登记的路由返回 known=false；调用方（守卫）据此拒绝且**不入队**。
func ActionFor(method, route string) (agent.Action, bool) {
	key := strings.ToUpper(method) + " " + normalizeRoute(route)

	// ---- 精确路由 ----
	switch key {
	// 需求
	case "POST /api/v1/stories/{id}/transitions":
		return agent.ActionStoryTransition, true
	case "POST /api/v1/stories/{id}/stage":
		return agent.ActionStoryTransition, true
	case "POST /api/v1/stories":
		return agent.ActionTaskCreate, true

	// 审批与会话运维（SPEC-AGENT-002）：
	// 审批决策 = spec.approve（human_only），会话运维 = agent.session_manage（human_only）。
	// 这两条必须精确登记：落到 GET 通配会变成 resource.read（auto），
	// 等于把「AI 能不能自己批自己」交给默认规则决定。
	case "POST /api/v1/agent/approvals/{trace_id}/approve",
		"POST /api/v1/agent/approvals/{trace_id}/reject":
		return agent.ActionSpecApprove, true
	case "POST /api/v1/agent/sessions",
		"POST /api/v1/agent/sessions/{agent_id}/revoke":
		return agent.ActionSessionManage, true

	// 用例域：写用例与套件属 test.write（ROADMAP §5.4「写 TEST yaml」）
	case "POST /api/v1/cases":
		return agent.ActionTestWrite, true
	case "PUT /api/v1/cases/{id}":
		return agent.ActionTestWrite, true
	case "POST /api/v1/cases/{id}/transitions":
		return agent.ActionTestWrite, true
	case "POST /api/v1/suites":
		return agent.ActionTestWrite, true
	case "POST /api/v1/suites/{id}/cases":
		return agent.ActionTestWrite, true

	// 测试执行
	case "POST /api/v1/test-tasks/{id}/import-result":
		return agent.ActionTestRun, true
	case "POST /api/v1/test-tasks/{id}/transitions":
		return agent.ActionTaskUpdate, true
	case "POST /api/v1/test-tasks":
		return agent.ActionTaskCreate, true

	// 发布域：创建需审批、改状态需审批（ROADMAP §5.4「创建并发布 release」）
	case "POST /api/v1/releases":
		return agent.ActionReleaseCreate, true
	case "POST /api/v1/releases/{id}/transitions":
		return agent.ActionReleasePublish, true
	case "POST /api/v1/builds":
		return agent.ActionReleaseCreate, true

	// 计划：创建与关联需求按需求处理对待
	case "POST /api/v1/plans":
		return agent.ActionStoryTransition, true
	case "POST /api/v1/plans/{id}/transitions":
		return agent.ActionStoryTransition, true
	case "POST /api/v1/plans/{id}/stories":
		return agent.ActionStoryTransition, true

	// 人类专属：权限与凭据分配（ROADMAP §5.4）
	case "POST /api/v1/auth/api-keys":
		return agent.ActionRoleGrant, true
	}

	// ---- 通配规则（放在精确规则之后） ----
	if strings.HasPrefix(normalizeRoute(route), "/api/v1/") {
		switch strings.ToUpper(method) {
		case "GET", "HEAD":
			// 读一律归 resource.read（auto 档）。没有这条，守卫一接线，
			// AI 连 GET /stories 都会被按 human_only 拒绝——「未知=拒绝」的
			// 失败方向是对的，但读路由必须显式登记，不能指望它自动放行。
			return agent.ActionResourceRead, true
		case "DELETE":
			// 任何删除（含软删）都需审批（ROADMAP §5.4「删除任何数据」）。
			return agent.ActionResourceDelete, true
		case "PUT", "PATCH":
			// 更新类：按资源类型区分——发布域要审批，其余按需求处理对待。
			if strings.Contains(route, "/releases/") || strings.Contains(route, "/builds/") {
				return agent.ActionReleaseCreate, true
			}
			return agent.ActionStoryTransition, true
		}
	}
	return "", false
}

// normalizeRoute 去掉路由模式的尾部斜杠，使登记与运行时形态一致。
func normalizeRoute(route string) string {
	return strings.TrimSuffix(route, "/")
}

// ---- 人类侧 ACL 的资源/动作映射（SPEC-ACCOUNT-001 REQ-04）----
//
// 与 ActionFor 的分工：ActionFor 回答「AI 该不该做」（agent.Action，含三档策略），
// 这里回答「这个人的角色能不能做」（资源 × 动作，DESIGN-ACCOUNT-001#DEC-06）。
// 两者共用同一批路由，但解读方式不同——刻意分成两个函数而不是一张大表，
// 因为 AI 的动作粒度（release.publish 要审批）与人类的资源粒度（release.edit）
// 本来就不是一一对应，硬塞进一张表只会互相牵制。

// 路由的 ACL 归属类型。
const (
	ACLRouteMapped  = "mapped"  // 正常映射到 资源 × 动作
	ACLRouteExempt  = "exempt"  // 由 handler 自行鉴权（本人 / admin）
	ACLRouteUnknown = "unknown" // 未登记：按失败方向安全处理（拒绝）
)

// ACLRouteOf 把「HTTP 方法 + chi 路由模式」映射为 ACL 判定输入。
//
// 三个返回值：
//   - kind=ACLRouteMapped：res/act 有效，交给 Authorize；
//   - kind=ACLRouteExempt：该接口只要求「已认证」，行级条件（本人 / admin）由 handler 判断。
//     「查看自己的权限」必须走这条——资源粒度表达不了「本人」，硬套矩阵会让
//     每个普通用户都看不到自己有什么权；
//   - kind=ACLRouteUnknown：未登记，拒绝。新增路由必须在这里登记，
//     否则上线即 403（这是有意的：ACL 的空白不能是"默认放行"）。
func ACLRouteOf(method, route string) (account.Resource, account.Action, string) {
	route = normalizeRoute(route)
	key := strings.ToUpper(method) + " " + route

	// ---- 账号自身（行级鉴权在 handler）----
	switch key {
	case "GET /api/v1/users/{id}/permissions",
		"GET /api/v1/users/{id}/roles":
		return "", 0, ACLRouteExempt
	}

	// ---- 会话自服务：只要求「已认证」----
	// /auth/me 返回调用者自己是谁、有哪些角色；/auth/logout 撤销调用者自己的会话。
	// 两者都不涉及"对别人的资源做什么"，矩阵里也没有能表达"本人"的粒度——
	// 硬套资源 × 动作会让 guest 之类的角色连自己是谁都查不到。
	// 未登记会被判 ACLRouteUnknown 而 403，且与权限无关，纯属配置遗漏。
	switch key {
	case "GET /api/v1/auth/me",
		"POST /api/v1/auth/logout",
		// 嵌入令牌签的是调用者自己（DESIGN-WEB-001#DEC-14），权限与他原有的一致，
		// 不产生任何提权；硬套资源×动作只会让 guest 连自己的嵌入位也用不了。
		"POST /api/v1/auth/embed-token":
		return "", 0, ACLRouteExempt
	}

	// ---- 报表：只读聚合，归 product.view（DESIGN-REPORT-001#DEC-06）----
	// 所有人类角色对 product 均有 view，故报表宽读；ai_contribution 虽全局也走此守卫，
	// 只暴露聚合计数。
	switch key {
	case "GET /api/v1/reports",
		"GET /api/v1/reports/{id}":
		return account.ResourceProduct, account.ActionView, ACLRouteMapped
	}

	// ---- 审计报表：数据来自 traces，沿用「留痕可见性」（DESIGN-AUDIT-001#DEC-02）----
	// guest 对 trace 无 view，因此审计报表对访客不可见；若并入 /api/v1/reports
	// 走 product.view，访客就能看到越权与审批统计——那是运维信息，不能给。
	switch key {
	case "GET /api/v1/audit/reports",
		"GET /api/v1/audit/reports/{id}":
		return account.ResourceTrace, account.ActionView, ACLRouteMapped
	}

	// ---- 资源前缀 → 资源 ----
	type prefix struct {
		path string
		res  account.Resource
	}
	// 顺序有意义：/test-tasks 必须先于 /test 之类的前缀（当前无冲突，新增时留意）。
	prefixes := []prefix{
		{"/api/v1/products", account.ResourceProduct},
		{"/api/v1/projects", account.ResourceProject},
		{"/api/v1/stories", account.ResourceStory},
		{"/api/v1/cases", account.ResourceCase},
		{"/api/v1/suites", account.ResourceSuite},
		{"/api/v1/test-tasks", account.ResourceTestTask},
		{"/api/v1/plans", account.ResourcePlan},
		{"/api/v1/releases", account.ResourceRelease},
		{"/api/v1/builds", account.ResourceBuild},
		{"/api/v1/specs", account.ResourceSpec},
		{"/api/v1/traces", account.ResourceTrace},
		// 以下三簇都是「授权与会话管理」，统一归 agent 资源（12 资源里没有 account，
		// 而 agent 的语义就是「AI 与会话/授权的载体」，见 DESIGN-ACCOUNT-001 §2）。
		{"/api/v1/agent", account.ResourceAgent},
		{"/api/v1/roles", account.ResourceAgent},
		{"/api/v1/auth/api-keys", account.ResourceAgent},
	}

	res := account.Resource("")
	base := ""
	for _, p := range prefixes {
		if route == p.path || strings.HasPrefix(route, p.path+"/") {
			res, base = p.res, p.path
			break
		}
	}
	if res == "" {
		return "", 0, ACLRouteUnknown
	}

	// ---- 方法 + 子路径 → 动作 ----
	act := account.Action(0)
	switch strings.ToUpper(method) {
	case "GET", "HEAD":
		act = account.ActionView
	case "DELETE":
		act = account.ActionDelete
	case "PUT", "PATCH":
		act = account.ActionEdit
	case "POST":
		// 「POST 到集合根」= 新建；其余（状态迁移、子资源挂载、导入、审批决策）= 修改。
		// 导入是例外：它批量造对象，语义是 create。
		rest := strings.TrimPrefix(route, base)
		switch {
		case rest == "":
			act = account.ActionCreate
		case strings.HasSuffix(rest, "/import-ztf"), strings.HasSuffix(rest, "/import"):
			act = account.ActionCreate
		default:
			act = account.ActionEdit
		}
	default:
		return "", 0, ACLRouteUnknown // OPTIONS 等不走 ACL：未知方法按未登记处理
	}
	return res, act, ACLRouteMapped
}
