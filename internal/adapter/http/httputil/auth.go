package httputil

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mst8222/regto/internal/domain/authn"
	"github.com/mst8222/regto/internal/domain/story"
	piauthn "github.com/mst8222/regto/internal/platform/authn"
)

// subjectKey 是上下文中认证主体的键（ctxKey 定义在 middleware.go）。
const subjectKey = ctxKey(2)

// 认证相关请求头（ROADMAP §3.4）。
const (
	HeaderAuthorization = "Authorization"
	HeaderAPIKey        = "X-Regto-Api-Key"
)

// WithSubject 把认证主体写入上下文。
func WithSubject(ctx context.Context, sub authn.Subject) context.Context {
	return context.WithValue(ctx, subjectKey, sub)
}

// SubjectFromContext 取上下文中的认证主体；未认证返回 false。
// 业务代码只能经此取主体，杜绝「客户端自报身份」（DESIGN-AUTHN-001 DEC-02）。
func SubjectFromContext(ctx context.Context) (authn.Subject, bool) {
	sub, ok := ctx.Value(subjectKey).(authn.Subject)
	if !ok || !sub.IsValid() {
		return authn.Subject{}, false
	}
	return sub, true
}

// ActorFromContext 把认证主体转为领域执行主体。
// 无主体时回落 system/0：宁可记为系统，也不把未知调用当成人或 AI。
func ActorFromContext(ctx context.Context) story.Actor {
	sub, ok := SubjectFromContext(ctx)
	if !ok {
		return story.Actor{ID: 0, Type: story.ActorSystem}
	}
	return story.Actor{ID: sub.ID, Type: sub.Kind}
}

// APIKeyVerifier 校验 API Key 明文并返回主体（由 usecase 注入，httputil 不反向依赖 usecase）。
type APIKeyVerifier func(ctx context.Context, plain string) (authn.Subject, error)

// Authenticator 是认证中间件。
// 默认全部路由受保护，仅白名单内路径放行（DEC-06）。
type Authenticator struct {
	issuer *piauthn.Issuer
	verify APIKeyVerifier
	logger *slog.Logger
	public map[string]struct{}
}

// NewAuthenticator 构造认证中间件；publicPaths 为无需认证的路径（精确匹配）。
func NewAuthenticator(issuer *piauthn.Issuer, verify APIKeyVerifier,
	logger *slog.Logger, publicPaths []string) *Authenticator {
	set := make(map[string]struct{}, len(publicPaths))
	for _, p := range publicPaths {
		set[p] = struct{}{}
	}
	return &Authenticator{issuer: issuer, verify: verify, logger: logger, public: set}
}

// IsPublic 报告路径是否在白名单内。
func (a *Authenticator) IsPublic(path string) bool {
	_, ok := a.public[path]
	return ok
}

// Middleware 返回认证中间件。
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.IsPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		sub, ok := a.authenticate(r)
		if !ok {
			// 认证失败不是业务动作：只记访问日志，不写留痕（NFR-04）。
			if a.logger != nil {
				a.logger.Warn("unauthenticated request",
					slog.String("path", r.URL.Path),
					slog.String("request_id", RequestID(r.Context())))
			}
			WriteProblem(w, r, a.logger, authn.Unauthenticated())
			return
		}
		next.ServeHTTP(w, r.WithContext(WithSubject(r.Context(), sub)))
	})
}

// authenticate 依次尝试 JWT 与 API Key；JWT 优先（人类操作优先被记录为人类）。
func (a *Authenticator) authenticate(r *http.Request) (authn.Subject, bool) {
	if raw := bearerToken(r); raw != "" && a.issuer != nil {
		if sub, err := a.issuer.Parse(raw); err == nil && sub.IsValid() {
			return sub, true
		}
		// JWT 存在但无效：不再回退到 API Key，避免"坏 JWT + 好 Key"的歧义来源。
		return authn.Subject{}, false
	}
	if raw := strings.TrimSpace(r.Header.Get(HeaderAPIKey)); raw != "" && a.verify != nil {
		sub, err := a.verify(r.Context(), raw)
		if err != nil || !sub.IsValid() {
			return authn.Subject{}, false
		}
		return sub, true
	}
	return authn.Subject{}, false
}

// bearerToken 提取 Bearer 令牌；格式不符返回空串。
func bearerToken(r *http.Request) string {
	raw := r.Header.Get(HeaderAuthorization)
	if raw == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(raw, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(raw, prefix))
}
