package shared

// Page 描述一次分页查询的参数与结果。
type Page struct {
	Page    int   `json:"page"`     // 页码，从 1 开始
	PerPage int   `json:"per_page"` // 每页条数
	Total   int64 `json:"total"`    // 总条数
	HasMore bool  `json:"has_more"` // 是否有下一页
}

// DefaultPerPage 与 MaxPerPage 是分页边界（AGENTS.md §3.4）。
const (
	DefaultPerPage = 50
	MaxPerPage     = 200
)

// Normalize 规范化分页参数：非法值回落默认，超上限截断。
func (p Page) Normalize() Page {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PerPage < 1 {
		p.PerPage = DefaultPerPage
	}
	if p.PerPage > MaxPerPage {
		p.PerPage = MaxPerPage
	}
	return p
}

// Offset 计算 SQL OFFSET。
func (p Page) Offset() int { return (p.Page - 1) * p.PerPage }

// ObjectRef 是跨领域对象引用（对应禅道 zt_relation 的 AType/AID 语义）。
type ObjectRef struct {
	Type string // story/task/bug/case/release/spec/...
	ID   string
}

// String 返回 "type:id" 形式，用于日志与 trace 关联。
func (r ObjectRef) String() string { return r.Type + ":" + r.ID }
