package authn

import (
	"context"

	"github.com/mst8222/regto/internal/domain/shared"
)

// Unauthenticated 构造认证失败错误。
// 三处（账号不存在 / 密码错误 / 令牌失效）共用同一消息与同一错误码，
// 不泄露失败原因（NFR-03）；HTTP 层映射为 401 UNAUTHENTICATED。
func Unauthenticated() error {
	return shared.New("UNAUTHENTICATED", "invalid or expired credentials", nil)
}

// UserRepository 提供认证所需的用户读取与登录时间更新。
type UserRepository interface {
	// FindByAccount 按登录名查找未删除用户；不存在返回 shared.ErrNotFound。
	FindByAccount(ctx context.Context, account string) (User, error)
	// FindByID 按 ID 查找未删除用户；不存在返回 shared.ErrNotFound。
	FindByID(ctx context.Context, id int64) (User, error)
	// TouchLogin 更新最后登录时间。
	TouchLogin(ctx context.Context, userID, atMs int64) error
}

// APIKeyRepository 管理 API Key。
type APIKeyRepository interface {
	// FindByHash 按哈希查找 key；不存在返回 shared.ErrNotFound。
	FindByHash(ctx context.Context, keyHash string) (APIKey, error)
	// FindByID 按 ID 查找 key；吊销后需据此算出缓存键以即时失效（DESIGN-REDIS-001 DEC-03）。
	FindByID(ctx context.Context, id int64) (APIKey, error)
	// Save 插入 key（明文不入库）。
	Save(ctx context.Context, k *APIKey) error
	// Revoke 撤销指定 key。
	Revoke(ctx context.Context, id, atMs int64) error
	// TouchUsed 更新最近使用时间。
	TouchUsed(ctx context.Context, id, atMs int64) error
}

// RefreshTokenRepository 管理 refresh token（一次性 + 家族连坐）。
type RefreshTokenRepository interface {
	// FindByHash 按哈希查找；不存在返回 shared.ErrNotFound。
	FindByHash(ctx context.Context, tokenHash string) (RefreshToken, error)
	// Save 插入新 token。
	Save(ctx context.Context, t *RefreshToken) error
	// MarkUsed 标记已使用。
	MarkUsed(ctx context.Context, id, atMs int64) error
	// RevokeFamily 撤销同一 family 的全部 token（重放检测后的连坐动作）。
	RevokeFamily(ctx context.Context, familyID string, atMs int64) error
}
