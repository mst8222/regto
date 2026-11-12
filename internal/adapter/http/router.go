package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/adapter/http/handler"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	"github.com/mst8222/regto/internal/adapter/http/webui"
	"github.com/mst8222/regto/internal/domain/shared"
	"github.com/mst8222/regto/internal/platform/redis"
	ucaccount "github.com/mst8222/regto/internal/usecase/account"
	ucagent "github.com/mst8222/regto/internal/usecase/agent"
	ucaudit "github.com/mst8222/regto/internal/usecase/audit"
	ucauthn "github.com/mst8222/regto/internal/usecase/authn"
	ucrel "github.com/mst8222/regto/internal/usecase/release"
	ucreport "github.com/mst8222/regto/internal/usecase/report"
	ucstory "github.com/mst8222/regto/internal/usecase/story"
	uctcase "github.com/mst8222/regto/internal/usecase/testcase"
	ucttask "github.com/mst8222/regto/internal/usecase/testtask"
)

// Deps 是路由所需的外部依赖（显式注入，禁止全局变量）。
type Deps struct {
	Logger  *slog.Logger
	Stories *ucstory.Service
	Auth    *ucauthn.Service // 可选：未配置时不注册认证路由
	Cases   *uctcase.Service // 可选：未配置时不注册用例路由
	Tasks   *ucttask.Service // 可选：未配置时不注册测试单路由
	Release *ucrel.Service   // 可选：未配置时不注册发布域路由（plan/release/build）
	DB      *pgxpool.Pool    // 用于 /readyz 探活
	Redis   *redis.Client    // 可选：未启用时 /readyz 跳过
	// Authenticator 是认证中间件；为 nil 表示不启用认证（仅测试/排障）。
	Authenticator *httputil.Authenticator
	// AgentGuard 是 AI 主体的授权守卫（SPEC-AGENT-001）；为 nil 表示不启用授权拦截
	// （此时 AI 一旦通过认证即可访问任何接口——仅测试/排障允许）。
	AgentGuard *httputil.AgentGuard
	// ACL 是人类主体的授权守卫（SPEC-ACCOUNT-001）；为 nil 表示不启用人类 ACL。
	ACL *httputil.ACLGuard
	// Approvals 是审批队列与决策服务（SPEC-AGENT-002）；为 nil 时不注册相关路由。
	Approvals *ucagent.ApprovalService
	// Sessions 是 Agent 会话运维服务（SPEC-AGENT-002）；为 nil 时不注册相关路由。
	Sessions *ucagent.SessionService
	// Accounts 是账号与角色服务（SPEC-ACCOUNT-001）；为 nil 时不注册角色路由。
	Accounts *ucaccount.Service
	// Reports 是固定报表服务（SPEC-REPORT-001）；为 nil 时不注册报表路由。
	Reports *ucreport.Service
	// Audit 是审计报表服务（SPEC-AUDIT-001）；为 nil 时不注册审计路由。
	Audit *ucaudit.Service
	// AllowedOrigins 是额外允许的浏览器来源（SPEC-WEB-001 REQ-04 的 Origin 白名单）。
	AllowedOrigins []string
	// CookieSecure 决定 refresh cookie 是否带 Secure；HTTP 本地开发必须为 false。
	CookieSecure bool
	// RefreshTTL 是 refresh cookie 的 Max-Age（与令牌有效期一致）。
	RefreshTTL time.Duration
	// Env 取值 local|dev|test|ci|prod，决定是否放宽回环地址的 Origin 判定。
	Env string
}

// PublicPaths 是无需认证的路径（DESIGN-AUTHN-001 DEC-06：默认全保护，开放需显式登记）。
var PublicPaths = []string{"/healthz", "/readyz", "/api/v1/auth/login", "/api/v1/auth/refresh"}

