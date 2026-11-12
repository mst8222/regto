package handler

import (
	"net/http"
	"time"

	"log/slog"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	"github.com/mst8222/regto/internal/domain/authn"
	"github.com/mst8222/regto/internal/domain/shared"
	ucaccount "github.com/mst8222/regto/internal/usecase/account"
	ucauthn "github.com/mst8222/regto/internal/usecase/authn"
)

// RefreshCookieName 是存放 refresh token 的 httpOnly cookie 名。
const RefreshCookieName = "regto_refresh"

// RefreshCookiePath 是 refresh cookie 的作用域。
//
// 规格原文（SPEC-WEB-001 REQ-04 v1）写的是 /api/v1/auth/refresh，只覆盖刷新端点；
// 实测发现那样登出就读不到令牌，只能"清 cookie 但不撤销服务端令牌"——会话在 TTL 内
// 仍可续期，登出成了前端独角戏。改成 /api/v1/auth 后 refresh / logout / me 三个端点
// 共用同一作用域，登出能精确撤销当前 family，暴露面只多两个同源端点。
// 详见 SPEC-WEB-001 v2 变更历史与 DESIGN-WEB-001#DEC-12。
const RefreshCookiePath = "/api/v1/auth"

// AuthOptions 是认证处理器的可选配置。
type AuthOptions struct {
	// Accounts 用于 /auth/me 取角色；为 nil 时 roles 返回空数组。
	Accounts *ucaccount.Service
	// AllowedOrigins 是额外允许的浏览器来源（同源之外的，如 vite dev 的 5173）。
	// 空 = 只认同源。
	AllowedOrigins []string
	// CookieSecure 决定 Set-Cookie 是否带 Secure（HTTPS-only）。
	// 本地 http://localhost 下必须为 false，否则浏览器直接丢弃 cookie，
	// 表现为"登录成功但一刷新就掉"。
	CookieSecure bool
	// RefreshTTL 与 refresh token 有效期一致，作为 cookie Max-Age。
	RefreshTTL time.Duration
	// Env 取值 local|dev|test|ci|prod。仅非生产环境放宽「回环地址」的来源判定
	// （见 originAllowed 的说明）。
	Env string
}

// AuthHandler 处理认证路由（SPEC-AUTHN-001 / SPEC-WEB-001 REQ-04）。
type AuthHandler struct {
	svc            *ucauthn.Service
	accounts       *ucaccount.Service
	logger         *slog.Logger
	allowedOrigins []string
	cookieSecure   bool
	refreshTTL     time.Duration
	env            string
}

// NewAuthHandler 构造认证处理器。
func NewAuthHandler(svc *ucauthn.Service, opts AuthOptions, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{
		svc:            svc,
		accounts:       opts.Accounts,
		logger:         logger,
		allowedOrigins: opts.AllowedOrigins,
		cookieSecure:   opts.CookieSecure,
		refreshTTL:     opts.RefreshTTL,
		env:            opts.Env,
	}
}

