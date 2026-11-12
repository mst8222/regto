// Package authn 是认证域的应用服务：登录、刷新、API Key 管理。
// usecase 是事务边界与编排层；密码学与存储细节在 platform / adapter（AGENTS.md §5）。
package authn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	dauthn "github.com/mst8222/regto/internal/domain/authn"
	"github.com/mst8222/regto/internal/domain/shared"
	piauthn "github.com/mst8222/regto/internal/platform/authn"
	"github.com/mst8222/regto/internal/platform/cache"
	"github.com/mst8222/regto/internal/platform/clock"
	"github.com/mst8222/regto/internal/platform/id"
)

// Service 是认证域应用服务。
type Service struct {
	users      dauthn.UserRepository
	keys       dauthn.APIKeyRepository
	tokens     dauthn.RefreshTokenRepository
	issuer     *piauthn.Issuer
	ids        *id.Generator
	clk        clock.Clock
	refreshTTL time.Duration
	embedTTL   time.Duration
	cache      cache.Store // 可选：未配置 Redis 时为空实现（DESIGN-REDIS-001 DEC-01）
}

// Deps 是构造 Service 所需的依赖（显式注入，禁止全局变量）。
type Deps struct {
	Users      dauthn.UserRepository
	Keys       dauthn.APIKeyRepository
	Tokens     dauthn.RefreshTokenRepository
	Issuer     *piauthn.Issuer
	IDs        *id.Generator
	Clock      clock.Clock
	RefreshTTL time.Duration
	// EmbedTTL 是嵌入位令牌有效期（DESIGN-WEB-001#DEC-14），缺省 5 分钟、上限 30 分钟。
	EmbedTTL time.Duration
	// Cache 是可选缓存；为 nil 时注入空实现，业务分支完全一致。
	Cache cache.Store
}

// New 构造服务并校验必需依赖。
func New(d Deps) (*Service, error) {
	if d.Users == nil || d.Keys == nil || d.Tokens == nil || d.Issuer == nil {
		return nil, fmt.Errorf("认证服务依赖缺失：users/keys/tokens/issuer 均不可为 nil")
	}
	if d.Clock == nil {
		return nil, fmt.Errorf("认证服务依赖缺失：clock 不可为 nil")
	}
	if d.IDs == nil {
		d.IDs = id.NewSystem()
	}
	if d.RefreshTTL <= 0 {
		d.RefreshTTL = piauthn.DefaultRefreshTTL
	}
	// 上限护栏：TTL 是嵌入令牌唯一真实的约束，允许无限配置等于取消这个约束。
	if d.EmbedTTL <= 0 {
		d.EmbedTTL = piauthn.DefaultEmbedTTL
	}
	if d.EmbedTTL > piauthn.MaxEmbedTTL {
		d.EmbedTTL = piauthn.MaxEmbedTTL
	}
	if d.Cache == nil {
		d.Cache = cache.Noop()
	}
	return &Service{
		users: d.Users, keys: d.Keys, tokens: d.Tokens, issuer: d.Issuer,
		ids: d.IDs, clk: d.Clock, refreshTTL: d.RefreshTTL, embedTTL: d.EmbedTTL, cache: d.Cache,
	}, nil
}

// LoginCommand 是登录输入。
type LoginCommand struct {
	Account  string
	Password string
}

// TokenPair 是签发的令牌对。
type TokenPair struct {
	AccessToken  string         `json:"access_token"`
	RefreshToken string         `json:"refresh_token"`
	TokenType    string         `json:"token_type"`
	ExpiresIn    int            `json:"expires_in"`
	Subject      dauthn.Subject `json:"subject"`
}

// Login 校验账号密码并签发令牌对。
// 失败路径统一：账号不存在时也执行一次 bcrypt 比较，
// 使响应耗时与「密码错误」同量级（NFR-03）。
func (s *Service) Login(ctx context.Context, cmd LoginCommand) (*TokenPair, error) {
	now := s.clk.Now()
	nowMs := now.UnixMilli()

	user, err := s.users.FindByAccount(ctx, cmd.Account)
	if err != nil {
		// 账号不存在：仍执行一次哈希比较，避免计时侧信道泄露账号存在性。
		_ = piauthn.VerifyPassword("", cmd.Password)
		return nil, dauthn.Unauthenticated()
	}
	if !piauthn.VerifyPassword(user.PasswordHash, cmd.Password) || user.Disabled {
		return nil, dauthn.Unauthenticated()
	}
	if err := s.users.TouchLogin(ctx, user.ID, nowMs); err != nil {
		return nil, err
	}
	return s.issuePair(ctx, user.Subject(), s.ids.NewString(), nowMs)
}

// RefreshCommand 是刷新输入。
type RefreshCommand struct {
	RefreshToken string
}

