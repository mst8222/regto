package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/authn"
	"github.com/mst8222/regto/internal/domain/shared"
)

// UserRepo 实现 authn.UserRepository。
type UserRepo struct{ pool *pgxpool.Pool }

// NewUserRepo 构造用户仓储。
func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

// FindByAccount 按登录名查找用户；不存在返回 shared.ErrNotFound。
func (r *UserRepo) FindByAccount(ctx context.Context, account string) (authn.User, error) {
	var (
		u         authn.User
		realname  *string
		passHash  string
		disabled  bool
		accountNm string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT id, account, password_hash, realname, disabled
		   FROM users WHERE account = $1 AND deleted_at IS NULL`, account,
	).Scan(&u.ID, &accountNm, &passHash, &realname, &disabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authn.User{}, fmt.Errorf("user %s: %w", account, shared.ErrNotFound)
		}
		return authn.User{}, fmt.Errorf("查询用户失败: %w", err)
	}
	u.Account = accountNm
	u.PasswordHash = passHash
	u.Disabled = disabled
	if realname != nil {
		u.Realname = *realname
	}
	return u, nil
}

// FindByID 按 ID 查找用户；不存在返回 shared.ErrNotFound。
func (r *UserRepo) FindByID(ctx context.Context, id int64) (authn.User, error) {
	var (
		u         authn.User
		realname  *string
		passHash  string
		disabled  bool
		accountNm string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT id, account, password_hash, realname, disabled
		   FROM users WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(&u.ID, &accountNm, &passHash, &realname, &disabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authn.User{}, fmt.Errorf("user %d: %w", id, shared.ErrNotFound)
		}
		return authn.User{}, fmt.Errorf("查询用户失败: %w", err)
	}
	u.Account = accountNm
	u.PasswordHash = passHash
	u.Disabled = disabled
	if realname != nil {
		u.Realname = *realname
	}
	return u, nil
}

// TouchLogin 更新最后登录时间。
func (r *UserRepo) TouchLogin(ctx context.Context, userID, atMs int64) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET last_login_at = $2 WHERE id = $1`, userID, ms(atMs)); err != nil {
		return fmt.Errorf("更新登录时间失败: %w", err)
	}
	return nil
}

// APIKeyRepo 实现 authn.APIKeyRepository。
// 明文永不落库：本仓储只接收哈希与展示前缀（DESIGN-AUTHN-001 DEC-03）。
type APIKeyRepo struct{ pool *pgxpool.Pool }

// NewAPIKeyRepo 构造 API Key 仓储。
func NewAPIKeyRepo(pool *pgxpool.Pool) *APIKeyRepo { return &APIKeyRepo{pool: pool} }

const apiKeyColumns = `id, key_prefix, key_hash, name, subject_kind, subject_id, agent_id,
	scopes, status, expires_at, last_used_at, created_by_id, created_at, updated_at, revoked_at`

// FindByHash 按哈希查找 key；不存在返回 shared.ErrNotFound。
func (r *APIKeyRepo) FindByHash(ctx context.Context, keyHash string) (authn.APIKey, error) {
	return r.queryOne(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE key_hash = $1`, keyHash)
}

// FindByID 按 ID 查找 key；不存在返回 shared.ErrNotFound。
func (r *APIKeyRepo) FindByID(ctx context.Context, id int64) (authn.APIKey, error) {
	return r.queryOne(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE id = $1`, id)
}

// queryOne 执行单行查询并映射为 APIKey（FindByHash / FindByID 共用）。
func (r *APIKeyRepo) queryOne(ctx context.Context, sql string, arg any) (authn.APIKey, error) {
	var (
		k         authn.APIKey
		scopes    []byte
		agentID   *string
		createdBy *int64
		expiresAt *time.Time
		lastUsed  *time.Time
		revokedAt *time.Time
		createdAt time.Time
		updatedAt time.Time
	)
	err := r.pool.QueryRow(ctx, sql, arg).Scan(&k.ID, &k.KeyPrefix, &k.KeyHash, &k.Name, &k.SubjectKind, &k.SubjectID, &agentID,
		&scopes, &k.Status, &expiresAt, &lastUsed, &createdBy, &createdAt, &updatedAt, &revokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authn.APIKey{}, fmt.Errorf("api key: %w", shared.ErrNotFound)
		}
		return authn.APIKey{}, fmt.Errorf("查询 api key 失败: %w", err)
	}
	if agentID != nil {
		k.AgentID = *agentID
	}
	if createdBy != nil {
		k.CreatedByID = *createdBy
	}
	if len(scopes) > 0 {
		if err := json.Unmarshal(scopes, &k.Scopes); err != nil {
			return authn.APIKey{}, fmt.Errorf("解析 scopes 失败: %w", err)
		}
	}
	k.ExpiresAtMs = msOf(expiresAt)
	k.CreatedAtMs = msOf(&createdAt)
	k.UpdatedAtMs = msOf(&updatedAt)
	k.RevokedAtMs = msOf(revokedAt)
	return k, nil
}

