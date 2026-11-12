package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/shared"
	"github.com/mst8222/regto/internal/domain/testcase"
)

// CaseRepo 实现 testcase.Repository（PostgreSQL + pgx）。
// 表结构见 migrations/000012_casedomain（cases 由 000008 扩展而来）。
type CaseRepo struct {
	pool *pgxpool.Pool
}

// NewCaseRepo 构造仓储；pool 不可为 nil。
func NewCaseRepo(pool *pgxpool.Pool) *CaseRepo {
	return &CaseRepo{pool: pool}
}

const caseColumns = `id, product_id, module_id, story_id, story_version, branch_id,
	title, precondition, keywords, pri, type, status, version,
	source_ref, source_hash, external_id, suite, automation, priority, spec_doc_id,
	last_runner, last_run_result, last_run_at,
	opened_by_id, opened_at, updated_at, deleted_at`

// scanner 抽象 pgx.Row 与 pgx.Rows 的 Scan，供单行/多行查询共用同一映射。
type scanner interface {
	Scan(dest ...any) error
}

// GetByID 加载用例聚合（含全部步骤）。不存在或已软删返回 shared.ErrNotFound。
func (r *CaseRepo) GetByID(ctx context.Context, id int64) (*testcase.Case, error) {
	c, err := r.scanOne(ctx, `SELECT `+caseColumns+` FROM cases WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	steps, err := r.loadSteps(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	c.Steps = steps
	return c, nil
}

// GetBySourceRef 按 SpecDD 来源引用加载（AC-04 冲突检测）。
func (r *CaseRepo) GetBySourceRef(ctx context.Context, sourceRef string) (*testcase.Case, error) {
	return r.scanOne(ctx,
		`SELECT `+caseColumns+` FROM cases WHERE source_ref = $1 AND deleted_at IS NULL`, sourceRef)
}

// FindByExternalID 按 (product_id, external_id) 查找（go test -json 匹配键）。
func (r *CaseRepo) FindByExternalID(ctx context.Context, productID int64, externalID string) (*testcase.Case, error) {
	return r.scanOne(ctx,
		`SELECT `+caseColumns+` FROM cases WHERE product_id = $1 AND external_id = $2 AND deleted_at IS NULL`,
		productID, externalID)
}

// scanOne 执行单行查询并映射为用例（三个查询入口共用）。
func (r *CaseRepo) scanOne(ctx context.Context, sql string, args ...any) (*testcase.Case, error) {
	row := r.pool.QueryRow(ctx, sql, args...)
	c, err := scanCase(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, shared.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// scanCase 把一行映射为用例（caseColumns 顺序必须与 dest 一一对应）。
// extra 是追加在用例列之后的扫描目标（如 COUNT(*) OVER() 的 total）。
func scanCase(sc scanner, extra ...any) (*testcase.Case, error) {
	var (
		c            testcase.Case
		moduleID     *int64
		storyID      *int64
		branchID     *int64
		sourceRef    *string
		sourceHash   *string
		externalID   *string
		suite        *string
		automation   *string
		priority     *string
		specDocID    *string
		lastRunner   *string
		lastRunRes   *string
		lastRunAt    *time.Time
		openedByID   *int64
		openedAt     *time.Time
		updatedAt    time.Time
		deletedAt    *time.Time
		precondition *string
		keywords     *string
	)
	dest := []any{
		&c.ID, &c.ProductID, &moduleID, &storyID, &c.StoryVersion, &branchID,
		&c.Title, &precondition, &keywords, &c.Pri, &c.Type, &c.Status, &c.Version,
		&sourceRef, &sourceHash, &externalID, &suite, &automation, &priority, &specDocID,
		&lastRunner, &lastRunRes, &lastRunAt, &openedByID, &openedAt, &updatedAt, &deletedAt,
	}
	dest = append(dest, extra...)
	if err := sc.Scan(dest...); err != nil {
		return nil, err
	}
	c.ModuleID = moduleID
	c.StoryID = storyID
	c.BranchID = branchID
	c.OpenedByID = int64OrZero(openedByID) // 0 = 未知主体（SpecDD/自动建档用例）
	c.SourceRef = strOrEmpty(sourceRef)
	c.SourceHash = strOrEmpty(sourceHash)
	c.ExternalID = strOrEmpty(externalID)
	c.Suite = strOrEmpty(suite)
	c.Automation = strOrEmpty(automation)
	c.Priority = strOrEmpty(priority)
	c.SpecDocID = strOrEmpty(specDocID)
	c.LastRunner = strOrEmpty(lastRunner)
	c.LastRunResult = strOrEmpty(lastRunRes)
	c.Precondition = strOrEmpty(precondition)
	c.Keywords = strOrEmpty(keywords)
	c.LastRunAtMs = msPtr(lastRunAt)
	c.OpenedAtMs = msOf(openedAt) // msOf 已处理 nil（→0）
	c.UpdatedAtMs = msOf(&updatedAt)
	c.DeletedAtMs = msPtr(deletedAt)
	c.LoadedAtMs = c.UpdatedAtMs
	return &c, nil
}

// loadSteps 加载用例的全部步骤（按 seq 升序）。
// 期望结果从 checks JSONB 取回（DEC-02：expect 不占独立列）。
func (r *CaseRepo) loadSteps(ctx context.Context, caseID int64) ([]testcase.Step, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, seq, name, step_type, checks FROM case_steps
		 WHERE case_id = $1 AND deleted_at IS NULL ORDER BY seq`, caseID)
	if err != nil {
		return nil, fmt.Errorf("查询用例步骤失败: %w", err)
	}
	defer rows.Close()

	out := make([]testcase.Step, 0)
	for rows.Next() {
		var (
			s      testcase.Step
			checks []byte
		)
		if err := rows.Scan(&s.ID, &s.Seq, &s.Name, &s.StepType, &checks); err != nil {
			return nil, fmt.Errorf("扫描用例步骤失败: %w", err)
		}
		s.Expect = expectsFromJSON(checks)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历用例步骤失败: %w", err)
	}
	return out, nil
}

