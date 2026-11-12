// Package story 是需求域的应用服务层：编排领域对象、编排事务边界、驱动留痕。
// 依赖方向：usecase → domain（禁止反向；AGENTS.md §2）。
package story

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/mst8222/regto/internal/domain/story"
	"github.com/mst8222/regto/internal/platform/cache"
	"github.com/mst8222/regto/internal/platform/clock"
)

// StoryCacheTTL 是需求详情缓存时长（REQ-05：30 秒）。
// 需求是高频协作对象，写多读多，长 TTL 换来的一致性成本不划算。
const StoryCacheTTL = 30 * time.Second

// Service 是需求域应用服务。
type Service struct {
	repo  story.Repository
	clk   clock.Clock
	cache cache.Store // 可选：未配置 Redis 时为空实现（DESIGN-REDIS-001 DEC-01）
}

// New 构造服务；repo 与 clk 均不可为 nil。cache 为 nil 时注入空实现。
func New(repo story.Repository, clk clock.Clock) *Service {
	return NewWithCache(repo, clk, cache.Noop())
}

// NewWithCache 构造带缓存的服务（Redis 启用时由 main 注入）。
func NewWithCache(repo story.Repository, clk clock.Clock, store cache.Store) *Service {
	if store == nil {
		store = cache.Noop()
	}
	return &Service{repo: repo, clk: clk, cache: store}
}

// CreateCommand 是创建需求的输入（由 adapter 层绑定）。
type CreateCommand struct {
	ProductID  int64
	ModuleID   *int64
	Title      string
	Type       string
	Pri        int
	Estimate   *float64
	Spec       story.SpecInput
	Reviewers  []int64
	SourceNote string
	AsDraft    bool
	Actor      story.Actor
}

