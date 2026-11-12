package zentao

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/importpkg"
	"github.com/mst8222/regto/internal/domain/release"
	"github.com/mst8222/regto/internal/domain/story"
	dtcase "github.com/mst8222/regto/internal/domain/testcase"
	dtt "github.com/mst8222/regto/internal/domain/testtask"
)

// ---------- 校验用的表级声明（数据驱动，避免 10 段重复 SQL） ----------

// fkCheck 是一条外键悬空检查（目标表.列 → 父表）。
type fkCheck struct {
	table  string
	column string
	parent string
}

// fkChecks 覆盖全部导入表的外键引用（REQ-06 #2）。
var fkChecks = []fkCheck{
	{"products", "owner_id", "users"},
	{"products", "created_by_id", "users"},
	{"projects", "parent_id", "projects"},
	{"projects", "pm_id", "users"},
	{"projects", "opened_by_id", "users"},
	{"branches", "product_id", "products"},
	{"modules", "parent_id", "modules"},
	{"modules", "owner_id", "users"},
	{"stories", "product_id", "products"},
	{"stories", "module_id", "modules"},
	{"stories", "opened_by_id", "users"},
	{"stories", "assigned_to_id", "users"},
	{"plans", "product_id", "products"},
	{"plans", "branch_id", "branches"},
	{"plans", "parent_id", "plans"},
	{"plans", "opened_by_id", "users"},
	{"plan_stories", "plan_id", "plans"},
	{"plan_stories", "story_id", "stories"},
	{"releases", "product_id", "products"},
	{"releases", "branch_id", "branches"},
	{"releases", "opened_by_id", "users"},
	{"builds", "product_id", "products"},
	{"builds", "project_id", "projects"},
	{"builds", "created_by_id", "users"},
	{"cases", "product_id", "products"},
	{"cases", "module_id", "modules"},
	{"cases", "story_id", "stories"},
	{"cases", "branch_id", "branches"},
	{"cases", "opened_by_id", "users"},
	{"case_steps", "case_id", "cases"},
	{"test_suites", "product_id", "products"},
	{"test_suites", "created_by_id", "users"},
	{"suite_cases", "suite_id", "test_suites"},
	{"suite_cases", "product_id", "products"},
	{"suite_cases", "case_id", "cases"},
	{"test_tasks", "product_id", "products"},
	{"test_tasks", "project_id", "projects"},
	{"test_tasks", "build_id", "builds"},
	{"test_tasks", "owner_id", "users"},
	{"test_tasks", "opened_by_id", "users"},
	{"test_task_cases", "test_task_id", "test_tasks"},
	{"test_task_cases", "case_id", "cases"},
	{"test_task_cases", "assigned_to_id", "users"},
	{"test_results", "test_task_id", "test_tasks"},
	{"test_results", "case_id", "cases"},
}

// enumCheck 是一条枚举域检查（目标列合法值来自领域常量，REQ-06 #3）。
type enumCheck struct {
	table  string
	column string
	legal  []string
}

// enumChecks 覆盖全部有域枚举的目标列（值必须 ∈ 领域常量）。
var enumChecks = []enumCheck{
	{"projects", "kind", []string{"sprint", "waterfall", "kanban", "program"}},
	{"projects", "status", storyProjectStatuses()},
	{"stories", "type", []string{"story", "requirement", "epic"}},
	{"stories", "status", storyStatuses()},
	{"stories", "pri", priLevels()},
	{"plans", "status", releasePlanStatuses()},
	{"releases", "status", releaseStatuses()},
	{"releases", "system", []string{"trunk", "branch"}},
	{"builds", "system", []string{"trunk", "branch"}},
	{"cases", "type", caseTypes()},
	{"cases", "status", caseStatuses()},
	{"case_steps", "step_type", stepTypes()},
	{"test_tasks", "status", taskStatuses()},
	{"test_results", "result", caseResults()},
	{"test_results", "actor_type", []string{"user", "system", "import"}},
}

// timeCheck 是必填时间列非空检查（REQ-06 #4）。
type timeCheck struct{ table, column string }

var timeChecks = []timeCheck{
	{"products", "created_at"},
	{"projects", "opened_at"},
	{"stories", "opened_at"},
	{"plans", "opened_at"},
	{"releases", "opened_at"},
	{"builds", "created_at"},
	{"cases", "opened_at"},
	{"test_tasks", "opened_at"},
	{"test_results", "executed_at"},
}

