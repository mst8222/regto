// cmd/regto-server：Regto API 服务入口。
// 生产禁止自动迁移：启动时只校验 schema_version，不匹配直接拒绝启动（AGENTS.md §5）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	reghttp "github.com/mst8222/regto/internal/adapter/http"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	"github.com/mst8222/regto/internal/adapter/persistence/postgres"
	pauthn "github.com/mst8222/regto/internal/platform/authn"
	"github.com/mst8222/regto/internal/platform/cache"
	"github.com/mst8222/regto/internal/platform/clock"
	"github.com/mst8222/regto/internal/platform/config"
	"github.com/mst8222/regto/internal/platform/db"
	"github.com/mst8222/regto/internal/platform/id"
	"github.com/mst8222/regto/internal/platform/log"
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
	uctrace "github.com/mst8222/regto/internal/usecase/trace"
	"github.com/mst8222/regto/migrations"
)

// upMigration 匹配迁移文件名：%06d_<scope>_<verb>.up.sql。
var upMigration = regexp.MustCompile(`^(\d{6})_.*\.up\.sql$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "regto-server: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	_ = config.LoadDotEnv(".env")

	want, err := latestMigrationVersion()
	if err != nil {
		return err
	}
	cfg, err := config.Load(want)
	if err != nil {
		return err
	}
	logger := log.New(cfg.Env, cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// schema 校验（只校验，不自动迁移）。
	cur, dirty, err := currentSchemaVersion(ctx, pool)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("迁移处于 dirty 状态（version=%d）：请人工修复后重启", cur)
	}
	if cur != want {
		return fmt.Errorf("schema_version 不匹配：库内=%d，二进制期望=%d；请执行 `make migrate-up`", cur, want)
	}
	logger.Info("schema 校验通过", slog.Uint64("version", uint64(want)))

	rdb, err := redis.Open(ctx, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer func() { _ = rdb.Close() }()

	// 缓存是可插拔的：未配置 Redis 时注入空实现，业务分支完全一致（DEC-01）。
	store := cache.NewRedisWithLogger(rdb.Raw(), logger)
	if !store.Enabled() {
		logger.Info("Redis 未配置，缓存降级为空实现（功能不变，仅无加速）")
	}

	// JWT 密钥缺失即拒绝启动（DESIGN-AUTHN-001 DEC-05）：
	// 弱密钥或默认密钥等于「任何人都能伪造令牌」。
	issuer, err := pauthn.NewIssuer([]byte(cfg.JWTSecret), cfg.JWTIssuer, cfg.AccessTTL, time.Now)
	if err != nil {
		return fmt.Errorf("认证未就绪：%w（请设置 REGTO_AUTH_JWT_SECRET，至少 %d 字节）",
			err, pauthn.MinSecretLen)
	}

	authSvc, err := ucauthn.New(ucauthn.Deps{
		Users:      postgres.NewUserRepo(pool),
		Keys:       postgres.NewAPIKeyRepo(pool),
		Tokens:     postgres.NewRefreshTokenRepo(pool),
		Issuer:     issuer,
		IDs:        id.NewSystem(),
		Clock:      clock.SystemClock{},
		RefreshTTL: cfg.RefreshTTL,
		EmbedTTL:   cfg.EmbedTTL,
		Cache:      store,
	})
	if err != nil {
		return err
	}

	authenticator := httputil.NewAuthenticator(issuer, authSvc.VerifyAPIKey, logger, reghttp.PublicPaths)

	stories := ucstory.NewWithCache(postgres.NewStoryRepo(pool), clock.SystemClock{}, store)
	caseRepo := postgres.NewCaseRepo(pool)
	taskRepo := postgres.NewTestTaskRepo(pool)
	cases := uctcase.New(caseRepo, clock.SystemClock{})
	tasks := ucttask.New(taskRepo, caseRepo, clock.SystemClock{}, id.NewSystem())
	// 发布域（M2-B）：plan/release/build；装配构建存在性校验器（REQ-07）。
	releaseRepo := postgres.NewReleaseRepo(pool)
	releases := ucrel.New(releaseRepo, clock.SystemClock{})
	tasks.SetBuildChecker(releases.BuildExists)

	// ---- M3：AI 授权守卫（SPEC-AGENT-001）----
	// 判定读 DB 副本（会话 / 批文 / 待审批队列），留痕写真源 + 副本（Recorder）。
	// 三个查询函数与留痕器缺一不可：少任何一个，守卫要么查不到授权（全拒），
	// 要么「越权但没留痕」（DEC-03 明令禁止）。
	agentPlumb := buildAgentPlumbing(ctx, cfg, pool, logger)
	acctPlumb := buildAccountPlumbing(ctx, cfg, pool, logger)

	// 固定报表（M4-B）：只读聚合，无状态。
	reportRepo := postgres.NewReportRepo(pool)
	reports := ucreport.NewService(reportRepo)
	auditRepo := postgres.NewAuditRepo(pool)
	audits := ucaudit.NewService(auditRepo)

	router := reghttp.NewRouter(reghttp.Deps{
		Logger:        logger,
		Stories:       stories,
		Cases:         cases,
		Tasks:         tasks,
		Release:       releases,
		Auth:          authSvc,
		DB:            pool,
		Redis:         rdb,
		Authenticator: authenticator,
		AgentGuard:    agentPlumb.guard,
		Approvals:     agentPlumb.approvals,
		Sessions:      agentPlumb.sessions,
		ACL:           acctPlumb.acl,
		Accounts:      acctPlumb.accounts,
		Reports:       reports,
		Audit:         audits,
		// refresh cookie（SPEC-WEB-001 REQ-04）：Secure 只在走 HTTPS 的环境置位。
		// 本地 http://localhost 带了 Secure 会被浏览器直接丢弃。
		AllowedOrigins: cfg.WebAllowedOrigins,
		Env:            cfg.Env,
		CookieSecure:   cookieSecure(cfg.Env),
		RefreshTTL:     cfg.RefreshTTL,
	})
	return serve(ctx, logger, cfg.HTTPAddr, router)
}

// cookieSecure 决定 refresh cookie 是否带 Secure 属性（SPEC-WEB-001 REQ-04）。
//
// 只有明确的本地/测试环境（都走 http）才关掉；其余取值——包括拼错的未知值——
// 一律带上。失败方向安全：宁可本地偶发登不上（浏览器丢弃 cookie，日志里一眼看得出），
// 也不能因为环境名写错就把长期凭据明文发过 http。
func cookieSecure(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "test", "ci":
		return false
	}
	return true
}

// accountPlumbing 汇总人类侧授权所需的两件东西：守卫与服务。
type accountPlumbing struct {
	acl      *httputil.ACLGuard
	accounts *ucaccount.Service
}

// buildAccountPlumbing 装配人类侧 ACL（SPEC-ACCOUNT-001 REQ-04）。
//
// 与 AI 守卫的关系：两者按主体类型分流，各自用自己的判定依据（DESIGN-ACCOUNT-001#DEC-06）。
// 这里的 Service 同时供 ACL 判定与角色管理接口使用——只有一个判定入口，
// 才不会出现「ACL 说不行、接口说行」的双源。
func buildAccountPlumbing(ctx context.Context, cfg config.Config, pool *pgxpool.Pool,
	logger *slog.Logger) accountPlumbing {
	repo := postgres.NewAccountRepo(pool)
	svc := ucaccount.NewService(repo)
	plumb := accountPlumbing{accounts: svc}
	if cfg.ACLMode == httputil.ACLModeOff {
		logger.Warn("人类 ACL 已关闭（REGTO_ACL_MODE=off）：任何通过认证的人类可访问任何接口")
		return plumb
	}
	if cfg.ACLMode == httputil.ACLModeWarn {
		logger.Warn("人类 ACL 处于 warn 模式：越权请求只会被记录，**不会**被拒绝；" +
			"确认角色配置无误后请切到 REGTO_ACL_MODE=enforce")
	}

	// 越权留痕与 AI 守卫共用同一 TracesRoot：审计只有一个真源目录。
	recorder := uctrace.NewRecorder(uctrace.NewStore(cfg.TracesRoot, id.NewSystem()),
		postgres.NewTraceRepo(pool), logger)
	warnLog := func(ctx context.Context, msg string, kv ...any) {
		logger.WarnContext(ctx, msg, kv...)
	}
	plumb.acl = httputil.NewACLGuard(cfg.ACLMode, svc.Decision, reghttp.ACLRouteOf,
		recorder.Append, warnLog)

	logger.Info("人类 ACL 已启用",
		slog.String("mode", cfg.ACLMode),
		slog.String("matrix", "5 角色 × 12 资源 × 4 动作（代码真源）"),
		slog.String("self_service", "GET /api/v1/users/{id}/permissions"))

	// 零管理员 = 系统再无人能授权（自锁现场）。这在 enforce 下表现为「所有人类请求 403」，
	// 现场很像「ACL 坏了」，其实是一个人都没配。启动时把它说透。
	var admins int
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT ur.user_id) FROM user_roles ur
		   JOIN roles r ON r.id = ur.role_id
		  WHERE r.code = 'admin' AND ur.revoked_at IS NULL`).Scan(&admins); err != nil {
		logger.Warn("统计管理员数量失败（ACL 仍会按无授权拒绝人类请求）",
			slog.String("error", err.Error()))
	} else if admins == 0 {
		logger.Warn("没有任何用户持有 admin 角色：enforce 模式下所有人类请求都会被 403；" +
			"用 docs/deploy/05-init-admin.sql 初始化首个管理员")
	} else {
		logger.Info("管理员数量", slog.Int("count", admins))
	}
	return plumb
}

