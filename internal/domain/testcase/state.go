package testcase

import "github.com/mst8222/regto/internal/domain/shared"

// Status 是用例主状态（对应禅道 zt_case.status）。
type Status string

// 状态全集（4 个）。
const (
	StatusWait        Status = "wait"        // 草稿
	StatusNormal      Status = "normal"      // 正常
	StatusBlocked     Status = "blocked"     // 阻塞
	StatusInvestigate Status = "investigate" // 待研究
)

// AllStatuses 返回全部状态（用于穷举测试）。
func AllStatuses() []Status {
	return []Status{StatusWait, StatusNormal, StatusBlocked, StatusInvestigate}
}

// Valid 报告状态是否合法。
func (s Status) Valid() bool {
	switch s {
	case StatusWait, StatusNormal, StatusBlocked, StatusInvestigate:
		return true
	}
	return false
}

// Action 是用例状态转移动作。
type Action string

// 动作全集。
const (
	ActionActivate    Action = "activate"    // wait → normal
	ActionBlock       Action = "block"       // normal|investigate → blocked
	ActionInvestigate Action = "investigate" // normal|blocked → investigate
	ActionResume      Action = "resume"      // blocked|investigate → normal
)

// AllActions 返回全部动作（用于穷举测试）。
func AllActions() []Action {
	return []Action{ActionActivate, ActionBlock, ActionInvestigate, ActionResume}
}

// ActionRule 描述「从某状态经某动作可到达的目标状态」。
type ActionRule struct {
	Target  Status
	SpecRef string
}

// transitionTable 是显式转移表（DESIGN-CASE-001 DEC-04）。
// 与 SPEC-CASE-001 REQ-04 一一对应；不在表中的 (status, action) 一律拒绝。
var transitionTable = map[Status]map[Action]ActionRule{
	StatusWait: {
		ActionActivate: {Target: StatusNormal, SpecRef: "SPEC-CASE-001#REQ-04"},
	},
	StatusNormal: {
		ActionBlock:       {Target: StatusBlocked, SpecRef: "SPEC-CASE-001#REQ-04"},
		ActionInvestigate: {Target: StatusInvestigate, SpecRef: "SPEC-CASE-001#REQ-04"},
	},
	StatusBlocked: {
		ActionResume:      {Target: StatusNormal, SpecRef: "SPEC-CASE-001#REQ-04"},
		ActionInvestigate: {Target: StatusInvestigate, SpecRef: "SPEC-CASE-001#REQ-04"},
	},
	StatusInvestigate: {
		ActionResume: {Target: StatusNormal, SpecRef: "SPEC-CASE-001#REQ-04"},
		ActionBlock:  {Target: StatusBlocked, SpecRef: "SPEC-CASE-001#REQ-04"},
	},
}

// lookupRule 查询转移表。
func lookupRule(from Status, action Action) (ActionRule, bool) {
	byAction, ok := transitionTable[from]
	if !ok {
		return ActionRule{}, false
	}
	rule, ok := byAction[action]
	return rule, ok
}

// CanTransition 报告从 from 状态经 action 转移是否合法（纯查询，无副作用）。
func CanTransition(from Status, action Action) bool {
	_, ok := lookupRule(from, action)
	return ok
}

// allowedActions 返回从 from 出发的全部合法动作（固定顺序，错误信息稳定可测）。
func allowedActions(from Status) []Action {
	byAction := transitionTable[from]
	out := make([]Action, 0, len(byAction))
	for _, a := range AllActions() {
		if _, ok := byAction[a]; ok {
			out = append(out, a)
		}
	}
	return out
}

// AllowedTargets 返回从 from 出发的全部合法目标状态（用于错误 Meta 与 CLI 提示）。
func AllowedTargets(from Status) []Status {
	seen := map[Status]bool{}
	out := []Status{}
	for _, a := range AllActions() {
		if rule, ok := lookupRule(from, a); ok {
			if !seen[rule.Target] {
				seen[rule.Target] = true
				out = append(out, rule.Target)
			}
		}
	}
	return out
}

// ActionSpecRef 返回动作对应的 Spec 条目引用（NFR 可追溯）。
func ActionSpecRef(action Action) string {
	for _, from := range AllStatuses() {
		if rule, ok := lookupRule(from, action); ok {
			return rule.SpecRef
		}
	}
	return ""
}

// Transition 执行状态转移（实体方法内集中校验，零副作用失败：NFR-03）。
// 成功返回目标状态；失败返回 ErrInvalidTransition（Meta 含 from/action/allowed）。
// 时间戳与事件由 usecase 统一处理，领域方法不触碰时钟（与 story 域一致）。
func (c *Case) Transition(action Action) (Status, error) {
	rule, ok := lookupRule(c.Status, action)
	if !ok {
		return c.Status, newTransitionError(c.Status, action)
	}
	c.Status = rule.Target
	return rule.Target, nil
}

// newTransitionError 构造 INVALID_TRANSITION 错误（Meta 含 from/action/allowed）。
func newTransitionError(from Status, action Action) error {
	return shared.New("INVALID_TRANSITION",
		"cannot apply action "+string(action)+" on case in status "+string(from),
		map[string]any{
			"from":    string(from),
			"action":  string(action),
			"allowed": allowedActions(from),
		})
}
