package zentao

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mst8222/regto/internal/domain/importpkg"
)

// fieldKind 是字段映射种类（DESIGN-IMPORT-001 §4 列映射规则）。
type fieldKind int

const (
	kindPlain       fieldKind = iota // 原样拷贝（读取层已归一类型）
	kindFKID                         // 外键：源 ID → id_map 目标 ID
	kindAccount                      // account 字符串 → users.id（缺失回退 system + MISSING_ACCOUNT）
	kindDeletedAt                    // deleted '0'/'1' → deleted_at
	kindConst                        // 常量（ref 为字符串常量）
	kindPassword                     // 密码占位（REQ-08：绝不导入源密码）
	kindJSONWrap                     // 普通字符串包成 JSONB 数组（casestep.expect → checks）
	kindMinutesToMs                  // 分钟 → 毫秒（zentao duration 语义）
	kindNowFallback                  // 源时间为空时取导入时刻
)

// field 是一列映射（源列 → 目标列）。
type field struct {
	dst  string
	src  string
	kind fieldKind
	ref  string // kindFKID：源表名；kindConst：常量值
	def  string // kindConst 的字符串值（与 ref 二选一，优先 ref）
	// date 表示目标列是 PG date 类型（MySQL driver 返回 time.Time，
	// 直绑 timestamptz 会报类型错，需格式化为 YYYY-MM-DD）。
	date bool
	// asBool 表示目标列是 PG boolean（MySQL tinyint 需转 bool）。
	asBool bool
	// nn 是 NOT NULL 列的回退值：源为 NULL 时用它代替
	// （显式插入 NULL 会违反 NOT NULL，列的 DEFAULT 不生效）。
	// nil 表示不需要回退。
	nn any
}

// fnn 构造带回退值的普通列映射（NOT NULL 无缺省列）。
func fnn(dst, src string, fallback any) field {
	return field{dst: dst, src: src, kind: kindPlain, nn: fallback}
}

// fdate 构造 date 目标列映射。
func fdate(dst, src string) field {
	return field{dst: dst, src: src, kind: kindPlain, date: true}
}

// fbool 构造 boolean 目标列映射。
func fbool(dst, src string) field {
	return field{dst: dst, src: src, kind: kindPlain, asBool: true}
}

// tableMap 描述一张表的导入契约。
type tableMap struct {
	source    string
	target    string
	fields    []field
	extraCols []string // 额外 SELECT 表达式（必须含 AS 别名）：派生列
	orderBy   string   // 覆盖 ORDER BY（默认 "id"）
}

// f 是 field 的简写构造。
func f(dst, src string) field { return field{dst: dst, src: src, kind: kindPlain} }

// fk 构造外键映射（src 列的源 ID 按 ref 表查 id_map）。
func fk(dst, src, refTable string) field {
	return field{dst: dst, src: src, kind: kindFKID, ref: refTable}
}

// acct 构造 account → user_id 映射。
func acct(dst, src string) field {
	return field{dst: dst, src: src, kind: kindAccount}
}

// deletedAt 构造 deleted → deleted_at 映射。
func deletedAt(dst string) field { return field{dst: dst, kind: kindDeletedAt} }

// konst 构造常量列。
func konst(dst, val string) field { return field{dst: dst, kind: kindConst, def: val} }