// expectsFromJSON 从 checks JSONB 取回全部期望（以 \n 连接，与 ZTF 多检查点对应）。
// checks 形状：[{"expect":"...","actual":"..."},...]；为空或非法返回空串。
func expectsFromJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var cps []struct {
		Expect string `json:"expect"`
	}
	if err := json.Unmarshal(raw, &cps); err != nil || len(cps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cps))
	for _, cp := range cps {
		parts = append(parts, cp.Expect)
	}
	return strings.Join(parts, "\n")
}

// checksJSON 把期望结果（多检查点以 \n 分隔）序列化为 checks JSONB 参数。
func checksJSON(expect string) any {
	if expect == "" {
		return nil
	}
	cps := make([]map[string]string, 0)
	for _, line := range strings.Split(expect, "\n") {
		if line == "" {
			continue
		}
		cps = append(cps, map[string]string{"expect": line, "actual": ""})
	}
	if len(cps) == 0 {
		return nil
	}
	b, err := json.Marshal(cps)
	if err != nil {
		return nil
	}
	return b
}

// Save 保存用例聚合：INSERT（LoadedAtMs=0）或乐观锁 UPDATE + 步骤整体替换
// （软删缺失 + 复活重现，与 spec sync 同语义——应用账号无 DELETE 权限）。
func (r *CaseRepo) Save(ctx context.Context, c *testcase.Case) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if c.LoadedAtMs == 0 {
		err = tx.QueryRow(ctx, `
			INSERT INTO cases (
				product_id, module_id, story_id, story_version, branch_id,
				title, precondition, keywords, pri, type, status, version,
				source_ref, source_hash, external_id, suite, automation, priority, spec_doc_id,
				opened_by_id, opened_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
				to_timestamp($21::float8/1000), to_timestamp($22::float8/1000))
			RETURNING id`,
			c.ProductID, c.ModuleID, c.StoryID, c.StoryVersion, c.BranchID,
			c.Title, nullIfEmpty(c.Precondition), nullIfEmpty(c.Keywords), c.Pri, string(c.Type),
			string(c.Status), c.Version,
			nullIfEmpty(c.SourceRef), nullIfEmpty(c.SourceHash), nullIfEmpty(c.ExternalID),
			nullIfEmpty(c.Suite), nullIfEmpty(c.Automation), nullIfEmpty(c.Priority), nullIfEmpty(c.SpecDocID),
			nullIfZero(c.OpenedByID), c.OpenedAtMs, c.UpdatedAtMs,
		).Scan(&c.ID)
		if err != nil {
			return fmt.Errorf("插入用例失败: %w", err)
		}
	} else {
		ct, err := tx.Exec(ctx, `
			UPDATE cases SET
				product_id=$2, module_id=$3, story_id=$4, story_version=$5, branch_id=$6,
				title=$7, precondition=$8, keywords=$9, pri=$10, type=$11, status=$12, version=$13,
				source_ref=$14, source_hash=$15, external_id=$16, suite=$17, automation=$18,
				priority=$19, spec_doc_id=$20, updated_at=to_timestamp($21::float8/1000)
			WHERE id=$1 AND updated_at = to_timestamp($22::float8/1000) AND deleted_at IS NULL`,
			c.ID,
			c.ProductID, c.ModuleID, c.StoryID, c.StoryVersion, c.BranchID,
			c.Title, nullIfEmpty(c.Precondition), nullIfEmpty(c.Keywords), c.Pri, string(c.Type),
			string(c.Status), c.Version,
			nullIfEmpty(c.SourceRef), nullIfEmpty(c.SourceHash), nullIfEmpty(c.ExternalID),
			nullIfEmpty(c.Suite), nullIfEmpty(c.Automation), nullIfEmpty(c.Priority), nullIfEmpty(c.SpecDocID),
			c.UpdatedAtMs, c.LoadedAtMs,
		)
		if err != nil {
			return fmt.Errorf("更新用例失败: %w", err)
		}
		if ct.RowsAffected() == 0 {
			// 乐观锁失败：区分「已软删」与「并发修改」两种语义。
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cases WHERE id=$1 AND deleted_at IS NULL)`, c.ID).Scan(&exists)
			if !exists {
				return shared.ErrNotFound
			}
			return shared.New("PRECONDITION_FAILED", "case was modified concurrently",
				map[string]any{"id": c.ID})
		}
	}

	if err := r.replaceSteps(ctx, tx, c.ID, c.Steps); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交用例事务失败: %w", err)
	}
	return nil
}

// replaceSteps 用当前步骤整体替换（软删缺失 + 复活重现，同 ReplaceItems 语义）。
func (r *CaseRepo) replaceSteps(ctx context.Context, tx pgx.Tx, caseID int64, steps []testcase.Step) error {
	if _, err := tx.Exec(ctx, `
		UPDATE case_steps SET deleted_at = now(), updated_at = now()
		 WHERE case_id = $1 AND deleted_at IS NULL`, caseID); err != nil {
		return fmt.Errorf("软删旧步骤失败: %w", err)
	}
	for _, st := range steps {
		_, err := tx.Exec(ctx, `
			INSERT INTO case_steps (case_id, seq, name, step_type, checks)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (case_id, seq) WHERE deleted_at IS NULL DO UPDATE SET
				name = EXCLUDED.name, step_type = EXCLUDED.step_type, checks = EXCLUDED.checks,
				updated_at = now(), deleted_at = NULL`,
			caseID, st.Seq, st.Name, string(st.StepType), checksJSON(st.Expect))
		if err != nil {
			return fmt.Errorf("写入用例步骤(seq=%d) 失败: %w", st.Seq, err)
		}
	}
	return nil
}

// List 分页查询（筛选/排序白名单/窗口函数一次取总数）。
func (r *CaseRepo) List(ctx context.Context, q testcase.Query) ([]*testcase.Case, int, error) {
	q.Page = q.Page.Normalize()
	where, args := buildCaseFilter(q)
	order := BuildCaseOrderBy(q.Sort)

	rows, err := r.pool.Query(ctx, `
		SELECT `+caseColumns+`, COUNT(*) OVER() AS total_count
		  FROM cases
		 `+where+`
		 `+order+`
		 LIMIT `+strconv.Itoa(q.Page.PerPage)+` OFFSET `+strconv.Itoa(q.Page.Offset()),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询用例列表失败: %w", err)
	}
	defer rows.Close()

	// 空结果返回空切片而非 nil：JSON 序列化为 [] 而非 null（与 story 域一致）。
	out := make([]*testcase.Case, 0, q.Page.PerPage)
	var total int
	for rows.Next() {
		c, err := scanCase(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历用例列表失败: %w", err)
	}
	return out, total, nil
}

// UpdateRunStats 更新冗余执行列（由测试执行域写结果后调用）。
func (r *CaseRepo) UpdateRunStats(ctx context.Context, id int64, runner, result string, atMs int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE cases SET last_runner=$2, last_run_result=$3, last_run_at=to_timestamp($4::float8/1000),
			updated_at = now()
		WHERE id=$1 AND deleted_at IS NULL`,
		id, nullIfEmpty(runner), nullIfEmpty(result), atMs)
	if err != nil {
		return fmt.Errorf("更新用例执行统计失败: %w", err)
	}
	return nil
}

