// Package audit 定义审计视角报表的注册表与统一返回结构。
//
// 为什么单独一个域而不并进 report（DESIGN-AUDIT-001#DEC-01）：
//   - 数据来源不同：report 读业务表（stories/cases/releases/test_results），
//     audit 只读 traces（留痕），是「关于系统自身的记录」。
//   - 可见性不同：审计数据是运维信息，guest 连 trace 都看不到（矩阵里
//     RoleGuest 的 ResourceTrace 为 0），审计报表必须沿用同一条边界；
//     并入 /api/v1/reports 会让 ACL 只能按路由粗粒度判定，做不出这个区分。
//   - 生命周期不同：业务报表随业务域演进，审计报表随合规要求演进。
//
// 与 report 一样：只读聚合、领域层零 SQL、SQL 在 adapter（DESIGN-AUDIT-001#DEC-03）。
package audit

import "time"

// ID 是审计报表的稳定标识，也是 HTTP 路径参数。
type ID string

// 三张审计报表。id 一旦发布不轻易改（NFR-03）。
const (
	Denied      ID = "audit_denied"
	ApprovalLat ID = "audit_approval_latency"
	TraceGrowth ID = "audit_trace_growth"
)

// Meta 是单张审计报表的注册信息。
type Meta struct {
	ID          ID     `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

var registry = map[ID]Meta{
	Denied: {
		ID:          Denied,
		Title:       "越权拦截",
		Description: "traces(outcome=blocked) 按动作×主体计数，人类与 AI 越权都在此",
		Category:    "权限",
	},
	ApprovalLat: {
		ID:          ApprovalLat,
		Title:       "审批时效",
		Description: "待审批留痕到 approve/reject 决策留痕的时间差；未决的给 pending 行",
		Category:    "审批",
	},
	TraceGrowth: {
		ID:          TraceGrowth,
		Title:       "留痕增长",
		Description: "traces 按天 × actor_type 计数，看留痕链路的增长与主体构成",
		Category:    "留痕",
	},
}

// All 返回所有已注册审计报表的元信息（按 id 字典序，便于稳定输出）。
func All() []Meta {
	out := make([]Meta, 0, len(registry))
	for _, m := range registry {
		out = append(out, m)
	}
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

// Report 是单张审计报表的返回结构（与 dto.AuditReportResponse 一一对应）。
type Report struct {
	ID          ID               `json:"id"`
	Title       string           `json:"title"`
	GeneratedAt time.Time        `json:"generated_at"`
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
}
