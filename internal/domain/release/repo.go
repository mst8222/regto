package release

import (
	"context"

	"github.com/mst8222/regto/internal/domain/shared"
)

// Repository 是发布域的持久化契约（adapter/persistence/postgres 实现）。
// 与 story 域同构：Save 单事务写聚合；查询方法返回聚合或领域错误。
type Repository interface {
	// ---------- Plan ----------

	// GetPlanByID 按 ID 加载计划；不存在返回 shared.ErrNotFound。
	GetPlanByID(ctx context.Context, id int64) (*Plan, error)
	// SavePlan 保存计划（INSERT 或乐观锁 UPDATE）。LoadedAtMs 为 0 走 INSERT 分支。
	SavePlan(ctx context.Context, p *Plan) error
	// ListPlans 分页查询计划（筛选：产品/状态/标题关键字）。
	ListPlans(ctx context.Context, q PlanQuery) ([]*Plan, int, error)

	// ---------- PlanStory ----------

	// AddPlanStory 把需求加入计划；重复关联返回 ErrPlanStoryConflict（AC-03）。
	AddPlanStory(ctx context.Context, planID, storyID int64, order int) error
	// ListPlanStories 按 order 升序返回计划下的需求 ID 列表。
	ListPlanStories(ctx context.Context, planID int64) ([]PlanStory, error)

	// ---------- Release ----------

	// GetReleaseByID 按 ID 加载发布；不存在返回 shared.ErrNotFound。
	GetReleaseByID(ctx context.Context, id int64) (*Release, error)
	// SaveRelease 保存发布（INSERT 或乐观锁 UPDATE）。
	SaveRelease(ctx context.Context, r *Release) error
	// ListReleases 分页查询发布（筛选：产品/状态/名称关键字）。
	ListReleases(ctx context.Context, q ReleaseQuery) ([]*Release, int, error)

	// ---------- Build ----------

	// GetBuildByID 按 ID 加载构建；不存在返回 shared.ErrNotFound。
	GetBuildByID(ctx context.Context, id int64) (*Build, error)
	// SaveBuild 保存构建；同产品下构建号重复返回 ErrBuildConflict（AC-04）。
	SaveBuild(ctx context.Context, b *Build) error
	// ListBuilds 分页查询构建（筛选：产品）。
	ListBuilds(ctx context.Context, q BuildQuery) ([]*Build, int, error)
}

// PlanQuery 是计划列表查询对象（与 story.StoryQuery 同构）。
type PlanQuery struct {
	ProductID int64
	Status    PlanStatus
	Keyword   string
	Page      shared.Page
	Sort      []SortKey
}

// Validate 校验查询对象（非法值返回 INVALID_FIELD）。
func (q PlanQuery) Validate() error {
	if q.Status != "" && !q.Status.Valid() {
		return NewInvalidError("status", "计划状态非法: "+string(q.Status))
	}
	if q.Page.PerPage > shared.MaxPerPage {
		return NewInvalidError("per_page", "每页条数超过上限")
	}
	return nil
}

// ReleaseQuery 是发布列表查询对象。
type ReleaseQuery struct {
	ProductID int64
	Status    ReleaseStatus
	Keyword   string
	Page      shared.Page
	Sort      []SortKey
}

// Validate 校验查询对象。
func (q ReleaseQuery) Validate() error {
	if q.Status != "" && !q.Status.Valid() {
		return NewInvalidError("status", "发布状态非法: "+string(q.Status))
	}
	if q.Page.PerPage > shared.MaxPerPage {
		return NewInvalidError("per_page", "每页条数超过上限")
	}
	return nil
}

// BuildQuery 是构建列表查询对象。
type BuildQuery struct {
	ProductID int64
	Keyword   string
	Page      shared.Page
	Sort      []SortKey
}

// Validate 校验查询对象。
func (q BuildQuery) Validate() error {
	if q.Page.PerPage > shared.MaxPerPage {
		return NewInvalidError("per_page", "每页条数超过上限")
	}
	return nil
}

// SortKey 排序键（三实体共用）。
type SortKey struct {
	Field string
	Desc  bool
}

// AllowedPlanSortFields 是计划排序白名单（防注入 + 可预测）。
var AllowedPlanSortFields = []string{"id", "title", "status", "begin", "end", "created_at", "updated_at"}

// AllowedReleaseSortFields 是发布排序白名单。
var AllowedReleaseSortFields = []string{"id", "name", "status", "date", "created_at", "updated_at"}

// AllowedBuildSortFields 是构建排序白名单。
var AllowedBuildSortFields = []string{"id", "name", "builds", "date", "created_at", "updated_at"}

// ValidSortField 报告字段是否在给定白名单内。
func ValidSortField(f string, allowed []string) bool {
	for _, a := range allowed {
		if f == a {
			return true
		}
	}
	return false
}
