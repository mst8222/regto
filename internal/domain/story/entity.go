package story

import (
	"github.com/mst8222/regto/internal/domain/shared"
	"strconv"
	"strings"
)

// Actor 是执行主体（AGENTS.md §3.1：AI / 人类 / 系统必须区分）。
type Actor struct {
	ID   int64
	Type string // human | ai | system
}

// 执行主体类型常量。
const (
	ActorHuman  = "human"
	ActorAI     = "ai"
	ActorSystem = "system"
)

// FieldChange 是字段级 diff（对应禅道 zt_history）。
type FieldChange struct {
	Field string
	From  string
	To    string
}

// Event 是领域事件（DEC-05：由 usecase 在同一事务写入 activities + activity_changes）。
type Event struct {
	ObjectType   string
	ObjectID     int64
	Action       Action
	Changes      []FieldChange
	Actor        Actor
	OccurredAtMs int64
	SpecRef      string
	Comment      string
	// Review 是评审单据：**仅评审四分支有值**（pass/clarify/revert/reject），其余动作为 nil。
	// 评审单据随事件在同一个事务内落 story_reviews，保证「状态转移与评审单据」的原子性（DEC-09）。
	Review *Review
}

// Story 是需求聚合根。字段命名与 spec/plans/ROADMAP.md §3.3 的 PostgreSQL DDL 对应。
type Story struct {
	ID             int64
	ProductID      int64
	ModuleID       *int64
	Title          string
	Type           string
	Pri            int
	Estimate       *float64
	Status         Status
	Stage          Stage
	SpecVersion    int
	ClosedReason   string
	Reviewers      []int64
	SourceNote     string
	OpenedByID     int64
	OpenedAtMs     int64
	AssignedToID   *int64
	AssignedAtMs   *int64
	LastEditedByID *int64
	LastEditedAtMs *int64
	ChangedByID    *int64 // 非空表示历史上发生过变更（reviewClarify 分支依据）
	ClosedByID     *int64
	ClosedAtMs     *int64
	DeletedAtMs    *int64
	CreatedAtMs    int64
	UpdatedAtMs    int64

	Specs  []Spec // 全部规格版本（含历史），按 version 升序
	events []Event

	// LoadedAtMs 是持久层加载该聚合时的 updated_at（毫秒），用于 Save 的乐观锁
	// （UPDATE ... WHERE id=$1 AND updated_at = loaded_at）。领域逻辑不读该字段；
	// 新建聚合为 0，表示走 INSERT 分支。见 DESIGN-STORY-001 DEC-07。
	LoadedAtMs int64
}

// TransitionOptions 是 Transition 的可选参数。不同动作取不同字段，多余字段忽略。
type TransitionOptions struct {
	Spec         *SpecInput   // change：新规格内容（必填）
	CloseReason  string       // close：关闭原因（必填）
	ReviewResult ReviewResult // 评审动作：聚合后的评审结果
	Reviewers    []int64      // submitReview：更新评审人集合（可选）
	ReviewerID   int64        // 评审动作：评审人 ID；为 0 时回退为 Actor.ID（DEC-09）
	Comment      string       // 备注（写入 Event.Comment）
	Actor        Actor        // 执行主体
	NowMs        int64        // 事件时间（unix 毫秒）
}

// NewStory 创建 Story（REQ-01 创建规则），初始 spec version=1。
func NewStory(p NewStoryParams) (*Story, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, shared.New("INVALID_TITLE", "title is required", nil)
	}
	if p.ProductID <= 0 {
		return nil, shared.New("INVALID_PRODUCT", "product_id is required",
			map[string]any{"product_id": p.ProductID})
	}
	if p.Pri < 0 || p.Pri > 4 {
		return nil, shared.New("INVALID_PRI", "pri must be in [0,4]",
			map[string]any{"pri": p.Pri})
	}
	if p.Spec.Pri < 0 || p.Spec.Pri > 4 {
		return nil, shared.New("INVALID_PRI", "spec pri must be in [0,4]",
			map[string]any{"pri": p.Spec.Pri})
	}
	typ := p.Type
	if typ == "" {
		typ = "story"
	}
	switch typ {
	case "story", "requirement", "epic":
	default:
		return nil, shared.New("INVALID_TYPE", "type must be story/requirement/epic",
			map[string]any{"type": typ})
	}
	reviewers := dedupeIDs(p.Reviewers)

	status := initialStatus(reviewers, p.AsDraft)
	s := &Story{
		ProductID:   p.ProductID,
		ModuleID:    p.ModuleID,
		Title:       p.Title,
		Type:        typ,
		Pri:         p.Pri,
		Estimate:    p.Estimate,
		Status:      status,
		Stage:       StageWait,
		SpecVersion: 1,
		Reviewers:   reviewers,
		SourceNote:  p.SourceNote,
		OpenedByID:  p.OpenedByID,
		OpenedAtMs:  p.OpenedAtMs,
		CreatedAtMs: p.OpenedAtMs,
		UpdatedAtMs: p.OpenedAtMs,
	}
	s.Specs = append(s.Specs, Spec{
		StoryID:     s.ID,
		Version:     1,
		Title:       p.Title,
		Spec:        p.Spec.Spec,
		Verify:      p.Spec.Verify,
		Pri:         p.Spec.Pri,
		Estimate:    p.Spec.Estimate,
		CreatedByID: p.OpenedByID,
		CreatedAt:   p.OpenedAtMs,
	})
	s.emit(Event{
		ObjectType:   "story",
		Action:       ActionCreate,
		Changes:      []FieldChange{{Field: "status", From: "", To: string(status)}},
		Actor:        Actor{ID: p.OpenedByID, Type: ActorHuman},
		OccurredAtMs: p.OpenedAtMs,
		SpecRef:      ActionSpecRef(ActionCreate),
	})
	return s, nil
}