// statusCheck 是状态分布检查（REQ-06 #7；源列与目标列名可能不同）。
type statusCheck struct {
	source, srcCol string
	table, column  string
}

var statusChecks = []statusCheck{
	{"zt_project", "status", "projects", "status"},
	{"zt_story", "status", "stories", "status"},
	{"zt_productplan", "status", "plans", "status"},
	{"zt_release", "status", "releases", "status"},
	{"zt_case", "status", "cases", "status"},
	{"zt_testtask", "status", "test_tasks", "status"},
	{"zt_testresult", "caseResult", "test_results", "result"},
}

// ---------- 领域枚举来源（契约与导入共用同一真源） ----------

func storyStatuses() []string {
	all := []story.Status{story.StatusDraft, story.StatusReviewing, story.StatusActive,
		story.StatusChanging, story.StatusClosed}
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func releasePlanStatuses() []string {
	all := release.AllPlanStatuses()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func releaseStatuses() []string {
	all := release.AllReleaseStatuses()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func caseTypes() []string {
	all := []dtcase.CaseType{dtcase.TypeFeature, dtcase.TypePerformance,
		dtcase.TypeConfig, dtcase.TypeInstall}
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func caseStatuses() []string {
	all := []dtcase.Status{dtcase.StatusWait, dtcase.StatusNormal,
		dtcase.StatusBlocked, dtcase.StatusInvestigate}
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func stepTypes() []string {
	return []string{string(dtcase.StepTypeStep), string(dtcase.StepTypeGroup)}
}

func taskStatuses() []string {
	all := []dtt.Status{dtt.StatusWait, dtt.StatusDoing, dtt.StatusDone, dtt.StatusBlocked}
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func caseResults() []string {
	all := dtt.AllResults()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func priLevels() []string { return []string{"1", "2", "3", "4"} }

func storyProjectStatuses() []string { return []string{"wait", "doing", "suspended", "closed", "done"} }

// Verify 执行 10 项校验清单（REQ-06：零写入，只读双库）。
func (im *Importer) Verify(ctx context.Context) []importpkg.CheckResult {
	results := make([]importpkg.CheckResult, 0, len(importpkg.AllCheckIDs()))
	for _, id := range importpkg.AllCheckIDs() {
		var r importpkg.CheckResult
		switch id {
		case importpkg.CheckRowCounts:
			r = im.checkRowCounts(ctx)
		case importpkg.CheckFKOrphans:
			r = im.checkFKOrphans(ctx)
		case importpkg.CheckEnumDomain:
			r = im.checkEnums(ctx)
		case importpkg.CheckTimeNonEmpty:
			r = im.checkTimeNonEmpty(ctx)
		case importpkg.CheckEstimatePrecision:
			r = im.checkEstimatePrecision(ctx)
		case importpkg.CheckSampleDiff:
			r = im.checkSampleDiff(ctx)
		case importpkg.CheckStatusDistribution:
			r = im.checkStatusDistribution(ctx)
		case importpkg.CheckTreeIntegrity:
			r = im.checkTreeIntegrity(ctx)
		case importpkg.CheckVersionedIntegrity:
			r = im.checkVersionedIntegrity(ctx)
		case importpkg.CheckAttachmentReachability:
			r = im.checkAttachments(ctx)
		}
		results = append(results, r)
	}
	return results
}

// checkRowCounts #1 行数比对（总数 + 未删分别比对）。
func (im *Importer) checkRowCounts(ctx context.Context) importpkg.CheckResult {
	var pairs []importpkg.CountPair
	for _, tm := range tableMaps {
		st, err := im.src.QueryRows(ctx, "SELECT COUNT(*) AS n FROM "+tm.source)
		if err != nil {
			return failResult(importpkg.CheckRowCounts, "源库查询失败: "+err.Error())
		}
		tt, err := im.countTarget(ctx, tm.target, false)
		if err != nil {
			return failResult(importpkg.CheckRowCounts, "目标库查询失败: "+err.Error())
		}
		pairs = append(pairs, importpkg.CountPair{
			Table: tm.target, Source: mustCount(st), Target: tt})

		sl, err := im.src.QueryRows(ctx,
			"SELECT COUNT(*) AS n FROM "+tm.source+" WHERE deleted = '0'")
		if err != nil {
			return failResult(importpkg.CheckRowCounts, "源库查询失败: "+err.Error())
		}
		tl, err := im.countTarget(ctx, tm.target, true)
		if err != nil {
			return failResult(importpkg.CheckRowCounts, "目标库查询失败: "+err.Error())
		}
		pairs = append(pairs, importpkg.CountPair{
			Table: tm.target + "(live)", Source: mustCount(sl), Target: tl})
	}
	return importpkg.RowCountsVerdict(pairs)
}

// countTarget 统计目标行数（live=true 只算未软删；按 run 限定到本次导入）。
func (im *Importer) countTarget(ctx context.Context, table string, live bool) (int64, error) {
	q := "SELECT COUNT(*) AS n FROM " + table + " WHERE " + im.runInClause(table)
	if live {
		has, err := im.hasColumn(ctx, table, "deleted_at")
		if err != nil {
			return 0, err
		}
		if has {
			q += " AND deleted_at IS NULL"
		}
	}
	rows, err := im.pool.Query(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, fmt.Errorf("空结果集")
	}
	var n int64
	if err := rows.Scan(&n); err != nil {
		return 0, err
	}
	return n, rows.Err()
}

// hasColumn 判断目标表是否含某列（避免对无 deleted_at 的表硬加 WHERE）。
func (im *Importer) hasColumn(ctx context.Context, table, column string) (bool, error) {
	var n int64
	err := im.pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// checkFKOrphans #2 外键悬空。
func (im *Importer) checkFKOrphans(ctx context.Context) importpkg.CheckResult {
	var orphans []importpkg.FKOrphan
	for _, c := range fkChecks {
		q := fmt.Sprintf(
			"SELECT %s.id, %s.%s FROM %s LEFT JOIN %s p ON %s.%s = p.id WHERE %s AND %s.%s IS NOT NULL AND p.id IS NULL LIMIT 5",
			c.table, c.table, c.column, c.table, c.parent, c.table, c.column, im.runInClause(c.table), c.table, c.column)
		rows, err := im.pool.Query(ctx, q)
		if err != nil {
			return failResult(importpkg.CheckFKOrphans, "目标库查询失败: "+err.Error())
		}
		for rows.Next() {
			var id int64
			var v *int64
			if err := rows.Scan(&id, &v); err != nil {
				rows.Close()
				return failResult(importpkg.CheckFKOrphans, err.Error())
			}
			val := int64(0)
			if v != nil {
				val = *v
			}
			orphans = append(orphans, importpkg.FKOrphan{
				Table: c.table, Column: c.column, Value: val})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return failResult(importpkg.CheckFKOrphans, err.Error())
		}
	}
	return importpkg.FKOrphansVerdict(orphans)
}

// checkEnums #3 枚举域。
func (im *Importer) checkEnums(ctx context.Context) importpkg.CheckResult {
	var violations []importpkg.EnumViolation
	for _, c := range enumChecks {
		q := fmt.Sprintf("SELECT DISTINCT %s AS v FROM %s WHERE %s IS NOT NULL AND %s",
			c.column, c.table, c.column, im.runInClause(c.table))
		rows, err := im.pool.Query(ctx, q)
		if err != nil {
			return failResult(importpkg.CheckEnumDomain, "目标库查询失败: "+err.Error())
		}
		for rows.Next() {
			var v any
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return failResult(importpkg.CheckEnumDomain, err.Error())
			}
			val := toStr(v)
			if val == "" {
				continue // 空串按缺省处理（NOT NULL DEFAULT ''）
			}
			if !contains(c.legal, val) {
				violations = append(violations, importpkg.EnumViolation{
					Table: c.table, Column: c.column, Value: val, Legal: c.legal})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return failResult(importpkg.CheckEnumDomain, err.Error())
		}
	}
	return importpkg.EnumDomainVerdict(violations)
}

// checkTimeNonEmpty #4 时间非空。
func (im *Importer) checkTimeNonEmpty(ctx context.Context) importpkg.CheckResult {
	var nulls []importpkg.TimeNull
	for _, c := range timeChecks {
		q := fmt.Sprintf("SELECT id FROM %s WHERE %s IS NULL AND %s LIMIT 5", c.table, c.column, im.runInClause(c.table))
		rows, err := im.pool.Query(ctx, q)
		if err != nil {
			return failResult(importpkg.CheckTimeNonEmpty, "目标库查询失败: "+err.Error())
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return failResult(importpkg.CheckTimeNonEmpty, err.Error())
			}
			nulls = append(nulls, importpkg.TimeNull{Table: c.table, Column: c.column, ID: id})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return failResult(importpkg.CheckTimeNonEmpty, err.Error())
		}
	}
	return importpkg.TimeNonEmptyVerdict(nulls)
}

// checkEstimatePrecision #5 工时精度（四舍五入到 0.1 后比对）。
func (im *Importer) checkEstimatePrecision(ctx context.Context) importpkg.CheckResult {
	srcRows, err := im.src.QueryRows(ctx,
		"SELECT id, ROUND(COALESCE(estimate, 0), 1) AS e FROM zt_story")
	if err != nil {
		return failResult(importpkg.CheckEstimatePrecision, "源库查询失败: "+err.Error())
	}
	tgtRows, err := im.pool.Query(ctx,
		"SELECT id, ROUND(COALESCE(estimate, 0), 1) AS e FROM stories WHERE "+im.runInClause("stories"))
	if err != nil {
		return failResult(importpkg.CheckEstimatePrecision, "目标库查询失败: "+err.Error())
	}
	tgt := map[int64]float64{}
	for tgtRows.Next() {
		var id int64
		var e *float64
		if err := tgtRows.Scan(&id, &e); err != nil {
			tgtRows.Close()
			return failResult(importpkg.CheckEstimatePrecision, err.Error())
		}
		if e != nil {
			tgt[id] = *e
		}
	}
	tgtRows.Close()
	if err := tgtRows.Err(); err != nil {
		return failResult(importpkg.CheckEstimatePrecision, err.Error())
	}

	var deltas []importpkg.EstimateDelta
	for _, r := range srcRows {
		id, _ := toInt64(r["id"])
		sv, _ := toFloat(r["e"])
		tv, ok := tgt[id]
		if !ok {
			continue // 行数比对负责
		}
		d := sv - tv
		if d < 0 {
			d = -d
		}
		if d > importpkg.EstimateTolerance {
			deltas = append(deltas, importpkg.EstimateDelta{
				Table: "stories", ID: id, Source: sv, Target: tv, Delta: d})
		}
	}
	return importpkg.EstimatePrecisionVerdict(deltas)
}

// checkSampleDiff #6 抽样 diff（每表按 ID 跨度确定性抽 20 行，逐列比对原样列）。
func (im *Importer) checkSampleDiff(ctx context.Context) importpkg.CheckResult {
	var mismatches []importpkg.SampleMismatch
	for _, tm := range tableMaps {
		// 只比对原样列（排除外键/account/deleted/派生列）。
		plain := plainFieldPairs(tm)
		if len(plain) == 0 {
			continue
		}
		ids, err := im.sampleIDs(ctx, tm)
		if err != nil {
			return failResult(importpkg.CheckSampleDiff, err.Error())
		}
		if len(ids) == 0 {
			continue
		}
		srcRows, err := im.src.QueryRows(ctx,
			"SELECT "+plainCols(plain)+" FROM "+tm.source+" WHERE id > 0 AND id IN ("+joinIDs(ids)+")")
		if err != nil {
			return failResult(importpkg.CheckSampleDiff, "源库查询失败: "+err.Error())
		}
		ph := placeholders(len(ids))
		targs := make([]any, len(ids))
		for i, id := range ids {
			targs[i] = id
		}
		tgtRows, err := im.pool.Query(ctx,
			"SELECT "+dstsOf(plain)+" FROM "+tm.target+" WHERE id IN ("+ph+") AND "+im.runInClause(tm.target), targs...)
		if err != nil {
			return failResult(importpkg.CheckSampleDiff, "目标库查询失败: "+err.Error())
		}
		tgt := map[int64]map[string]any{}
		fields := tgtRows.FieldDescriptions()
		for tgtRows.Next() {
			holders := make([]any, len(fields))
			ptrs := make([]any, len(fields))
			for i := range holders {
				ptrs[i] = &holders[i]
			}
			if err := tgtRows.Scan(ptrs...); err != nil {
				tgtRows.Close()
				return failResult(importpkg.CheckSampleDiff, err.Error())
			}
			m := map[string]any{}
			var id int64
			for i, fd := range fields {
				name := strings.ToLower(string(fd.Name))
				if name == "id" {
					id, _ = toInt64(holders[i])
					continue
				}
				m[name] = normalizeValue(holders[i])
			}
			tgt[id] = m
		}
		tgtRows.Close()
		if err := tgtRows.Err(); err != nil {
			return failResult(importpkg.CheckSampleDiff, err.Error())
		}

		for _, sr := range srcRows {
			id, _ := toInt64(sr["id"])
			tr, ok := tgt[id]
			if !ok {
				continue
			}
			for _, p := range plain {
				if p.dst == "id" {
					continue // id 是连接键，不参与值比较
				}
				// 源/目标两侧经同一「普通列映射」归一（fnn 回退、date 格式
				// 化、bool 转换），再比较：避免源原始 nil 与目标 ""、date 字符串
				// 与 time.Time 等「映射后才相等」的伪不一致（DESIGN-IMPORT-001 §4）。
				sv := canonicalPlain(p, sr[p.src])
				tv := canonicalPlain(p, tr[p.dst])
				if !valuesEqual(sv, tv) {
					mismatches = append(mismatches, importpkg.SampleMismatch{
						Table: tm.target, Source: id, Column: p.dst,
						Source2: sv, Target: tv})
				}
			}
		}
	}
	return importpkg.SampleDiffVerdict(mismatches)
}

// checkStatusDistribution #7 状态分布直方图。
func (im *Importer) checkStatusDistribution(ctx context.Context) importpkg.CheckResult {
	var hists []importpkg.StatusHistogram
	for _, c := range statusChecks {
		src, err := im.histogram(ctx, im.src, c.source, c.srcCol, "")
		if err != nil {
			return failResult(importpkg.CheckStatusDistribution, "源库查询失败: "+err.Error())
		}
		tgt, err := im.histogram(ctx, pgRowSource{pool: im.pool}, c.table, c.column, im.runInClause(c.table))
		if err != nil {
			return failResult(importpkg.CheckStatusDistribution, "目标库查询失败: "+err.Error())
		}
		hists = append(hists, importpkg.StatusHistogram{
			Table: c.table, Source: src, Target: tgt})
	}
	return importpkg.StatusDistributionVerdict(hists)
}

// checkTreeIntegrity #8 树完整性（parent 存在 + grade = 父 grade + 1）。
func (im *Importer) checkTreeIntegrity(ctx context.Context) importpkg.CheckResult {
	var breaks []importpkg.TreeBreak
	for _, t := range []string{"projects", "modules"} {
		q := fmt.Sprintf(`
			SELECT %s.id, %s.parent_id, %s.grade, COALESCE(p.grade, -1)
			  FROM %s LEFT JOIN %s p ON %s.parent_id = p.id
			 WHERE %s AND %s.parent_id IS NOT NULL AND (p.id IS NULL OR %s.grade <> p.grade + 1)
			 LIMIT 5`, t, t, t, t, t, t, im.runInClause(t), t, t)
		rows, err := im.pool.Query(ctx, q)
		if err != nil {
			return failResult(importpkg.CheckTreeIntegrity, "目标库查询失败: "+err.Error())
		}
		for rows.Next() {
			var id, parent, grade, pgrade int64
			if err := rows.Scan(&id, &parent, &grade, &pgrade); err != nil {
				rows.Close()
				return failResult(importpkg.CheckTreeIntegrity, err.Error())
			}
			reason := "父节点缺失"
			if pgrade >= 0 {
				reason = fmt.Sprintf("grade=%d 与父 grade=%d 不连续", grade, pgrade)
			}
			breaks = append(breaks, importpkg.TreeBreak{
				Table: t, ID: id, ParentID: parent, Reason: reason})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return failResult(importpkg.CheckTreeIntegrity, err.Error())
		}
	}
	return importpkg.TreeIntegrityVerdict(breaks)
}

// checkVersionedIntegrity #9 版本化完整性（cases.version ≥ 1；
// test_results.run_no 每 (task,case) 从 1 连续）。
func (im *Importer) checkVersionedIntegrity(ctx context.Context) importpkg.CheckResult {
	var gaps []importpkg.VersionGap
	rows, err := im.pool.Query(ctx,
		"SELECT id, version FROM cases WHERE version < 1 AND "+im.runInClause("cases")+" LIMIT 5")
	if err != nil {
		return failResult(importpkg.CheckVersionedIntegrity, "目标库查询失败: "+err.Error())
	}
	for rows.Next() {
		var id, v int64
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return failResult(importpkg.CheckVersionedIntegrity, err.Error())
		}
		gaps = append(gaps, importpkg.VersionGap{Table: "cases", ID: id, Have: v, Want: 1})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return failResult(importpkg.CheckVersionedIntegrity, err.Error())
	}

	rrows, err := im.pool.Query(ctx, `
		SELECT test_task_id, case_id, MIN(run_no), MAX(run_no), COUNT(*)
		  FROM test_results WHERE `+im.runInClause("test_results")+`
		 GROUP BY test_task_id, case_id
		 HAVING MIN(run_no) <> 1 OR MAX(run_no) <> COUNT(*) LIMIT 5`)
	if err != nil {
		return failResult(importpkg.CheckVersionedIntegrity, "目标库查询失败: "+err.Error())
	}
	for rrows.Next() {
		var taskID, caseID, minNo, maxNo, cnt int64
		if err := rrows.Scan(&taskID, &caseID, &minNo, &maxNo, &cnt); err != nil {
			rrows.Close()
			return failResult(importpkg.CheckVersionedIntegrity, err.Error())
		}
		gaps = append(gaps, importpkg.VersionGap{
			Table: "test_results", ID: taskID*1000000 + caseID,
			Have: maxNo, Want: cnt})
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return failResult(importpkg.CheckVersionedIntegrity, err.Error())
	}
	return importpkg.VersionedIntegrityVerdict(gaps)
}

// checkAttachments #10 附件可达（WARN 级：文件搬迁属运维动作）。
func (im *Importer) checkAttachments(ctx context.Context) importpkg.CheckResult {
	if im.dataDir == "" {
		return importpkg.CheckResult{
			ID: importpkg.CheckAttachmentReachability, Verdict: importpkg.VerdictWarn,
			Detail: "未提供 --data-dir，跳过可达性比对（WARN）"}
	}
	rows, err := im.src.QueryRows(ctx, "SELECT pathname FROM zt_file LIMIT 1000")
	if err != nil {
		return importpkg.CheckResult{
			ID: importpkg.CheckAttachmentReachability, Verdict: importpkg.VerdictWarn,
			Detail: "源库无 zt_file 或查询失败（WARN）：" + err.Error()}
	}
	var missing []importpkg.AttachmentMissing
	for _, r := range rows {
		p := toStr(r["pathname"])
		if p == "" {
			continue
		}
		if _, err := os.Stat(im.dataDir + "/" + p); err != nil {
			missing = append(missing, importpkg.AttachmentMissing{Path: p})
		}
	}
	return importpkg.AttachmentReachabilityVerdict(missing)
}

// ---------- 辅助 ----------

// targetToSource 是目标表 → 源表 的反查（verify 需按 run 限定到目标行）。
var targetToSource = func() map[string]string {
	m := map[string]string{}
	for _, tm := range tableMaps {
		m[tm.target] = tm.source
	}
	return m
}()

// runInClause 返回限定到本次 run 的目标行 id 集合子查询（verify 只校验本次导入，
// 避免与库中既有 / 其他 run 数据冲突）。run_key 经单引号转义后内联（仅用于只读校验）。
// 用基表名限定 id，避免多表 JOIN（FK/树检查）时 id 歧义。
func (im *Importer) runInClause(targetTable string) string {
	src, ok := targetToSource[targetTable]
	if !ok {
		src = targetTable
	}
	rk := strings.ReplaceAll(im.runKey, "'", "''")
	return fmt.Sprintf(
		"%s.id IN (SELECT target_id FROM regto_import.import_id_map WHERE run_key = '%s' AND table_name = '%s')",
		targetTable, rk, src)
}

func failResult(id importpkg.CheckID, detail string) importpkg.CheckResult {
	return importpkg.CheckResult{ID: id, Verdict: importpkg.VerdictFail, Detail: detail}
}

// mustCount 取 QueryRows 首行首列计数。
func mustCount(rows []map[string]any) int64 {
	if len(rows) == 0 {
		return -1
	}
	n, _ := toInt64(rows[0]["n"])
	return n
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// plainFieldPairs 取原样列对（src==dst 语义，排除派生/映射列）。
func plainFieldPairs(tm tableMap) []field {
	var out []field
	for _, fl := range tm.fields {
		if fl.kind != kindPlain {
			continue
		}
		if strings.HasPrefix(fl.src, "__") {
			continue // 派生列
		}
		out = append(out, fl)
	}
	return out
}

// canonicalPlain 把（源原始值或目标扫描值）归一为可比形态，与 importer 的
// mapField(kindPlain) 完全一致：date 列 → YYYY-MM-DD 字符串、bool 转换、零值
// 经 normalizeValue、NOT NULL 列回退 nn。两侧用同一函数保证「映射后才相等」的列
// （如 closed_reason 源 nil/目标 ""、begin_date 源字符串/目标 time.Time）不会
// 被抽样 diff 误判为不一致。
func canonicalPlain(fl field, raw any) any {
	v := plainValue(fl, raw)
	if v == nil && fl.nn != nil {
		return fl.nn
	}
	return v
}

func plainCols(fs []field) string {
	cols := make([]string, 0, len(fs)+1)
	cols = append(cols, bt("id"))
	for _, f := range fs {
		cols = append(cols, bt(f.src))
	}
	return strings.Join(cols, ", ")
}

func dstsOf(fs []field) string {
	cols := make([]string, 0, len(fs)+1)
	cols = append(cols, qt("id"))
	for _, f := range fs {
		cols = append(cols, qt(f.dst))
	}
	return strings.Join(cols, ", ")
}

func joinIDs(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// placeholders 生成 $1,$2,... 占位符列表（避免 = ANY($1) 与子查询在 pgx 下的组合异常）。
func placeholders(n int) string {
	parts := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		parts = append(parts, fmt.Sprintf("$%d", i))
	}
	return strings.Join(parts, ",")
}

// valuesEqual 归一化相等比对（时间统一 RFC3339Nano，数字去尾零，字符串直比）。
func valuesEqual(a, b any) bool {
	na, nb := norm(a), norm(b)
	if na == nil || nb == nil {
		return na == nil && nb == nil
	}
	return fmt.Sprintf("%v", na) == fmt.Sprintf("%v", nb)
}

// norm 把值归一为可稳定字符串化的形式。
func norm(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format("2006-01-02T15:04:05.999999999")
	case pgtype.Numeric:
		// PG numeric 列经 pgx 返回 pgtype.Numeric，需转 float64 再格式化。
		if f, err := x.Float64Value(); err == nil && f.Valid {
			return strconv.FormatFloat(f.Float64, 'f', 4, 64)
		}
		return nil
	case float64:
		return strconv.FormatFloat(x, 'f', 4, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return v
	}
}

// sampleIDs 按 ID 跨度确定性抽最多 20 个 ID（可复现，测试稳定）。
func (im *Importer) sampleIDs(ctx context.Context, tm tableMap) ([]int64, error) {
	rows, err := im.src.QueryRows(ctx,
		"SELECT MIN(id) AS lo, MAX(id) AS hi FROM "+tm.source)
	if err != nil {
		return nil, fmt.Errorf("源库查询失败: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	lo, _ := toInt64(rows[0]["lo"])
	hi, _ := toInt64(rows[0]["hi"])
	if hi <= 0 {
		return nil, nil
	}
	if lo <= 0 {
		lo = 1
	}
	span := hi - lo + 1
	n := int64(20)
	if span < n {
		n = span
	}
	ids := make([]int64, 0, n)
	for i := int64(0); i < n; i++ {
		ids = append(ids, lo+i*span/n)
	}
	return ids, nil
}

// histogram 统计某列分布（where 非空时附加过滤，用于按 run 限定目标库范围）。
func (im *Importer) histogram(ctx context.Context, rs rowSource, table, column, where string) (map[string]int64, error) {
	q := fmt.Sprintf("SELECT %s AS k, COUNT(*) AS n FROM %s", column, table)
	if where != "" {
		q += " WHERE " + where
	}
	q += " GROUP BY " + column
	rows, err := rs.QueryRows(ctx, q)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		k := toStr(r["k"])
		if k == "" {
			k = "(empty)"
		}
		n, _ := toInt64(r["n"])
		out[k] = n
	}
	return out, nil
}

// pgRowSource 是 rowSource 的 PostgreSQL 实现（校验清单目标库侧）。
// 与 mysqlSource 同形：查询结果归一为「列名（小写）→ 值」。
type pgRowSource struct {
	pool *pgxpool.Pool
}

// QueryRows 执行查询并归一为行集合。
func (p pgRowSource) QueryRows(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	out := make([]map[string]any, 0, 32)
	for rows.Next() {
		holders := make([]any, len(fields))
		ptrs := make([]any, len(fields))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(map[string]any, len(fields))
		for i, fd := range fields {
			r[strings.ToLower(string(fd.Name))] = holders[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
