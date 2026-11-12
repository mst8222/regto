// Package trace 定义 SpecDD 留痕记录（ROADMAP §5.2）与 hash 链。
// 领域层零外部依赖：仅标准库，禁止引入 DB/HTTP/三方包（AGENTS.md §2）。
package trace

import (
	"encoding/json"
	"strings"
)

// SchemaVersion 是留痕记录结构版本；结构变更时递增，旧记录仍可校验。
const SchemaVersion int16 = 1

// PrevHashLength 是 hash 的十六进制长度（sha256）。
const PrevHashLength = 64

// GenesisPrevHash 是链首记录的 prev_hash（64 个 0）。
const GenesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"

// 动作类型（ROADMAP §5.2）。
const (
	ActionRead         = "read"
	ActionReview       = "review"
	ActionImplement    = "implement"
	ActionTest         = "test"
	ActionFix          = "fix"
	ActionRegress      = "regress"
	ActionStatusChange = "status_change"
	ActionSpecChange   = "spec_change"
	ActionDecision     = "decision"
	ActionApprove      = "approve"
	ActionReject       = "reject"
	ActionImport       = "import"
)

// AllActions 返回全部合法动作（顺序稳定，供校验与测试穷举）。
func AllActions() []string {
	return []string{ActionRead, ActionReview, ActionImplement, ActionTest, ActionFix,
		ActionRegress, ActionStatusChange, ActionSpecChange, ActionDecision, ActionApprove,
		ActionReject, ActionImport}
}

// attemptActionMaxLen 是 traces.action 的列宽（migrations/000009 VARCHAR(24)）。
const attemptActionMaxLen = 24

// IsAttemptAction 判断 action 是否为「域.动词」形状的**尝试动作**。
//
// 为什么 action 允许第二类取值（SPEC-AGENT-001 REQ-03 / DESIGN-AGENT-001 DEC-03）：
// AI 越权被拦截时，留痕要让人一眼看出「AI 想干什么」，所以写入的是被拦截的域动作
// （release.publish / migration.write），而不是 12 个审计动词之一。
// 这类记录的形状是 action=<域动作>、outcome=blocked、needs_human_review=1。
//
// 约束：只放行「小写域.小写动词」——审计动词是单词（read/review/approve…），
// 两者形状天然可分；拼错的大写、空格、无点串仍然非法，枚举护栏没有被拆掉。
func IsAttemptAction(a string) bool {
	if a == "" || len(a) > attemptActionMaxLen {
		return false
	}
	dot := strings.IndexByte(a, '.')
	if dot <= 0 || dot == len(a)-1 {
		return false
	}
	for _, part := range []string{a[:dot], a[dot+1:]} {
		for i := 0; i < len(part); i++ {
			c := part[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
				continue
			}
			return false
		}
	}
	return true
}

// ValidAction 判断 action 是否合法：12 个审计动作之一，或是域动作形状的尝试动作。
func ValidAction(a string) bool {
	for _, v := range AllActions() {
		if v == a {
			return true
		}
	}
	return IsAttemptAction(a)
}

// 执行主体类型：AI / 人类 / 系统（AGENTS.md §3.1 核心区分）。
const (
	ActorHuman  = "human"
	ActorAI     = "ai"
	ActorSystem = "system"
)

// ValidActorType 判断执行主体类型是否合法。
func ValidActorType(t string) bool {
	switch t {
	case ActorHuman, ActorAI, ActorSystem:
		return true
	}
	return false
}

// Outcome 是动作结果。
type Outcome string

// 动作结果常量。
const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomePartial Outcome = "partial"
	OutcomeBlocked Outcome = "blocked"
)

// ValidOutcome 判断结果是否合法（空串表示未填，合法）。
func ValidOutcome(o Outcome) bool {
	switch o {
	case "", OutcomeSuccess, OutcomeFailure, OutcomePartial, OutcomeBlocked:
		return true
	}
	return false
}

// Evidence 是一条证据（diff/日志/测试报告/截图），带 sha256 防篡改。
type Evidence struct {
	Kind   string `json:"kind"`             // diff|log|report|screenshot|testresult
	URI    string `json:"uri"`              // 位置（相对路径或 URL）
	SHA256 string `json:"sha256,omitempty"` // 内容摘要
	Size   int64  `json:"size,omitempty"`
}

