// Package agent 定义 AI 执行主体的权限边界：动作、策略与判定。
//
// 本包零外部依赖（AGENTS.md §0 铁律 3）：只有标准库，不含 DB/HTTP/三方包。
// 判定是纯函数——HTTP 层负责取主体与查库，本包只回答「该不该放行」。
//
// 规格来源：spec/agent/boundary/SPEC-AGENT-001.md（ROADMAP §5.4 三档权限表）。
package agent

// Action 是一次有意义的操作单元（与具体 HTTP 路由解耦，DESIGN-AGENT-001#DEC-01）。
type Action string

// AI 自主档（auto）：读、写草稿、跑测试、建任务、提 PR、追加留痕、建 Bug。
const (
	ActionSpecRead        Action = "spec.read"
	ActionCodeRead        Action = "code.read"
	ActionReportRead      Action = "report.read"
	ActionResourceRead    Action = "resource.read"
	ActionSpecWriteDraft  Action = "spec.write_draft"
	ActionTestWrite       Action = "test.write"
	ActionTestRun         Action = "test.run"
	ActionTaskCreate      Action = "task.create"
	ActionTaskUpdate      Action = "task.update"
	ActionBranchPush      Action = "branch.push"
	ActionPRCreate        Action = "pr.create"
	ActionTraceAppend     Action = "trace.append"
	ActionBugCreate       Action = "bug.create"
	ActionStoryTransition Action = "story.transition"
)

// AI 需审批档（approval）：改已批准规格、合并主干、关 Bug、改迁移、发布、删除、关需求、改他人工时。
const (
	ActionSpecChangeApproved Action = "spec.change_approved"
	ActionMergeMain          Action = "merge.main"
	ActionBugResolve         Action = "bug.resolve"
	ActionMigrationWrite     Action = "migration.write"
	ActionReleaseCreate      Action = "release.create"
	ActionReleasePublish     Action = "release.publish"
	ActionResourceDelete     Action = "resource.delete"
	ActionStoryClose         Action = "story.close"
	ActionEffortEditOthers   Action = "effort.edit_others"
)

// 人类专属档（human_only）：批准规格、分配权限、生产发布、删归档项目、覆盖复核、导生产数据、改 Agent 策略。
const (
	ActionSpecApprove    Action = "spec.approve"
	ActionRoleGrant      Action = "role.grant"
	ActionProdDeploy     Action = "prod.deploy"
	ActionArchiveDelete  Action = "archive.delete"
	ActionReviewOverride Action = "review.override"
	ActionImportProd     Action = "import.prod"
	ActionAgentPolicy    Action = "agent.policy_edit"
	// ActionSessionManage 是会话运维（登记/吊销 Agent 会话）——
	// 它是「调整某个 Agent 的能力边界」，比 agent.policy_edit 更具体，同属人类专属（SPEC-AGENT-002 REQ-10）。
	ActionSessionManage Action = "agent.session_manage"
)

// AllActions 返回全部内置动作（顺序稳定，供测试穷举与文档生成）。
func AllActions() []Action {
	return []Action{
		// auto
		ActionSpecRead, ActionCodeRead, ActionReportRead, ActionResourceRead,
		ActionSpecWriteDraft,
		ActionTestWrite, ActionTestRun, ActionTaskCreate, ActionTaskUpdate,
		ActionBranchPush, ActionPRCreate, ActionTraceAppend, ActionBugCreate,
		ActionStoryTransition,
		// approval
		ActionSpecChangeApproved, ActionMergeMain, ActionBugResolve, ActionMigrationWrite,
		ActionReleaseCreate, ActionReleasePublish, ActionResourceDelete,
		ActionStoryClose, ActionEffortEditOthers,
		// human_only
		ActionSpecApprove, ActionRoleGrant, ActionProdDeploy, ActionArchiveDelete,
		ActionReviewOverride, ActionImportProd, ActionAgentPolicy, ActionSessionManage,
	}
}

// Policy 是动作对 AI 的策略档位。
type Policy string

// 三档策略（SPEC-AGENT-001 REQ-01）。
const (
	PolicyAuto      Policy = "auto"       // AI 自主
	PolicyApproval  Policy = "approval"   // AI 需审批
	PolicyHumanOnly Policy = "human_only" // 人类专属
)

// ValidPolicy 判断策略值是否合法（用于 per-agent 策略覆盖的校验）。
func ValidPolicy(p Policy) bool {
	switch p {
	case PolicyAuto, PolicyApproval, PolicyHumanOnly:
		return true
	}
	return false
}

// defaultPolicy 是 ROADMAP §5.4 权限表的代码化：动作 → 默认策略。
var defaultPolicy = map[Action]Policy{
	// ---- auto ----
	ActionSpecRead:        PolicyAuto,
	ActionCodeRead:        PolicyAuto,
	ActionReportRead:      PolicyAuto,
	ActionResourceRead:    PolicyAuto,
	ActionSpecWriteDraft:  PolicyAuto,
	ActionTestWrite:       PolicyAuto,
	ActionTestRun:         PolicyAuto,
	ActionTaskCreate:      PolicyAuto,
	ActionTaskUpdate:      PolicyAuto,
	ActionBranchPush:      PolicyAuto,
	ActionPRCreate:        PolicyAuto,
	ActionTraceAppend:     PolicyAuto,
	ActionBugCreate:       PolicyAuto,
	ActionStoryTransition: PolicyAuto,

	// ---- approval ----
	ActionSpecChangeApproved: PolicyApproval,
	ActionMergeMain:          PolicyApproval,
	ActionBugResolve:         PolicyApproval,
	ActionMigrationWrite:     PolicyApproval,
	ActionReleaseCreate:      PolicyApproval,
	ActionReleasePublish:     PolicyApproval,
	ActionResourceDelete:     PolicyApproval,
	ActionStoryClose:         PolicyApproval,
	ActionEffortEditOthers:   PolicyApproval,

	// ---- human_only ----
	ActionSpecApprove:    PolicyHumanOnly,
	ActionRoleGrant:      PolicyHumanOnly,
	ActionProdDeploy:     PolicyHumanOnly,
	ActionArchiveDelete:  PolicyHumanOnly,
	ActionReviewOverride: PolicyHumanOnly,
	ActionImportProd:     PolicyHumanOnly,
	ActionAgentPolicy:    PolicyHumanOnly,
	ActionSessionManage:  PolicyHumanOnly,
}

// IsKnownAction 报告动作是否已登记在策略表里。
//
// 与 PolicyOf 的区别：PolicyOf 对未知动作返回 human_only（失败方向安全），
// 但那让「拼错的动作」和「真的人类专属动作」在判定层无法区分。
// 配置校验（会话能力白名单）必须能分辨这两者：前者是配置错误，后者是越权授权。
func IsKnownAction(a Action) bool {
	_, ok := defaultPolicy[a]
	return ok
}

// PolicyOf 返回动作的默认策略。
//
// 未知动作一律按 human_only 处理（DESIGN-AGENT-001#DEC-01）：
// 新增接口忘了登记策略时，失败方向是「拒绝」而不是「放行」。
func PolicyOf(a Action) Policy {
	if p, ok := defaultPolicy[a]; ok {
		return p
	}
	return PolicyHumanOnly
}
