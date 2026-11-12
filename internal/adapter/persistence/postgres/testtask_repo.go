package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/shared"
	dtt "github.com/mst8222/regto/internal/domain/testtask"
)

// TestTaskRepo 实现 testtask.Repository（PostgreSQL + pgx）。
// 表结构见 migrations/000013_testtask。
type TestTaskRepo struct {
	pool *pgxpool.Pool
}

// NewTestTaskRepo 构造仓储；pool 不可为 nil。
func NewTestTaskRepo(pool *pgxpool.Pool) *TestTaskRepo {
	return &TestTaskRepo{pool: pool}
}

const taskColumns = `id, name, product_id, project_id, build_id, type, owner_id, pri,
	begin_date, end_date, status, "desc", opened_by_id, opened_at, updated_at, deleted_at`

// GetByID 加载测试单；不存在或已软删返回 shared.ErrNotFound。
func (r *TestTaskRepo) GetByID(ctx context.Context, id int64) (*dtt.TestTask, error) {
	var (
		t         dtt.TestTask
		projectID *int64
		buildID   *int64
		beginDate *time.Time
		endDate   *time.Time
		openedAt  time.Time
		updatedAt time.Time
		deletedAt *time.Time
		desc      *string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM test_tasks WHERE id = $1 AND deleted_at IS NULL`, id,
	).Scan(
		&t.ID, &t.Name, &t.ProductID, &projectID, &buildID, &t.Type, &t.OwnerID, &t.Pri,
		&beginDate, &endDate, &t.Status, &desc, &t.OpenedByID, &openedAt, &updatedAt, &deletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, fmt.Errorf("查询测试单失败: %w", err)
	}
	t.ProjectID = projectID
	t.BuildID = buildID
	t.Desc = strOrEmpty(desc)
	t.BeginMs = dateMs(beginDate)
	t.EndMs = dateMs(endDate)
	t.OpenedAtMs = msOf(&openedAt)
	t.UpdatedAtMs = msOf(&updatedAt)
	t.DeletedAtMs = msPtr(deletedAt)
	t.LoadedAtMs = t.UpdatedAtMs
	return &t, nil
}

// Save 保存测试单（INSERT 或乐观锁 UPDATE）。
func (r *TestTaskRepo) Save(ctx context.Context, t *dtt.TestTask) error {
	if t.LoadedAtMs == 0 {
		err := r.pool.QueryRow(ctx, `
			INSERT INTO test_tasks (
				name, product_id, project_id, build_id, type, owner_id, pri,
				begin_date, end_date, status, "desc", opened_by_id, opened_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,
				to_timestamp($8::float8/1000)::date, to_timestamp($9::float8/1000)::date,
				$10,$11,$12, to_timestamp($13::float8/1000), to_timestamp($14::float8/1000))
			RETURNING id`,
			t.Name, t.ProductID, t.ProjectID, t.BuildID, t.Type, t.OwnerID, t.Pri,
			nullMs(t.BeginMs), nullMs(t.EndMs),
			string(t.Status), nullIfEmpty(t.Desc), t.OpenedByID, t.OpenedAtMs, t.UpdatedAtMs,
		).Scan(&t.ID)
		if err != nil {
			return fmt.Errorf("插入测试单失败: %w", err)
		}
		return nil
	}
	ct, err := r.pool.Exec(ctx, `
		UPDATE test_tasks SET
			name=$2, product_id=$3, project_id=$4, build_id=$5, type=$6, owner_id=$7, pri=$8,
			begin_date=to_timestamp($9::float8/1000)::date, end_date=to_timestamp($10::float8/1000)::date,
			status=$11, "desc"=$12, updated_at=to_timestamp($13::float8/1000)
		WHERE id=$1 AND updated_at = to_timestamp($14::float8/1000) AND deleted_at IS NULL`,
		t.ID,
		t.Name, t.ProductID, t.ProjectID, t.BuildID, t.Type, t.OwnerID, t.Pri,
		nullMs(t.BeginMs), nullMs(t.EndMs),
		string(t.Status), nullIfEmpty(t.Desc), t.UpdatedAtMs, t.LoadedAtMs,
	)
	if err != nil {
		return fmt.Errorf("更新测试单失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		var exists bool
		_ = r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM test_tasks WHERE id=$1 AND deleted_at IS NULL)`, t.ID).Scan(&exists)
		if !exists {
			return shared.ErrNotFound
		}
		return shared.New("PRECONDITION_FAILED", "test task was modified concurrently",
			map[string]any{"id": t.ID})
	}
	return nil
}

