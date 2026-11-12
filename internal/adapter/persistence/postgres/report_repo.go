package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/report"
)

// ReportRepo 执行只读聚合查询。它不写库、不开事务——纯 SELECT。
//
// 设计（DESIGN-REPORT-001#DEC-03/#DEC-04）：六张报表形状各异，统一用
// queryMaps 把结果读成 []map[string]any；合并类报表（burndown / trend /
// ai_contribution）在 Go 端归并后再装进 Report.Rows。
type ReportRepo struct {
	pool *pgxpool.Pool
}

// NewReportRepo 构造报表仓储。
func NewReportRepo(pool *pgxpool.Pool) *ReportRepo {
	return &ReportRepo{pool: pool}
}

// Query 按 id 跑对应报表；productID 为 nil 表示不限定产品。
// id 的合法性由 usecase 层用 report.IsValid 先行校验，这里只处理已知 id。
func (r *ReportRepo) Query(ctx context.Context, id report.ID, productID *int64) (*report.Report, error) {
	meta, _ := report.Get(id)
	now := time.Now()
	cols := func(c ...string) []string { return c }

	switch id {
	case report.StoryStatus:
		rows, err := r.simple(ctx, productID,
			"status", "count",
			`SELECT status, count(*) AS count FROM stories WHERE deleted_at IS NULL %s GROUP BY status ORDER BY count DESC`)
		if err != nil {
			return nil, err
		}
		return &report.Report{ID: id, Title: meta.Title, GeneratedAt: now,
			Columns: cols("status", "count"), Rows: rows}, nil

	case report.CaseCoverage:
		rows, err := r.simple(ctx, productID,
			"status", "count",
			`SELECT status, count(*) AS count FROM cases WHERE deleted_at IS NULL %s GROUP BY status ORDER BY count DESC`)
		if err != nil {
			return nil, err
		}
		return &report.Report{ID: id, Title: meta.Title, GeneratedAt: now,
			Columns: cols("status", "count"), Rows: rows}, nil

	case report.StoryBurndown:
		return r.storyBurndown(ctx, meta, now, productID)

	case report.ReleaseQuality:
		return r.releaseQuality(ctx, meta, now, productID)

	case report.AIContribution:
		return r.aiContribution(ctx, meta, now)

	case report.TestExecutionTrend:
		return r.testExecutionTrend(ctx, meta, now, productID)

	default:
		return nil, fmt.Errorf("未实现的报表: %s", id)
	}
}

// simple 跑一条「单列分组计数」查询（status/count 形态）。
func (r *ReportRepo) simple(ctx context.Context, productID *int64, _ string, _ string, tmpl string) ([]map[string]any, error) {
	suffix, args := scope(productID)
	sql := fmt.Sprintf(tmpl, suffix)
	return queryMaps(ctx, r.pool, sql, args...)
}

// scope 返回 product_id 过滤片段与参数；productID 为 nil 时返回空。
// 调用方模板里用 %s 占位，且最多只有一个 $1 参数（本文件所有查询都满足）。
func scope(productID *int64) (string, []any) {
	if productID == nil {
		return "", nil
	}
	return "AND product_id = $1", []any{*productID}
}

// storyBurndown：opened / closed 两条按月聚合，再按 period 对齐。
func (r *ReportRepo) storyBurndown(ctx context.Context, meta report.Meta, now time.Time, productID *int64) (*report.Report, error) {
	suffix, args := scope(productID)
	opened, err := queryMaps(ctx, r.pool,
		fmt.Sprintf(`SELECT to_char(date_trunc('month',opened_at),'YYYY-MM') AS period, count(*) AS opened
		 FROM stories WHERE deleted_at IS NULL %s GROUP BY 1 ORDER BY 1`, suffix), args...)
	if err != nil {
		return nil, err
	}
	closed, err := queryMaps(ctx, r.pool,
		fmt.Sprintf(`SELECT to_char(date_trunc('month',closed_at),'YYYY-MM') AS period, count(*) AS closed
		 FROM stories WHERE closed_at IS NOT NULL %s GROUP BY 1 ORDER BY 1`, suffix), args...)
	if err != nil {
		return nil, err
	}

	merged := map[string]map[string]any{}
	add := func(rows []map[string]any, key string) {
		for _, row := range rows {
			p := asString(row["period"])
			m, ok := merged[p]
			if !ok {
				m = map[string]any{"period": p, "opened": 0, "closed": 0}
				merged[p] = m
			}
			m[key] = asInt(row[key])
		}
	}
	add(opened, "opened")
	add(closed, "closed")

	periods := make([]string, 0, len(merged))
	for p := range merged {
		periods = append(periods, p)
	}
	sortStrings(periods)
	rows := make([]map[string]any, 0, len(periods))
	for _, p := range periods {
		rows = append(rows, merged[p])
	}
	return &report.Report{ID: report.StoryBurndown, Title: meta.Title, GeneratedAt: now,
		Columns: []string{"period", "opened", "closed"}, Rows: rows}, nil
}