// tableMaps 是全部 S1/S2 表映射，顺序即拓扑序（与 importpkg.Tables 一致）。
// 新增表只需在此登记 + 在 importpkg.Tables 登记元数据。
var tableMaps = []tableMap{
	{
		source: "zt_user", target: "users",
		fields: []field{
			f("id", "id"),
			f("account", "account"),
			{kind: kindPassword, dst: "password_hash"},
			fnn("realname", "realname", ""),
			f("email", "email"),
			f("mobile", "mobile"),
			f("avatar", "avatar"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_product", target: "products",
		fields: []field{
			f("id", "id"),
			f("name", "name"),
			f("code", "code"),
			f("description", "desc"),
			acct("owner_id", "PO"),
			acct("created_by_id", "createdBy"),
			{kind: kindNowFallback, dst: "created_at", src: "createdDate"},
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_project", target: "projects",
		// AC-01：按 grade 升序保证父先于子（parent 引用同表）。
		orderBy: "grade, id",
		fields: []field{
			f("id", "id"),
			f("kind", "type"),
			fk("parent_id", "parent", "zt_project"),
			fnn("path", "path", ""),
			f("grade", "grade"),
			f("name", "name"),
			f("code", "code"),
			f("status", "status"),
			acct("pm_id", "PM"),
			f("description", "desc"),
			fdate("begin_date", "begin"),
			fdate("end_date", "end"),
			acct("opened_by_id", "openedBy"),
			{kind: kindNowFallback, dst: "opened_at", src: "openedDate"},
			acct("closed_by_id", "closedBy"),
			f("closed_at", "closedDate"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_branch", target: "branches",
		fields: []field{
			f("id", "id"),
			fk("product_id", "product", "zt_product"),
			f("name", "name"),
			f("status", "status"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_module", target: "modules",
		orderBy: "grade, id",
		fields: []field{
			f("id", "id"),
			f("type", "type"),
			fk("parent_id", "parent", "zt_module"),
			fnn("path", "path", ""),
			f("grade", "grade"),
			f("name", "name"),
			fnn("sort_order", "order", int64(0)),
			acct("owner_id", "owner"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_story", target: "stories",
		fields: []field{
			f("id", "id"),
			fk("product_id", "product", "zt_product"),
			fk("module_id", "module", "zt_module"),
			f("title", "title"),
			f("type", "type"),
			fnn("pri", "pri", int64(3)),
			f("estimate", "estimate"),
			f("status", "status"),
			f("stage", "stage"),
			fnn("spec_version", "version", int64(1)),
			f("source_note", "sourceNote"),
			acct("opened_by_id", "openedBy"),
			{kind: kindNowFallback, dst: "opened_at", src: "openedDate"},
			acct("assigned_to_id", "assignedTo"),
			f("assigned_at", "assignedDate"),
			acct("last_edited_by_id", "lastEditedBy"),
			f("last_edited_at", "lastEditedDate"),
			acct("closed_by_id", "closedBy"),
			f("closed_at", "closedDate"),
			f("closed_reason", "closedReason"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_productplan", target: "plans",
		fields: []field{
			f("id", "id"),
			fk("product_id", "product", "zt_product"),
			fk("branch_id", "branch", "zt_branch"),
			fk("parent_id", "parent", "zt_productplan"),
			f("title", "title"),
			f("status", "status"),
			f("desc", "desc"),
			fdate("begin_date", "begin"),
			fdate("end_date", "end"),
			f("finished_at", "finishedDate"),
			f("closed_at", "closedDate"),
			fnn("closed_reason", "closedReason", ""),
			acct("opened_by_id", "createdBy"),
			{kind: kindNowFallback, dst: "opened_at", src: "createdDate"},
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_planstory", target: "plan_stories",
		fields: []field{
			f("id", "id"),
			fk("plan_id", "plan", "zt_productplan"),
			fk("story_id", "story", "zt_story"),
			fnn("order", "order", int64(0)),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_release", target: "releases",
		fields: []field{
			f("id", "id"),
			fk("product_id", "product", "zt_product"),
			fk("branch_id", "branch", "zt_branch"),
			f("name", "name"),
			f("system", "system"),
			fbool("marker", "marker"),
			f("status", "status"),
			fdate("date", "date"),
			f("released_at", "releasedDate"),
			fnn("story_ids", "stories", ""),
			fnn("bug_ids", "bugs", ""),
			fnn("build_ids", "build", ""),
			fnn("mailto", "mailto", ""),
			fnn("notify", "notify", ""),
			f("desc", "desc"),
			acct("opened_by_id", "createdBy"),
			{kind: kindNowFallback, dst: "opened_at", src: "createdDate"},
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_build", target: "builds",
		fields: []field{
			f("id", "id"),
			fk("product_id", "product", "zt_product"),
			// execution 在禅道里是项目/迭代 ID（zt_project）。
			fk("project_id", "execution", "zt_project"),
			f("name", "name"),
			f("builds", "builds"),
			f("system", "system"),
			fnn("scm_path", "scmPath", ""),
			fnn("file_path", "filePath", ""),
			fdate("date", "date"),
			fnn("builder", "builder", ""),
			fnn("story_ids", "stories", ""),
			fnn("bug_ids", "bugs", ""),
			f("desc", "desc"),
			acct("created_by_id", "createdBy"),
			{kind: kindNowFallback, dst: "created_at", src: "createdDate"},
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_case", target: "cases",
		fields: []field{
			f("id", "id"),
			fk("product_id", "product", "zt_product"),
			fk("module_id", "module", "zt_module"),
			fk("story_id", "story", "zt_story"),
			fnn("story_version", "storyVersion", int64(0)),
			fk("branch_id", "branch", "zt_branch"),
			f("title", "title"),
			fnn("pri", "pri", int64(3)),
			f("type", "type"),
			f("status", "status"),
			fnn("precondition", "precondition", ""),
			f("keywords", "keywords"),
			f("version", "version"),
			acct("opened_by_id", "openedBy"),
			{kind: kindNowFallback, dst: "opened_at", src: "openedDate"},
			f("last_runner", "lastRunner"),
			f("last_run_at", "lastRunDate"),
			f("last_run_result", "lastRunResult"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_casestep", target: "case_steps",
		// __seq：同 case 内按 id 升序的行号（zentao 无显式序号列）。
		// 注意 case 是 MySQL 保留字，子查询内必须反引号包裹（bt() 只作用于
		// sourceColumns 的列名，extraCols 是裸 SQL 需手写反引号）。
		extraCols: []string{
			"(SELECT COUNT(*) FROM zt_casestep s2 WHERE s2.`case` = zt_casestep.`case` AND s2.id <= zt_casestep.id) AS __seq",
		},
		fields: []field{
			f("id", "id"),
			fk("case_id", "case", "zt_case"),
			f("seq", "__seq"),
			f("name", "desc"),
			{kind: kindJSONWrap, dst: "checks", src: "expect"},
			f("step_type", "type"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_testsuite", target: "test_suites",
		fields: []field{
			f("id", "id"),
			fk("product_id", "product", "zt_product"),
			f("name", "name"),
			f("desc", "desc"),
			f("type", "type"),
			acct("created_by_id", "addedBy"),
			{kind: kindNowFallback, dst: "created_at", src: "addedDate"},
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_suitecase", target: "suite_cases",
		fields: []field{
			f("id", "id"),
			fk("suite_id", "suite", "zt_testsuite"),
			fk("product_id", "product", "zt_product"),
			fk("case_id", "case", "zt_case"),
			f("case_version", "caseVersion"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_testtask", target: "test_tasks",
		fields: []field{
			f("id", "id"),
			f("name", "name"),
			fk("product_id", "product", "zt_product"),
			fk("project_id", "project", "zt_project"),
			fk("build_id", "build", "zt_build"),
			f("type", "type"),
			acct("owner_id", "owner"),
			fnn("pri", "pri", int64(3)),
			fdate("begin_date", "begin"),
			fdate("end_date", "end"),
			f("status", "status"),
			f("desc", "desc"),
			acct("opened_by_id", "createdBy"),
			{kind: kindNowFallback, dst: "opened_at", src: "createdDate"},
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_testrun", target: "test_task_cases",
		fields: []field{
			f("id", "id"),
			fk("test_task_id", "task", "zt_testtask"),
			fk("case_id", "case", "zt_case"),
			fnn("case_version", "caseVersion", int64(0)),
			acct("assigned_to_id", "assignedTo"),
			f("status", "status"),
			f("last_run_result", "lastRunResult"),
			f("last_run_at", "lastRunDate"),
			deletedAt("deleted_at"),
		},
	},
	{
		source: "zt_testresult", target: "test_results",
		// __task：经 zt_testrun 反查所属测试单；__run_no：同一 run 内的结果序号。
		extraCols: []string{
			"(SELECT task FROM zt_testrun WHERE id = zt_testresult.run) AS __task",
			"(SELECT COUNT(*) FROM zt_testresult r2 WHERE r2.run = zt_testresult.run AND r2.id <= zt_testresult.id) AS __run_no",
		},
		fields: []field{
			f("id", "id"),
			fk("test_task_id", "__task", "zt_testtask"),
			fk("case_id", "case", "zt_case"),
			f("case_version", "version"),
			f("run_no", "__run_no"),
			f("result", "caseResult"),
			f("step_results", "stepResults"),
			{kind: kindMinutesToMs, dst: "duration_ms", src: "duration"},
			konst("actor_type", "user"),
			{kind: kindPlain, dst: "actor_id", src: "lastRunner", nn: "imported"},
			f("raw_output", "ZTFResult"),
			{kind: kindNowFallback, dst: "executed_at", src: "date"},
			konst("trace_id", ""),
		},
	},
}

// rowCtx 是单行映射的上下文（错误记账用）。
type rowCtx struct {
	table  string
	srcID  int64
	runKey string
}

// resolver 是映射期的 ID 解析依赖（由 PG 记账实现，测试可注入假件）。
type resolver struct {
	// addPending 登记本批已成功映射的源行（解决批内前向依赖：父行先映射先登记）。
	addPending func(srcTable string, srcID int64)
	// fk 查 id_map：源表 + 源 ID → 目标 ID。不存在返回 (0, false)。
	fk func(ctx context.Context, srcTable string, srcID int64) (int64, bool, error)
	// account 按 account 字符串查 users.id；不存在时实现负责创建/回退 system
	// 并记账 MISSING_ACCOUNT。
	account func(ctx context.Context, rc rowCtx, account string) (int64, error)
}

// sourceColumns 返回该表的源列清单（含派生列，供 SELECT）。
func (tm tableMap) sourceColumns() []string {
	cols := make([]string, 0, len(tm.fields)+len(tm.extraCols))
	seen := map[string]bool{}
	add := func(c string) {
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		cols = append(cols, c)
	}
	for _, fl := range tm.fields {
		switch fl.kind {
		case kindConst, kindPassword, kindDeletedAt:
			// 不读源列。
		default:
			add(fl.src)
		}
	}
	for _, ex := range tm.extraCols {
		if i := strings.Index(strings.ToUpper(ex), " AS "); i >= 0 {
			add(strings.TrimSpace(ex[i+4:]))
		}
	}
	return cols
}

// bt 给 MySQL 标识符加反引号（desc/order/case 等保留字必须包裹）。
func bt(id string) string { return "`" + id + "`" }

// selectSQL 生成源库分页查询（REQ-01：id > cursor ORDER BY id LIMIT n）。
func (tm tableMap) selectSQL() string {
	cols := tm.sourceColumns()
	quoted := make([]string, 0, len(cols)+len(tm.extraCols))
	for _, c := range cols {
		quoted = append(quoted, bt(c))
	}
	quoted = append(quoted, tm.extraCols...)
	order := tm.orderBy
	if order == "" {
		order = "id"
	}
	return fmt.Sprintf("SELECT %s FROM %s WHERE id > ? ORDER BY %s LIMIT ?",
		strings.Join(quoted, ", "), tm.source, order)
}

// mapRow 把一行源数据映射为目标列 → 值（纯函数，除 resolver 外无副作用）。
// 返回 error 表示该行必须记入 import_errors 并跳过。
func mapRow(ctx context.Context, tm tableMap, row map[string]any, res resolver, rc rowCtx, now time.Time) (map[string]any, error) {
	out := make(map[string]any, len(tm.fields))
	for _, fl := range tm.fields {
		v, err := mapField(ctx, fl, row, res, rc, now)
		if err != nil {
			return nil, err
		}
		out[fl.dst] = v
	}
	return out, nil
}

// mapField 映射单列。
func mapField(ctx context.Context, fl field, row map[string]any, res resolver, rc rowCtx, now time.Time) (any, error) {
	switch fl.kind {
	case kindConst:
		return fl.def, nil
	case kindPassword:
		// REQ-08：源密码绝不导入；占位串任何 bcrypt 比对都不可能命中。
		return importpkg.NewPasswordPlaceholder(fmt.Sprintf("%s#%d", rc.table, rc.srcID)), nil
	case kindDeletedAt:
		if isDeleted(row) {
			return now, nil
		}
		return nil, nil
	case kindFKID:
		srcID, ok := toInt64(row[fl.src])
		if !ok || srcID <= 0 {
			return nil, nil // 0/空 = 无引用
		}
		tid, found, err := res.fk(ctx, fl.ref, srcID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, &rowError{code: "FK_MISSING",
				msg: fmt.Sprintf("外键悬空：%s.%s=%d 未在 id_map（%s）", rc.table, fl.src, srcID, fl.ref)}
		}
		return tid, nil
	case kindAccount:
		name := toStr(row[fl.src])
		if name == "" {
			return nil, nil
		}
		return res.account(ctx, rc, name)
	case kindJSONWrap:
		s := toStr(row[fl.src])
		if s == "" {
			return nil, nil
		}
		return jsonStringArray(s), nil
	case kindMinutesToMs:
		fv, ok := toFloat(row[fl.src])
		if !ok {
			return int64(0), nil
		}
		return int64(fv * 60000), nil
	case kindNowFallback:
		if t, ok := toTime(row[fl.src]); ok && !t.IsZero() {
			return t, nil
		}
		return now, nil
	default: // kindPlain
		v := plainValue(fl, row[fl.src])
		if v == nil && fl.nn != nil {
			return fl.nn, nil
		}
		return v, nil
	}
}

// plainValue 归一普通列：date 列格式化、bool 列转换、零时间置 NULL
// （zentao 的 0000-00-00 零值在 PG 无对应表示）。
func plainValue(fl field, v any) any {
	if fl.asBool {
		switch x := normalizeValue(v).(type) {
		case nil:
			return nil
		case bool:
			return x
		case int64:
			return x != 0
		case string:
			return x != "" && x != "0"
		default:
			return nil
		}
	}
	nv := normalizeValue(v)
	if t, ok := nv.(time.Time); ok {
		if t.IsZero() {
			return nil
		}
		if fl.date {
			return t.Format("2006-01-02")
		}
		return t
	}
	return nv
}

// rowError 是单行映射/写入失败（记 import_errors，不中断）。
type rowError struct {
	code string
	msg  string
}

func (e *rowError) Error() string { return e.msg }

// isDeleted 判定源行是否已软删（zentao deleted 为 '0'/'1' 字符串或 0/1）。
func isDeleted(row map[string]any) bool {
	v, ok := row["deleted"]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case string:
		return x == "1"
	case []byte:
		return string(x) == "1"
	case int64:
		return x == 1
	case bool:
		return x
	}
	return false
}
