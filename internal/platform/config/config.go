// Package config 集中读取进程配置（环境变量注入，凭据永不入库，见 ROADMAP §7.2）。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	pauthn "github.com/mst8222/regto/internal/platform/authn"
)

// Config 是进程运行期配置。
type Config struct {
	// HTTPAddr 是 API 监听地址，默认 :8080。
	HTTPAddr string
	// DatabaseURL 是 PostgreSQL 连接串（应用账号：regto_app）。
	DatabaseURL string
	// MigrationURL 是迁移专用连接串（超级用户，仅开发/CI），缺省回退 DatabaseURL。
	MigrationURL string
	// RedisURL 是 Redis 连接串，可选（不配置则禁用缓存）。
	RedisURL string
	// Env 取值 local|ci|prod，影响日志格式与错误详情暴露。
	Env string
	// LogLevel 取值 debug|info|warn|error。
	LogLevel string
	// JWTSecret 是 access token 的 HMAC 密钥；缺失时服务拒绝启动（DESIGN-AUTHN-001 DEC-05）。
	JWTSecret string
	// JWTIssuer 是签发者标识，默认 regto。
	JWTIssuer string
	// AccessTTL 是 access token 有效期，默认 15 分钟。
	AccessTTL time.Duration
	// RefreshTTL 是 refresh token 有效期，默认 7 天。
	RefreshTTL time.Duration
	// EmbedTTL 是嵌入位令牌的有效期（DESIGN-WEB-001#DEC-14），默认 5 分钟。
	// 它必须远短于 AccessTTL：iframe 里的第三方页面不配持有主会话长度的凭据。
	EmbedTTL time.Duration
	// SchemaVersion 是二进制内置的期望迁移版本号（用于启动校验）。
	SchemaVersion uint
	// TracesRoot 是留痕真源（jsonl）根目录，默认 traces。
	// 守卫写审批留痕时落在 <TracesRoot>/YYYY-MM/YYYY-MM-DD.jsonl。
	TracesRoot string
	// AgentGuard 报告是否启用 AI 授权守卫（SPEC-AGENT-001）。
	// 默认启用；仅本地排障允许关闭，启动时会打 WARN 提醒。
	AgentGuard bool
	// ACLMode 是人类侧授权守卫的运行模式（SPEC-ACCOUNT-001 REQ-04）：
	// enforce（默认）| warn | off。默认 enforce 是有意为之——默认 warn 的
	// 权限系统会安静地记日志，所有人都以为它已经生效。
	ACLMode string
	// WebAllowedOrigins 是额外允许的浏览器来源（SPEC-WEB-001 REQ-04 的 Origin 白名单）。
	// 缺省为空 = 只认同源（Origin 必须与请求 Host 一致）。开发期 vite dev 跑在
	// 5173 端口、经 proxy 转发后 Origin 仍是 5173，故需要在 .env 里显式加进来。
	// 这里不做通配符：`*` 会让 CSRF 防线彻底失效，写错比不写更危险。
	WebAllowedOrigins []string
}

// Load 从环境变量加载配置并校验必填项。
// 生产禁止自动迁移：server 只校验 schema_version，不匹配拒绝启动（AGENTS.md §5）。
func Load(schemaVersion uint) (Config, error) {
	c := Config{
		HTTPAddr:          envOr("REGTO_HTTP_ADDR", ":8080"),
		DatabaseURL:       envOr("REGTO_DATABASE_URL", ""),
		MigrationURL:      envOr("REGTO_MIGRATION_URL", ""),
		RedisURL:          envOr("REGTO_REDIS_URL", ""),
		Env:               envOr("REGTO_ENV", "local"),
		LogLevel:          envOr("REGTO_LOG_LEVEL", "info"),
		JWTSecret:         os.Getenv("REGTO_AUTH_JWT_SECRET"), // 不 TrimSpace：空格也是密钥的一部分
		JWTIssuer:         envOr("REGTO_AUTH_JWT_ISSUER", pauthn.DefaultIssuer),
		AccessTTL:         durationOr("REGTO_AUTH_ACCESS_TTL", pauthn.DefaultAccessTTL),
		RefreshTTL:        durationOr("REGTO_AUTH_REFRESH_TTL", pauthn.DefaultRefreshTTL),
		EmbedTTL:          durationOr("REGTO_AUTH_EMBED_TTL", pauthn.DefaultEmbedTTL),
		SchemaVersion:     schemaVersion,
		TracesRoot:        envOr("REGTO_TRACES_ROOT", "traces"),
		AgentGuard:        boolOr("REGTO_AGENT_GUARD", true),
		ACLMode:           strings.ToLower(envOr("REGTO_ACL_MODE", "enforce")),
		WebAllowedOrigins: splitList(os.Getenv("REGTO_WEB_ALLOWED_ORIGINS")),
	}
	if c.MigrationURL == "" {
		c.MigrationURL = c.DatabaseURL
	}
	switch c.ACLMode {
	case "enforce", "warn", "off":
	default:
		// 非法值不能静默回退成 off：那等于把权限系统关掉还没人知道。
		return c, fmt.Errorf("REGTO_ACL_MODE 非法: %s（允许 enforce|warn|off）", c.ACLMode)
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("REGTO_DATABASE_URL 未设置：请在 .env 中配置后重试")
	}
	return c, nil
}

// splitList 按逗号切分列表型环境变量，去空白、丢空项。
// 空字符串返回 nil（而不是含一个空串的切片）：调用方用 len()==0 判断"未配置"。
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// durationOr 解析时长环境变量；非法或缺失回落默认值。
func durationOr(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// boolOr 解析布尔环境变量；只把明确的假值视为关闭（off/false/0/no），
// 其余（含拼错）一律按默认值处理——守卫这类安全开关不能因为手滑就静默失效。
func boolOr(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return def
	case "off", "false", "0", "no":
		return false
	case "on", "true", "1", "yes":
		return true
	}
	return def
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
