package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/account"
	"github.com/mst8222/regto/internal/domain/shared"
)

// AccountRepo 读写账号角色与用户-角色绑定（migrations/000022）。
//
// 只碰「事实」两张表（roles / user_roles）；权限矩阵不在这里——
// 它在 domain/account/matrix.go，是代码真源（DESIGN-ACCOUNT-001#DEC-01）。
type AccountRepo struct {
	pool *pgxpool.Pool
}

// NewAccountRepo 构造账号仓储。
func NewAccountRepo(pool *pgxpool.Pool) *AccountRepo { return &AccountRepo{pool: pool} }

// ListRoles 读取角色元数据（按 id 升序，保证输出稳定）。
func (r *AccountRepo) ListRoles(ctx context.Context) ([]account.Meta, error) {
	const q = `SELECT code, name, description, builtin FROM roles ORDER BY id`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("查询角色列表失败: %w", err)
	}
	defer rows.Close()

	var out []account.Meta
	for rows.Next() {
		var m account.Meta
		var code string
		if err := rows.Scan(&code, &m.Name, &m.Description, &m.Builtin); err != nil {
			return nil, fmt.Errorf("扫描角色失败: %w", err)
		}
		m.Code = account.Role(code)
		out = append(out, m)
	}
	return out, rows.Err()
}

// RolesOfUser 返回用户当前**活跃**的角色（已吊销的不算）。
//
// 查不到返回空切片而非错误：没有角色是合法状态（只是什么都做不了），
// 由 Authorize 用 NO_ROLE 表达，调用方不必区分「查不到」与「没配」。
func (r *AccountRepo) RolesOfUser(ctx context.Context, userID int64) ([]account.Role, error) {
	const q = `SELECT r.code
	             FROM user_roles ur JOIN roles r ON r.id = ur.role_id
	            WHERE ur.user_id = $1 AND ur.revoked_at IS NULL
	            ORDER BY r.id`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("查询用户角色失败: %w", err)
	}
	defer rows.Close()

	out := make([]account.Role, 0, 2)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("扫描用户角色失败: %w", err)
		}
		out = append(out, account.Role(code))
	}
	return out, rows.Err()
}

// AssignRole 绑定角色（幂等：已有活跃绑定时不新增行）。
//
// 幂等靠 uniq_user_roles_active 这个部分唯一索引 + ON CONFLICT DO NOTHING：
// 并发重复绑定也只会有一行，不需要应用层加锁。
// 若 roles 表里没有该 code（代码与迁移不一致），INSERT 会插入 0 行——这种情况
// 必须报出来，否则调用方会以为绑定成功、实际权限没变。
func (r *AccountRepo) AssignRole(ctx context.Context, userID int64, role account.Role, grantedBy int64) error {
	const q = `INSERT INTO user_roles (user_id, role_id, granted_by)
	           SELECT $1, r.id, NULLIF($3, 0) FROM roles r WHERE r.code = $2
	           ON CONFLICT (user_id, role_id) WHERE revoked_at IS NULL DO NOTHING`
	ct, err := r.pool.Exec(ctx, q, userID, string(role), grantedBy)
	if err != nil {
		if isForeignKeyViolation(err) {
			return shared.New("NOT_FOUND", "用户不存在: "+strconv.FormatInt(userID, 10),
				map[string]any{"user_id": userID})
		}
		return fmt.Errorf("绑定角色失败: %w", err)
	}
	if ct.RowsAffected() == 0 && !r.bindingExists(ctx, userID, role) {
		// 两种可能：角色 code 不存在（迁移没跑或被改），或并发下已被别人插入。
		// 后者 bindingExists 会为真，走到这里说明确实没有该角色。
		return shared.New("UNKNOWN_ROLE", "角色未登记: "+string(role),
			map[string]any{"role": string(role)})
	}
	return nil
}

// bindingExists 报告用户是否已有该角色的活跃绑定（用于区分「幂等命中」与「角色不存在」）。
func (r *AccountRepo) bindingExists(ctx context.Context, userID int64, role account.Role) bool {
	const q = `SELECT EXISTS(
	              SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
	               WHERE ur.user_id = $1 AND r.code = $2 AND ur.revoked_at IS NULL)`
	var ok bool
	if err := r.pool.QueryRow(ctx, q, userID, string(role)).Scan(&ok); err != nil {
		return false
	}
	return ok
}

// RevokeRole 解绑（软删：revoked_at 置位，行保留）。
//
// 物理删除会抹掉「谁在什么时候有过什么角色」这段审计事实，而权限恰恰最需要回溯。
func (r *AccountRepo) RevokeRole(ctx context.Context, userID int64, role account.Role) error {
	const q = `UPDATE user_roles ur SET revoked_at = now()
	             FROM roles r
	            WHERE ur.role_id = r.id AND r.code = $2
	              AND ur.user_id = $1 AND ur.revoked_at IS NULL`
	ct, err := r.pool.Exec(ctx, q, userID, string(role))
	if err != nil {
		return fmt.Errorf("解绑角色失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return shared.New("NOT_FOUND", "该用户没有这个活跃角色: "+string(role),
			map[string]any{"user_id": userID, "role": string(role)})
	}
	return nil
}

// CountHolders 统计持有某角色的**不同用户数**（不含已吊销绑定）。
//
// 用 DISTINCT user_id 而不是行数：自锁防护关心的是「还有没有别人能授权」，
// 同一个人有多条历史绑定不算。
func (r *AccountRepo) CountHolders(ctx context.Context, role account.Role) (int, error) {
	const q = `SELECT count(DISTINCT ur.user_id)
	             FROM user_roles ur JOIN roles r ON r.id = ur.role_id
	            WHERE r.code = $1 AND ur.revoked_at IS NULL`
	var n int
	if err := r.pool.QueryRow(ctx, q, string(role)).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计角色持有者失败: %w", err)
	}
	return n, nil
}

// UserExists 报告用户存在且未软删。
func (r *AccountRepo) UserExists(ctx context.Context, userID int64) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL)`
	var ok bool
	if err := r.pool.QueryRow(ctx, q, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("查询用户失败: %w", err)
	}
	return ok, nil
}

// isForeignKeyViolation 判断是否为外键冲突（23503）。
// 在绑定角色时它意味着 user_id 不存在——映射成 404 而不是 500。
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// EnsureUser 按 account 建用户（已存在则复用），返回 user_id 与是否新建。
//
// 为什么放在账号仓储里：这是「首个管理员」初始化的一部分（SPEC-ACCOUNT-001 ASM-01），
// 而本仓库此前没有任何创建用户的入口——users 只能靠 DBA 或禅道导入，
// 于是「装完系统没人能登录」成了默认体验。密码哈希由调用方（platform/authn）生成，
// 本方法只落库，不碰明文。
func (r *AccountRepo) EnsureUser(ctx context.Context, account, passwordHash, realname string) (int64, bool, error) {
	const q = `WITH ins AS (
	               INSERT INTO users (account, password_hash, realname)
	               VALUES ($1, $2, $3)
	               ON CONFLICT (account) WHERE deleted_at IS NULL DO NOTHING
	               RETURNING id
	           )
	           SELECT id, true FROM ins
	            UNION ALL
	           SELECT id, false FROM users WHERE account = $1 AND deleted_at IS NULL
	            LIMIT 1`
	var (
		id      int64
		created bool
	)
	if err := r.pool.QueryRow(ctx, q, account, passwordHash, realname).Scan(&id, &created); err != nil {
		return 0, false, fmt.Errorf("初始化用户失败: %w", err)
	}
	return id, created, nil
}
