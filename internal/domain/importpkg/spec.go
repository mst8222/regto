// Package importpkg 是禅道存量导入器的领域规则层：表清单、外键拓扑序、
// 10 项校验判定。本包是纯逻辑，零外部依赖（SPEC-IMPORT-001）：
//   - 不 import adapter/、platform/、数据库与 HTTP 库；
//   - 表清单与拓扑序是可被表驱动测试穷举的纯数据；
//   - 校验判定是纯函数（比对输入 → PASS/FAIL/WARN）。
//
// DB 编排（连接、游标、记账）在 cmd/import-zentao，不在本包。
package importpkg

// Phase 是导入阶段（REQ-01 三阶段按外键拓扑）。
type Phase string

// 阶段全集。
const (
	PhaseS1   Phase = "s1" // 主数据（users/products/projects/branches/modules）
	PhaseS2   Phase = "s2" // 业务主表（stories/plans/.../test_results）
	PhaseS3   Phase = "s3" // 关联与流水（默认跳过，REQ-07）
	PhaseDone Phase = "done"
)

// Valid 报告阶段是否合法。
func (p Phase) Valid() bool {
	switch p {
	case PhaseS1, PhaseS2, PhaseS3, PhaseDone:
		return true
	}
	return false
}

// TableSpec 描述一张表的导入契约（DESIGN-IMPORT-001 §4）。
type TableSpec struct {
	Source string // 源表名（禅道 zt_ 前缀）
	Target string // 目标表名（Regto）
	Phase  Phase
	// Order 是阶段内拓扑序（外键依赖先写）：projects 按 grade 升序在 S1 内
	// 由专门的排序查询保证，Ordinal 只约束表间顺序。
	Ordinal int
	// KeyColumn 是游标列（全部为 id）。
	KeyColumn string
	// Notes 记录关键映射说明（供 report 与测试引用）。
	Notes string
}

