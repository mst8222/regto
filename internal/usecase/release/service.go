// Package release 是发布域的应用服务层：编排领域对象、事务边界、驱动留痕。
// 依赖方向：usecase → domain（禁止反向；AGENTS.md §2）。
package release

import (
	"context"
	"errors"

	drel "github.com/mst8222/regto/internal/domain/release"
	"github.com/mst8222/regto/internal/domain/shared"
	"github.com/mst8222/regto/internal/platform/clock"
)

// Service 是发布域应用服务。
type Service struct {
	repo drel.Repository
	clk  clock.Clock
}

// New 构造服务；repo 与 clk 均不可为 nil。
func New(repo drel.Repository, clk clock.Clock) *Service {
	return &Service{repo: repo, clk: clk}
}

// ---------- Plan ----------

// CreatePlanCommand 是创建计划的输入。
type CreatePlanCommand struct {
	ProductID int64
	BranchID  *int64
	ParentID  *int64
	Title     string
	Desc      string
	BeginMs   *int64
	EndMs     *int64
	ActorID   int64
}

// Create 创建产品计划（初始状态 wait，REQ-01）。
func (s *Service) CreatePlan(ctx context.Context, cmd CreatePlanCommand) (*drel.Plan, error) {
	now := s.clk.Now().UnixMilli()
	p := drel.NewPlan(cmd.ProductID, cmd.Title, cmd.ActorID, now)
	p.BranchID = cmd.BranchID
	p.ParentID = cmd.ParentID
	p.Desc = cmd.Desc
	p.BeginMs = cmd.BeginMs
	p.EndMs = cmd.EndMs
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.SavePlan(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// GetPlan 按 ID 加载计划；不存在返回 NOT_FOUND。
func (s *Service) GetPlan(ctx context.Context, id int64) (*drel.Plan, error) {
	return s.repo.GetPlanByID(ctx, id)
}

// UpdatePlanCommand 是修改计划的输入（nil 字段保持不变）。
type UpdatePlanCommand struct {
	PlanID  int64
	Title   *string
	Desc    *string
	BeginMs *int64
	EndMs   *int64
}

// Update 修改计划主数据（状态只能经 Transition 变更，与既有域一致）。
func (s *Service) UpdatePlan(ctx context.Context, cmd UpdatePlanCommand) (*drel.Plan, error) {
	p, err := s.repo.GetPlanByID(ctx, cmd.PlanID)
	if err != nil {
		return nil, err
	}
	if cmd.Title != nil {
		if *cmd.Title == "" {
			return nil, drel.NewInvalidError("title", "计划标题不能为空")
		}
		p.Title = *cmd.Title
	}
	if cmd.Desc != nil {
		p.Desc = *cmd.Desc
	}
	if cmd.BeginMs != nil {
		p.BeginMs = cmd.BeginMs
	}
	if cmd.EndMs != nil {
		p.EndMs = cmd.EndMs
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	p.UpdatedAtMs = s.clk.Now().UnixMilli()
	if err := s.repo.SavePlan(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// TransitionPlanCommand 是计划状态转移的输入。
type TransitionPlanCommand struct {
	PlanID  int64
	Action  drel.PlanAction
	Reason  string // close 时写入 closed_reason
	ActorID int64
}

// TransitionPlan 执行计划状态转移（领域集中校验 + 转移副作用，REQ-02）。
// 副作用（与禅道语义对齐）：finish→finished_at；close→closed_at+reason；
// activate→清空 closed_at/finished_at（复活）。
func (s *Service) TransitionPlan(ctx context.Context, cmd TransitionPlanCommand) (*drel.Plan, error) {
	p, err := s.repo.GetPlanByID(ctx, cmd.PlanID)
	if err != nil {
		return nil, err
	}
	target, err := p.Transition(cmd.Action)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now().UnixMilli()
	switch cmd.Action {
	case drel.PlanActionFinish:
		p.FinishedAtMs = &now
	case drel.PlanActionClose:
		p.ClosedAtMs = &now
		p.ClosedReason = cmd.Reason
	case drel.PlanActionActivate:
		p.FinishedAtMs = nil
		p.ClosedAtMs = nil
		p.ClosedReason = ""
	}
	_ = target // 目标状态已由领域方法写入实体
	p.UpdatedAtMs = now
	if err := s.repo.SavePlan(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// LinkStoryCommand 是计划关联需求的输入。
type LinkStoryCommand struct {
	PlanID  int64
	StoryID int64
}

// LinkStory 把需求加入计划（重复关联返回 PLAN_STORY_CONFLICT，AC-03）。
func (s *Service) LinkStory(ctx context.Context, cmd LinkStoryCommand) error {
	return s.repo.AddPlanStory(ctx, cmd.PlanID, cmd.StoryID, 0)
}

// ListPlanStories 按 order 升序返回计划下的需求关联（REQ-03）。
func (s *Service) ListPlanStories(ctx context.Context, planID int64) ([]drel.PlanStory, error) {
	return s.repo.ListPlanStories(ctx, planID)
}

// ListPlansQuery 是计划列表查询输入。
type ListPlansQuery struct {
	ProductID int64
	Status    drel.PlanStatus
	Keyword   string
	Page      int
	PerPage   int
	Sort      []drel.SortKey
}

// ListPlans 分页查询计划。
func (s *Service) ListPlans(ctx context.Context, q ListPlansQuery) ([]*drel.Plan, int, error) {
	dq := drel.PlanQuery{
		ProductID: q.ProductID,
		Status:    q.Status,
		Keyword:   q.Keyword,
		Sort:      q.Sort,
	}
	if err := dq.Validate(); err != nil {
		return nil, 0, err
	}
	dq.Page.Page = q.Page
	dq.Page.PerPage = q.PerPage
	return s.repo.ListPlans(ctx, dq)
}

// ---------- Release ----------

// CreateReleaseCommand 是创建发布的输入。
type CreateReleaseCommand struct {
	ProductID int64
	BranchID  *int64
	Name      string
	System    drel.System
	Marker    bool
	DateMs    *int64
	Desc      string
	ActorID   int64
}

// Create 创建发布（初始状态 wait，system 默认 trunk，REQ-04）。
func (s *Service) CreateRelease(ctx context.Context, cmd CreateReleaseCommand) (*drel.Release, error) {
	now := s.clk.Now().UnixMilli()
	rel := drel.NewRelease(cmd.ProductID, cmd.Name, cmd.ActorID, now)
	rel.BranchID = cmd.BranchID
	if cmd.System != "" {
		rel.System = cmd.System
	}
	rel.Marker = cmd.Marker
	rel.DateMs = cmd.DateMs
	rel.Desc = cmd.Desc
	if err := rel.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.SaveRelease(ctx, rel); err != nil {
		return nil, err
	}
	return rel, nil
}

// GetRelease 按 ID 加载发布；不存在返回 NOT_FOUND。
func (s *Service) GetRelease(ctx context.Context, id int64) (*drel.Release, error) {
	return s.repo.GetReleaseByID(ctx, id)
}

// UpdateReleaseCommand 是修改发布的输入（nil 字段保持不变）。
type UpdateReleaseCommand struct {
	ReleaseID int64
	Name      *string
	DateMs    *int64
	Desc      *string
	Mailto    *string
	Notify    *string
}

// Update 修改发布主数据（状态只能经 Transition 变更）。
func (s *Service) UpdateRelease(ctx context.Context, cmd UpdateReleaseCommand) (*drel.Release, error) {
	rel, err := s.repo.GetReleaseByID(ctx, cmd.ReleaseID)
	if err != nil {
		return nil, err
	}
	if cmd.Name != nil {
		if *cmd.Name == "" {
			return nil, drel.NewInvalidError("name", "发布名称不能为空")
		}
		rel.Name = *cmd.Name
	}
	if cmd.DateMs != nil {
		rel.DateMs = cmd.DateMs
	}
	if cmd.Desc != nil {
		rel.Desc = *cmd.Desc
	}
	if cmd.Mailto != nil {
		rel.Mailto = *cmd.Mailto
	}
	if cmd.Notify != nil {
		rel.Notify = *cmd.Notify
	}
	if err := rel.Validate(); err != nil {
		return nil, err
	}
	rel.UpdatedAtMs = s.clk.Now().UnixMilli()
	if err := s.repo.SaveRelease(ctx, rel); err != nil {
		return nil, err
	}
	return rel, nil
}

// TransitionReleaseCommand 是发布状态转移的输入。
type TransitionReleaseCommand struct {
	ReleaseID int64
	Action    drel.ReleaseAction
}

// TransitionRelease 执行发布状态转移（REQ-05）。
// 副作用：publish → released_at（禅道 changeStatus 记录 releasedDate）。
func (s *Service) TransitionRelease(ctx context.Context, cmd TransitionReleaseCommand) (*drel.Release, error) {
	rel, err := s.repo.GetReleaseByID(ctx, cmd.ReleaseID)
	if err != nil {
		return nil, err
	}
	if _, err := rel.Transition(cmd.Action); err != nil {
		return nil, err
	}
	now := s.clk.Now().UnixMilli()
	if cmd.Action == drel.ReleaseActionPublish {
		rel.ReleasedAtMs = &now
	}
	rel.UpdatedAtMs = now
	if err := s.repo.SaveRelease(ctx, rel); err != nil {
		return nil, err
	}
	return rel, nil
}

// ListReleasesQuery 是发布列表查询输入。
type ListReleasesQuery struct {
	ProductID int64
	Status    drel.ReleaseStatus
	Keyword   string
	Page      int
	PerPage   int
	Sort      []drel.SortKey
}

// ListReleases 分页查询发布。
func (s *Service) ListReleases(ctx context.Context, q ListReleasesQuery) ([]*drel.Release, int, error) {
	dq := drel.ReleaseQuery{
		ProductID: q.ProductID,
		Status:    q.Status,
		Keyword:   q.Keyword,
		Sort:      q.Sort,
	}
	if err := dq.Validate(); err != nil {
		return nil, 0, err
	}
	dq.Page.Page = q.Page
	dq.Page.PerPage = q.PerPage
	return s.repo.ListReleases(ctx, dq)
}

// ---------- Build ----------

// CreateBuildCommand 是创建构建的输入。
type CreateBuildCommand struct {
	ProductID int64
	ProjectID *int64
	Name      string
	Builds    string
	System    drel.System
	DateMs    *int64
	Builder   string
	Desc      string
	ActorID   int64
}

// Create 创建构建（无状态机，REQ-06；构建号产品内唯一 → BUILD_CONFLICT）。
func (s *Service) CreateBuild(ctx context.Context, cmd CreateBuildCommand) (*drel.Build, error) {
	now := s.clk.Now().UnixMilli()
	b := drel.NewBuild(cmd.ProductID, cmd.Name, cmd.Builds, cmd.ActorID, now)
	b.ProjectID = cmd.ProjectID
	if cmd.System != "" {
		b.System = cmd.System
	}
	b.DateMs = cmd.DateMs
	b.Builder = cmd.Builder
	b.Desc = cmd.Desc
	if err := b.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.SaveBuild(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

// GetBuild 按 ID 加载构建；不存在返回 NOT_FOUND。
func (s *Service) GetBuild(ctx context.Context, id int64) (*drel.Build, error) {
	return s.repo.GetBuildByID(ctx, id)
}

// UpdateBuildCommand 是修改构建的输入（nil 字段保持不变）。
type UpdateBuildCommand struct {
	BuildID  int64
	Name     *string
	Builds   *string
	DateMs   *int64
	Builder  *string
	SCMPath  *string
	FilePath *string
	Desc     *string
}

// Update 修改构建主数据。
func (s *Service) UpdateBuild(ctx context.Context, cmd UpdateBuildCommand) (*drel.Build, error) {
	b, err := s.repo.GetBuildByID(ctx, cmd.BuildID)
	if err != nil {
		return nil, err
	}
	if cmd.Name != nil {
		if *cmd.Name == "" {
			return nil, drel.NewInvalidError("name", "构建名称不能为空")
		}
		b.Name = *cmd.Name
	}
	if cmd.Builds != nil {
		if *cmd.Builds == "" {
			return nil, drel.NewInvalidError("builds", "构建号不能为空")
		}
		b.Builds = *cmd.Builds
	}
	if cmd.DateMs != nil {
		b.DateMs = cmd.DateMs
	}
	if cmd.Builder != nil {
		b.Builder = *cmd.Builder
	}
	if cmd.SCMPath != nil {
		b.SCMPath = *cmd.SCMPath
	}
	if cmd.FilePath != nil {
		b.FilePath = *cmd.FilePath
	}
	if cmd.Desc != nil {
		b.Desc = *cmd.Desc
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	b.UpdatedAtMs = s.clk.Now().UnixMilli()
	if err := s.repo.SaveBuild(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

// ListBuildsQuery 是构建列表查询输入。
type ListBuildsQuery struct {
	ProductID int64
	Keyword   string
	Page      int
	PerPage   int
	Sort      []drel.SortKey
}

// ListBuilds 分页查询构建。
func (s *Service) ListBuilds(ctx context.Context, q ListBuildsQuery) ([]*drel.Build, int, error) {
	dq := drel.BuildQuery{
		ProductID: q.ProductID,
		Keyword:   q.Keyword,
		Sort:      q.Sort,
	}
	if err := dq.Validate(); err != nil {
		return nil, 0, err
	}
	dq.Page.Page = q.Page
	dq.Page.PerPage = q.PerPage
	return s.repo.ListBuilds(ctx, dq)
}

// BuildExists 供测试执行域校验 test_tasks.build_id（REQ-07，DEC-07）。
func (s *Service) BuildExists(ctx context.Context, id int64) (bool, error) {
	if _, err := s.repo.GetBuildByID(ctx, id); err != nil {
		if errors.Is(err, shared.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
