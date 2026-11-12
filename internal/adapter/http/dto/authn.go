package dto

import (
	"github.com/mst8222/regto/internal/domain/authn"
	ucauthn "github.com/mst8222/regto/internal/usecase/authn"
)

// LoginRequest 是登录请求体。
type LoginRequest struct {
	Account  string `json:"account"`
	Password string `json:"password"`
}

// RefreshRequest 是刷新请求体。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// CreateAPIKeyRequest 是创建 API Key 的请求体。
type CreateAPIKeyRequest struct {
	Name        string   `json:"name"`
	SubjectKind string   `json:"subject_kind,omitempty"` // human | ai；缺省按 agent_id 推断
	SubjectID   int64    `json:"subject_id,omitempty"`   // 缺省绑定创建者本人
	AgentID     string   `json:"agent_id,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	TTLDays     int      `json:"ttl_days,omitempty"` // 0 = 永不过期
}

// SubjectResponse 是执行主体摘要。
type SubjectResponse struct {
	Kind    string `json:"kind"`
	ID      int64  `json:"id"`
	Account string `json:"account,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

// MeResponse 是当前会话主体（GET /api/v1/auth/me，SPEC-WEB-001 REQ-07）。
// Roles 仅用于前端菜单粗筛，不是权限判定依据（判定源是服务端 ACLGuard）。
type MeResponse struct {
	Subject SubjectResponse `json:"subject"`
	Roles   []string        `json:"roles"`
}

// EmbedTokenResponse 是嵌入位令牌响应（DESIGN-WEB-001#DEC-14）。
//
// 字段名沿用 access_token / expires_in：mo-mind 侧按标准 Bearer 用法处理即可，
// 不需要为嵌入场景学一套新字段。
type EmbedTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// TokenPairResponse 是令牌对响应。
type TokenPairResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	TokenType    string          `json:"token_type"`
	ExpiresIn    int             `json:"expires_in"`
	Subject      SubjectResponse `json:"subject"`
}

// APIKeyResponse 是 API Key 响应；Key 字段仅在创建时返回一次。
type APIKeyResponse struct {
	ID          int64    `json:"id"`
	Key         string   `json:"key,omitempty"`
	KeyPrefix   string   `json:"key_prefix"`
	Name        string   `json:"name"`
	SubjectKind string   `json:"subject_kind"`
	SubjectID   int64    `json:"subject_id"`
	AgentID     string   `json:"agent_id,omitempty"`
	Scopes      []string `json:"scopes"`
	Status      string   `json:"status"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

// TokenPairFromDomain 把令牌对映射为响应体。
func TokenPairFromDomain(p *ucauthn.TokenPair) TokenPairResponse {
	return TokenPairResponse{
		AccessToken:  p.AccessToken,
		RefreshToken: p.RefreshToken,
		TokenType:    p.TokenType,
		ExpiresIn:    p.ExpiresIn,
		Subject:      SubjectFromDomain(p.Subject),
	}
}

// SubjectFromDomain 把领域主体映射为响应体。
func SubjectFromDomain(s authn.Subject) SubjectResponse {
	return SubjectResponse{
		Kind:    s.Kind,
		ID:      s.ID,
		Account: s.Account,
		AgentID: s.AgentID,
	}
}

// APIKeyFromDomain 把 API Key 聚合映射为响应体（不含哈希）。
func APIKeyFromDomain(k *authn.APIKey) APIKeyResponse {
	return APIKeyResponse{
		ID:          k.ID,
		KeyPrefix:   k.KeyPrefix,
		Name:        k.Name,
		SubjectKind: k.SubjectKind,
		SubjectID:   k.SubjectID,
		AgentID:     k.AgentID,
		Scopes:      k.Scopes,
		Status:      k.Status,
		ExpiresAt:   rfc3339(k.ExpiresAtMs),
		CreatedAt:   rfc3339(k.CreatedAtMs),
	}
}