// RelatedRef 是关联对象（任务、提交、PR 等）。
type RelatedRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Record 是一条完整留痕记录（jsonl 一行）。字段顺序即 canonical JSON 顺序，变更需递增 SchemaVersion。
type Record struct {
	// 链
	TraceID       string `json:"trace_id"`
	Seq           int64  `json:"seq"`
	PrevHash      string `json:"prev_hash"`
	Hash          string `json:"hash"`
	SchemaVersion int16  `json:"schema_version"`

	// 时间
	OccurredAtMs int64 `json:"occurred_at_ms"`
	RecordedAtMs int64 `json:"recorded_at_ms"`
	DurationMs   int64 `json:"duration_ms,omitempty"`

	// 动作
	Action  string  `json:"action"`
	Outcome Outcome `json:"outcome,omitempty"`

	// 主体
	ActorType      string `json:"actor_type"`
	ActorID        string `json:"actor_id"`
	ActorModel     string `json:"actor_model,omitempty"`
	ActorVersion   string `json:"actor_version,omitempty"`
	ActorSessionID string `json:"actor_session_id,omitempty"`

	// 对象
	ObjectType string       `json:"object_type"`
	ObjectID   string       `json:"object_id"`
	ObjectRef  string       `json:"object_ref,omitempty"`
	Related    []RelatedRef `json:"related,omitempty"`
	FromState  string       `json:"from_state,omitempty"`
	ToState    string       `json:"to_state,omitempty"`

	// 内容
	Summary      string     `json:"summary"`
	Detail       string     `json:"detail,omitempty"`
	Evidence     []Evidence `json:"evidence,omitempty"`
	PromptDigest string     `json:"prompt_digest,omitempty"` // sha256(prompt 前 4KB)，不落原文

	// AI 特有
	Confidence       int    `json:"confidence,omitempty"`
	NeedsHumanReview bool   `json:"needs_human_review"`
	ReviewStatus     string `json:"review_status,omitempty"` // pending|approved|rejected|waived
	ReviewedBy       string `json:"reviewed_by,omitempty"`
	ReviewedAtMs     int64  `json:"reviewed_at_ms,omitempty"`

	// 上下文
	Repo      string   `json:"repo,omitempty"`
	CommitSHA string   `json:"commit_sha,omitempty"`
	PR        string   `json:"pr,omitempty"`
	Env       string   `json:"env,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// canonical 返回参与 hash 计算的规范序列化：剔除 hash 与 recorded_at_ms（落库时间不参与）。
func (r Record) canonical() ([]byte, error) {
	type payload struct {
		TraceID       string `json:"trace_id"`
		Seq           int64  `json:"seq"`
		PrevHash      string `json:"prev_hash"`
		SchemaVersion int16  `json:"schema_version"`

		OccurredAtMs int64 `json:"occurred_at_ms"`
		DurationMs   int64 `json:"duration_ms,omitempty"`

		Action  string  `json:"action"`
		Outcome Outcome `json:"outcome,omitempty"`

		ActorType      string `json:"actor_type"`
		ActorID        string `json:"actor_id"`
		ActorModel     string `json:"actor_model,omitempty"`
		ActorVersion   string `json:"actor_version,omitempty"`
		ActorSessionID string `json:"actor_session_id,omitempty"`

		ObjectType string       `json:"object_type"`
		ObjectID   string       `json:"object_id"`
		ObjectRef  string       `json:"object_ref,omitempty"`
		Related    []RelatedRef `json:"related,omitempty"`
		FromState  string       `json:"from_state,omitempty"`
		ToState    string       `json:"to_state,omitempty"`

		Summary      string     `json:"summary"`
		Detail       string     `json:"detail,omitempty"`
		Evidence     []Evidence `json:"evidence,omitempty"`
		PromptDigest string     `json:"prompt_digest,omitempty"`

		Confidence       int    `json:"confidence,omitempty"`
		NeedsHumanReview bool   `json:"needs_human_review"`
		ReviewStatus     string `json:"review_status,omitempty"`
		ReviewedBy       string `json:"reviewed_by,omitempty"`
		ReviewedAtMs     int64  `json:"reviewed_at_ms,omitempty"`

		Repo      string   `json:"repo,omitempty"`
		CommitSHA string   `json:"commit_sha,omitempty"`
		PR        string   `json:"pr,omitempty"`
		Env       string   `json:"env,omitempty"`
		Tags      []string `json:"tags,omitempty"`
	}

	p := payload{
		TraceID:          r.TraceID,
		Seq:              r.Seq,
		PrevHash:         r.PrevHash,
		SchemaVersion:    r.SchemaVersion,
		OccurredAtMs:     r.OccurredAtMs,
		DurationMs:       r.DurationMs,
		Action:           r.Action,
		Outcome:          r.Outcome,
		ActorType:        r.ActorType,
		ActorID:          r.ActorID,
		ActorModel:       r.ActorModel,
		ActorVersion:     r.ActorVersion,
		ActorSessionID:   r.ActorSessionID,
		ObjectType:       r.ObjectType,
		ObjectID:         r.ObjectID,
		ObjectRef:        r.ObjectRef,
		Related:          r.Related,
		FromState:        r.FromState,
		ToState:          r.ToState,
		Summary:          r.Summary,
		Detail:           r.Detail,
		Evidence:         r.Evidence,
		PromptDigest:     r.PromptDigest,
		Confidence:       r.Confidence,
		NeedsHumanReview: r.NeedsHumanReview,
		ReviewStatus:     r.ReviewStatus,
		ReviewedBy:       r.ReviewedBy,
		ReviewedAtMs:     r.ReviewedAtMs,
		Repo:             r.Repo,
		CommitSHA:        r.CommitSHA,
		PR:               r.PR,
		Env:              r.Env,
		Tags:             r.Tags,
	}
	return json.Marshal(p)
}