// Transition 是唯一状态转移入口（DEC-01）。查转移表 → 计算目标 → 校验 → 应用 → 记事件。
// 非法转移返回 ErrInvalidTransition（Meta 含 from/to/allowed），且零副作用（NFR-03）。
func (s *Story) Transition(action Action, opts TransitionOptions) error {
	if s.DeletedAtMs != nil {
		return shared.New("DELETED", "story is deleted", map[string]any{"id": s.ID})
	}
	rule, ok := lookupRule(s.Status, action)
	if !ok {
		return shared.New("INVALID_TRANSITION",
			"action "+string(action)+" not allowed from status "+string(s.Status),
			map[string]any{
				"from":            s.Status,
				"to":              nil,
				"action":          action,
				"allowed":         allowedActions(s.Status),
				"allowed_targets": allowedTargets(s.Status),
			})
	}

	// 各动作前置校验（在任何字段变更之前，保证拒绝路径零副作用）。
	switch action {
	case ActionSubmitReview:
		if len(opts.Reviewers) > 0 {
			s.Reviewers = dedupeIDs(opts.Reviewers)
		}
		if len(s.Reviewers) == 0 {
			return shared.New("REVIEWERS_REQUIRED", "reviewers required for submitReview",
				map[string]any{"from": s.Status})
		}
	case ActionChange:
		if opts.Spec == nil || opts.Spec.IsEmpty() {
			return shared.New("EMPTY_SPEC", "spec content is empty", nil)
		}
		if s.SpecVersion >= maxSpecVersion {
			return shared.New("VERSION_OVERFLOW", "spec version overflow",
				map[string]any{"version": s.SpecVersion})
		}
	case ActionReviewRevert:
		if s.SpecVersion <= 1 {
			return shared.New("NO_PREVIOUS_VERSION", "no previous spec version to revert",
				map[string]any{"version": s.SpecVersion})
		}
	case ActionReviewReject:
		// 结果由调用方保证已聚合；此处仅校验取值。
		if opts.ReviewResult != "" && !opts.ReviewResult.Valid() {
			return shared.New("INVALID_REVIEW_RESULT", "invalid review result",
				map[string]any{"result": opts.ReviewResult})
		}
	case ActionClose:
		if !isPublicCloseReason(opts.CloseReason) {
			return shared.New("INVALID_CLOSE_REASON", "invalid close reason: "+opts.CloseReason,
				map[string]any{"reason": opts.CloseReason, "allowed": AllCloseReasons()})
		}
	}

	// 计算目标状态（条件分支）。
	to := s.resolveTarget(action, rule)

	// 应用副作用。
	from := s.Status
	// reviewedVersion：被评审的规格版本，必须在任何版本变更（revert 递减）之前捕获。
	reviewedVersion := s.SpecVersion
	changes := []FieldChange{{Field: "status", From: string(from), To: string(to)}}
	now := opts.NowMs

	switch action {
	case ActionReviewRevert:
		s.SpecVersion--
		changes = append(changes, FieldChange{Field: "spec_version", From: intToStr(s.SpecVersion + 1), To: intToStr(s.SpecVersion)})
	case ActionReviewReject:
		s.ClosedReason = ReasonReviewRejected
		s.ClosedAtMs = &now
		s.ClosedByID = &opts.Actor.ID
		changes = append(changes, FieldChange{Field: "closed_reason", From: "", To: ReasonReviewRejected})
	case ActionChange:
		newVersion := s.SpecVersion + 1
		actorID := opts.Actor.ID
		s.ChangedByID = &actorID
		s.SpecVersion = newVersion
		s.Specs = append(s.Specs, Spec{
			StoryID:     s.ID,
			Version:     newVersion,
			Title:       opts.Spec.Title,
			Spec:        opts.Spec.Spec,
			Verify:      opts.Spec.Verify,
			Pri:         opts.Spec.Pri,
			Estimate:    opts.Spec.Estimate,
			CreatedByID: opts.Actor.ID,
			CreatedAt:   now,
		})
		if strings.TrimSpace(opts.Spec.Title) != "" {
			changes = append(changes, FieldChange{Field: "title", From: s.Title, To: opts.Spec.Title})
			s.Title = opts.Spec.Title
		}
		changes = append(changes, FieldChange{Field: "spec_version", From: intToStr(newVersion - 1), To: intToStr(newVersion)})
	case ActionClose:
		s.ClosedReason = opts.CloseReason
		s.ClosedAtMs = &now
		s.ClosedByID = &opts.Actor.ID
		changes = append(changes, FieldChange{Field: "closed_reason", From: "", To: opts.CloseReason})
	case ActionActivate:
		if s.ClosedReason != "" {
			changes = append(changes, FieldChange{Field: "closed_reason", From: s.ClosedReason, To: ""})
		}
		s.ClosedReason = ""
		s.ClosedAtMs = nil
		s.ClosedByID = nil
	}

	lastEdited := opts.Actor.ID
	s.LastEditedByID = &lastEdited
	s.LastEditedAtMs = &now
	s.Status = to
	s.UpdatedAtMs = now

	ev := Event{
		ObjectType:   "story",
		ObjectID:     s.ID,
		Action:       action,
		Changes:      changes,
		Actor:        opts.Actor,
		OccurredAtMs: now,
		SpecRef:      ActionSpecRef(action),
		Comment:      opts.Comment,
	}
	// 评审四分支：把评审单据挂到事件上，随事件在同一事务落 story_reviews（DEC-09）。
	if opts.ReviewResult != "" {
		reviewerID := opts.ReviewerID
		if reviewerID == 0 {
			reviewerID = opts.Actor.ID // 未显式指定评审人时，执行主体即评审人
		}
		ev.Review = &Review{
			StoryID:      s.ID,
			Version:      reviewedVersion,
			ReviewerID:   reviewerID,
			Result:       opts.ReviewResult,
			Comment:      opts.Comment,
			ReviewedAtMs: now,
		}
	}
	s.emit(ev)
	return nil
}