// ---- 查询辅助（与 story_query.go 同构，导出仅供外部测试） ----

// buildCaseFilter 把查询条件编译为 WHERE 子句与绑定参数。
// 所有字面量片段来自本函数内的常量，用户输入只作为 $n 参数绑定。
func buildCaseFilter(q testcase.Query) (string, []any) {
	var (
		conds []string
		args  []any
	)
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	// 软删默认排除（ROADMAP §3.4）
	conds = append(conds, "deleted_at IS NULL")
	if q.ProductID > 0 {
		add("product_id = $%d", q.ProductID)
	}
	if q.ModuleID > 0 {
		add("module_id = $%d", q.ModuleID)
	}
	if q.StoryID > 0 {
		add("story_id = $%d", q.StoryID)
	}
	if q.Status != "" {
		add("status = $%d", string(q.Status))
	}
	if q.Type != "" {
		add("type = $%d", string(q.Type))
	}
	if q.ExternalID != "" {
		add("external_id = $%d", q.ExternalID)
	}
	if q.Keyword != "" {
		// 标题/关键字/前提的模糊匹配；% 与 _ 做字面转义，防通配符注入。
		// 三处各绑一个同值参数（不用 $1 复用：保持与 story 域一致的显式风格）。
		like := "%" + escapeLike(q.Keyword) + "%"
		n := len(args) + 1
		conds = append(conds, fmt.Sprintf(
			"(title ILIKE $%d OR keywords ILIKE $%d OR precondition ILIKE $%d)", n, n+1, n+2))
		args = append(args, like, like, like)
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// BuildCaseOrderBy 把排序键编译为 ORDER BY 子句（白名单外字段回落 id）。
func BuildCaseOrderBy(keys []testcase.SortKey) string {
	if len(keys) == 0 {
		return "ORDER BY id DESC"
	}
	cols := make([]string, 0, len(keys))
	for _, k := range keys {
		col, ok := caseSortColumn(k.Field)
		if !ok {
			continue // 白名单外字段静默忽略（调用方已在领域层校验）
		}
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		cols = append(cols, col+" "+dir)
	}
	if len(cols) == 0 {
		return "ORDER BY id DESC"
	}
	return "ORDER BY " + strings.Join(cols, ", ")
}

// caseSortColumn 排序字段白名单（与 testcase.ValidSortField 对应）。
func caseSortColumn(field string) (string, bool) {
	switch field {
	case "id", "title", "pri", "status", "type", "created_at", "updated_at":
		return field, true
	}
	return "", false
}

// strOrEmpty 把 *string 解引用；nil 返回空串。
func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// int64OrZero 把 *int64 解引用；nil 返回 0（0 = 未知主体）。
func int64OrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// nullIfEmpty 把空串转为 nil（写 NULL）；非空返回原值。
func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// CreateSuite 创建套件（同产品下套件名唯一，软删让位——000012 uniq_suite_product_name）。
func (r *CaseRepo) CreateSuite(ctx context.Context, productID int64, name, desc string, actorID int64) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO test_suites (product_id, name, "desc", created_by_id)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (product_id, name) WHERE deleted_at IS NULL DO NOTHING
		RETURNING id`, productID, name, nullIfEmpty(desc), nullIfZero(actorID)).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, shared.New("SUITE_CONFLICT", "suite name already exists in this product",
				map[string]any{"product_id": productID, "name": name})
		}
		return 0, fmt.Errorf("创建套件失败: %w", err)
	}
	return id, nil
}

// AddToSuite 把用例加入套件（重复加入返回 SUITE_CASE_CONFLICT，TC-05）。
func (r *CaseRepo) AddToSuite(ctx context.Context, suiteID, caseID int64, version int) error {
	ct, err := r.pool.Exec(ctx, `
		INSERT INTO suite_cases (suite_id, product_id, case_id, case_version)
		SELECT $1, c.product_id, $2, $3 FROM cases c WHERE c.id = $2 AND c.deleted_at IS NULL
		ON CONFLICT (suite_id, case_id) WHERE deleted_at IS NULL DO NOTHING`,
		suiteID, caseID, version)
	if err != nil {
		return fmt.Errorf("加入套件失败: %w", err)
	}
	// DO NOTHING 命中既有行时 RowsAffected=0：显式报冲突，
	// 不让「重复加入」伪装成成功（TC-05：第二次加入应 409）。
	if ct.RowsAffected() == 0 {
		return shared.New("SUITE_CASE_CONFLICT", "case already in suite",
			map[string]any{"suite_id": suiteID, "case_id": caseID})
	}
	return nil
}

// nullIfZero 把 0 转为 nil（写 NULL）。
func nullIfZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