// List 分页查询测试单（窗口函数一次取总数）。
func (r *TestTaskRepo) List(ctx context.Context, q dtt.TaskQuery) ([]*dtt.TestTask, int, error) {
	perPage := q.Page.PerPage
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}
	page := q.Page.Page
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * perPage

	var (
		conds []string
		args  []any
	)
	conds = append(conds, "deleted_at IS NULL")
	if q.ProductID > 0 {
		args = append(args, q.ProductID)
		conds = append(conds, "product_id = $"+strconv.Itoa(len(args)))
	}
	if q.Status != "" {
		args = append(args, string(q.Status))
		conds = append(conds, "status = $"+strconv.Itoa(len(args)))
	}
	if q.Keyword != "" {
		args = append(args, "%"+escapeLike(q.Keyword)+"%")
		conds = append(conds, "name ILIKE $"+strconv.Itoa(len(args)))
	}
	where := "WHERE " + joinAnd(conds)

	rows, err := r.pool.Query(ctx, `
		SELECT `+taskColumns+`, COUNT(*) OVER() AS total_count
		  FROM test_tasks
		 `+where+`
		 ORDER BY id DESC
		 LIMIT `+strconv.Itoa(perPage)+` OFFSET `+strconv.Itoa(offset),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询测试单列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]*dtt.TestTask, 0, perPage)
	var total int
	for rows.Next() {
		var (
			t         dtt.TestTask
			projectID *int64
			buildID   *int64
			beginDate *time.Time
			endDate   *time.Time
			openedAt  time.Time
			updatedAt time.Time
			deletedAt *time.Time
			desc      *string
		)
		if err := rows.Scan(
			&t.ID, &t.Name, &t.ProductID, &projectID, &buildID, &t.Type, &t.OwnerID, &t.Pri,
			&beginDate, &endDate, &t.Status, &desc, &t.OpenedByID, &openedAt, &updatedAt, &deletedAt,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("扫描测试单失败: %w", err)
		}
		t.ProjectID = projectID
		t.BuildID = buildID
		t.Desc = strOrEmpty(desc)
		t.BeginMs = dateMs(beginDate)
		t.EndMs = dateMs(endDate)
		t.OpenedAtMs = msOf(&openedAt)
		t.UpdatedAtMs = msOf(&updatedAt)
		t.DeletedAtMs = msPtr(deletedAt)
		t.LoadedAtMs = t.UpdatedAtMs
		out = append(out, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历测试单列表失败: %w", err)
	}
	return out, total, nil
}

// AddCases 把用例加入测试单（锁定当前 case_version；重复加入由唯一键忽略）。
func (r *TestTaskRepo) AddCases(ctx context.Context, taskID int64, caseIDs []int64, caseVersions []int) (int, error) {
	added := 0
	for i, cid := range caseIDs {
		ver := 1
		if i < len(caseVersions) {
			ver = caseVersions[i]
		}
		ct, err := r.pool.Exec(ctx, `
			INSERT INTO test_task_cases (test_task_id, case_id, case_version)
			VALUES ($1,$2,$3)
			ON CONFLICT (test_task_id, case_id) WHERE deleted_at IS NULL DO NOTHING`,
			taskID, cid, ver)
		if err != nil {
			return added, fmt.Errorf("加入用例 %d 失败: %w", cid, err)
		}
		added += int(ct.RowsAffected())
	}
	return added, nil
}

// ListCases 列出测试单内的用例及执行状态。
func (r *TestTaskRepo) ListCases(ctx context.Context, taskID int64) ([]dtt.TaskCase, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ttc.id, ttc.test_task_id, ttc.case_id, ttc.case_version,
		       c.title, ttc.status, COALESCE(ttc.last_run_result, '')
		  FROM test_task_cases ttc
		  JOIN cases c ON c.id = ttc.case_id AND c.deleted_at IS NULL
		 WHERE ttc.test_task_id = $1 AND ttc.deleted_at IS NULL
		 ORDER BY ttc.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("查询测试单用例失败: %w", err)
	}
	defer rows.Close()

	out := make([]dtt.TaskCase, 0)
	for rows.Next() {
		var tc dtt.TaskCase
		if err := rows.Scan(&tc.ID, &tc.TestTaskID, &tc.CaseID, &tc.CaseVersion,
			&tc.CaseTitle, &tc.Status, &tc.LastRunResult); err != nil {
			return nil, fmt.Errorf("扫描测试单用例失败: %w", err)
		}
		out = append(out, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历测试单用例失败: %w", err)
	}
	return out, nil
}

