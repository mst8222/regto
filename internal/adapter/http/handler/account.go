package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	daccount "github.com/mst8222/regto/internal/domain/account"
	"github.com/mst8222/regto/internal/domain/shared"
	ucaccount "github.com/mst8222/regto/internal/usecase/account"
)

// AccountHandler 处理角色与用户权限（SPEC-ACCOUNT-001 REQ-05）。
type AccountHandler struct {
	svc    *ucaccount.Service
	logger *slog.Logger
}

// NewAccountHandler 构造处理器。
func NewAccountHandler(svc *ucaccount.Service, logger *slog.Logger) *AccountHandler {
	return &AccountHandler{svc: svc, logger: logger}
}

// ListRoles 处理 GET /api/v1/roles。
func (h *AccountHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	metas, err := h.svc.Roles(r.Context())
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	out := make([]dto.RoleResponse, 0, len(metas))
	for _, m := range metas {
		out = append(out, dto.RoleFromDomain(m))
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// RolePermissions 处理 GET /api/v1/roles/{code}/permissions（48 格）。
//
// 矩阵对外可读（DESIGN-ACCOUNT-001#DEC-07）：它不是秘密，藏起来只会让
// 「我为什么 403」只能靠猜。可见范围由 ACL 的 agent.view 控制（guest 看不到）。
func (h *AccountHandler) RolePermissions(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	role, ok := daccount.ParseRole(code)
	if !ok {
		httputil.WriteProblem(w, r, h.logger,
			shared.New(ucaccount.CodeUnknownRole, "角色未登记: "+code, map[string]any{"role": code}))
		return
	}
	perms, err := h.svc.PermissionsOfRole(r.Context(), role)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	meta, _ := daccount.MetaOf(role)
	httputil.WriteJSON(w, http.StatusOK, dto.RolePermissionsResponse{
		Role:        dto.RoleFromDomain(meta),
		Permissions: dto.PermissionsFromDomain(perms),
	})
}

// UserPermissions 处理 GET /api/v1/users/{id}/permissions。
//
// 行级鉴权在本方法内（ACLRouteExempt）：本人或 admin 才能查。
// 资源粒度的矩阵表达不了「本人」，硬套会让每个普通用户都看不到自己有什么权。
func (h *AccountHandler) UserPermissions(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.authorizeSelfOrAdmin(w, r)
	if !ok {
		return
	}
	roles, perms, err := h.svc.Effective(r.Context(), userID)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.UserPermissionsResponse{
		UserID:      userID,
		Roles:       roleStrings(roles),
		Permissions: dto.PermissionsFromDomain(perms),
	})
}

// UserRoles 处理 GET /api/v1/users/{id}/roles（同样本人或 admin）。
func (h *AccountHandler) UserRoles(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.authorizeSelfOrAdmin(w, r)
	if !ok {
		return
	}
	roles, err := h.svc.RolesOfUser(r.Context(), userID)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.UserRolesResponse{
		UserID: userID, Roles: roleStrings(roles),
	})
}

// BindRole 处理 POST /api/v1/users/{id}/roles（admin 专属，ACL 侧 agent.edit）。
func (h *AccountHandler) BindRole(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUserID(r)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.BindRoleRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	role, ok := daccount.ParseRole(strings.TrimSpace(req.Role))
	if !ok {
		httputil.WriteProblem(w, r, h.logger,
			shared.New(ucaccount.CodeUnknownRole, "角色未登记: "+req.Role,
				map[string]any{"role": req.Role}))
		return
	}
	// 授予人取认证主体，不接受请求体自报（ASM-02）。
	by := httputil.ActorFromContext(r.Context()).ID
	if err := h.svc.Bind(r.Context(), userID, role, by); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	// 201 而不是 200：语义上"绑定关系被创建"。幂等重复绑定也返回 201，
	// 因为对外无法区分（也不需要区分）——重复绑定就是没变化。
	httputil.WriteJSON(w, http.StatusCreated, dto.UserRolesResponse{
		UserID: userID, Roles: h.rolesOf(r.Context(), userID),
	})
}

// UnbindRole 处理 POST /api/v1/users/{id}/roles/{role}/revoke。
func (h *AccountHandler) UnbindRole(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUserID(r)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	code := chi.URLParam(r, "role")
	role, ok := daccount.ParseRole(code)
	if !ok {
		httputil.WriteProblem(w, r, h.logger,
			shared.New(ucaccount.CodeUnknownRole, "角色未登记: "+code, map[string]any{"role": code}))
		return
	}
	if err := h.svc.Unbind(r.Context(), userID, role); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusNoContent, nil)
}

// authorizeSelfOrAdmin 校验「本人或 admin」，并返回目标 user_id。
//
// 这是 ACL 豁免路由的行级鉴权（ACL 只到资源粒度，表达不了「本人」）：
// 查别人有哪些角色属于需要收敛的信息，查自己的则是基本权利。
func (h *AccountHandler) authorizeSelfOrAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	userID, err := pathUserID(r)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return 0, false
	}
	me := httputil.ActorFromContext(r.Context())
	if me.ID == userID {
		return userID, true
	}
	if h.isAdmin(r.Context(), me.ID) {
		return userID, true
	}
	httputil.WriteProblem(w, r, h.logger,
		shared.New("FORBIDDEN", "只能查看自己的权限（或由管理员查看）",
			map[string]any{"user_id": userID}))
	return 0, false
}

// isAdmin 报告该用户当前是否持有 admin 角色。
func (h *AccountHandler) isAdmin(ctx context.Context, userID int64) bool {
	roles, err := h.svc.RolesOfUser(ctx, userID)
	if err != nil {
		return false // 查不到就按「不是 admin」处理：失败方向安全
	}
	for _, role := range roles {
		if role == daccount.RoleAdmin {
			return true
		}
	}
	return false
}

// rolesOf 取用户角色，失败时返回空切片（仅用于响应体回显）。
func (h *AccountHandler) rolesOf(ctx context.Context, userID int64) []string {
	roles, err := h.svc.RolesOfUser(ctx, userID)
	if err != nil {
		return []string{}
	}
	return roleStrings(roles)
}

// pathUserID 解析路径里的 {id}；非正整数按 400 处理。
func pathUserID(r *http.Request) (int64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, shared.New("INVALID_ID", "用户 ID 非法: "+raw, map[string]any{"id": raw})
	}
	return id, nil
}

// roleStrings 把角色集合转成字符串切片（保证空集合序列化为 [] 而不是 null）。
func roleStrings(roles []daccount.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	return out
}
