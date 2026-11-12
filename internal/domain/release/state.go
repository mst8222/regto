package release

import "github.com/mst8222/regto/internal/domain/shared"

// PlanStatus 是产品计划主状态（对应禅道 zt_productplan.status）。
type PlanStatus string

// 状态全集（4 个）。
const (
	PlanStatusWait   PlanStatus = "wait"   // 未开始
	PlanStatusDoing  PlanStatus = "doing"  // 进行中
	PlanStatusDone   PlanStatus = "done"   // 已完成
	PlanStatusClosed PlanStatus = "closed" // 已关闭
)

// AllPlanStatuses 返回全部状态（用于穷举测试）。
func AllPlanStatuses() []PlanStatus {
	return []PlanStatus{PlanStatusWait, PlanStatusDoing, PlanStatusDone, PlanStatusClosed}
}

// Valid 报告状态是否合法。
func (s PlanStatus) Valid() bool {
	switch s {
	case PlanStatusWait, PlanStatusDoing, PlanStatusDone, PlanStatusClosed:
		return true
	}
	return false
}

// PlanAction 是计划状态转移动作。
type PlanAction string

// 动作全集。
const (
	PlanActionActivate PlanAction = "activate" // done|closed → wait
	PlanActionStart    PlanAction = "start"    // wait → doing
	PlanActionFinish   PlanAction = "finish"   // doing → done
	PlanActionClose    PlanAction = "close"    // wait|doing|done → closed
)

// AllPlanActions 返回全部动作（用于穷举测试）。
func AllPlanActions() []PlanAction {
	return []PlanAction{PlanActionActivate, PlanActionStart, PlanActionFinish, PlanActionClose}
}

// ActionRule 描述「从某状态经某动作可到达的目标状态」。
type ActionRule struct {
	Target  PlanStatus
	SpecRef string
}

// planTransitionTable 是显式转移表（DESIGN-RELEASE-001 DEC-02）。
// 忠于禅道 module/productplan/model.php isClickable 推导的 7 条合法转移；
// 不在表中的 (status, action) 一律拒绝。
var planTransitionTable = map[PlanStatus]map[PlanAction]ActionRule{
	PlanStatusWait: {
		PlanActionStart: {Target: PlanStatusDoing, SpecRef: "SPEC-RELEASE-001#REQ-02"},
		PlanActionClose: {Target: PlanStatusClosed, SpecRef: "SPEC-RELEASE-001#REQ-02"},
	},
	PlanStatusDoing: {
		PlanActionFinish: {Target: PlanStatusDone, SpecRef: "SPEC-RELEASE-001#REQ-02"},
		PlanActionClose:  {Target: PlanStatusClosed, SpecRef: "SPEC-RELEASE-001#REQ-02"},
	},
	PlanStatusDone: {
		PlanActionClose:    {Target: PlanStatusClosed, SpecRef: "SPEC-RELEASE-001#REQ-02"},
		PlanActionActivate: {Target: PlanStatusWait, SpecRef: "SPEC-RELEASE-001#REQ-02"},
	},
	PlanStatusClosed: {
		PlanActionActivate: {Target: PlanStatusWait, SpecRef: "SPEC-RELEASE-001#REQ-02"},
	},
}

// lookupPlanRule 查询计划转移表。
func lookupPlanRule(from PlanStatus, action PlanAction) (ActionRule, bool) {
	byAction, ok := planTransitionTable[from]
	if !ok {
		return ActionRule{}, false
	}
	rule, ok := byAction[action]
	return rule, ok
}

// PlanCanTransition 报告从 from 状态经 action 转移是否合法（纯查询，无副作用）。
func PlanCanTransition(from PlanStatus, action PlanAction) bool {
	_, ok := lookupPlanRule(from, action)
	return ok
}

// planAllowedActions 返回从 from 出发的全部合法动作（固定顺序，错误信息稳定可测）。
func planAllowedActions(from PlanStatus) []PlanAction {
	byAction := planTransitionTable[from]
	out := make([]PlanAction, 0, len(byAction))
	for _, a := range AllPlanActions() {
		if _, ok := byAction[a]; ok {
			out = append(out, a)
		}
	}
	return out
}

// PlanAllowedTargets 返回从 from 出发的全部合法目标状态（用于错误 Meta 与 CLI 提示）。
func PlanAllowedTargets(from PlanStatus) []PlanStatus {
	seen := map[PlanStatus]bool{}
	out := []PlanStatus{}
	for _, a := range AllPlanActions() {
		if rule, ok := lookupPlanRule(from, a); ok {
			if !seen[rule.Target] {
				seen[rule.Target] = true
				out = append(out, rule.Target)
			}
		}
	}
	return out
}

// Transition 执行计划状态转移（实体方法内集中校验，零副作用失败）。
// 成功返回目标状态；失败返回 INVALID_TRANSITION（Meta 含 from/action/allowed）。
// 时间戳与事件由 usecase 统一处理，领域方法不触碰时钟（与既有域一致）。
func (p *Plan) Transition(action PlanAction) (PlanStatus, error) {
	rule, ok := lookupPlanRule(p.Status, action)
	if !ok {
		return p.Status, newPlanTransitionError(p.Status, action)
	}
	p.Status = rule.Target
	return rule.Target, nil
}

// newPlanTransitionError 构造 INVALID_TRANSITION 错误（Meta 含 from/action/allowed）。
func newPlanTransitionError(from PlanStatus, action PlanAction) error {
	return shared.New("INVALID_TRANSITION",
		"cannot apply action "+string(action)+" on plan in status "+string(from),
		map[string]any{
			"from":    string(from),
			"action":  string(action),
			"allowed": planAllowedActions(from),
		})
}
