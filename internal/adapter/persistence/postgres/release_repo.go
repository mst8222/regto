package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/release"
	"github.com/mst8222/regto/internal/domain/shared"
)

// ReleaseRepo 实现 release.Repository（PostgreSQL + pgx）。
// 表结构见 migrations/000017_release_domain（plans/plan_stories/releases/builds）。
type ReleaseRepo struct {
	pool *pgxpool.Pool
}

// NewReleaseRepo 构造仓储；pool 不可为 nil。
func NewReleaseRepo(pool *pgxpool.Pool) *ReleaseRepo {
	return &ReleaseRepo{pool: pool}
}

// ---------- Plan ----------

const planColumns = `id, product_id, branch_id, parent_id, title, status,
	"desc", begin_date, end_date, finished_at, closed_at, closed_reason,
	opened_by_id, opened_at, updated_at, deleted_at`

// GetPlanByID 加载计划；不存在或已软删返回 shared.ErrNotFound。
func (r *ReleaseRepo) GetPlanByID(ctx context.Context, id int64) (*release.Plan, error) {
	return r.scanOnePlan(ctx, `SELECT `+planColumns+` FROM plans WHERE id = $1 AND deleted_at IS NULL`, id)
}

func (r *ReleaseRepo) scanOnePlan(ctx context.Context, sql string, args ...any) (*release.Plan, error) {
	row := r.pool.QueryRow(ctx, sql, args...)
	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// scanPlan 扫描单行计划（scanner 抽象供单行/多行共用，与 case_repo 一致）。
func scanPlan(sc scanner) (*release.Plan, error) {
	var (
		p            release.Plan
		branchID     *int64
		parentID     *int64
		desc         *string
		beginDate    *time.Time
		endDate      *time.Time
		finishedAt   *time.Time
		closedAt     *time.Time
		closedReason *string
		openedByID   *int64
		openedAt     *time.Time
		updatedAt    time.Time
		deletedAt    *time.Time
	)
	if err := sc.Scan(&p.ID, &p.ProductID, &branchID, &parentID, &p.Title, &p.Status,
		&desc, &beginDate, &endDate, &finishedAt, &closedAt, &closedReason,
		&openedByID, &openedAt, &updatedAt, &deletedAt); err != nil {
		return nil, err
	}
	p.BranchID = branchID
	p.ParentID = parentID
	p.Desc = strOrEmpty(desc)
	p.BeginMs = msPtr(beginDate)
	p.EndMs = msPtr(endDate)
	p.FinishedAtMs = msPtr(finishedAt)
	p.ClosedAtMs = msPtr(closedAt)
	p.ClosedReason = strOrEmpty(closedReason)
	p.OpenedByID = int64OrZero(openedByID)
	p.OpenedAtMs = msOf(openedAt)
	p.UpdatedAtMs = msOf(&updatedAt)
	p.DeletedAtMs = msPtr(deletedAt)
	p.LoadedAtMs = p.UpdatedAtMs
	return &p, nil
}

// SavePlan 保存计划（INSERT 或乐观锁 UPDATE；与 case_repo.Save 同构）。
func (r *ReleaseRepo) SavePlan(ctx context.Context, p *release.Plan) error {
	if p.OpenedByID <= 0 {
		return release.NewInvalidError("opened_by_id", "执行主体不能为空（opened_by_id NOT NULL）")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if p.LoadedAtMs == 0 {
		err = tx.QueryRow(ctx, `
			INSERT INTO plans (
				product_id, branch_id, parent_id, title, status, "desc",
				begin_date, end_date, closed_reason, opened_by_id, opened_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,
				to_timestamp($7::float8/1000), to_timestamp($8::float8/1000), $9, $10,
				to_timestamp($11::float8/1000), to_timestamp($12::float8/1000))
			RETURNING id`,
			p.ProductID, p.BranchID, p.ParentID, p.Title, string(p.Status), nullIfEmpty(p.Desc),
			msArg(p.BeginMs), msArg(p.EndMs), p.ClosedReason, nullIfZero(p.OpenedByID),
			p.OpenedAtMs, p.UpdatedAtMs,
		).Scan(&p.ID)
		if err != nil {
			return fmt.Errorf("插入计划失败: %w", err)
		}
	} else {
		ct, err := tx.Exec(ctx, `
			UPDATE plans SET
				product_id=$2, branch_id=$3, parent_id=$4, title=$5, status=$6, "desc"=$7,
				begin_date=to_timestamp($8::float8/1000), end_date=to_timestamp($9::float8/1000),
				finished_at=CASE WHEN $10 THEN to_timestamp($11::float8/1000) ELSE finished_at END,
				closed_at=CASE WHEN $12 THEN to_timestamp($13::float8/1000) ELSE closed_at END,
				closed_reason=$14, updated_at=to_timestamp($15::float8/1000)
			WHERE id=$1 AND updated_at = to_timestamp($16::float8/1000) AND deleted_at IS NULL`,
			p.ID,
			p.ProductID, p.BranchID, p.ParentID, p.Title, string(p.Status), nullIfEmpty(p.Desc),
			msArg(p.BeginMs), msArg(p.EndMs),
			p.FinishedAtMs != nil, msArg(p.FinishedAtMs),
			p.ClosedAtMs != nil, msArg(p.ClosedAtMs),
			p.ClosedReason, p.UpdatedAtMs, p.LoadedAtMs,
		)
		if err != nil {
			return fmt.Errorf("更新计划失败: %w", err)
		}
		if ct.RowsAffected() == 0 {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM plans WHERE id=$1 AND deleted_at IS NULL)`, p.ID).Scan(&exists)
			if !exists {
				return shared.ErrNotFound
			}
			return shared.New("PRECONDITION_FAILED", "plan was modified concurrently",
				map[string]any{"id": p.ID})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交计划事务失败: %w", err)
	}
	return nil
}

// ListPlans 分页查询计划（筛选/排序白名单；先 count 再取页）。
func (r *ReleaseRepo) ListPlans(ctx context.Context, q release.PlanQuery) ([]*release.Plan, int, error) {
	q.Page = q.Page.Normalize()
	where := []string{"deleted_at IS NULL"}
	var args []any
	if q.ProductID > 0 {
		args = append(args, q.ProductID)
		where = append(where, "product_id = $"+strconv.Itoa(len(args)))
	}
	if q.Status != "" {
		args = append(args, string(q.Status))
		where = append(where, "status = $"+strconv.Itoa(len(args)))
	}
	if q.Keyword != "" {
		args = append(args, "%"+q.Keyword+"%")
		where = append(where, "title ILIKE $"+strconv.Itoa(len(args)))
	}
	order := buildOrderBy(q.Sort, release.AllowedPlanSortFields, "id")

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM plans WHERE `+strings.Join(where, " AND "), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计计划总数失败: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+planColumns+`
		  FROM plans
		 WHERE `+strings.Join(where, " AND ")+`
		 `+order+`
		 LIMIT `+strconv.Itoa(q.Page.PerPage)+` OFFSET `+strconv.Itoa(q.Page.Offset()),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询计划列表失败: %w", err)
	}
	defer rows.Close()
	// 空结果返回空切片而非 nil：JSON 序列化为 [] 而非 null（与既有域一致）。
	out := make([]*release.Plan, 0, q.Page.PerPage)
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历计划列表失败: %w", err)
	}
	return out, total, nil
}

// ---------- PlanStory ----------

// AddPlanStory 把需求加入计划；重复关联返回 ErrPlanStoryConflict（AC-03）。
func (r *ReleaseRepo) AddPlanStory(ctx context.Context, planID, storyID int64, order int) error {
	ct, err := r.pool.Exec(ctx, `
		INSERT INTO plan_stories (plan_id, story_id, "order")
		VALUES ($1, $2, $3)
		ON CONFLICT (plan_id, story_id) WHERE deleted_at IS NULL DO NOTHING`,
		planID, storyID, order)
	if err != nil {
		return fmt.Errorf("关联计划需求失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return release.ErrPlanStoryConflict
	}
	return nil
}

// ListPlanStories 按 order 升序返回计划下的关联（REQ-03 列表查询）。
func (r *ReleaseRepo) ListPlanStories(ctx context.Context, planID int64) ([]release.PlanStory, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, plan_id, story_id, "order"
		  FROM plan_stories
		 WHERE plan_id = $1 AND deleted_at IS NULL
		 ORDER BY "order", id`, planID)
	if err != nil {
		return nil, fmt.Errorf("查询计划需求失败: %w", err)
	}
	defer rows.Close()
	out := make([]release.PlanStory, 0)
	for rows.Next() {
		var ps release.PlanStory
		if err := rows.Scan(&ps.ID, &ps.PlanID, &ps.StoryID, &ps.Order); err != nil {
			return nil, fmt.Errorf("扫描计划需求失败: %w", err)
		}
		out = append(out, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历计划需求失败: %w", err)
	}
	return out, nil
}

// ---------- Release ----------

const releaseColumns = `id, product_id, branch_id, name, system, marker, status,
	date, released_at, story_ids, bug_ids, build_ids,
	mailto, notify, "desc", opened_by_id, opened_at, updated_at, deleted_at`

// GetReleaseByID 加载发布；不存在或已软删返回 shared.ErrNotFound。
func (r *ReleaseRepo) GetReleaseByID(ctx context.Context, id int64) (*release.Release, error) {
	return r.scanOneRelease(ctx, `SELECT `+releaseColumns+` FROM releases WHERE id = $1 AND deleted_at IS NULL`, id)
}

func (r *ReleaseRepo) scanOneRelease(ctx context.Context, sql string, args ...any) (*release.Release, error) {
	row := r.pool.QueryRow(ctx, sql, args...)
	rel, err := scanRelease(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, err
	}
	return rel, nil
}

func scanRelease(sc scanner) (*release.Release, error) {
	var (
		rel        release.Release
		branchID   *int64
		date       *time.Time
		releasedAt *time.Time
		storyIDs   *string
		bugIDs     *string
		buildIDs   *string
		mailto     *string
		notify     *string
		desc       *string
		openedByID *int64
		openedAt   *time.Time
		updatedAt  time.Time
		deletedAt  *time.Time
	)
	if err := sc.Scan(&rel.ID, &rel.ProductID, &branchID, &rel.Name, &rel.System, &rel.Marker, &rel.Status,
		&date, &releasedAt, &storyIDs, &bugIDs, &buildIDs,
		&mailto, &notify, &desc, &openedByID, &openedAt, &updatedAt, &deletedAt); err != nil {
		return nil, err
	}
	rel.BranchID = branchID
	rel.DateMs = msPtr(date)
	rel.ReleasedAtMs = msPtr(releasedAt)
	rel.StoryIDs = strOrEmpty(storyIDs)
	rel.BugIDs = strOrEmpty(bugIDs)
	rel.BuildIDs = strOrEmpty(buildIDs)
	rel.Mailto = strOrEmpty(mailto)
	rel.Notify = strOrEmpty(notify)
	rel.Desc = strOrEmpty(desc)
	rel.OpenedByID = int64OrZero(openedByID)
	rel.OpenedAtMs = msOf(openedAt)
	rel.UpdatedAtMs = msOf(&updatedAt)
	rel.DeletedAtMs = msPtr(deletedAt)
	rel.LoadedAtMs = rel.UpdatedAtMs
	return &rel, nil
}

// SaveRelease 保存发布（INSERT 或乐观锁 UPDATE）。
func (r *ReleaseRepo) SaveRelease(ctx context.Context, rel *release.Release) error {
	if rel.OpenedByID <= 0 {
		return release.NewInvalidError("opened_by_id", "执行主体不能为空（opened_by_id NOT NULL）")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if rel.LoadedAtMs == 0 {
		err = tx.QueryRow(ctx, `
			INSERT INTO releases (
				product_id, branch_id, name, system, marker, status, date,
				story_ids, bug_ids, build_ids, mailto, notify, "desc",
				opened_by_id, opened_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,
				to_timestamp($7::float8/1000), $8,$9,$10,$11,$12,$13, $14,
				to_timestamp($15::float8/1000), to_timestamp($16::float8/1000))
			RETURNING id`,
			rel.ProductID, rel.BranchID, rel.Name, string(rel.System), rel.Marker, string(rel.Status),
			msArg(rel.DateMs),
			rel.StoryIDs, rel.BugIDs, rel.BuildIDs, rel.Mailto, rel.Notify, nullIfEmpty(rel.Desc),
			nullIfZero(rel.OpenedByID), rel.OpenedAtMs, rel.UpdatedAtMs,
		).Scan(&rel.ID)
		if err != nil {
			return fmt.Errorf("插入发布失败: %w", err)
		}
	} else {
		ct, err := tx.Exec(ctx, `
			UPDATE releases SET
				product_id=$2, branch_id=$3, name=$4, system=$5, marker=$6, status=$7,
				date=to_timestamp($8::float8/1000),
				released_at=CASE WHEN $9 THEN to_timestamp($10::float8/1000) ELSE released_at END,
				story_ids=$11, bug_ids=$12, build_ids=$13, mailto=$14, notify=$15, "desc"=$16,
				updated_at=to_timestamp($17::float8/1000)
			WHERE id=$1 AND updated_at = to_timestamp($18::float8/1000) AND deleted_at IS NULL`,
			rel.ID,
			rel.ProductID, rel.BranchID, rel.Name, string(rel.System), rel.Marker, string(rel.Status),
			msArg(rel.DateMs),
			rel.ReleasedAtMs != nil, msArg(rel.ReleasedAtMs),
			rel.StoryIDs, rel.BugIDs, rel.BuildIDs, rel.Mailto, rel.Notify, nullIfEmpty(rel.Desc),
			rel.UpdatedAtMs, rel.LoadedAtMs,
		)
		if err != nil {
			return fmt.Errorf("更新发布失败: %w", err)
		}
		if ct.RowsAffected() == 0 {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM releases WHERE id=$1 AND deleted_at IS NULL)`, rel.ID).Scan(&exists)
			if !exists {
				return shared.ErrNotFound
			}
			return shared.New("PRECONDITION_FAILED", "release was modified concurrently",
				map[string]any{"id": rel.ID})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交发布事务失败: %w", err)
	}
	return nil
}

