package authn

// 凭据状态。
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// APIKey 是 API Key 聚合。
// 明文只在创建响应中出现一次，聚合内只有哈希与展示前缀（DESIGN-AUTHN-001 DEC-03）。
type APIKey struct {
	ID          int64
	KeyPrefix   string
	KeyHash     string
	Name        string
	SubjectKind string
	SubjectID   int64
	AgentID     string
	Scopes      []string
	Status      string
	ExpiresAtMs int64 // 0 表示永不过期
	CreatedByID int64
	CreatedAtMs int64
	UpdatedAtMs int64
	RevokedAtMs int64
}

// NewAPIKey 构造待落库的 API Key（状态为 active，时间戳由调用方注入）。
func NewAPIKey(keyPrefix, keyHash, name, subjectKind string, subjectID int64,
	agentID string, scopes []string, expiresAtMs, createdByID, nowMs int64) APIKey {
	if scopes == nil {
		scopes = []string{}
	}
	return APIKey{
		KeyPrefix:   keyPrefix,
		KeyHash:     keyHash,
		Name:        name,
		SubjectKind: subjectKind,
		SubjectID:   subjectID,
		AgentID:     agentID,
		Scopes:      scopes,
		Status:      StatusActive,
		ExpiresAtMs: expiresAtMs,
		CreatedByID: createdByID,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}
}

// Usable 报告该 key 在 nowMs 时刻是否可用（未撤销且未过期）。
func (k APIKey) Usable(nowMs int64) bool {
	if k.Status != StatusActive {
		return false
	}
	if k.ExpiresAtMs > 0 && nowMs >= k.ExpiresAtMs {
		return false
	}
	return true
}

// Revoke 撤销 key（终态，不复活，见 SPEC-AUTHN-001 状态迁移表）。
func (k *APIKey) Revoke(atMs int64) {
	k.Status = StatusRevoked
	k.RevokedAtMs = atMs
	k.UpdatedAtMs = atMs
}

// Subject 返回该 key 绑定的执行主体。
func (k APIKey) Subject() Subject {
	kind := k.SubjectKind
	if kind == "" {
		kind = KindAI
	}
	return Subject{Kind: kind, ID: k.SubjectID, AgentID: k.AgentID}
}

// RefreshToken 是一次性刷新令牌。
type RefreshToken struct {
	ID          int64
	TokenHash   string
	UserID      int64
	FamilyID    string
	ExpiresAtMs int64
	UsedAtMs    int64
	RevokedAtMs int64
	CreatedAtMs int64
}

// NewRefreshToken 构造待落库的 refresh token。
func NewRefreshToken(tokenHash string, userID int64, familyID string, expiresAtMs, nowMs int64) RefreshToken {
	return RefreshToken{
		TokenHash:   tokenHash,
		UserID:      userID,
		FamilyID:    familyID,
		ExpiresAtMs: expiresAtMs,
		CreatedAtMs: nowMs,
	}
}

// Usable 报告该 token 是否仍可使用：未使用、未撤销、未过期。
func (t RefreshToken) Usable(nowMs int64) bool {
	if t.UsedAtMs > 0 || t.RevokedAtMs > 0 {
		return false
	}
	return t.ExpiresAtMs <= 0 || nowMs < t.ExpiresAtMs
}

// MarkUsed 标记为已使用（一次性：使用后不得再用于刷新）。
func (t *RefreshToken) MarkUsed(atMs int64) { t.UsedAtMs = atMs }

// Revoke 撤销（家族连坐时对整个 family 生效）。
func (t *RefreshToken) Revoke(atMs int64) { t.RevokedAtMs = atMs }

// User 是认证所需的用户视图（不含密码外的敏感字段）。
type User struct {
	ID           int64
	Account      string
	PasswordHash string
	Realname     string
	Disabled     bool
}

// Subject 返回用户对应的执行主体（人类）。
func (u User) Subject() Subject {
	return Subject{Kind: KindHuman, ID: u.ID, Account: u.Account}
}