// Login 处理 POST /api/v1/auth/login。
//
// 响应体仍是完整令牌对（契约不变）：SDK / CLI 把 refresh_token 放 body 里用。
// 浏览器侧额外收到 httpOnly cookie，SPA 只读 access_token、refresh 走 cookie。
// 也就是说"响应体里有 refresh_token"是**兼容性保留**，不是给前端用的——
// 前端一旦把它存进 localStorage，REQ-04 的初衷（XSS 拿不到长凭据）就白费了。
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req dto.LoginRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	pair, err := h.svc.Login(r.Context(), ucauthn.LoginCommand{
		Account: req.Account, Password: req.Password,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	h.setRefreshCookie(w, pair.RefreshToken)
	httputil.WriteJSON(w, http.StatusOK, dto.TokenPairFromDomain(pair))
}

// Refresh 处理 POST /api/v1/auth/refresh。
//
// 令牌来源优先级：cookie > 请求体。cookie 优先是因为浏览器场景必须用它——
// 若先读 body，被 XSS 注入的脚本反倒能拿旧令牌续期。
// 两种来源都要能跑：SDK 没有 cookie，浏览器不会往 body 里放令牌。
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		httputil.WriteProblem(w, r, h.logger, h.originDenied(r))
		return
	}

	token, fromCookie := h.refreshTokenFrom(r)
	if token == "" && !fromCookie {
		// 没有 cookie 时才去读 body；cookie 命中就不解析 body（它可能压根是空的）。
		var req dto.RefreshRequest
		if r.ContentLength != 0 {
			if err := httputil.DecodeJSON(r, &req); err != nil {
				httputil.WriteProblem(w, r, h.logger, err)
				return
			}
		}
		token = req.RefreshToken
	}

	pair, err := h.svc.Refresh(r.Context(), ucauthn.RefreshCommand{RefreshToken: token})
	if err != nil {
		// 续期失败要顺手把死掉的 cookie 清掉：否则浏览器带着一个必然失败的令牌
		// 反复触发续期，每次都在服务端走一遍"重放检测"，甚至误触 family 连坐。
		h.clearRefreshCookie(w)
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	if fromCookie {
		// 轮换后必须回写：旧令牌已标记使用，不换 cookie 的话下一次刷新就是重放，
		// 会触发整个 family 连坐撤销——表现为"用户被莫名踢下线"。
		h.setRefreshCookie(w, pair.RefreshToken)
	}
	httputil.WriteJSON(w, http.StatusOK, dto.TokenPairFromDomain(pair))
}

// Logout 处理 POST /api/v1/auth/logout。
//
// 撤销当前会话的 refresh family 并清 cookie。幂等：会话已失效时返回 204 而不是 401，
// 否则"登出"在会话过期后反而报错，前端只能吞掉错误。
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if !h.originAllowed(r) {
		httputil.WriteProblem(w, r, h.logger, h.originDenied(r))
		return
	}

	token, fromCookie := h.refreshTokenFrom(r)
	if token == "" && !fromCookie && r.ContentLength != 0 {
		var req dto.RefreshRequest
		if err := httputil.DecodeJSON(r, &req); err == nil {
			token = req.RefreshToken
		}
	}

	if err := h.svc.Logout(r.Context(), ucauthn.LogoutCommand{RefreshToken: token}); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// Me 处理 GET /api/v1/auth/me。
//
// roles 只给前端做菜单粗筛（REQ-07）。前端**不得**据此做允许/拒绝判断：
// 真正的判定在服务端 ACLGuard。两处逻辑一旦分叉，就会出现"菜单看得见、点了 403"，
// 或者更糟的"菜单藏了、接口其实放行"。
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	sub, ok := httputil.SubjectFromContext(r.Context())
	if !ok {
		httputil.WriteProblem(w, r, h.logger, authn.Unauthenticated())
		return
	}
	roles := []string{}
	if sub.Kind == authn.KindHuman && h.accounts != nil {
		rs, err := h.accounts.RolesOfUser(r.Context(), sub.ID)
		if err != nil {
			httputil.WriteProblem(w, r, h.logger, err)
			return
		}
		roles = roleStrings(rs)
	}
	httputil.WriteJSON(w, http.StatusOK, dto.MeResponse{
		Subject: dto.SubjectFromDomain(sub),
		Roles:   roles,
	})
}

// EmbedToken 处理 POST /api/v1/auth/embed-token（DESIGN-WEB-001#DEC-14）。
//
// 只为**当前已认证主体**签发一枚短 TTL 令牌，交给前端经 postMessage 传给嵌入的
// mo-mind iframe。这里不产生任何提权：签的就是调用者自己，权限与它原有的一致。
// 保护来自 TTL（默认 5 分钟）与传输通道（postMessage，不进 URL）。
func (h *AuthHandler) EmbedToken(w http.ResponseWriter, r *http.Request) {
	sub, ok := httputil.SubjectFromContext(r.Context())
	if !ok {
		httputil.WriteProblem(w, r, h.logger, authn.Unauthenticated())
		return
	}
	raw, expiresIn, err := h.svc.EmbedToken(r.Context(), sub)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.EmbedTokenResponse{
		AccessToken: raw,
		TokenType:   "Bearer",
		ExpiresIn:   expiresIn,
	})
}

