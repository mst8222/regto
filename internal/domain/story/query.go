package story

import (
	"fmt"
	"strings"

	"github.com/mst8222/regto/internal/domain/shared"
)

// 排序字段白名单（NFR-01：用户输入只能映射到这些字段，绝不进 SQL 文本）。
const (
	SortCreatedAt = "created_at"
	SortUpdatedAt = "updated_at"
	SortPri       = "pri"
	SortID        = "id"
	SortTitle     = "title"
)

// SortWhitelist 是可排序字段全集（固定顺序，保证错误信息稳定可测）。
var SortWhitelist = []string{SortCreatedAt, SortUpdatedAt, SortPri, SortID, SortTitle}

// MaxKeywordLen 是标题关键字的最大长度（与 stories.title 的 VARCHAR(255) 对齐）。
const MaxKeywordLen = 255

// SortKey 是一个排序键。
type SortKey struct {
	Field string // SortWhitelist 中的字段
	Desc  bool   // true 表示降序
}

// StoryQuery 是列表查询条件。
// 校验放在领域层而非 handler：筛选与排序的合法性是业务规则，
// 脱离 HTTP 也能被单测锁死（DESIGN-STORY-002 DEC-01）。
type StoryQuery struct {
	Page           shared.Page
	ProductID      *int64
	ModuleID       *int64
	Statuses       []Status
	Stages         []Stage
	Types          []string
	AssignedToID   *int64
	OpenedByID     *int64
	Keyword        string
	Sort           []SortKey
	IncludeDeleted bool
}

// StoryListItem 是列表投影（DESIGN-STORY-002 DEC-04：不含规格版本链，避免 N+1）。
type StoryListItem struct {
	ID           int64
	ProductID    int64
	ModuleID     *int64
	Title        string
	Type         string
	Pri          int
	Status       Status
	Stage        Stage
	SpecVersion  int
	ClosedReason string
	AssignedToID *int64
	OpenedByID   int64
	CreatedAtMs  int64
	UpdatedAtMs  int64
	DeletedAtMs  *int64
}

// DefaultSort 返回默认排序（REQ-03：默认 -created_at）。
func DefaultSort() []SortKey {
	return []SortKey{{Field: SortCreatedAt, Desc: true}}
}

// Normalize 补齐默认值并把越界值收敛到允许范围（REQ-01）。
// 与 Validate 的分工：越界是常见客户端行为 → 截断；非法枚举是错误 → 拒绝。
func (q StoryQuery) Normalize() StoryQuery {
	q.Page = q.Page.Normalize()
	if len(q.Sort) == 0 {
		q.Sort = DefaultSort()
	}
	q.Keyword = strings.TrimSpace(q.Keyword)
	if len(q.Keyword) > MaxKeywordLen {
		q.Keyword = q.Keyword[:MaxKeywordLen]
	}
	return q
}

// Validate 校验筛选枚举值与排序字段（AC-03 / AC-04）。
func (q StoryQuery) Validate() error {
	for _, s := range q.Statuses {
		if !s.Valid() {
			return shared.New("INVALID_QUERY", "invalid status filter: "+string(s),
				map[string]any{"field": "status", "value": string(s), "allowed": statusStrings()})
		}
	}
	for _, s := range q.Stages {
		if !s.Valid() {
			return shared.New("INVALID_QUERY", "invalid stage filter: "+string(s),
				map[string]any{"field": "stage", "value": string(s), "allowed": stageStrings()})
		}
	}
	for _, f := range q.Sort {
		if !sortAllowed(f.Field) {
			return shared.New("INVALID_QUERY", fmt.Sprintf("invalid sort field: %s", f.Field),
				map[string]any{"field": "sort", "value": f.Field, "allowed": SortWhitelist})
		}
	}
	return nil
}

// HasFilter 报告是否带有任何筛选条件（用于测试与日志）。
func (q StoryQuery) HasFilter() bool {
	return q.ProductID != nil || q.ModuleID != nil || len(q.Statuses) > 0 ||
		len(q.Stages) > 0 || len(q.Types) > 0 || q.AssignedToID != nil ||
		q.OpenedByID != nil || q.Keyword != ""
}

// sortAllowed 判断排序字段是否在白名单内。
func sortAllowed(field string) bool {
	for _, f := range SortWhitelist {
		if f == field {
			return true
		}
	}
	return false
}

// statusStrings 返回合法状态字符串集合（错误信息 Meta 用，顺序固定）。
func statusStrings() []string {
	all := AllStatuses()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

// stageStrings 返回合法阶段字符串集合（错误信息 Meta 用，顺序固定）。
func stageStrings() []string {
	all := AllStages()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}