// Create 创建需求并落库（含 create 事件 → activities）。
func (s *Service) Create(ctx context.Context, cmd CreateCommand) (*story.Story, error) {
	now := s.clk.Now().UnixMilli()
	st, err := story.NewStory(story.NewStoryParams{
		ProductID:  cmd.ProductID,
		ModuleID:   cmd.ModuleID,
		Title:      cmd.Title,
		Type:       cmd.Type,
		Pri:        cmd.Pri,
		Estimate:   cmd.Estimate,
		Spec:       cmd.Spec,
		Reviewers:  cmd.Reviewers,
		SourceNote: cmd.SourceNote,
		AsDraft:    cmd.AsDraft,
		OpenedByID: cmd.Actor.ID,
		OpenedAtMs: now,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.Save(ctx, st, st.DrainEvents()); err != nil {
		return nil, err
	}
	s.invalidate(ctx, st.ID)
	return st, nil
}

// Get 按 ID 加载需求；不存在返回 shared.ErrNotFound。
//
// 命中缓存时返回**深拷贝**（JSON 往返得到新对象），避免调用方改动污染缓存（DEC-05）。
// 不缓存「不存在」的结果：防止用不存在的 ID 打满缓存。
func (s *Service) Get(ctx context.Context, id int64) (*story.Story, error) {
	ck := storyCacheKey(id)
	var cached story.Story
	if hit, err := s.cache.Get(ctx, ck, &cached); err == nil && hit {
		cp := cached // 已是反序列化出的独立对象
		return &cp, nil
	}
	st, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = s.cache.Set(ctx, ck, st, StoryCacheTTL)
	return st, nil
}

// invalidate 失效需求详情缓存（写路径统一调用，REQ-05）。
func (s *Service) invalidate(ctx context.Context, id int64) {
	_ = s.cache.Delete(ctx, storyCacheKey(id))
}

// storyCacheKey 构造需求详情缓存键（regto:v1:story:<id>）。
func storyCacheKey(id int64) string {
	return cache.Key("story", strconv.FormatInt(id, 10))
}

// List 按条件返回需求列表与总数。
// usecase 再次执行 Normalize/Validate：即使调用方不是 HTTP（如 CLI），
// 进入仓储前也必须先收敛与校验（DESIGN-STORY-002 DEC-01）。
func (s *Service) List(ctx context.Context, q story.StoryQuery) ([]story.StoryListItem, int64, error) {
	q = q.Normalize()
	if err := q.Validate(); err != nil {
		return nil, 0, err
	}
	return s.repo.List(ctx, q)
}

// TransitionCommand 是状态转移的输入（统一入口，对应 POST /stories/{id}/transitions）。
type TransitionCommand struct {
	ID            int64
	Action        story.Action
	Spec          *story.SpecInput     // change：新规格
	CloseReason   string               // close：关闭原因
	ReviewResults []story.ReviewResult // 评审动作：多人评审结果（空则按 action 推导单人结果）
	Reviewers     []int64              // submitReview：评审人集合
	Comment       string
	ReviewerID    int64 // 评审动作：当前评审人（写 story_reviews）
	Actor         story.Actor
}

// Transition 执行状态转移：加载聚合 → 领域规则 → 落库（聚合 + 事件 + 评审记录）。
func (s *Service) Transition(ctx context.Context, cmd TransitionCommand) (*story.Story, error) {
	st, err := s.repo.GetByID(ctx, cmd.ID)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now().UnixMilli()
	opts := story.TransitionOptions{
		Spec:        cmd.Spec,
		CloseReason: cmd.CloseReason,
		Reviewers:   cmd.Reviewers,
		Comment:     cmd.Comment,
		Actor:       cmd.Actor,
		NowMs:       now,
	}

	if isReviewAction(cmd.Action) {
		results := cmd.ReviewResults
		if len(results) == 0 {
			r, ok := resultFromAction(cmd.Action)
			if !ok {
				return nil, fmt.Errorf("action %s 无法推导评审结果", cmd.Action)
			}
			results = []story.ReviewResult{r}
		}
		aggregated, err := story.AggregateReviewResults(results)
		if err != nil {
			return nil, err
		}
		// 评审人：请求未显式指定时回退为执行主体（领域层保证，见 DEC-09）。
		opts.ReviewerID = cmd.ReviewerID
		// 评审单据不再单独落库：它挂在领域事件上，与状态转移同事务提交（DEC-09）。
		// 旧行为「先写评审再转移」会让非法转移留下脏评审记录且无法回滚。
		if err := st.ApplyReviewResult(aggregated, opts); err != nil {
			return nil, err
		}
	} else if err := st.Transition(cmd.Action, opts); err != nil {
		return nil, err
	}

	if err := s.repo.Save(ctx, st, st.DrainEvents()); err != nil {
		return nil, err
	}
	s.invalidate(ctx, st.ID)
	return st, nil
}

// AdvanceStageCommand 是推进研发阶段的输入。
type AdvanceStageCommand struct {
	ID    int64
	Stage story.Stage
	Actor story.Actor
}

// AdvanceStage 推进研发阶段（stage 只能前进，回退返回 STAGE_REGRESSION）。
func (s *Service) AdvanceStage(ctx context.Context, cmd AdvanceStageCommand) (*story.Story, error) {
	st, err := s.repo.GetByID(ctx, cmd.ID)
	if err != nil {
		return nil, err
	}
	if err := st.AdvanceStage(cmd.Stage); err != nil {
		return nil, err
	}
	// 领域层 AdvanceStage 无执行主体入参（签名由规格锁定），此处补齐 Actor 与时间，
	// 保证 activities 的 actor_type/actor_id 不为空（AGENTS.md §3.1）。
	events := st.DrainEvents()
	now := s.clk.Now().UnixMilli()
	for i := range events {
		if events[i].Actor.ID == 0 {
			events[i].Actor = cmd.Actor
			events[i].OccurredAtMs = now
		}
	}
	if err := s.repo.Save(ctx, st, events); err != nil {
		return nil, err
	}
	s.invalidate(ctx, st.ID)
	return st, nil
}

// isReviewAction 判断动作是否属于评审四分支。
func isReviewAction(a story.Action) bool {
	switch a {
	case story.ActionReviewPass, story.ActionReviewClarify,
		story.ActionReviewRevert, story.ActionReviewReject:
		return true
	}
	return false
}

// resultFromAction 把评审动作映射为单人评审结果（客户端未显式传 results 时使用）。
func resultFromAction(a story.Action) (story.ReviewResult, bool) {
	switch a {
	case story.ActionReviewPass:
		return story.ReviewPass, true
	case story.ActionReviewClarify:
		return story.ReviewClarify, true
	case story.ActionReviewRevert:
		return story.ReviewRevert, true
	case story.ActionReviewReject:
		return story.ReviewReject, true
	}
	return "", false
}