// releaseQuality：拉明细，Go 端拆逗号串计数 + 按 status 汇总。
func (r *ReportRepo) releaseQuality(ctx context.Context, meta report.Meta, now time.Time, productID *int64) (*report.Report, error) {
	suffix, args := scope(productID)
	rows, err := queryMaps(ctx, r.pool,
		fmt.Sprintf(`SELECT id, name, status, date, story_ids, bug_ids, build_ids
		 FROM releases WHERE deleted_at IS NULL %s ORDER BY date DESC NULLS LAST`, suffix), args...)
	if err != nil {
		return nil, err
	}

	out := []map[string]any{}
	byStatus := map[string]int{}
	for _, rel := range rows {
		st := asString(rel["status"])
		byStatus[st]++
		out = append(out, map[string]any{
			"dimension": "release",
			"id":        asInt(rel["id"]),
			"name":      asString(rel["name"]),
			"status":    st,
			"stories":   countCSV(asString(rel["story_ids"])),
			"bugs":      countCSV(asString(rel["bug_ids"])),
			"builds":    countCSV(asString(rel["build_ids"])),
		})
	}
	for st, c := range byStatus {
		out = append(out, map[string]any{
			"dimension": "by_status",
			"status":    st,
			"count":     c,
		})
	}
	return &report.Report{ID: report.ReleaseQuality, Title: meta.Title, GeneratedAt: now,
		Columns: []string{"dimension", "id", "name", "status", "stories", "bugs", "builds", "count"}, Rows: out}, nil
}

// aiContribution：三查合并，忽略 product_id（全局）。
func (r *ReportRepo) aiContribution(ctx context.Context, meta report.Meta, now time.Time) (*report.Report, error) {
	byActor, err := queryMaps(ctx, r.pool,
		`SELECT actor_id, count(*) AS count FROM traces WHERE actor_type='ai'
		 GROUP BY 1 ORDER BY 2 DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	byAction, err := queryMaps(ctx, r.pool,
		`SELECT action, count(*) AS count FROM traces WHERE actor_type='ai' GROUP BY 1 ORDER BY 2 DESC`)
	if err != nil {
		return nil, err
	}
	byResult, err := queryMaps(ctx, r.pool,
		`SELECT actor_type, count(*) AS count FROM test_results GROUP BY 1 ORDER BY 2 DESC`)
	if err != nil {
		return nil, err
	}

	out := []map[string]any{}
	for _, m := range byActor {
		out = append(out, map[string]any{"dimension": "by_actor", "actor_id": asString(m["actor_id"]), "count": asInt(m["count"])})
	}
	for _, m := range byAction {
		out = append(out, map[string]any{"dimension": "by_action", "action": asString(m["action"]), "count": asInt(m["count"])})
	}
	for _, m := range byResult {
		out = append(out, map[string]any{"dimension": "test_results", "actor_type": asString(m["actor_type"]), "count": asInt(m["count"])})
	}
	return &report.Report{ID: report.AIContribution, Title: meta.Title, GeneratedAt: now,
		Columns: []string{"dimension", "actor_id", "action", "actor_type", "count"}, Rows: out}, nil
}

// testExecutionTrend：按月 × result 计数，再按 period 聚合成 pass/fail/blocked/skip/na。
func (r *ReportRepo) testExecutionTrend(ctx context.Context, meta report.Meta, now time.Time, productID *int64) (*report.Report, error) {
	var suffix string
	var args []any
	if productID == nil {
		suffix = ""
	} else {
		suffix = "JOIN test_tasks tt ON tt.id = tr.test_task_id AND tt.product_id = $1"
		args = []any{*productID}
	}
	rows, err := queryMaps(ctx, r.pool,
		fmt.Sprintf(`SELECT to_char(date_trunc('month',executed_at),'YYYY-MM') AS period, result, count(*) AS cnt
		 FROM test_results tr %s GROUP BY 1,2 ORDER BY 1,2`, suffix), args...)
	if err != nil {
		return nil, err
	}

	merged := map[string]map[string]any{}
	for _, row := range rows {
		p := asString(row["period"])
		m, ok := merged[p]
		if !ok {
			m = map[string]any{"period": p, "pass": 0, "fail": 0, "blocked": 0, "skip": 0, "na": 0}
			merged[p] = m
		}
		switch asString(row["result"]) {
		case "pass":
			m["pass"] = asInt(row["cnt"])
		case "fail":
			m["fail"] = asInt(row["cnt"])
		case "blocked":
			m["blocked"] = asInt(row["cnt"])
		case "skip":
			m["skip"] = asInt(row["cnt"])
		default:
			m["na"] = asInt(row["cnt"])
		}
	}
	periods := make([]string, 0, len(merged))
	for p := range merged {
		periods = append(periods, p)
	}
	sortStrings(periods)
	out := make([]map[string]any, 0, len(periods))
	for _, p := range periods {
		out = append(out, merged[p])
	}
	return &report.Report{ID: report.TestExecutionTrend, Title: meta.Title, GeneratedAt: now,
		Columns: []string{"period", "pass", "fail", "blocked", "skip", "na"}, Rows: out}, nil
}

// queryMaps 用通用扫描器把查询结果读成 []map[string]any（DESIGN-REPORT-001#DEC-04）。
func queryMaps(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]map[string]any, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := rows.FieldDescriptions()
	out := make([]map[string]any, 0, 32)
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[string(c.Name)] = normalize(vals[i])
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// normalize 把 pgx 的 []byte 规范成 string（JSON 友好），其余类型原样返回。
func normalize(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return v
	}
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func asInt(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case int32:
		return int(x)
	case int:
		return x
	case float64:
		return int(x)
	case []byte:
		var n int
		fmt.Sscanf(string(x), "%d", &n)
		return n
	case string:
		var n int
		fmt.Sscanf(x, "%d", &n)
		return n
	default:
		return 0
	}
}

// countCSV 数逗号串里的非空元素个数（"1,2,3"→3，""→0）。
// releases 的 story_ids/bug_ids/build_ids 是这种逗号串快照（见 releases 表定义）。
func countCSV(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return len(strings.Split(s, ","))
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