// NewRouter 构建 chi 路由树。
// 路由约定见 ROADMAP §3.4：/api/v1 前缀，复数资源名，transitions 统一入口。
// 认证默认全开：除 PublicPaths 外的一切请求都必须携带 JWT 或 API Key。
func NewRouter(d Deps) *chi.Mux {
	r := chi.NewRouter()
	for _, mw := range httputil.Middlewares(d.Logger) {
		r.Use(mw)
	}

	r.Get("/healthz", healthz())
	r.Get("/readyz", readyz(d))

	// 认证入口本身无需凭据（否则无法换取第一个令牌）。
	// Login 与 Refresh 共用同一套 cookie 选项：refresh 要轮换 cookie，
	// 两处 Path/属性一旦不一致，浏览器就会并存两个同名 cookie。
	authOpts := handler.AuthOptions{
		Accounts:       d.Accounts,
		AllowedOrigins: d.AllowedOrigins,
		CookieSecure:   d.CookieSecure,
		RefreshTTL:     d.RefreshTTL,
		Env:            d.Env,
	}
	if d.Auth != nil {
		authH := handler.NewAuthHandler(d.Auth, authOpts, d.Logger)
		r.Post("/api/v1/auth/login", authH.Login)
		r.Post("/api/v1/auth/refresh", authH.Refresh)
	}

	// 受保护区域：默认全保护，新增路由自动继承。
	r.Group(func(r chi.Router) {
		if d.Authenticator != nil {
			r.Use(d.Authenticator.Middleware)
		}
		r.Route("/api/v1", func(r chi.Router) {
			// 授权守卫必须挂在**端点链**上（r.With），不能用组中间件（r.Use）：
			// chi 的组/Route 中间件在挂载点执行，RoutePattern() 只会是 /api/v1/*（实测），
			// 守卫按路由模式查动作必然全部落空 → 一律按未知动作 human_only 拒绝，
			// 表现就是「AI 干什么都 403」而没有任何报错线索。
			// With 把中间件放进端点自身的手柄链，此时 RoutePattern() 才是完整模式
			// （DESIGN-AGENT-001#DEC-01 要求用路由模式而非具体路径）。
			// 位置在认证之后：守卫要从上下文里取主体（ROADMAP §5.4）。
			//
			// ACL 与 AgentGuard 按主体类型分流（DESIGN-ACCOUNT-001#DEC-06）：
			// 人类走角色矩阵，AI 走会话能力与三档策略。两者都在 Kind 不匹配时直接
			// next，所以顺序不影响判定，只是「先问人、再问 AI」更符合阅读顺序。
			var chain []func(http.Handler) http.Handler
			if d.ACL != nil {
				chain = append(chain, d.ACL.Middleware)
			}
			if d.AgentGuard != nil {
				chain = append(chain, d.AgentGuard.Middleware)
			}
			api := r.With(chain...)
			if d.Auth != nil {
				authH := handler.NewAuthHandler(d.Auth, authOpts, d.Logger)
				// 会话自服务（SPEC-WEB-001 REQ-04/REQ-07）：只要求已认证，
				// ACL 侧登记为 exempt，鉴权边界就是"调用者只能看到/撤销自己"。
				api.Get("/auth/me", authH.Me)
				api.Post("/auth/logout", authH.Logout)
				api.Post("/auth/embed-token", authH.EmbedToken)
				api.Post("/auth/api-keys", authH.CreateAPIKey)
				api.Delete("/auth/api-keys/{id}", authH.RevokeAPIKey)
			}
			if d.Approvals != nil {
				// 审批队列与决策（SPEC-AGENT-002 REQ-08/09）。
				// 决策接口的动作映射为 spec.approve（human_only）：
				// AI 不能自己批自己，这条由守卫保证，不靠 handler 自觉。
				approvals := handler.NewApprovalHandler(d.Approvals, d.Logger)
				api.Get("/agent/approvals", approvals.List)
				api.Post("/agent/approvals/{trace_id}/approve", approvals.Approve)
				api.Post("/agent/approvals/{trace_id}/reject", approvals.Reject)
			}
			if d.Sessions != nil {
				// 会话运维（SPEC-AGENT-002 REQ-10）：动作 agent.session_manage（human_only）。
				sessions := handler.NewAgentSessionHandler(d.Sessions, d.Logger)
				api.Get("/agent/sessions", sessions.List)
				api.Post("/agent/sessions", sessions.Create)
				api.Post("/agent/sessions/{agent_id}/revoke", sessions.Revoke)
			}
			if d.Accounts != nil {
				// 角色与权限（SPEC-ACCOUNT-001 REQ-05）。
				// 读矩阵 = agent.view；绑定/解绑 = agent.edit（admin 专属，见矩阵）。
				// 「查看自己的权限」是 ACL 豁免路由，行级条件（本人 / admin）在 handler。
				acct := handler.NewAccountHandler(d.Accounts, d.Logger)
				api.Get("/roles", acct.ListRoles)
				api.Get("/roles/{code}/permissions", acct.RolePermissions)
				api.Get("/users/{id}/permissions", acct.UserPermissions)
				api.Get("/users/{id}/roles", acct.UserRoles)
				api.Post("/users/{id}/roles", acct.BindRole)
				api.Post("/users/{id}/roles/{role}/revoke", acct.UnbindRole)
			}
			if d.Stories != nil {
				stories := handler.NewStoryHandler(d.Stories, d.Logger)
				api.Get("/stories", stories.List)
				api.Post("/stories", stories.Create) // 详情直接挂在 {id} 上，不用 Route("/{id}")+Get("/") 的嵌套写法：
				// 后者会让路由表登记成 /stories/{id}/（尾部斜杠），与 OpenAPI 契约不符。
				api.Get("/stories/{id}", stories.Get)
				api.Post("/stories/{id}/transitions", stories.Transition)
				api.Post("/stories/{id}/stage", stories.AdvanceStage)
			}
			if d.Reports != nil {
				// 固定报表（SPEC-REPORT-001）：列表 + 单张查询。
				// 守卫归 product.view（DESIGN-REPORT-001#DEC-06）。
				reps := handler.NewReportHandler(d.Reports, d.Logger)
				api.Get("/reports", reps.List)
				api.Get("/reports/{id}", reps.Show)
			}
			if d.Audit != nil {
				// 审计报表（SPEC-AUDIT-001）：独立路径，守卫归 trace.view
				// （DESIGN-AUDIT-001#DEC-02），访客看不到留痕故看不到审计。
				aud := handler.NewAuditHandler(d.Audit, d.Logger)
				api.Get("/audit/reports", aud.List)
				api.Get("/audit/reports/{id}", aud.Show)
			}
			if d.Cases != nil {
				cases := handler.NewCaseHandler(d.Cases, d.Logger)
				api.Get("/cases", cases.List)
				api.Post("/cases", cases.Create)
				api.Post("/cases/import-ztf", cases.ImportZTF)
				api.Get("/cases/{id}", cases.Get)
				api.Put("/cases/{id}", cases.Update)
				api.Get("/cases/{id}/export-ztf", cases.ExportZTF)
				api.Post("/cases/{id}/transitions", cases.Transition)
				// 套件（REQ-03）：创建 + 加入用例；列表查询本期不提供（DESIGN-CASE-001）
				suites := handler.NewSuiteHandler(d.Cases, d.Logger)
				api.Post("/suites", suites.Create)
				api.Post("/suites/{id}/cases", suites.AddCases)
			}
			if d.Tasks != nil {
				tasks := handler.NewTestTaskHandler(d.Tasks, d.Logger)
				api.Get("/test-tasks", tasks.List)
				api.Post("/test-tasks", tasks.Create)
				api.Get("/test-tasks/{id}", tasks.Get)
				api.Post("/test-tasks/{id}/transitions", tasks.Transition)
				api.Get("/test-tasks/{id}/cases", tasks.ListCases)
				api.Post("/test-tasks/{id}/cases", tasks.AddCases)
				api.Get("/test-tasks/{id}/results", tasks.ListResults)
				api.Get("/test-tasks/{id}/summary", tasks.Summary)
				api.Post("/test-tasks/{id}/import-result", tasks.ImportResult)
			}
			if d.Release != nil {
				// 发布域（SPEC-RELEASE-001 REQ-01~06）
				plans := handler.NewPlanHandler(d.Release, d.Logger)
				api.Get("/plans", plans.List)
				api.Post("/plans", plans.Create)
				api.Get("/plans/{id}", plans.Get)
				api.Put("/plans/{id}", plans.Update)
				api.Post("/plans/{id}/transitions", plans.Transition)
				api.Get("/plans/{id}/stories", plans.ListStories)
				api.Post("/plans/{id}/stories", plans.LinkStory)

				releases := handler.NewReleaseHandler(d.Release, d.Logger)
				api.Get("/releases", releases.List)
				api.Post("/releases", releases.Create)
				api.Get("/releases/{id}", releases.Get)
				api.Put("/releases/{id}", releases.Update)
				api.Post("/releases/{id}/transitions", releases.Transition)

				builds := handler.NewBuildHandler(d.Release, d.Logger)
				api.Get("/builds", builds.List)
				api.Post("/builds", builds.Create)
				api.Get("/builds/{id}", builds.Get)
				api.Put("/builds/{id}", builds.Update)
			}
		})
	})

	// 未匹配路由分两类（SPEC-WEB-001 AC-02）：
	//   /api/**、/healthz、/readyz → 保持 problem+json 404，绝不回落 index.html；
	//   其余（/、/stories/123）→ SPA 静态资源，未命中的无扩展名路径回落 index.html。
	// 挂在 root 而不是认证 Group 内：前端页面本身不需要凭据，否则未登录打开首页会 401。
	r.NotFound(notFoundHandler(d))
	return r
}