// Refresh 用 refresh token 换取新的令牌对。
//
// 一次性语义（DEC-04）：旧 token 使用后立即作废；若被重放，
// 视为令牌泄露，撤销整个 family（含刚发出的新 token）。
func (s *Service) Refresh(ctx context.Context, cmd RefreshCommand) (*TokenPair, error) {
	if cmd.RefreshToken == "" {
		return nil, dauthn.Unauthenticated()
	}
	nowMs := s.clk.Now().UnixMilli()

	stored, err := s.tokens.FindByHash(ctx, piauthn.HashKey(cmd.RefreshToken))
	if err != nil {
		return nil, dauthn.Unauthenticated()
	}
	if !stored.Usable(nowMs) {
		// 已使用过 = 重放：连坐撤销整个 family，防止攻击者在轮换间隙续期。
		if stored.UsedAtMs > 0 {
			if revokeErr := s.tokens.RevokeFamily(ctx, stored.FamilyID, nowMs); revokeErr != nil {
				return nil, revokeErr
			}
		}
		return nil, dauthn.Unauthenticated()
	}
	if err := s.tokens.MarkUsed(ctx, stored.ID, nowMs); err != nil {
		return nil, err
	}
	// 主体信息以库中用户为准（账号改名后新 token 自动带上新账号）。
	subject := dauthn.Subject{Kind: dauthn.KindHuman, ID: stored.UserID}
	if u, err := s.users.FindByID(ctx, stored.UserID); err == nil {
		subject = u.Subject()
	}
	return s.issuePair(ctx, subject, stored.FamilyID, nowMs)
}

// LogoutCommand 是登出输入。
type LogoutCommand struct {
	RefreshToken string
}

// Logout 撤销该 refresh token 所在的整个 family（当前设备立即失去续期能力）。
//
// 为什么撤 family 而不是只撤这一个 token：refresh 是一次性轮换的（DEC-04），
// 库里同 family 可能已经躺着若干个"已使用"的历史 token；只撤当前这一个的话，
// 一次竞态里刚签发的新 token 仍然可用，登出就成了概率事件。
//
// 幂等：令牌不存在、已过期、已撤销都返回 nil。登出的语义是"确保这个会话不再能
// 续期"，前端在 401 续期竞态下可能拿一个刚被轮换掉的旧 token 来登出，这时候
// 报错对用户毫无意义（他确实已经登出了）。
func (s *Service) Logout(ctx context.Context, cmd LogoutCommand) error {
	if cmd.RefreshToken == "" {
		return nil
	}
	nowMs := s.clk.Now().UnixMilli()
	stored, err := s.tokens.FindByHash(ctx, piauthn.HashKey(cmd.RefreshToken))
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return nil
		}
		return err // 查库失败必须暴露：否则"登出成功"是假的
	}
	return s.tokens.RevokeFamily(ctx, stored.FamilyID, nowMs)
}

// EmbedToken 为当前主体签发一枚**短 TTL** 的嵌入令牌（DESIGN-WEB-001#DEC-14）。
//
// 签的是调用者自己，不存在提权路径；真正的保护来自 TTL（默认 5 分钟）与传输通道
// （前端经 postMessage 交给 iframe，绝不进 URL query——URL 会落到 referer、访问
// 日志与浏览器历史里）。校验方式与 access token 完全一致，所以 mo-mind 侧无需新的验签逻辑。
func (s *Service) EmbedToken(ctx context.Context, sub dauthn.Subject) (string, int64, error) {
	if sub.ID <= 0 {
		return "", 0, dauthn.Unauthenticated()
	}
	raw, err := s.issuer.IssueWithTTL(sub, s.embedTTL)
	if err != nil {
		return "", 0, err
	}
	return raw, int64(s.embedTTL.Seconds()), nil
}

// CreateAPIKeyCommand 是创建 API Key 的输入。
type CreateAPIKeyCommand struct {
	Name        string
	SubjectKind string // human | ai；为空时按 agent_id 推断
	SubjectID   int64  // 绑定用户
	AgentID     string
	Scopes      []string
	TTLDays     int // 0 表示永不过期
	CreatedByID int64
}

// CreateAPIKey 创建 API Key，返回聚合与**仅此一次**可见的明文。
func (s *Service) CreateAPIKey(ctx context.Context, cmd CreateAPIKeyCommand) (*dauthn.APIKey, string, error) {
	plain, prefix, hash, err := piauthn.GenerateAPIKey()
	if err != nil {
		return nil, "", err
	}
	nowMs := s.clk.Now().UnixMilli()
	var expiresAtMs int64
	if cmd.TTLDays > 0 {
		expiresAtMs = time.UnixMilli(nowMs).AddDate(0, 0, cmd.TTLDays).UnixMilli()
	}
	kind := cmd.SubjectKind
	if kind == "" {
		if cmd.AgentID != "" {
			kind = dauthn.KindAI
		} else {
			kind = dauthn.KindHuman
		}
	}
	k := dauthn.NewAPIKey(prefix, hash, cmd.Name, kind, cmd.SubjectID, cmd.AgentID,
		cmd.Scopes, expiresAtMs, cmd.CreatedByID, nowMs)
	if err := s.keys.Save(ctx, &k); err != nil {
		return nil, "", err
	}
	return &k, plain, nil
}