// CreateAPIKey 处理 POST /api/v1/auth/api-keys。
// 分配 API Key 是人类专属动作（AGENTS.md §3.3 权限边界）：AI 主体一律 403。
func (h *AuthHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	sub, ok := httputil.SubjectFromContext(r.Context())
	if !ok || sub.Kind != authn.KindHuman {
		httputil.WriteProblem(w, r, h.logger,
			shared.New("FORBIDDEN", "只有人类主体可以分配 API Key", nil))
		return
	}
	var req dto.CreateAPIKeyRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	subjectID := req.SubjectID
	if subjectID == 0 {
		subjectID = sub.ID
	}
	k, plain, err := h.svc.CreateAPIKey(r.Context(), ucauthn.CreateAPIKeyCommand{
		Name:        req.Name,
		SubjectKind: req.SubjectKind,
		SubjectID:   subjectID,
		AgentID:     req.AgentID,
		Scopes:      req.Scopes,
		TTLDays:     req.TTLDays,
		CreatedByID: sub.ID,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	resp := dto.APIKeyFromDomain(k)
	resp.Key = plain // 明文仅此一次出现在响应中
	httputil.WriteJSON(w, http.StatusCreated, resp)
}

// RevokeAPIKey 处理 DELETE /api/v1/auth/api-keys/{id}。
func (h *AuthHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	sub, ok := httputil.SubjectFromContext(r.Context())
	if !ok || sub.Kind != authn.KindHuman {
		httputil.WriteProblem(w, r, h.logger,
			shared.New("FORBIDDEN", "只有人类主体可以撤销 API Key", nil))
		return
	}
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	if err := h.svc.RevokeAPIKey(r.Context(), id); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "status": authn.StatusRevoked})
}

// setRefreshCookie 下发 refresh cookie。
// MaxAge 取 refresh TTL：cookie 比令牌活得久没有意义；反过来（cookie 先死）
// 会让还没过期的 refresh token 白白失效。
func (h *AuthHandler) setRefreshCookie(w http.ResponseWriter, token string) {
	if token == "" {
		return
	}
	c := &http.Cookie{
		Name:     RefreshCookieName,
		Value:    token,
		Path:     RefreshCookiePath,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
	if h.refreshTTL > 0 {
		c.MaxAge = int(h.refreshTTL.Seconds())
	}
	http.SetCookie(w, c)
}

// clearRefreshCookie 清除 refresh cookie。
// Path 必须与下发时一致，否则浏览器认为这是另一个 cookie，会在旁边再存一个空值
// cookie，真正的那个照样每次带上——"登出无效"最典型的原因就是这个。
func (h *AuthHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     RefreshCookiePath,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// refreshTokenFrom 取 refresh token，并报告它是否来自 cookie。
func (h *AuthHandler) refreshTokenFrom(r *http.Request) (string, bool) {
	c, err := r.Cookie(RefreshCookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}

// originDenied 构造来源被拒的错误体。
func (h *AuthHandler) originDenied(r *http.Request) error {
	return shared.New("ORIGIN_NOT_ALLOWED", "请求来源不在白名单内", map[string]any{
		"origin": r.Header.Get("Origin"),
		"host":   r.Host,
	})
}

// originAllowed 做 Origin 白名单校验（CSRF 纵深防御，SPEC-WEB-001 REQ-04）。
//
// 缺 Origin 一律放行：curl、SDK、服务端调用都不发这个头，拦掉只会让非浏览器
// 客户端莫名其妙全挂。浏览器的跨站请求**一定**带 Origin，所以这条防线对真正
// 要防的场景是有效的（叠加 SameSite=Lax 已是双重保险）。
func (h *AuthHandler) originAllowed(r *http.Request) bool {
	return httputil.AllowOrigin(r.Header.Get("Origin"), r.Host, h.allowedOrigins, h.env)
}
