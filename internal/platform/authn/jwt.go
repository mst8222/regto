// Package authn 提供认证的技术实现：JWT 签发/校验、随机凭据生成与哈希。
// 它只依赖 domain/authn 的值语义，不触碰存储与 HTTP（依赖方向：platform → domain）。
package authn

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/golang-jwt/jwt/v5"

	dauthn "github.com/mst8222/regto/internal/domain/authn"
)

// 凭据类型。
const (
	// APIKeyDisplayPrefix 是 API Key 明文前缀（人类可辨识的来源标识）。
	APIKeyDisplayPrefix = "rk_live_"
	// APIKeyPrefixLen 是入库展示用的前缀长度（与 api_keys.key_prefix 的 VARCHAR(12) 对齐）。
	APIKeyPrefixLen = 12
	// APIKeyRandomBytes 是 API Key 的随机字节数（256 位熵）。
	APIKeyRandomBytes = 32
	// RefreshTokenRandomBytes 是 refresh token 的随机字节数（256 位熵）。
	RefreshTokenRandomBytes = 32
	// MinSecretLen 是 JWT 密钥的最小长度（HMAC-SHA256 要求密钥足够长才有意义）。
	MinSecretLen = 32
	// ClockSkew 是允许的时钟偏移（SPEC-AUTHN-001 REQ-02：±60 秒）。
	ClockSkew = 60 * time.Second
	// DefaultAccessTTL 是 access token 默认有效期（REQ-01：15 分钟）。
	DefaultAccessTTL = 15 * time.Minute
	// DefaultIssuer 是默认签发者标识。
	DefaultIssuer = "regto"
)

// DefaultRefreshTTL 是 refresh token 默认有效期（SPEC-AUTHN-001 REQ-01：7 天）。
const DefaultRefreshTTL = 7 * 24 * time.Hour

// DefaultEmbedTTL 是嵌入位令牌默认有效期（DESIGN-WEB-001#DEC-14）。
//
// 5 分钟的依据：够完成 iframe 加载 + 握手 + 首屏数据拉取，
// 而泄漏后的可用窗口以分钟计。再短会在慢网络下握手失败，再长则失去意义。
const DefaultEmbedTTL = 5 * time.Minute

// MaxEmbedTTL 是嵌入令牌可配置的上限（30 分钟）。
//
// 上限存在的理由：TTL 是这里唯一真实的约束（scope 声明对第三方验证方形同虚设），
// 若允许任意配置，一次误配就能把短 TTL 的保护完全抵消。上限即护栏。
const MaxEmbedTTL = 30 * time.Minute

// Claims 是 access token 的载荷。
type Claims struct {
	jwt.RegisteredClaims
	Kind    string `json:"kind"`               // human | ai | system
	AgentID string `json:"agent_id,omitempty"` // 仅 AI 主体
	Account string `json:"account,omitempty"`
}

// Issuer 签发与校验 access token。
type Issuer struct {
	secret    []byte
	issuer    string
	accessTTL time.Duration
	now       func() time.Time
}

// NewIssuer 构造签发器。密钥过短直接报错——
// 弱密钥等于「全世界都能伪造令牌」，属于配置缺陷必须启动期暴露（DEC-05）。
func NewIssuer(secret []byte, issuer string, accessTTL time.Duration, now func() time.Time) (*Issuer, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("JWT 密钥长度不足 %d 字节（当前 %d）", MinSecretLen, len(secret))
	}
	if issuer == "" {
		issuer = DefaultIssuer
	}
	if accessTTL <= 0 {
		accessTTL = DefaultAccessTTL
	}
	if now == nil {
		now = time.Now
	}
	return &Issuer{secret: secret, issuer: issuer, accessTTL: accessTTL, now: now}, nil
}

// AccessTTL 返回 access token 有效期（供登录响应返回 expires_in）。
func (s *Issuer) AccessTTL() time.Duration { return s.accessTTL }

// IssueWithTTL 按指定有效期签发 access token（嵌入位用，DESIGN-WEB-001#DEC-14）。
//
// 为什么不直接复用 Issue：嵌入令牌要的是**比主会话短得多**的 TTL（默认 5 分钟）。
// 把 iframe 里的第三方页面当作主会话持有者来发令牌，等于把全部接口的访问权
// 交给它整个 access 周期——这不是"方便"，是把主会话凭据的暴露面按最大值放大。
func (s *Issuer) IssueWithTTL(sub dauthn.Subject, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = s.accessTTL
	}
	now := s.now().UTC()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", sub.ID),
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Kind:    sub.Kind,
		AgentID: sub.AgentID,
		Account: sub.Account,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("签发 JWT 失败: %w", err)
	}
	return signed, nil
}

// Issue 为主体签发 access token。
func (s *Issuer) Issue(sub dauthn.Subject) (string, error) {
	return s.IssueWithTTL(sub, s.accessTTL)
}

// Parse 校验并解析 access token。
// 必校签名、签发者、时间与类型；任何一项不符都返回同一错误，不泄露原因（NFR-03）。
func (s *Issuer) Parse(raw string) (dauthn.Subject, error) {
	var claims Claims
	token, err := jwt.ParseWithClaims(raw, &claims,
		func(*jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithIssuer(s.issuer),
		jwt.WithLeeway(ClockSkew),
		jwt.WithTimeFunc(s.now),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return dauthn.Subject{}, dauthn.Unauthenticated()
	}
	var id int64
	if _, err := fmt.Sscanf(claims.Subject, "%d", &id); err != nil || id <= 0 {
		return dauthn.Subject{}, dauthn.Unauthenticated()
	}
	kind := claims.Kind
	if kind != dauthn.KindHuman && kind != dauthn.KindAI && kind != dauthn.KindSystem {
		return dauthn.Subject{}, dauthn.Unauthenticated()
	}
	return dauthn.Subject{
		Kind:    kind,
		ID:      id,
		Account: claims.Account,
		AgentID: claims.AgentID,
	}, nil
}

// GenerateAPIKey 生成 API Key：返回明文（仅此一次）、展示前缀与 sha256 哈希。
// 明文永不入库（DESIGN-AUTHN-001 DEC-03）。
func GenerateAPIKey() (plain, prefix, hash string, err error) {
	body, err := randomString(APIKeyRandomBytes)
	if err != nil {
		return "", "", "", err
	}
	plain = APIKeyDisplayPrefix + body
	return plain, plain[:APIKeyPrefixLen], HashKey(plain), nil
}

// GenerateRefreshToken 生成 refresh token：返回明文（仅此一次）与 sha256 哈希。
func GenerateRefreshToken() (plain, hash string, err error) {
	plain, err = randomString(RefreshTokenRandomBytes)
	if err != nil {
		return "", "", err
	}
	return plain, HashKey(plain), nil
}

// HashKey 返回凭据明文的 sha256 十六进制串。
// 用 SHA-256 而非慢哈希：key 本身是 256 位随机串，不存在字典攻击面。
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqual 常量时间比较（避免通过响应时间侧信道推断哈希）。
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// randomString 生成 URL 安全的随机字符串（32 字节 → 43 字符）。
func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", fmt.Errorf("读取随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