// RevokeAPIKey 撤销 API Key（终态，即时生效）。
//
// 吊销是安全动作：成功后立即删除缓存键，不等 TTL 自然过期（REQ-03 / DEC-03）。
func (s *Service) RevokeAPIKey(ctx context.Context, id int64) error {
	// 先取一次哈希用于算缓存键：吊销后即时失效优于「多一次读库」。
	hash := ""
	if k, err := s.keys.FindByID(ctx, id); err == nil {
		hash = k.KeyHash
	}
	if err := s.keys.Revoke(ctx, id, s.clk.Now().UnixMilli()); err != nil {
		return err
	}
	if hash != "" {
		_ = s.cache.Delete(ctx, apiKeyCacheKey(hash))
	}
	return nil
}

// APIKeyCacheTTL 是 API Key 校验结果的缓存时长（REQ-02：60 秒）。
// 取短 TTL 是因为认证路径对吊销延迟敏感，不能让失效等待过久。
const APIKeyCacheTTL = 60 * time.Second

// TouchUsedThrottle 是「最近使用时间」写库节流窗口（REQ-04：60 秒）。
const TouchUsedThrottle = 60 * time.Second

// VerifyAPIKey 校验 API Key 明文并返回其主体。
//
// 撤销/过期的 key 一律拒绝（REQ-06）。命中缓存时不再查库；
// 缓存键用明文哈希，**明文永不进入缓存**（NFR-01）。
func (s *Service) VerifyAPIKey(ctx context.Context, plain string) (dauthn.Subject, error) {
	if plain == "" {
		return dauthn.Subject{}, dauthn.Unauthenticated()
	}
	hash := piauthn.HashKey(plain)
	ck := apiKeyCacheKey(hash)

	var k dauthn.APIKey
	if hit, err := s.cache.Get(ctx, ck, &k); err == nil && hit {
		// 缓存命中仍需校验可用性：状态可能在缓存期内已过期（TTL 内到期）。
		if !k.Usable(s.clk.Now().UnixMilli()) {
			return dauthn.Subject{}, dauthn.Unauthenticated()
		}
		s.touchUsedThrottled(ctx, k.ID)
		return k.Subject(), nil
	}

	k, err := s.keys.FindByHash(ctx, hash)
	if err != nil {
		return dauthn.Subject{}, dauthn.Unauthenticated()
	}
	if !k.Usable(s.clk.Now().UnixMilli()) {
		return dauthn.Subject{}, dauthn.Unauthenticated()
	}
	_ = s.cache.Set(ctx, ck, k, APIKeyCacheTTL)
	s.touchUsedThrottled(ctx, k.ID)
	return k.Subject(), nil
}

// touchUsedThrottled 按时间窗口合并「最近使用时间」的写库（REQ-04）。
// SetNX 出错时照常写库——节流组件故障不得吞掉使用记录（DEC-04）。
func (s *Service) touchUsedThrottled(ctx context.Context, keyID int64) {
	nowMs := s.clk.Now().UnixMilli()
	gate, err := s.cache.SetNX(ctx, cache.Key("authn", "keyused", strconv.FormatInt(keyID, 10)),
		TouchUsedThrottle)
	if err == nil && !gate {
		return // 窗口内已写过，跳过
	}
	_ = s.keys.TouchUsed(ctx, keyID, nowMs)
}

// apiKeyCacheKey 构造 API Key 缓存键（regto:v1:authn:key:<sha256>）。
func apiKeyCacheKey(keyHash string) string {
	return cache.Key("authn", "key", keyHash)
}

// issuePair 签发 access + refresh 令牌对（refresh 与旧 token 同 family，便于连坐）。
func (s *Service) issuePair(ctx context.Context, subject dauthn.Subject, familyID string, nowMs int64) (*TokenPair, error) {
	access, err := s.issuer.Issue(subject)
	if err != nil {
		return nil, err
	}
	plain, hash, err := piauthn.GenerateRefreshToken()
	if err != nil {
		return nil, err
	}
	rt := dauthn.NewRefreshToken(hash, subject.ID, familyID,
		time.UnixMilli(nowMs).Add(s.refreshTTL).UnixMilli(), nowMs)
	if err := s.tokens.Save(ctx, &rt); err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: plain,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.issuer.AccessTTL().Seconds()),
		Subject:      subject,
	}, nil
}