// notFoundHandler 兜底未匹配路由：后端路径写 problem+json，其余交给 SPA。
func notFoundHandler(d Deps) http.HandlerFunc {
	static := webui.Handler()
	return func(w http.ResponseWriter, r *http.Request) {
		if webui.IsBackendPath(r.URL.Path) {
			httputil.WriteProblem(w, r, d.Logger,
				shared.New("NOT_FOUND", "no route matched "+r.Method+" "+r.URL.Path, nil))
			return
		}
		static.ServeHTTP(w, r)
	}
}

// healthz 进程存活探针（不依赖外部组件）。
func healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// readyz 就绪探针：校验数据库（必选）与 Redis（可选）连通性。
func readyz(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		checks := map[string]string{"postgres": "ok"}
		status := http.StatusOK

		if d.DB == nil {
			checks["postgres"] = "not_configured"
			status = http.StatusServiceUnavailable
		} else if err := d.DB.Ping(ctx); err != nil {
			checks["postgres"] = "unavailable"
			status = http.StatusServiceUnavailable
		}

		if d.Redis != nil {
			if err := d.Redis.Ping(ctx); err != nil {
				checks["redis"] = "unavailable"
				status = http.StatusServiceUnavailable
			} else {
				checks["redis"] = "ok"
			}
		} else {
			checks["redis"] = "disabled"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(checks)
	}
}