// ListReleases 分页查询发布（筛选/排序白名单）。
func (r *ReleaseRepo) ListReleases(ctx context.Context, q release.ReleaseQuery) ([]*release.Release, int, error) {
	q.Page = q.Page.Normalize()
	where := []string{"deleted_at IS NULL"}
	var args []any
	if q.ProductID > 0 {
		args = append(args, q.ProductID)
		where = append(where, "product_id = $"+strconv.Itoa(len(args)))
	}
	if q.Status != "" {
		args = append(args, string(q.Status))
		where = append(where, "status = $"+strconv.Itoa(len(args)))
	}
	if q.Keyword != "" {
		args = append(args, "%"+q.Keyword+"%")
		where = append(where, "name ILIKE $"+strconv.Itoa(len(args)))
	}
	order := buildOrderBy(q.Sort, release.AllowedReleaseSortFields, "id")

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM releases WHERE `+strings.Join(where, " AND "), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计发布总数失败: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+releaseColumns+`
		  FROM releases
		 WHERE `+strings.Join(where, " AND ")+`
		 `+order+`
		 LIMIT `+strconv.Itoa(q.Page.PerPage)+` OFFSET `+strconv.Itoa(q.Page.Offset()),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询发布列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]*release.Release, 0, q.Page.PerPage)
	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历发布列表失败: %w", err)
	}
	return out, total, nil
}

// ---------- Build ----------

const buildColumns = `id, product_id, project_id, name, builds, system,
	scm_path, file_path, date, builder, story_ids, bug_ids, "desc",
	created_by_id, created_at, updated_at, deleted_at`

// GetBuildByID 加载构建；不存在或已软删返回 shared.ErrNotFound。
func (r *ReleaseRepo) GetBuildByID(ctx context.Context, id int64) (*release.Build, error) {
	return r.scanOneBuild(ctx, `SELECT `+buildColumns+` FROM builds WHERE id = $1 AND deleted_at IS NULL`, id)
}

func (r *ReleaseRepo) scanOneBuild(ctx context.Context, sql string, args ...any) (*release.Build, error) {
	row := r.pool.QueryRow(ctx, sql, args...)
	b, err := scanBuild(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, err
	}
	return b, nil
}

func scanBuild(sc scanner) (*release.Build, error) {
	var (
		b           release.Build
		projectID   *int64
		scmPath     *string
		filePath    *string
		date        *time.Time
		builder     *string
		storyIDs    *string
		bugIDs      *string
		desc        *string
		createdByID *int64
		createdAt   *time.Time
		updatedAt   time.Time
		deletedAt   *time.Time
	)
	if err := sc.Scan(&b.ID, &b.ProductID, &projectID, &b.Name, &b.Builds, &b.System,
		&scmPath, &filePath, &date, &builder, &storyIDs, &bugIDs, &desc,
		&createdByID, &createdAt, &updatedAt, &deletedAt); err != nil {
		return nil, err
	}
	b.ProjectID = projectID
	b.SCMPath = strOrEmpty(scmPath)
	b.FilePath = strOrEmpty(filePath)
	b.DateMs = msPtr(date)
	b.Builder = strOrEmpty(builder)
	b.StoryIDs = strOrEmpty(storyIDs)
	b.BugIDs = strOrEmpty(bugIDs)
	b.Desc = strOrEmpty(desc)
	b.CreatedByID = int64OrZero(createdByID)
	b.CreatedAtMs = msOf(createdAt)
	b.UpdatedAtMs = msOf(&updatedAt)
	b.DeletedAtMs = msPtr(deletedAt)
	b.LoadedAtMs = b.UpdatedAtMs
	return &b, nil
}

// SaveBuild 保存构建；同产品下构建号重复返回 ErrBuildConflict（AC-04）。
func (r *ReleaseRepo) SaveBuild(ctx context.Context, b *release.Build) error {
	if b.CreatedByID <= 0 {
		return release.NewInvalidError("created_by_id", "执行主体不能为空（created_by_id NOT NULL）")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if b.LoadedAtMs == 0 {
		err = tx.QueryRow(ctx, `
			INSERT INTO builds (
				product_id, project_id, name, builds, system,
				scm_path, file_path, date, builder, story_ids, bug_ids, "desc",
				created_by_id, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,
				to_timestamp($8::float8/1000), $9,$10,$11,$12, $13,
				to_timestamp($14::float8/1000), to_timestamp($15::float8/1000))
			RETURNING id`,
			b.ProductID, b.ProjectID, b.Name, b.Builds, string(b.System),
			b.SCMPath, b.FilePath, msArg(b.DateMs), b.Builder, b.StoryIDs, b.BugIDs, nullIfEmpty(b.Desc),
			nullIfZero(b.CreatedByID), b.CreatedAtMs, b.UpdatedAtMs,
		).Scan(&b.ID)
		if err != nil {
			if isUniqueViolation(err) {
				return release.ErrBuildConflict
			}
			return fmt.Errorf("插入构建失败: %w", err)
		}
	} else {
		ct, err := tx.Exec(ctx, `
			UPDATE builds SET
				product_id=$2, project_id=$3, name=$4, builds=$5, system=$6,
				scm_path=$7, file_path=$8, date=to_timestamp($9::float8/1000), builder=$10,
				story_ids=$11, bug_ids=$12, "desc"=$13, updated_at=to_timestamp($14::float8/1000)
			WHERE id=$1 AND updated_at = to_timestamp($15::float8/1000) AND deleted_at IS NULL`,
			b.ID,
			b.ProductID, b.ProjectID, b.Name, b.Builds, string(b.System),
			b.SCMPath, b.FilePath, msArg(b.DateMs), b.Builder,
			b.StoryIDs, b.BugIDs, nullIfEmpty(b.Desc), b.UpdatedAtMs, b.LoadedAtMs,
		)
		if err != nil {
			if isUniqueViolation(err) {
				return release.ErrBuildConflict
			}
			return fmt.Errorf("更新构建失败: %w", err)
		}
		if ct.RowsAffected() == 0 {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM builds WHERE id=$1 AND deleted_at IS NULL)`, b.ID).Scan(&exists)
			if !exists {
				return shared.ErrNotFound
			}
			return shared.New("PRECONDITION_FAILED", "build was modified concurrently",
				map[string]any{"id": b.ID})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交构建事务失败: %w", err)
	}
	return nil
}