// Save 插入 key 并回填自增 ID。
func (r *APIKeyRepo) Save(ctx context.Context, k *authn.APIKey) error {
	scopes, err := json.Marshal(k.Scopes)
	if err != nil {
		return fmt.Errorf("序列化 scopes 失败: %w", err)
	}
	var agentID, createdBy any
	if k.AgentID != "" {
		agentID = k.AgentID
	}
	if k.CreatedByID > 0 {
		createdBy = k.CreatedByID
	}
	err = r.pool.QueryRow(ctx, `
		INSERT INTO api_keys (key_prefix, key_hash, name, subject_kind, subject_id, agent_id,
			scopes, status, expires_at, created_by_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		k.KeyPrefix, k.KeyHash, k.Name, k.SubjectKind, k.SubjectID, agentID, scopes,
		k.Status, msOrNil(k.ExpiresAtMs), createdBy, ms(k.CreatedAtMs), ms(k.UpdatedAtMs),
	).Scan(&k.ID)
	if err != nil {
		return fmt.Errorf("插入 api key 失败: %w", err)
	}
	return nil
}

// Revoke 撤销 key（终态）。
func (r *APIKeyRepo) Revoke(ctx context.Context, id, atMs int64) error {
	ct, err := r.pool.Exec(ctx,
		`UPDATE api_keys SET status = 'revoked', revoked_at = $2, updated_at = $2 WHERE id = $1`,
		id, ms(atMs))
	if err != nil {
		return fmt.Errorf("撤销 api key 失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("api key %d: %w", id, shared.ErrNotFound)
	}
	return nil
}

// TouchUsed 更新最近使用时间。
func (r *APIKeyRepo) TouchUsed(ctx context.Context, id, atMs int64) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE api_keys SET last_used_at = $2 WHERE id = $1`, id, ms(atMs)); err != nil {
		return fmt.Errorf("更新 api key 使用时间失败: %w", err)
	}
	return nil
}

// RefreshTokenRepo 实现 authn.RefreshTokenRepository。
type RefreshTokenRepo struct{ pool *pgxpool.Pool }

// NewRefreshTokenRepo 构造 refresh token 仓储。
func NewRefreshTokenRepo(pool *pgxpool.Pool) *RefreshTokenRepo {
	return &RefreshTokenRepo{pool: pool}
}

// FindByHash 按哈希查找；不存在返回 shared.ErrNotFound。
func (r *RefreshTokenRepo) FindByHash(ctx context.Context, tokenHash string) (authn.RefreshToken, error) {
	var (
		t         authn.RefreshToken
		expiresAt time.Time
		usedAt    *time.Time
		revokedAt *time.Time
		createdAt time.Time
	)
	err := r.pool.QueryRow(ctx, `
		SELECT id, token_hash, user_id, family_id, expires_at, used_at, revoked_at, created_at
		  FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&t.ID, &t.TokenHash, &t.UserID, &t.FamilyID, &expiresAt, &usedAt, &revokedAt, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authn.RefreshToken{}, fmt.Errorf("refresh token: %w", shared.ErrNotFound)
		}
		return authn.RefreshToken{}, fmt.Errorf("查询 refresh token 失败: %w", err)
	}
	t.ExpiresAtMs = msOf(&expiresAt)
	t.UsedAtMs = msOf(usedAt)
	t.RevokedAtMs = msOf(revokedAt)
	t.CreatedAtMs = msOf(&createdAt)
	return t, nil
}

// Save 插入 token 并回填 ID。
func (r *RefreshTokenRepo) Save(ctx context.Context, t *authn.RefreshToken) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, family_id, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		t.TokenHash, t.UserID, t.FamilyID, ms(t.ExpiresAtMs), ms(t.CreatedAtMs),
	).Scan(&t.ID)
	if err != nil {
		return fmt.Errorf("插入 refresh token 失败: %w", err)
	}
	return nil
}

// MarkUsed 标记 token 已使用（一次性）。
func (r *RefreshTokenRepo) MarkUsed(ctx context.Context, id, atMs int64) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET used_at = $2 WHERE id = $1`, id, ms(atMs)); err != nil {
		return fmt.Errorf("标记 refresh token 已使用失败: %w", err)
	}
	return nil
}

// RevokeFamily 撤销同一 family 的全部 token（重放检测后的连坐动作，DEC-04）。
func (r *RefreshTokenRepo) RevokeFamily(ctx context.Context, familyID string, atMs int64) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID, ms(atMs)); err != nil {
		return fmt.Errorf("撤销 refresh token 家族失败: %w", err)
	}
	return nil
}

// msOrNil 把毫秒转为时间参数；0 写 NULL（用于「永不过期」语义）。
func msOrNil(v int64) any {
	if v == 0 {
		return nil
	}
	return ms(v)
}