// NextRunNo 取该测试单当前最大 run_no + 1（DEC-07 缺省轮次）。
func (r *TestTaskRepo) NextRunNo(ctx context.Context, taskID int64) (int, error) {
	var next int
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(max(run_no), 0) + 1 FROM test_results WHERE test_task_id = $1`,
		taskID).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("计算 run_no 失败: %w", err)
	}
	return next, nil
}

// SaveResult 写入执行结果（幂等键 task+case+run_no）并更新用例行的最新结果。
// 应用账号无 DELETE 权限，故「同一 run_no 重跑」用 ON CONFLICT DO UPDATE 覆盖（REQ-07）。
func (r *TestTaskRepo) SaveResult(ctx context.Context, res dtt.RunResult) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var stepJSON any
	if res.StepResults != "" {
		stepJSON = []byte(res.StepResults)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO test_results (
			test_task_id, case_id, case_version, run_no, result,
			step_results, duration_ms, actor_type, actor_id, trace_id, raw_output, executed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, to_timestamp($12::float8/1000))
		ON CONFLICT (test_task_id, case_id, run_no) DO UPDATE SET
			case_version = EXCLUDED.case_version,
			result = EXCLUDED.result,
			step_results = EXCLUDED.step_results,
			duration_ms = EXCLUDED.duration_ms,
			actor_type = EXCLUDED.actor_type,
			actor_id = EXCLUDED.actor_id,
			trace_id = EXCLUDED.trace_id,
			raw_output = EXCLUDED.raw_output,
			executed_at = EXCLUDED.executed_at`,
		res.TestTaskID, res.CaseID, res.CaseVersion, res.RunNo, string(res.Result),
		stepJSON, res.DurationMs, res.ActorType, res.ActorID, nullIfEmpty(res.TraceID),
		nullIfEmpty(res.RawOutput), res.ExecutedAtMs)
	if err != nil {
		return fmt.Errorf("写入执行结果失败: %w", err)
	}

	// 同步用例行的最新结果（test_task_cases.last_run_result 是冗余展示列）
	_, err = tx.Exec(ctx, `
		UPDATE test_task_cases SET
			last_run_result = $3, last_run_at = to_timestamp($4::float8/1000),
			status = 'done', updated_at = now()
		WHERE test_task_id = $1 AND case_id = $2 AND deleted_at IS NULL`,
		res.TestTaskID, res.CaseID, string(res.Result), res.ExecutedAtMs)
	if err != nil {
		return fmt.Errorf("更新测试单用例状态失败: %w", err)
	}

	return tx.Commit(ctx)
}

// ListResults 分页查询测试单的执行结果。
func (r *TestTaskRepo) ListResults(ctx context.Context, taskID int64, page dtt.Page) ([]dtt.RunResult, int, error) {
	perPage := page.PerPage
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}
	p := page.Page
	if p < 1 {
		p = 1
	}
	offset := (p - 1) * perPage

	rows, err := r.pool.Query(ctx, `
		SELECT id, test_task_id, case_id, case_version, run_no, result,
		       COALESCE(step_results::text, ''), duration_ms, actor_type, actor_id,
		       COALESCE(trace_id, ''), COALESCE(raw_output, ''), executed_at,
		       COUNT(*) OVER() AS total_count
		  FROM test_results
		 WHERE test_task_id = $1
		 ORDER BY id DESC
		 LIMIT `+strconv.Itoa(perPage)+` OFFSET `+strconv.Itoa(offset), taskID)
	if err != nil {
		return nil, 0, fmt.Errorf("查询执行结果失败: %w", err)
	}
	defer rows.Close()

	out := make([]dtt.RunResult, 0, perPage)
	var total int
	for rows.Next() {
		var (
			res        dtt.RunResult
			executedAt time.Time
		)
		if err := rows.Scan(
			&res.ID, &res.TestTaskID, &res.CaseID, &res.CaseVersion, &res.RunNo, &res.Result,
			&res.StepResults, &res.DurationMs, &res.ActorType, &res.ActorID,
			&res.TraceID, &res.RawOutput, &executedAt, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("扫描执行结果失败: %w", err)
		}
		res.ExecutedAtMs = msOf(&executedAt)
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历执行结果失败: %w", err)
	}
	return out, total, nil
}

// CountResults 统计测试单内各结果的条数（报表与 CLI 汇总）。
func (r *TestTaskRepo) CountResults(ctx context.Context, taskID int64) (map[dtt.Result]int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT result, count(*) FROM test_results
		 WHERE test_task_id = $1 AND run_no = (
			-- 只统计每个用例的最新一轮（同一用例多次执行取最大 run_no）
			SELECT max(run_no) FROM test_results r2
			 WHERE r2.test_task_id = test_results.test_task_id AND r2.case_id = test_results.case_id
		 )
		 GROUP BY result`, taskID)
	if err != nil {
		return nil, fmt.Errorf("统计执行结果失败: %w", err)
	}
	defer rows.Close()

	out := map[dtt.Result]int{}
	for rows.Next() {
		var (
			res dtt.Result
			n   int
		)
		if err := rows.Scan(&res, &n); err != nil {
			return nil, fmt.Errorf("扫描结果统计失败: %w", err)
		}
		out[res] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历结果统计失败: %w", err)
	}
	return out, nil
}

// ---- 辅助 ----

// dateMs 把 DATE 转为当日零点的 unix 毫秒；nil 返回 nil。
func dateMs(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

// nullMs 把毫秒指针转为数据库时间参数；nil 返回 nil。
func nullMs(v *int64) *time.Time {
	if v == nil || *v == 0 {
		return nil
	}
	t := time.UnixMilli(*v).UTC()
	return &t
}

// joinAnd 用 AND 连接条件片段。
func joinAnd(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}
