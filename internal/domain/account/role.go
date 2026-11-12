package account

// Role 是人类主体的角色（SPEC-ACCOUNT-001 REQ-01）。
//
// 角色是「权限集合的命名打包」，不是组织架构。部门（禅道 dept）与角色正交，
// 本期不做组织层级：一个人有什么权，只取决于他被授予了哪些角色。
type Role string

// 5 个内置角色。顺序即文档表格与 golden 的顺序。
const (
	RoleAdmin Role = "admin" // 管理员：除物理禁止项外全权
	RolePM    Role = "pm"    // 项目经理：产品/需求/计划/发布线的主导者
	RoleDev   Role = "dev"   // 开发：读为主，写用例/构建/规格草稿
	RoleQA    Role = "qa"    // 测试：用例与测试单的拥有者
	RoleGuest Role = "guest" // 访客：只读，且看不到审计流与会话
)

// allRoles 是稳定顺序的角色清单。
var allRoles = []Role{RoleAdmin, RolePM, RoleDev, RoleQA, RoleGuest}

// AllRoles 返回 5 个角色（副本）。
func AllRoles() []Role {
	out := make([]Role, len(allRoles))
	copy(out, allRoles)
	return out
}

// IsKnownRole 报告角色是否已登记。
func IsKnownRole(r Role) bool {
	switch r {
	case RoleAdmin, RolePM, RoleDev, RoleQA, RoleGuest:
		return true
	}
	return false
}

// ParseRole 把线上字符串解析为角色；非法值返回 ok=false。
func ParseRole(s string) (Role, bool) {
	r := Role(s)
	if !IsKnownRole(r) {
		return "", false
	}
	return r, true
}

// String 让角色可直接当字符串用。
func (r Role) String() string { return string(r) }

// Meta 是角色的展示元数据（落库于 roles 表，迁移 000022 种子）。
//
// 权限矩阵不落库（DESIGN-ACCOUNT-001#DEC-01），但角色的「名字」要落库：
// 前端、CLI、审计日志都要一个稳定的人类可读标签，且它可能被本地化。
type Meta struct {
	Code        Role
	Name        string
	Description string
	Builtin     bool
}

// Metas 返回 5 个角色的元数据（与迁移 000022 的种子保持一致）。
func Metas() []Meta {
	return []Meta{
		{RoleAdmin, "管理员", "除物理禁止项外全权；唯一可解绑他人角色的角色", true},
		{RolePM, "项目经理", "产品、需求、计划、发布线的主导者", true},
		{RoleDev, "开发", "读为主；可写用例、套件、构建与规格草稿", true},
		{RoleQA, "测试", "用例与测试单的拥有者，可删自己的资产", true},
		{RoleGuest, "访客", "只读；看不到审计流与 AI 会话", true},
	}
}

// MetaOf 返回指定角色的元数据。
func MetaOf(r Role) (Meta, bool) {
	for _, m := range Metas() {
		if m.Code == r {
			return m, true
		}
	}
	return Meta{}, false
}
