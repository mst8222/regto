package release

import "github.com/mst8222/regto/internal/domain/shared"

// ReleaseStatus 是发布主状态（对应禅道 zt_release.status）。
type ReleaseStatus string

// 状态全集（4 个）。
const (
	ReleaseStatusWait      ReleaseStatus = "wait"      // 未开始
	ReleaseStatusNormal    ReleaseStatus = "normal"    // 已发布
	ReleaseStatusFail      ReleaseStatus = "fail"      // 发布失败
	ReleaseStatusTerminate ReleaseStatus = "terminate" // 停止维护
)

// AllReleaseStatuses 返回全部状态（用于穷举测试）。
func AllReleaseStatuses() []ReleaseStatus {
	return []ReleaseStatus{ReleaseStatusWait, ReleaseStatusNormal, ReleaseStatusFail, ReleaseStatusTerminate}
}

// Valid 报告状态是否合法。
func (s ReleaseStatus) Valid() bool {
	switch s {
	case ReleaseStatusWait, ReleaseStatusNormal, ReleaseStatusFail, ReleaseStatusTerminate:
		return true
	}
	return false
}

// ReleaseAction 是发布状态转移动作。
type ReleaseAction string

// 动作全集。
const (
	ReleaseActionPublish   ReleaseAction = "publish"   // wait|fail → normal
	ReleaseActionPause     ReleaseAction = "pause"     // normal → fail
	ReleaseActionTerminate ReleaseAction = "terminate" // normal → terminate
	ReleaseActionPlay      ReleaseAction = "play"      // terminate → normal
)

// AllReleaseActions 返回全部动作（用于穷举测试）。
func AllReleaseActions() []ReleaseAction {
	return []ReleaseAction{ReleaseActionPublish, ReleaseActionPause, ReleaseActionTerminate, ReleaseActionPlay}
}

// ReleaseRule 描述「从某状态经某动作可到达的目标状态」。
type ReleaseRule struct {
	Target  ReleaseStatus
	SpecRef string
}

// releaseTransitionTable 是显式转移表（DESIGN-RELEASE-001 DEC-05）。
// 忠于禅道 module/release/model.php isClickable + changeStatus 推导的 5 条合法转移；
// 不在表中的 (status, action) 一律拒绝。
var releaseTransitionTable = map[ReleaseStatus]map[ReleaseAction]ReleaseRule{
	ReleaseStatusWait: {
		ReleaseActionPublish: {Target: ReleaseStatusNormal, SpecRef: "SPEC-RELEASE-001#REQ-05"},
	},
	ReleaseStatusFail: {
		ReleaseActionPublish: {Target: ReleaseStatusNormal, SpecRef: "SPEC-RELEASE-001#REQ-05"},
	},
	ReleaseStatusNormal: {
		ReleaseActionPause:     {Target: ReleaseStatusFail, SpecRef: "SPEC-RELEASE-001#REQ-05"},
		ReleaseActionTerminate: {Target: ReleaseStatusTerminate, SpecRef: "SPEC-RELEASE-001#REQ-05"},
	},
	ReleaseStatusTerminate: {
		ReleaseActionPlay: {Target: ReleaseStatusNormal, SpecRef: "SPEC-RELEASE-001#REQ-05"},
	},
}

// lookupReleaseRule 查询发布转移表。
func lookupReleaseRule(from ReleaseStatus, action ReleaseAction) (ReleaseRule, bool) {
	byAction, ok := releaseTransitionTable[from]
	if !ok {
		return ReleaseRule{}, false
	}
	rule, ok := byAction[action]
	return rule, ok
}

// ReleaseCanTransition 报告从 from 状态经 action 转移是否合法（纯查询，无副作用）。
func ReleaseCanTransition(from ReleaseStatus, action ReleaseAction) bool {
	_, ok := lookupReleaseRule(from, action)
	return ok
}

// releaseAllowedActions 返回从 from 出发的全部合法动作（固定顺序）。
func releaseAllowedActions(from ReleaseStatus) []ReleaseAction {
	byAction := releaseTransitionTable[from]
	out := make([]ReleaseAction, 0, len(byAction))
	for _, a := range AllReleaseActions() {
		if _, ok := byAction[a]; ok {
			out = append(out, a)
		}
	}
	return out
}

// ReleaseAllowedTargets 返回从 from 出发的全部合法目标状态（错误 Meta 与 CLI 提示）。
func ReleaseAllowedTargets(from ReleaseStatus) []ReleaseStatus {
	seen := map[ReleaseStatus]bool{}
	out := []ReleaseStatus{}
	for _, a := range AllReleaseActions() {
		if rule, ok := lookupReleaseRule(from, a); ok {
			if !seen[rule.Target] {
				seen[rule.Target] = true
				out = append(out, rule.Target)
			}
		}
	}
	return out
}

// Transition 执行发布状态转移（实体方法内集中校验，零副作用失败）。
func (r *Release) Transition(action ReleaseAction) (ReleaseStatus, error) {
	rule, ok := lookupReleaseRule(r.Status, action)
	if !ok {
		return r.Status, newReleaseTransitionError(r.Status, action)
	}
	r.Status = rule.Target
	return rule.Target, nil
}

// newReleaseTransitionError 构造 INVALID_TRANSITION 错误（Meta 含 from/action/allowed）。
func newReleaseTransitionError(from ReleaseStatus, action ReleaseAction) error {
	return shared.New("INVALID_TRANSITION",
		"cannot apply action "+string(action)+" on release in status "+string(from),
		map[string]any{
			"from":    string(from),
			"action":  string(action),
			"allowed": releaseAllowedActions(from),
		})
}
