// Package story 实现需求（Story）领域：主状态机、阶段机、评审流、版本化规格。
//
// 本包是纯领域逻辑，零外部依赖（NFR-01）：
//   - 禁止 import adapter/、platform/、usecase/、数据库与 HTTP 库；
//   - 全部状态转移由 state.go 的显式转移表驱动（DEC-01），可被表驱动测试穷举；
//   - 每次成功转移返回一条 DomainEvent，由 usecase 落库（DEC-05）。
//
// 规格来源：spec/story/state-machine/SPEC-STORY-001.md（附录 A 转移表）。
package story

// Status 是 Story 主状态（对应禅道 zt_story.status）。
type Status string

// 主状态全集（5 个）。
const (
	StatusDraft     Status = "draft"     // 草稿
	StatusReviewing Status = "reviewing" // 评审中
	StatusActive    Status = "active"    // 激活
	StatusChanging  Status = "changing"  // 变更中
	StatusClosed    Status = "closed"    // 已关闭
)

// AllStatuses 返回全部主状态（用于穷举测试）。
func AllStatuses() []Status {
	return []Status{StatusDraft, StatusReviewing, StatusActive, StatusChanging, StatusClosed}
}

// Valid 报告状态是否合法。
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusReviewing, StatusActive, StatusChanging, StatusClosed:
		return true
	}
	return false
}

// Action 是触发状态转移的动作（对应禅道各 model 方法）。
type Action string

// 动作全集。
const (
	ActionCreate        Action = "create"
	ActionSubmitReview  Action = "submitReview"
	ActionReviewPass    Action = "reviewPass"
	ActionReviewClarify Action = "reviewClarify"
	ActionReviewRevert  Action = "reviewRevert"
	ActionReviewReject  Action = "reviewReject"
	ActionChange        Action = "change"
	ActionClose         Action = "close"
	ActionActivate      Action = "activate"
)

// AllActions 返回全部动作（用于穷举测试）。
func AllActions() []Action {
	return []Action{
		ActionCreate, ActionSubmitReview,
		ActionReviewPass, ActionReviewClarify, ActionReviewRevert, ActionReviewReject,
		ActionChange, ActionClose, ActionActivate,
	}
}

// ActionRule 描述「从某状态经某动作可到达的目标状态集合」。
// Targets 含多个元素表示条件分支（由领域方法按规则选择其一）。
type ActionRule struct {
	Targets []Status
	SpecRef string // 该动作对应的 Spec 条目（NFR-05 可追溯）
}

// transitionTable 是显式转移表（DEC-01）。
// 与 SPEC-STORY-001 附录 A 一一对应；不在表中的 (status, action) 组合一律拒绝。
// create 不在此表：它作用于「尚不存在的对象」，初始状态由 NewStory 计算。
var transitionTable = map[Status]map[Action]ActionRule{
	StatusDraft: {
		ActionSubmitReview: {Targets: []Status{StatusReviewing}, SpecRef: "SPEC-STORY-001#REQ-02"},
		ActionClose:        {Targets: []Status{StatusClosed}, SpecRef: "SPEC-STORY-001#REQ-04"},
	},
	StatusReviewing: {
		ActionReviewPass:    {Targets: []Status{StatusActive}, SpecRef: "SPEC-STORY-001#REQ-02"},
		ActionReviewClarify: {Targets: []Status{StatusChanging, StatusDraft}, SpecRef: "SPEC-STORY-001#REQ-02"},
		ActionReviewRevert:  {Targets: []Status{StatusActive}, SpecRef: "SPEC-STORY-001#REQ-02"},
		ActionReviewReject:  {Targets: []Status{StatusClosed}, SpecRef: "SPEC-STORY-001#REQ-02"},
	},
	StatusActive: {
		ActionChange: {Targets: []Status{StatusChanging}, SpecRef: "SPEC-STORY-001#REQ-03"},
		ActionClose:  {Targets: []Status{StatusClosed}, SpecRef: "SPEC-STORY-001#REQ-04"},
	},
	StatusChanging: {
		ActionSubmitReview: {Targets: []Status{StatusReviewing}, SpecRef: "SPEC-STORY-001#REQ-02"},
		ActionChange:       {Targets: []Status{StatusChanging}, SpecRef: "SPEC-STORY-001#REQ-03"},
		ActionClose:        {Targets: []Status{StatusClosed}, SpecRef: "SPEC-STORY-001#REQ-04"},
	},
	StatusClosed: {
		ActionActivate: {Targets: []Status{StatusActive, StatusReviewing}, SpecRef: "SPEC-STORY-001#REQ-05"},
	},
}

// lookupRule 查询转移表；(rule, ok)。
func lookupRule(from Status, action Action) (ActionRule, bool) {
	byAction, ok := transitionTable[from]
	if !ok {
		return ActionRule{}, false
	}
	rule, ok := byAction[action]
	return rule, ok
}

// allowedActions 返回从 from 状态出发的全部合法动作（用于错误 Meta 与 CLI 提示）。
func allowedActions(from Status) []Action {
	byAction := transitionTable[from]
	out := make([]Action, 0, len(byAction))
	// 固定顺序输出，保证错误信息稳定可测。
	for _, a := range AllActions() {
		if _, ok := byAction[a]; ok {
			out = append(out, a)
		}
	}
	return out
}

// allowedTargets 返回从 from 状态出发的全部合法目标状态。
func allowedTargets(from Status) []Status {
	seen := map[Status]bool{}
	out := []Status{}
	for _, a := range AllActions() {
		if rule, ok := lookupRule(from, a); ok {
			for _, t := range rule.Targets {
				if !seen[t] {
					seen[t] = true
					out = append(out, t)
				}
			}
		}
	}
	return out
}

// ActionSpecRef 返回动作对应的 Spec 条目引用（NFR-05）。
func ActionSpecRef(action Action) string {
	switch action {
	case ActionCreate:
		return "SPEC-STORY-001#REQ-01"
	case ActionSubmitReview, ActionReviewPass, ActionReviewClarify,
		ActionReviewRevert, ActionReviewReject:
		return "SPEC-STORY-001#REQ-02"
	case ActionChange:
		return "SPEC-STORY-001#REQ-03"
	case ActionClose:
		return "SPEC-STORY-001#REQ-04"
	case ActionActivate:
		return "SPEC-STORY-001#REQ-05"
	}
	return ""
}

// StageSpecRef 阶段推进对应的 Spec 条目。
const StageSpecRef = "SPEC-STORY-001#REQ-06"