// Tables 是全部导入表清单，顺序即拓扑序（S1 全部先于 S2）。
// 新增表只需在此登记 + 在 cmd/import-zentao 补映射函数。
var Tables = []TableSpec{
	// ---- S1 主数据 ----
	{Source: "zt_user", Target: "users", Phase: PhaseS1, Ordinal: 1, KeyColumn: "id",
		Notes: "password 不导入（REQ-08）；account→user_id 映射的基准表"},
	{Source: "zt_product", Target: "products", Phase: PhaseS1, Ordinal: 2, KeyColumn: "id",
		Notes: "PO→owner_id（MISSING_ACCOUNT 回退 system）"},
	{Source: "zt_project", Target: "projects", Phase: PhaseS1, Ordinal: 3, KeyColumn: "id",
		Notes: "type→kind；按 grade 升序导入保证父先于子（AC-01）"},
	{Source: "zt_branch", Target: "branches", Phase: PhaseS1, Ordinal: 4, KeyColumn: "id",
		Notes: "product→product_id（映射）"},
	{Source: "zt_module", Target: "modules", Phase: PhaseS1, Ordinal: 5, KeyColumn: "id",
		Notes: "type/parent/grade/order 原样；parent 引用同表（grade 升序）"},
	// ---- S2 业务主表 ----
	{Source: "zt_story", Target: "stories", Phase: PhaseS2, Ordinal: 6, KeyColumn: "id",
		Notes: "product/module→映射 ID；openedBy→opened_by_id"},
	{Source: "zt_productplan", Target: "plans", Phase: PhaseS2, Ordinal: 7, KeyColumn: "id",
		Notes: "status 枚举 wait/doing/done/closed 与 Regto 一致"},
	{Source: "zt_planstory", Target: "plan_stories", Phase: PhaseS2, Ordinal: 8, KeyColumn: "id",
		Notes: "plan/story→映射 ID；order 保留"},
	{Source: "zt_release", Target: "releases", Phase: PhaseS2, Ordinal: 9, KeyColumn: "id",
		Notes: "status wait/normal/fail/terminate 与 Regto 一致"},
	{Source: "zt_build", Target: "builds", Phase: PhaseS2, Ordinal: 10, KeyColumn: "id",
		Notes: "builds→构建号；product/execution→映射 ID"},
	{Source: "zt_case", Target: "cases", Phase: PhaseS2, Ordinal: 11, KeyColumn: "id",
		Notes: "type/pri/status 同域；source_ref 留空（业务用例）"},
	{Source: "zt_casestep", Target: "case_steps", Phase: PhaseS2, Ordinal: 12, KeyColumn: "id",
		Notes: "type step/item→step/group；desc/expect→name/expect"},
	{Source: "zt_testsuite", Target: "test_suites", Phase: PhaseS2, Ordinal: 13, KeyColumn: "id",
		Notes: "product→product_id"},
	{Source: "zt_suitecase", Target: "suite_cases", Phase: PhaseS2, Ordinal: 14, KeyColumn: "id",
		Notes: "suite/case→映射 ID；version 快照"},
	{Source: "zt_testtask", Target: "test_tasks", Phase: PhaseS2, Ordinal: 15, KeyColumn: "id",
		Notes: "status 同域；build→build_id 映射"},
	{Source: "zt_testrun", Target: "test_task_cases", Phase: PhaseS2, Ordinal: 16, KeyColumn: "id",
		Notes: "task/case→映射 ID；case_version 快照"},
	{Source: "zt_testresult", Target: "test_results", Phase: PhaseS2, Ordinal: 17, KeyColumn: "id",
		Notes: "caseResult→result；run 号沿用 testrun 语义"},
	// ---- S3 关联与流水（默认跳过，REQ-07） ----
	{Source: "zt_action", Target: "activities", Phase: PhaseS3, Ordinal: 18, KeyColumn: "id",
		Notes: "默认跳过；--with-s3 仅记账不写业务表"},
	{Source: "zt_history", Target: "activity_changes", Phase: PhaseS3, Ordinal: 19, KeyColumn: "id",
		Notes: "默认跳过"},
	{Source: "zt_file", Target: "attachments", Phase: PhaseS3, Ordinal: 20, KeyColumn: "id",
		Notes: "默认跳过"},
	{Source: "zt_relation", Target: "relations", Phase: PhaseS3, Ordinal: 21, KeyColumn: "id",
		Notes: "默认跳过"},
}

// TablesInPhase 返回某阶段按拓扑序排列的表清单（不含 S3，除非显式含）。
func TablesInPhase(p Phase) []TableSpec {
	out := make([]TableSpec, 0, len(Tables))
	for _, t := range Tables {
		if t.Phase == p {
			out = append(out, t)
		}
	}
	return out
}

// FindTable 按源表名查询；不存在返回 false。
func FindTable(source string) (TableSpec, bool) {
	for _, t := range Tables {
		if t.Source == source {
			return t, true
		}
	}
	return TableSpec{}, false
}

// PhaseOrder 返回导入应执行的表（S1 → S2；S3 仅当 includeS3）。
func PhaseOrder(includeS3 bool) []TableSpec {
	out := make([]TableSpec, 0, len(Tables))
	for _, p := range []Phase{PhaseS1, PhaseS2, PhaseS3} {
		if p == PhaseS3 && !includeS3 {
			continue
		}
		out = append(out, TablesInPhase(p)...)
	}
	return out
}

// SystemAccount 是 MISSING_ACCOUNT 回退占位账号（REQ-02）。
const SystemAccount = "system"

// PasswordPlaceholderPrefix 是导入用户的密码占位前缀（REQ-08）。
const PasswordPlaceholderPrefix = "!imported:"

// NewPasswordPlaceholder 生成不可用的密码占位串（任何 bcrypt 比对不可能匹配）。
func NewPasswordPlaceholder(seed string) string {
	return PasswordPlaceholderPrefix + seed
}
