package account

// 默认权限矩阵——**权限规则的真源**（DESIGN-ACCOUNT-001#DEC-01）。
//
// 为什么不落库：矩阵一旦成为运行时可 UPDATE 的数据，「谁能改权限」就没人说得清。
// 放在代码里有三个直接好处——
//  1. 能被 code review（谁放宽了 product.delete 一目了然）；
//  2. 能被 golden 穷举断言（240 格，AC-01）；
//  3. 每次变更都自带 commit 与 trace，不用另建审计。
//
// 代价是改权限要发版；在 5 角色固定的阶段，这个代价远小于收益。
//
// 掩码简写：v=view c=create e=edit d=delete。
const (
	v = ActionMask(ActionView)
	c = ActionMask(ActionCreate)
	e = ActionMask(ActionEdit)
	d = ActionMask(ActionDelete)

	vc   = v | c
	ve   = v | e
	vce  = v | c | e
	vced = v | c | e | d
)

// defaultMatrix 是 5 角色 × 12 资源 → 动作掩码。
var defaultMatrix = map[Role]map[Resource]ActionMask{
	// 管理员：除物理禁止项（trace.edit/delete）外全权。
	RoleAdmin: {
		ResourceProduct: vced, ResourceProject: vced, ResourceStory: vced,
		ResourceCase: vced, ResourceSuite: vced, ResourceTestTask: vced,
		ResourcePlan: vced, ResourceRelease: vced, ResourceBuild: vced,
		ResourceSpec: vced,
		// 留痕只能追加：edit/delete 在 authorize.go 里被物理约束短路，这里写 vc 保持表格可读。
		ResourceTrace: vc,
		ResourceAgent: vced,
	},

	// 项目经理：产品/需求/计划主导，发布线与用例线可写不可删。
	RolePM: {
		ResourceProduct: vced, ResourceProject: vce, ResourceStory: vced,
		ResourceCase: vce, ResourceSuite: vce, ResourceTestTask: vce,
		ResourcePlan: vced, ResourceRelease: vce, ResourceBuild: vce,
		ResourceSpec: vce, ResourceTrace: v, ResourceAgent: v,
	},

	// 开发：读为主；story 可改不可建不可删（需求由 PM 提），构建与规格草稿可写。
	RoleDev: {
		ResourceProduct: v, ResourceProject: v, ResourceStory: ve,
		ResourceCase: vce, ResourceSuite: vce, ResourceTestTask: ve,
		ResourcePlan: v, ResourceRelease: v, ResourceBuild: vce,
		ResourceSpec: ve, ResourceTrace: vc, ResourceAgent: v,
	},

	// 测试：用例/套件/测试单是 QA 自己的资产，可删（用户 2026-10-26 复核后确认放开）。
	// 需求/计划/发布/构建的删除仍然只给 admin——那些是跨角色的公共资产。其余只读。
	RoleQA: {
		ResourceProduct: v, ResourceProject: v, ResourceStory: v,
		ResourceCase: vced, ResourceSuite: vced, ResourceTestTask: vced,
		ResourcePlan: v, ResourceRelease: v, ResourceBuild: v,
		ResourceSpec: v, ResourceTrace: vc, ResourceAgent: v,
	},

	// 访客：只读，且**看不到**审计流与 AI 会话——那是运维信息。
	RoleGuest: {
		ResourceProduct: v, ResourceProject: v, ResourceStory: v,
		ResourceCase: v, ResourceSuite: v, ResourceTestTask: v,
		ResourcePlan: v, ResourceRelease: v, ResourceBuild: v,
		ResourceSpec: v, ResourceTrace: 0, ResourceAgent: 0,
	},
}

// maskOf 返回某角色在某资源上的动作掩码；未登记一律 0（拒绝）。
//
// 两个「未登记」都会走到这里：未知角色（调用方应先用 IsKnownRole 拦）、
// 以及矩阵漏写了某个资源。后者表现为「新人全都不能用」，属于安全侧的沉默。
func maskOf(r Role, res Resource) ActionMask {
	row, ok := defaultMatrix[r]
	if !ok {
		return 0
	}
	return row[res]
}

// Allows 报告**单个角色**是否可以做某资源上的某动作。
//
// 这是纯矩阵查询，不含物理约束与并集逻辑——授权判定请用 Authorize。
// 单独暴露是为了让 `/roles/{code}/permissions` 能逐格渲染，
// 且 golden 测试可以只测「表」而不掺判定顺序。
func Allows(r Role, res Resource, act Action) bool {
	return maskOf(r, res).Has(act)
}

// Permission 是矩阵的一格（用于查询接口与 DTO）。
type Permission struct {
	Resource Resource `json:"resource"`
	Action   Action   `json:"-"`
	Verb     string   `json:"action"`
	Allowed  bool     `json:"allowed"`
}

// PermissionsOf 展开某角色的 12 × 4 = 48 格（资源主序，动作次序）。
func PermissionsOf(r Role) []Permission {
	out := make([]Permission, 0, len(allResources)*len(allActions))
	for _, res := range allResources {
		for _, act := range allActions {
			out = append(out, Permission{
				Resource: res, Action: act, Verb: act.String(),
				Allowed: Allows(r, res, act),
			})
		}
	}
	return out
}

// EffectivePermissions 展开多角色的**并集**（12 × 4 = 48 格）。
//
// 并集而非交集（DESIGN-ACCOUNT-001#DEC-02）：多给一个角色绝不能导致降权，
// 否则「给 admin 顺手加个 guest 做演示」会静默地把管理员变成只读。
func EffectivePermissions(roles []Role) []Permission {
	out := make([]Permission, 0, len(allResources)*len(allActions))
	for _, res := range allResources {
		for _, act := range allActions {
			allowed := false
			for _, r := range roles {
				if Allows(r, res, act) {
					allowed = true
					break
				}
			}
			out = append(out, Permission{
				Resource: res, Action: act, Verb: act.String(), Allowed: allowed,
			})
		}
	}
	return out
}
