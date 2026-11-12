// Package report 定义固定报表的注册表与统一返回结构。
//
// 设计取舍（DESIGN-REPORT-001#DEC-01/#DEC-02）：
//   - 报表是只读聚合，不进业务写路径，所以这里只放「注册表 + 结构」，不放 SQL；
//     SQL 在 adapter/persistence 层（符合领域层零外部依赖的纪律）。
//   - 六张报表形状各异，统一用 []map[string]any 承载行，避免 6 套强类型 DTO 撑爆 handler。
package report

import "time"

// ID 是报表的稳定标识，也是 HTTP 路径与 CLI 的参数。
type ID string

// 六张固定报表。id 一旦发布不轻易改（NFR-03）。
const (
	StoryStatus        ID = "story_status"
	StoryBurndown      ID = "story_burndown"
	CaseCoverage       ID = "case_coverage"
	ReleaseQuality     ID = "release_quality"
	AIContribution     ID = "ai_contribution"
	TestExecutionTrend ID = "test_execution_trend"
)

// Meta 是单张报表的注册信息。
type Meta struct {
	ID          ID     `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	// Scoped 表示是否接受 product_id 过滤；false 的报表（如 ai_contribution）忽略该参数。
	Scoped bool `json:"scoped"`
}

var registry = map[ID]Meta{
	StoryStatus: {
		ID: StoryStatus, Title: "需求状态分布",
		Description: "stories 按 status 计数（软删除外）",
		Category:    "需求", Scoped: true,
	},
	StoryBurndown: {
		ID: StoryBurndown, Title: "需求吞吐/燃尽",
		Description: "按月统计 opened（opened_at）与 closed（closed_at）",
		Category:    "需求", Scoped: true,
	},
	CaseCoverage: {
		ID: CaseCoverage, Title: "用例覆盖率",
		Description: "cases 按 status 计数",
		Category:    "测试", Scoped: true,
	},
	ReleaseQuality: {
		ID: ReleaseQuality, Title: "发布质量",
		Description: "每个 release 的 story/bug/build 快照计数 + 按 status 汇总",
		Category:    "发布", Scoped: true,
	},
	AIContribution: {
		ID: AIContribution, Title: "AI 贡献度",
		Description: "traces(actor_type=ai) 按 actor_id/action 计数；test_results 按 actor_type 计数（全局）",
		Category:    "AI", Scoped: false,
	},
	TestExecutionTrend: {
		ID: TestExecutionTrend, Title: "测试执行趋势",
		Description: "test_results 按月 × result 计数",
		Category:    "测试", Scoped: true,
	},
}

// All 返回所有已注册报表的元信息（按 id 字典序，便于稳定输出）。
func All() []Meta {
	out := make([]Meta, 0, len(registry))
	for _, m := range registry {
		out = append(out, m)
	}
	// 字典序：手写稳定排序，避免依赖 sort 包的原始顺序不确定性。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ID > out[j].ID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Get 取单张报表元信息；未知 id 返回 ok=false。
func Get(id ID) (Meta, bool) {
	m, ok := registry[id]
	return m, ok
}

// IsValid 报告 id 是否在注册表中。
func IsValid(id string) bool {
	_, ok := registry[ID(id)]
	return ok
}

// Report 是单张报表的返回结构（与 dto.ReportResponse 一一对应）。
//
// Columns 是该报表稳定的列顺序（NFR-03 的契约）；Rows 每行是对象，键集合 ⊆ Columns
// （release_quality / ai_contribution 行含 dimension 区分，键会多于基础的计数键，属设计内）。
type Report struct {
	ID          ID               `json:"id"`
	Title       string           `json:"title"`
	GeneratedAt time.Time        `json:"generated_at"`
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
}
