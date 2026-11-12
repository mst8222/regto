package story

// Spec 是 Story 的一个规格版本（对应禅道 zt_storyspec）。
// 旧版本不可变：一旦写入聚合，只能整体追加新版本（REQ-03）。
type Spec struct {
	ID          int64
	StoryID     int64
	Version     int
	Title       string
	Spec        string // 需求描述
	Verify      string // 验收标准
	Pri         int
	Estimate    *float64
	CreatedByID int64
	CreatedAt   int64 // unix 毫秒（领域层不依赖 time 包以外的运行时类型）
}

// SpecInput 是创建/变更规格时的输入（DTO 由 adapter 层绑定）。
type SpecInput struct {
	Title    string
	Spec     string
	Verify   string
	Pri      int
	Estimate *float64
}

// IsEmpty 报告规格内容是否为空（title/spec/verify 全部为空视为空，AC：change 空内容拒绝）。
func (in SpecInput) IsEmpty() bool {
	return in.Title == "" && in.Spec == "" && in.Verify == ""
}

// NewStoryParams 创建 Story 的参数。
type NewStoryParams struct {
	ProductID  int64
	ModuleID   *int64
	Title      string
	Type       string // story/requirement/epic，空按 story
	Pri        int
	Estimate   *float64
	Spec       SpecInput
	Reviewers  []int64
	SourceNote string
	AsDraft    bool // true=存草稿；false=直接提交
	OpenedByID int64
	OpenedAtMs int64 // unix 毫秒
}

// initialStatus 计算创建时的初始状态（REQ-01 创建规则）：
//   - 有评审人 → reviewing
//   - 无评审人且草稿 → draft
//   - 无评审人且非草稿 → active
//
// initialStatus 推导新建需求的初始状态。
// 优先级：显式 as_draft > 指定评审人 > 默认激活。
// as_draft 优先的理由：客户端显式声明「存为草稿」时，评审人只是**预设**，
// 不应被自动提交评审——否则 as_draft 参数在带评审人时完全失效（语义不一致）。
func initialStatus(reviewers []int64, asDraft bool) Status {
	if asDraft {
		return StatusDraft
	}
	if len(reviewers) > 0 {
		return StatusReviewing
	}
	return StatusActive
}
