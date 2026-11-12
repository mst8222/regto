package account

// Resource 是权限判定的对象类别（SPEC-ACCOUNT-001 REQ-01）。
//
// 每个资源对应一组真实存在的表/域，不发明概念：12 个资源与 AGENTS.md §2 的
// 域划分一一对齐。之所以要「固定 12 个」而不是按路由自动生成，
// 是因为 ACL 的粒度必须比路由稳定——路由会为了 HTTP 语义拆拆合合
// （/cases/import-ztf 与 /cases 都是「对用例资源的写」），权限不该跟着抖。
type Resource string

// 12 个资源。顺序即 golden 与文档表格的顺序，调整顺序会让 golden 全红，勿随意改。
//
// 注意 task / bug 不在此列：它们是 stories.type 的枚举值（ROADMAP §2），
// 与 story 共用一张表与一套权限。给它们单列资源只会让矩阵虚胖。
const (
	ResourceProduct  Resource = "product"  // 产品（含分支概念，本期不细分）
	ResourceProject  Resource = "project"  // 项目/迭代（projects.kind 区分）
	ResourceStory    Resource = "story"    // 需求（含 task/bug 两种 type）
	ResourceCase     Resource = "case"     // 用例
	ResourceSuite    Resource = "suite"    // 用例套件
	ResourceTestTask Resource = "testtask" // 测试单与执行结果
	ResourcePlan     Resource = "plan"     // 产品计划
	ResourceRelease  Resource = "release"  // 发布
	ResourceBuild    Resource = "build"    // 构建
	ResourceSpec     Resource = "spec"     // 规格文档（SpecDD）
	ResourceTrace    Resource = "trace"    // 留痕（edit/delete 物理禁止，见 authorize.go）
	ResourceAgent    Resource = "agent"    // AI 会话（能力白名单的载体）
)

// allResources 是稳定顺序的资源清单（枚举与 golden 依赖它）。
var allResources = []Resource{
	ResourceProduct, ResourceProject, ResourceStory, ResourceCase,
	ResourceSuite, ResourceTestTask, ResourcePlan, ResourceRelease,
	ResourceBuild, ResourceSpec, ResourceTrace, ResourceAgent,
}

// AllResources 返回 12 个资源（副本，调用方可安全修改返回值）。
func AllResources() []Resource {
	out := make([]Resource, len(allResources))
	copy(out, allResources)
	return out
}

// IsKnownResource 报告资源是否已登记。
//
// 未登记的资源必须被拒绝而不是默认放行：新增业务域时忘了登记矩阵，
// 表现应该是「新人全都不能用」（立刻有人来问），而不是「谁都能用」（没人发现）。
func IsKnownResource(r Resource) bool {
	switch r {
	case ResourceProduct, ResourceProject, ResourceStory, ResourceCase,
		ResourceSuite, ResourceTestTask, ResourcePlan, ResourceRelease,
		ResourceBuild, ResourceSpec, ResourceTrace, ResourceAgent:
		return true
	}
	return false
}

// String 让资源可直接当字符串用（日志、DTO、SQL 参数）。
func (r Resource) String() string { return string(r) }
