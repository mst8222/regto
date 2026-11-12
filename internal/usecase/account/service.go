package account

import (
	"context"

	daccount "github.com/mst8222/regto/internal/domain/account"
	"github.com/mst8222/regto/internal/domain/shared"
)

// 账号服务的错误码（HTTP 层映射：UNKNOWN_ROLE/LAST_ADMIN → 422，NOT_FOUND → 404）。
const (
	CodeUnknownRole = "UNKNOWN_ROLE" // 角色 code 未登记（拼错，或迁移没跑）
	CodeLastAdmin   = "LAST_ADMIN"   // 自锁防护：不能解绑最后一个管理员
	CodeInvalidUser = "INVALID_USER" // user_id 非法（<= 0）
)

// Repo 是账号服务需要的持久化能力；由 postgres.AccountRepo 实现。
type Repo interface {
	ListRoles(ctx context.Context) ([]daccount.Meta, error)
	RolesOfUser(ctx context.Context, userID int64) ([]daccount.Role, error)
	AssignRole(ctx context.Context, userID int64, role daccount.Role, grantedBy int64) error
	RevokeRole(ctx context.Context, userID int64, role daccount.Role) error
	CountHolders(ctx context.Context, role daccount.Role) (int, error)
	UserExists(ctx context.Context, userID int64) (bool, error)
}

// Service 处理角色绑定与「这个人能做什么」的判定。
//
// 权限边界不在本服务里（它不判断调用者是谁）：角色绑定是 human_only 动作，
// 由 HTTP 守卫拦 AI（actionmap 里登记为 agent.session_manage 同档）。
// 本服务只保证绑定关系本身合法——两条防线缺一不可（与 SessionService 同理）。
type Service struct {
	repo Repo
}

// NewService 构造账号服务。
func NewService(repo Repo) *Service { return &Service{repo: repo} }

// Roles 返回 5 个角色的元数据（落库版本，可能与代码 Metas 存在本地化差异）。
func (s *Service) Roles(ctx context.Context) ([]daccount.Meta, error) {
	return s.repo.ListRoles(ctx)
}

// RolesOfUser 返回用户当前活跃的角色。
func (s *Service) RolesOfUser(ctx context.Context, userID int64) ([]daccount.Role, error) {
	if userID <= 0 {
		return nil, shared.New(CodeInvalidUser, "user_id 非法", map[string]any{"user_id": userID})
	}
	return s.repo.RolesOfUser(ctx, userID)
}

// Decision 判定某个用户能否对某资源执行某动作（ACL 中间件唯一入口）。
//
// 用户没有角色不是错误，而是一个明确的拒绝（NO_ROLE）——调用方据此返回 403
// 并给出可读理由。只有「查库失败」才返回 error，此时中间件必须拒绝
// （fail-closed）：无法证明有授权就不放行。
func (s *Service) Decision(ctx context.Context, userID int64, res daccount.Resource, act daccount.Action) (daccount.Decision, error) {
	if userID <= 0 {
		return daccount.Denied(daccount.ReasonNoRole), nil
	}
	roles, err := s.repo.RolesOfUser(ctx, userID)
	if err != nil {
		return daccount.Decision{}, err
	}
	return daccount.Authorize(roles, res, act), nil
}

// PermissionsOfRole 展开某角色的 48 格（供 /roles/{code}/permissions）。
func (s *Service) PermissionsOfRole(_ context.Context, role daccount.Role) ([]daccount.Permission, error) {
	if !daccount.IsKnownRole(role) {
		return nil, shared.New(CodeUnknownRole, "角色未登记: "+string(role),
			map[string]any{"role": string(role)})
	}
	return daccount.PermissionsOf(role), nil
}

// Effective 返回用户的有效权限：角色列表 + 48 格并集（供 /users/{id}/permissions）。
func (s *Service) Effective(ctx context.Context, userID int64) ([]daccount.Role, []daccount.Permission, error) {
	roles, err := s.RolesOfUser(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return roles, daccount.EffectivePermissions(roles), nil
}

// Bind 绑定角色（幂等）。unknown role / 用户不存在都会被拒绝，而不是静默插入 0 行。
func (s *Service) Bind(ctx context.Context, userID int64, role daccount.Role, grantedBy int64) error {
	if userID <= 0 {
		return shared.New(CodeInvalidUser, "user_id 非法", map[string]any{"user_id": userID})
	}
	if !daccount.IsKnownRole(role) {
		return shared.New(CodeUnknownRole, "角色未登记: "+string(role),
			map[string]any{"role": string(role)})
	}
	// 先确认用户存在且未软删：user_roles 的外键只保证行存在，
	// 已软删的用户仍能通过 FK，业务上不该再被授权。
	ok, err := s.repo.UserExists(ctx, userID)
	if err != nil {
		return err
	}
	if !ok {
		return shared.New("NOT_FOUND", "用户不存在或已删除", map[string]any{"user_id": userID})
	}
	return s.repo.AssignRole(ctx, userID, role, grantedBy)
}

// Unbind 解绑角色（软删），并阻止把最后一个管理员解绑。
//
// 自锁是权限系统最现实的可用性故障：不是被攻破，而是「没人能授权了」。
// 在应用层拦掉这一步，比事后教人用 DBA 手工 INSERT 恢复便宜得多。
func (s *Service) Unbind(ctx context.Context, userID int64, role daccount.Role) error {
	if userID <= 0 {
		return shared.New(CodeInvalidUser, "user_id 非法", map[string]any{"user_id": userID})
	}
	if !daccount.IsKnownRole(role) {
		return shared.New(CodeUnknownRole, "角色未登记: "+string(role),
			map[string]any{"role": string(role)})
	}

	// 先确认该用户确实持有这个角色：否则「解绑一个不存在的绑定」会被误报成
	// LAST_ADMIN（因为库里只有 1 个 admin 时，任何解绑请求都会撞上那条判断）。
	roles, err := s.repo.RolesOfUser(ctx, userID)
	if err != nil {
		return err
	}
	if !containsRole(roles, role) {
		return shared.New("NOT_FOUND", "该用户没有这个活跃角色: "+string(role),
			map[string]any{"user_id": userID, "role": string(role)})
	}

	if role == daccount.RoleAdmin {
		n, err := s.repo.CountHolders(ctx, daccount.RoleAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return shared.New(CodeLastAdmin,
				"不能解绑最后一个管理员：解绑后将无人能授权",
				map[string]any{"user_id": userID, "role": string(role)})
		}
	}
	return s.repo.RevokeRole(ctx, userID, role)
}

// containsRole 判断角色集合里是否有某角色。
func containsRole(roles []daccount.Role, want daccount.Role) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}
