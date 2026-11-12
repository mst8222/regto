// Package testtask 实现测试执行域：测试单/执行/结果与 go test -json 解析。
//
// 本包是纯领域逻辑，零外部依赖（SPEC-TESTTASK-001 NFR-02）：
//   - 状态转移由 state.go 显式转移表驱动（DEC-01）；
//   - go test -json 解析是纯函数（gotest.go），聚合规则见 REQ-04。
//
// 规格来源：spec/testtask/execution/SPEC-TESTTASK-001.md。
package testtask

import "github.com/mst8222/regto/internal/domain/shared"

// Status 是测试单状态（对应禅道 zt_testtask.status）。
type Status string

// 状态全集（4 个）。
const (
	StatusWait    Status = "wait"    // 待执行
	StatusDoing   Status = "doing"   // 执行中
	StatusDone    Status = "done"    // 已完成
	StatusBlocked Status = "blocked" // 阻塞
)

// AllStatuses 返回全部状态（用于穷举测试）。
func AllStatuses() []Status {
	return []Status{StatusWait, StatusDoing, StatusDone, StatusBlocked}
}

// Valid 报告状态是否合法。
func (s Status) Valid() bool {
	switch s {
	case StatusWait, StatusDoing, StatusDone, StatusBlocked:
		return true
	}
	return false
}

// Action 是测试单状态转移动作。
type Action string

// 动作全集。
const (
	ActionStart  Action = "start"  // wait|blocked → doing
	ActionFinish Action = "finish" // doing|blocked → done
	ActionBlock  Action = "block"  // wait|doing → blocked
)

// AllActions 返回全部动作（用于穷举测试）。
func AllActions() []Action {
	return []Action{ActionStart, ActionFinish, ActionBlock}
}

// transitionTable 是显式转移表（DESIGN-TESTTASK-001 DEC-01）。
// done 是终态：无出边（阻塞后可完成，完成后不可再动）。
var transitionTable = map[Status]map[Action]Status{
	StatusWait: {
		ActionStart: StatusDoing,
		ActionBlock: StatusBlocked,
	},
	StatusDoing: {
		ActionFinish: StatusDone,
		ActionBlock:  StatusBlocked,
	},
	StatusBlocked: {
		ActionStart:  StatusDoing,
		ActionFinish: StatusDone,
	},
	// StatusDone：终态，无转移
}

// CanTransition 报告转移是否合法（纯查询）。
func CanTransition(from Status, action Action) bool {
	_, ok := transitionTable[from][action]
	return ok
}

// allowedActions 返回从 from 出发的合法动作（固定顺序，错误信息稳定可测）。
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

// AllowedTargets 返回从 from 出发的合法目标状态（错误 Meta 与 CLI 提示用）。
func AllowedTargets(from Status) []Status {
	seen := map[Status]bool{}
	out := []Status{}
	for _, a := range AllActions() {
		if t, ok := transitionTable[from][a]; ok {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// ActionSpecRef 返回动作对应的 Spec 条目引用。
func ActionSpecRef(action Action) string {
	if action == ActionStart || action == ActionBlock || action == ActionFinish {
		return "SPEC-TESTTASK-001#REQ-01"
	}
	return ""
}

// newTransitionError 构造 INVALID_TRANSITION 错误（Meta 含 from/action/allowed）。
func newTransitionError(from Status, action Action) error {
	return shared.New("INVALID_TRANSITION",
		"cannot apply action "+string(action)+" on test task in status "+string(from),
		map[string]any{
			"from":    string(from),
			"action":  string(action),
			"allowed": allowedActions(from),
		})
}

// Transition 执行状态转移（零副作用失败）。
func (t *TestTask) Transition(action Action) (Status, error) {
	target, ok := transitionTable[t.Status][action]
	if !ok {
		return t.Status, newTransitionError(t.Status, action)
	}
	t.Status = target
	return target, nil
}

// CaseRunStatus 是用例在测试单中的执行状态（zt_testrun.status 简化）。
type CaseRunStatus string

// 执行状态全集。
const (
	CaseWait    CaseRunStatus = "wait"
	CaseRunning CaseRunStatus = "running"
	CaseDone    CaseRunStatus = "done"
)

// Result 是执行结果（对应禅道 zt_testresult.caseResult）。
type Result string

// 结果全集（5 个，与禅道一致）。
const (
	ResultPass    Result = "pass"
	ResultFail    Result = "fail"
	ResultBlocked Result = "blocked"
	ResultSkip    Result = "skip"
	ResultNotApp  Result = "n/a"
)

// AllResults 返回全部结果（用于穷举测试与 CLI 提示）。
func AllResults() []Result {
	return []Result{ResultPass, ResultFail, ResultBlocked, ResultSkip, ResultNotApp}
}

// Valid 报告结果是否合法。
func (r Result) Valid() bool {
	switch r {
	case ResultPass, ResultFail, ResultBlocked, ResultSkip, ResultNotApp:
		return true
	}
	return false
}

// Severity 返回结果的严重程度（越大越严重）。
// 用例级结果取所有步骤的最差值（DESIGN-TESTTASK-001：与禅道 stepStatus 汇聚一致）。
func (r Result) Severity() int {
	switch r {
	case ResultNotApp:
		return 0
	case ResultSkip:
		return 1
	case ResultPass:
		return 2
	case ResultBlocked:
		return 3
	case ResultFail:
		return 4
	}
	return -1
}

// WorstResult 返回一组结果中最严重的一个（空集合返回 ResultNotApp）。
func WorstResult(results []Result) Result {
	worst := ResultNotApp
	for _, r := range results {
		if r.Severity() > worst.Severity() {
			worst = r
		}
	}
	return worst
}