// resolveTarget 计算条件分支的目标状态，并防御性校验其合法（∈ rule.Targets）。
func (s *Story) resolveTarget(action Action, rule ActionRule) Status {
	var to Status
	switch action {
	case ActionReviewClarify:
		// 曾变更 → changing；从未变更 → draft（REQ-02）。
		if s.ChangedByID != nil {
			to = StatusChanging
		} else {
			to = StatusDraft
		}
	case ActionActivate:
		// 评审拒绝关闭的重新激活回到 reviewing，其余回到 active（REQ-05）。
		if s.ClosedReason == ReasonReviewRejected {
			to = StatusReviewing
		} else {
			to = StatusActive
		}
	default:
		to = rule.Targets[0]
	}
	for _, t := range rule.Targets {
		if t == to {
			return to
		}
	}
	// 理论上不可达：条件分支结果必须来自 Targets。
	return rule.Targets[0]
}

// ApplyReviewResult 是评审结果的便捷入口：聚合校验后走 Transition。
func (s *Story) ApplyReviewResult(result ReviewResult, opts TransitionOptions) error {
	action, ok := reviewResultToAction(result)
	if !ok {
		return shared.New("INVALID_REVIEW_RESULT", "invalid review result: "+string(result),
			map[string]any{"result": result})
	}
	opts.ReviewResult = result
	return s.Transition(action, opts)
}

// reviewResultToAction 映射评审结果到转移动作。
func reviewResultToAction(result ReviewResult) (Action, bool) {
	switch result {
	case ReviewPass:
		return ActionReviewPass, true
	case ReviewClarify:
		return ActionReviewClarify, true
	case ReviewRevert:
		return ActionReviewRevert, true
	case ReviewReject:
		return ActionReviewReject, true
	}
	return "", false
}

// CurrentSpec 返回当前生效的规格版本；不存在返回 nil（数据不一致的防御）。
func (s *Story) CurrentSpec() *Spec {
	for i := range s.Specs {
		if s.Specs[i].Version == s.SpecVersion {
			return &s.Specs[i]
		}
	}
	return nil
}

// SpecAt 返回指定版本规格；不存在返回 nil。
func (s *Story) SpecAt(version int) *Spec {
	for i := range s.Specs {
		if s.Specs[i].Version == version {
			return &s.Specs[i]
		}
	}
	return nil
}

// DrainEvents 取出并清空累积的领域事件（usecase 在同一事务落库）。
func (s *Story) DrainEvents() []Event {
	ev := s.events
	s.events = nil
	return ev
}

// Events 返回累积事件（不清空，仅供测试断言）。
func (s *Story) Events() []Event { return s.events }

// emit 追加一条领域事件。
func (s *Story) emit(e Event) { s.events = append(s.events, e) }

// dedupeIDs 去重、去零值，保持顺序稳定。
func dedupeIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// intToStr 是 strconv.Itoa 的薄封装（领域层仅用于事件 diff 渲染）。
func intToStr(v int) string { return strconv.Itoa(v) }