// agentPlumbing 汇总 AI 授权闭环的三个部件。
//
// 为什么一起构建（SPEC-AGENT-002 DESIGN-AGENT-002#DEC-06）：
// 守卫、审批、会话运维必须共用**同一个** Recorder 与 TraceRepo——
// 守卫写 pending、人类写 approve，只要两边不是同一个写入路径，
// 「批了立刻生效」就会退化成「批了等起效」。
type agentPlumbing struct {
	guard     *httputil.AgentGuard
	approvals *ucagent.ApprovalService
	sessions  *ucagent.SessionService
}

// buildAgentPlumbing 装配 AI 授权守卫与审批/会话服务（SPEC-AGENT-001 + SPEC-AGENT-002）。
//
// 若 REGTO_AGENT_GUARD=off 则 guard 为 nil（Deps.AgentGuard 为 nil = 不注册中间件），
// 此时 AI 主体通过认证即可调用任何接口——只允许本地排障，故打 WARN。
// 审批与会话服务仍装配：它们是**人类侧**的运维入口，与守卫开关无关。
func buildAgentPlumbing(ctx context.Context, cfg config.Config, pool *pgxpool.Pool,
	logger *slog.Logger) agentPlumbing {
	traceRepo := postgres.NewTraceRepo(pool)
	sessionRepo := postgres.NewAgentSessionRepo(pool)
	recorder := uctrace.NewRecorder(uctrace.NewStore(cfg.TracesRoot, id.NewSystem()), traceRepo, logger)

	plumb := agentPlumbing{
		approvals: ucagent.NewApprovalService(traceRepo, recorder, cfg.Env),
		sessions:  ucagent.NewSessionService(sessionRepo),
	}

	if !cfg.AgentGuard {
		logger.Warn("AI 授权守卫已关闭（REGTO_AGENT_GUARD=off）：" +
			"AI 主体通过认证即可调用任何接口，仅限本地排障")
		return plumb
	}

	logger.Info("AI 授权守卫已启用",
		slog.String("sessions_source", "agent_sessions"),
		slog.String("traces_source", cfg.TracesRoot+"/*.jsonl"),
		slog.String("approval_view", "traces(DB 只读副本)"),
		slog.String("approval_api", "POST /api/v1/agent/approvals/{trace_id}/approve|reject"))

	// 没有任何生效会话 = 所有 AI 请求都会 AGENT_CAPABILITY_DENIED。
	// 这虽然是「最保守」的正确行为（SPEC-AGENT-001 ASM-03），但现场很像「守卫坏了」，
	// 启动时把这件事说透，省得排查半天。
	var active int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_sessions WHERE status = 'active'`).Scan(&active); err != nil {
		logger.Warn("统计 agent_sessions 失败（守卫仍会按空白名单拒绝 AI）",
			slog.String("error", err.Error()))
	} else if active == 0 {
		logger.Warn("agent_sessions 无生效会话：AI 主体将被全部拒绝（AGENT_CAPABILITY_DENIED）；" +
			"可用 POST /api/v1/agent/sessions 登记（人类专属）")
	} else {
		logger.Info("生效的 Agent 会话", slog.Int64("count", active))
	}

	plumb.guard = httputil.NewAgentGuard(sessionRepo.ByAgentID, traceRepo.ApprovalState,
		traceRepo.FindPending, recorder.Append, reghttp.ActionFor)
	return plumb
}

// serve 启动 HTTP 服务并支持优雅退出。
func serve(ctx context.Context, logger *slog.Logger, addr string, handler *chi.Mux) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		logger.Info("regto-server 启动", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("收到退出信号，优雅关闭中")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// latestMigrationVersion 从内嵌迁移文件解析最大版本号。
func latestMigrationVersion() (uint, error) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return 0, fmt.Errorf("读取内嵌迁移目录失败: %w", err)
	}
	var max uint
	for _, e := range entries {
		m := upMigration.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.ParseUint(m[1], 10, 32)
		if err != nil {
			continue
		}
		if uint(v) > max {
			max = uint(v)
		}
	}
	if max == 0 {
		return 0, errors.New("未找到任何迁移文件")
	}
	return max, nil
}

// currentSchemaVersion 读取库内 schema 版本；schema_meta 表不存在返回 0。
//
// 设计约束（DEC-08）：应用账号对 golang-migrate 的 schema_migrations 表**零权限**
// （防应用误改迁移历史），因此改读业务契约表 schema_meta（应用只读）。
// dirty 标志属于迁移工具内部状态，应用侧无从也无需感知——
// 它由 `regtoctl schema` / `migrate validate` 负责，见 docs/deploy/02-init-permissions.sql。
func currentSchemaVersion(ctx context.Context, pool *pgxpool.Pool) (uint, bool, error) {
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_meta')`,
	).Scan(&exists); err != nil {
		return 0, false, fmt.Errorf("检查 schema_meta 失败: %w", err)
	}
	if !exists {
		return 0, false, nil
	}
	var version uint
	err := pool.QueryRow(ctx, `SELECT version FROM schema_meta WHERE singleton`).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("读取 schema_meta 失败: %w", err)
	}
	return version, false, nil
}