// ListBuilds 分页查询构建（筛选/排序白名单）。
func (r *ReleaseRepo) ListBuilds(ctx context.Context, q release.BuildQuery) ([]*release.Build, int, error) {
	q.Page = q.Page.Normalize()
	where := []string{"deleted_at IS NULL"}
	var args []any
	if q.ProductID > 0 {
		args = append(args, q.ProductID)
		where = append(where, "product_id = $"+strconv.Itoa(len(args)))
	}
	if q.Keyword != "" {
		args = append(args, "%"+q.Keyword+"%")
		where = append(where, "name ILIKE $"+strconv.Itoa(len(args)))
	}
	order := buildOrderBy(q.Sort, release.AllowedBuildSortFields, "id")

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM builds WHERE `+strings.Join(where, " AND "), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计构建总数失败: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT `+buildColumns+`
		  FROM builds
		 WHERE `+strings.Join(where, " AND ")+`
		 `+order+`
		 LIMIT `+strconv.Itoa(q.Page.PerPage)+` OFFSET `+strconv.Itoa(q.Page.Offset()),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询构建列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]*release.Build, 0, q.Page.PerPage)
	for rows.Next() {
		b, err := scanBuild(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历构建列表失败: %w", err)
	}
	return out, total, nil
}

// msArg 把毫秒时间戳转为 SQL 参数（nil → NULL；to_timestamp 由调用方套）。
func msArg(ms *int64) any {
	if ms == nil {
		return nil
	}
	return *ms
}

// buildOrderBy 把排序键编译为 ORDER BY 子句（白名单外字段忽略；
// 全部无效时回落 fallback 字段 DESC，与 BuildCaseOrderBy 同策略）。
func buildOrderBy(keys []release.SortKey, allowed []string, fallback string) string {
	cols := make([]string, 0, len(keys))
	for _, k := range keys {
		if !release.ValidSortField(k.Field, allowed) {
			continue
		}
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		cols = append(cols, k.Field+" "+dir)
	}
	if len(cols) == 0 {
		return "ORDER BY " + fallback + " DESC"
	}
	return "ORDER BY " + strings.Join(cols, ", ")
}

// isUniqueViolation 判断是否为唯一约束冲突（23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
