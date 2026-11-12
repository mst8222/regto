package dto

import daccount "github.com/mst8222/regto/internal/domain/account"

// RoleResponse 是角色元数据的对外表示。
type RoleResponse struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Builtin     bool   `json:"builtin"`
}

// RoleFromDomain 把角色元数据转为响应体。
func RoleFromDomain(m daccount.Meta) RoleResponse {
	return RoleResponse{Code: string(m.Code), Name: m.Name, Description: m.Description, Builtin: m.Builtin}
}

// PermissionResponse 是矩阵的一格。
//
// 只暴露 resource / action / allowed：**不暴露 reason**——单格没有判定上下文，
// 带上理由只会让人误以为"这一格因为某事被拒"，实际是整格就是配置。
type PermissionResponse struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Allowed  bool   `json:"allowed"`
}

// PermissionsFromDomain 批量转换。
func PermissionsFromDomain(perms []daccount.Permission) []PermissionResponse {
	out := make([]PermissionResponse, 0, len(perms))
	for _, p := range perms {
		out = append(out, PermissionResponse{
			Resource: string(p.Resource), Action: p.Verb, Allowed: p.Allowed,
		})
	}
	return out
}

// RolePermissionsResponse 是某角色的全部权限格（48 格）。
type RolePermissionsResponse struct {
	Role        RoleResponse         `json:"role"`
	Permissions []PermissionResponse `json:"permissions"`
}

// UserPermissionsResponse 是某用户的有效权限（并集）与角色列表。
type UserPermissionsResponse struct {
	UserID      int64                `json:"user_id"`
	Roles       []string             `json:"roles"`
	Permissions []PermissionResponse `json:"permissions"`
}

// UserRolesResponse 是某用户的角色列表（供「查看自己有什么角色」）。
type UserRolesResponse struct {
	UserID int64    `json:"user_id"`
	Roles  []string `json:"roles"`
}

// BindRoleRequest 是绑定角色的请求体。
type BindRoleRequest struct {
	Role string `json:"role"`
}
