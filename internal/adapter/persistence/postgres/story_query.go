package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mst8222/regto/internal/domain/story"
)

// List 返回需求列表投影与满足条件的总数（DESIGN-STORY-002 DEC-04 / DEC-05）。
//
// 关键点：
//   - 用户输入（筛选值、排序键）先经领域层校验，此处的第二道白名单把它们
//     映射为**固定 SQL 片段**，用户输入永远不进入 SQL 文本（NFR-01）；
//   - 总数用 COUNT(*) OVER() 与数据行在同一快照内取回，避免两条查询口径不一致；
//   - 排序末尾强制追加 id ASC 作为 tie-break，保证翻页稳定（NFR-02）。
func (r *StoryRepo) List(ctx context.Context, q story.StoryQuery) ([]story.StoryListItem, int64, error) {
	q = q.Normalize()
	if err := q.Validate(); err != nil {
		return nil, 0, err
	}

	where, args := BuildStoryFilter(q)
	query := fmt.Sprintf(`
		SELECT id, product_id, module_id, title, type, pri, status, stage, spec_version,
		       closed_reason, assigned_to_id, opened_by_id, created_at, updated_at, deleted_at,
		       COUNT(*) OVER() AS total_count
		  FROM stories
		  %s
		  %s
		 LIMIT %d OFFSET %d`,
		where, BuildStoryOrderBy(q.Sort), q.Page.PerPage, q.Page.Offset())

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询 stories 列表失败: %w", err)
	}
	defer rows.Close()

	// 空结果也必须返回空切片（而非 nil），否则 JSON 序列化为 null（AC-04 / TC-04）。
	items := make([]story.StoryListItem, 0, q.Page.PerPage)
	var total int64
	for rows.Next() {
		var (
			it        story.StoryListItem
			createdAt time.Time
			updatedAt time.Time
			deletedAt *time.Time
			closeRsn  *string
		)
		if err := rows.Scan(
			&it.ID, &it.ProductID, &it.ModuleID, &it.Title, &it.Type, &it.Pri,
			&it.Status, &it.Stage, &it.SpecVersion, &closeRsn, &it.AssignedToID,
			&it.OpenedByID, &createdAt, &updatedAt, &deletedAt, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("扫描 stories 列表失败: %w", err)
		}
		if closeRsn != nil {
			it.ClosedReason = *closeRsn
		}
		it.CreatedAtMs = msOf(&createdAt)
		it.UpdatedAtMs = msOf(&updatedAt)
		it.DeletedAtMs = msPtr(deletedAt)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("遍历 stories 列表失败: %w", err)
	}
	return items, total, nil
}

// buildStoryFilter 把查询条件编译为 WHERE 子句与绑定参数。
// 所有字面量片段来自本函数内的常量，用户输入只作为 $n 参数绑定。
func BuildStoryFilter(q story.StoryQuery) (string, []any) {
	// 导出仅为可测（tests/unit 位于外部包）；不应被业务代码直接调用。
	var (
		conds []string
		args  []any
	)
	add := func(frag string, vals ...any) {
		for _, v := range vals {
			args = append(args, v)
			frag = strings.Replace(frag, "?", fmt.Sprintf("$%d", len(args)), 1)
		}
		conds = append(conds, frag)
	}

	if !q.IncludeDeleted {
		conds = append(conds, "deleted_at IS NULL")
	}
	if q.ProductID != nil {
		add("product_id = ?", *q.ProductID)
	}
	if q.ModuleID != nil {
		add("module_id = ?", *q.ModuleID)
	}
	if q.AssignedToID != nil {
		add("assigned_to_id = ?", *q.AssignedToID)
	}
	if q.OpenedByID != nil {
		add("opened_by_id = ?", *q.OpenedByID)
	}
	if len(q.Statuses) > 0 {
		vals := make([]any, 0, len(q.Statuses))
		for _, s := range q.Statuses {
			vals = append(vals, string(s))
		}
		add("status IN ("+placeholders(len(q.Statuses))+")", vals...)
	}
	if len(q.Stages) > 0 {
		vals := make([]any, 0, len(q.Stages))
		for _, s := range q.Stages {
			vals = append(vals, string(s))
		}
		add("stage IN ("+placeholders(len(q.Stages))+")", vals...)
	}
	if len(q.Types) > 0 {
		vals := make([]any, 0, len(q.Types))
		for _, t := range q.Types {
			vals = append(vals, t)
		}
		add("type IN ("+placeholders(len(q.Types))+")", vals...)
	}
	if q.Keyword != "" {
		add("title ILIKE ? ESCAPE '\\'", "%"+escapeLike(q.Keyword)+"%")
	}

	if len(conds) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// buildStoryOrderBy 把排序键编译为 ORDER BY 子句，并追加 id 作为最终 tie-break。
func BuildStoryOrderBy(keys []story.SortKey) string {
	// 导出仅为可测（tests/unit 位于外部包）；不应被业务代码直接调用。
	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		col, ok := sortColumn(k.Field)
		if !ok {
			continue // 已由领域层 Validate 拦截，此处只做防御
		}
		dir := "ASC"
		if k.Desc {
			dir = "DESC"
		}
		parts = append(parts, col+" "+dir)
	}
	parts = append(parts, "id ASC")
	return "ORDER BY " + strings.Join(parts, ", ")
}

// sortColumn 是排序字段 → 列名的白名单映射（值来自常量，非用户输入）。
func sortColumn(field string) (string, bool) {
	switch field {
	case story.SortCreatedAt:
		return "created_at", true
	case story.SortUpdatedAt:
		return "updated_at", true
	case story.SortPri:
		return "pri", true
	case story.SortID:
		return "id", true
	case story.SortTitle:
		return "title", true
	}
	return "", false
}

// placeholders 生成 n 个占位符（? 由 add 替换为 $k）。
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// escapeLike 转义 LIKE 模式中的通配符，防止用户输入改变匹配语义。
// 注意：这不影响 SQL 注入（参数已绑定），只保证"搜 % 就是搜 % 字符"。
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}
