package account

// Action 是对资源的四类操作（SPEC-ACCOUNT-001 REQ-01）。
//
// 为什么只有 4 个而不是跟着 HTTP 方法走：`PATCH /stories/{id}` 与
// `POST /stories/{id}/transitions` 在业务上都是「改这条需求」，
// 权限若按方法区分，就会出现「能改标题但不能推进状态」这种没人想维护的组合。
// 动作是业务语义，不是传输语义。
type Action uint8

// 四个动作，位掩码表示（一个角色的一个资源可以同时允许多种动作）。
const (
	ActionView Action = 1 << iota
	ActionCreate
	ActionEdit
	ActionDelete
)

// ActionMask 是若干动作的集合。
type ActionMask uint8

// allActions 是稳定顺序的动作清单（枚举与 golden 依赖它）。
var allActions = []Action{ActionView, ActionCreate, ActionEdit, ActionDelete}

// AllActions 返回 4 个动作（副本）。
func AllActions() []Action {
	out := make([]Action, len(allActions))
	copy(out, allActions)
	return out
}

// String 返回动作的线上表示（view/create/edit/delete）。
func (a Action) String() string {
	switch a {
	case ActionView:
		return "view"
	case ActionCreate:
		return "create"
	case ActionEdit:
		return "edit"
	case ActionDelete:
		return "delete"
	}
	return "unknown"
}

// IsKnownAction 报告动作是否已登记。
func IsKnownAction(a Action) bool {
	return a == ActionView || a == ActionCreate || a == ActionEdit || a == ActionDelete
}

// ParseAction 把线上字符串解析为动作；非法值返回 ok=false。
//
// 不区分大小写会掩盖拼写错误（"Delete" 与 "delete" 混用），首尾空格由调用方
// 负责 trim——这里保持严格，与 agent 的动作解析一致。
func ParseAction(s string) (Action, bool) {
	switch s {
	case "view":
		return ActionView, true
	case "create":
		return ActionCreate, true
	case "edit":
		return ActionEdit, true
	case "delete":
		return ActionDelete, true
	}
	return 0, false
}

// Has 报告掩码是否包含某动作。
func (m ActionMask) Has(a Action) bool { return m&ActionMask(a) != 0 }

// Actions 把掩码展开成动作列表（按 ActionView..ActionDelete 的固定顺序）。
func (m ActionMask) Actions() []Action {
	var out []Action
	for _, a := range allActions {
		if m.Has(a) {
			out = append(out, a)
		}
	}
	return out
}

// IsEmpty 报告掩码是否没有任何动作。
func (m ActionMask) IsEmpty() bool { return m == 0 }
