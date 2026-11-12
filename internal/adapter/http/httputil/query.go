package httputil

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/domain/shared"
	"github.com/mst8222/regto/internal/domain/story"
)

// 查询参数名（ROADMAP §3.4：page / per_page / sort / filter）。
const (
	paramPage       = "page"
	paramPerPage    = "per_page"
	paramSort       = "sort"
	paramKeyword    = "keyword"
	paramIncludeDel = "include_deleted"
	paramProductID  = "product_id"
	paramModuleID   = "module_id"
	paramAssignedTo = "assigned_to_id"
	paramOpenedBy   = "opened_by_id"
	paramStatus     = "status"
	paramStage      = "stage"
	paramType       = "type"
)

// StoryQueryFromRequest 把查询字符串解析为领域查询条件。
// 解析失败一律返回 400 INVALID_QUERY（不静默忽略，避免客户端拿到错误结果而不自知）。
func StoryQueryFromRequest(r *http.Request) (story.StoryQuery, error) {
	vals := r.URL.Query()

	page, err := intParam(vals, paramPage)
	if err != nil {
		return story.StoryQuery{}, err
	}
	perPage, err := intParam(vals, paramPerPage)
	if err != nil {
		return story.StoryQuery{}, err
	}

	sortKeys, err := parseSort(vals.Get(paramSort))
	if err != nil {
		return story.StoryQuery{}, err
	}

	q := story.StoryQuery{
		Page:           shared.Page{Page: page, PerPage: perPage},
		Statuses:       statusesOf(splitList(vals.Get(paramStatus))),
		Stages:         stagesOf(splitList(vals.Get(paramStage))),
		Types:          splitList(vals.Get(paramType)),
		Keyword:        vals.Get(paramKeyword),
		IncludeDeleted: vals.Get(paramIncludeDel) == "true",
		Sort:           sortKeys,
	}
	if v := vals.Get(paramProductID); v != "" {
		id, ok := int64Param(v, paramProductID)
		if !ok {
			return story.StoryQuery{}, invalidQuery(paramProductID, v, nil)
		}
		q.ProductID = &id
	}
	if v := vals.Get(paramModuleID); v != "" {
		id, ok := int64Param(v, paramModuleID)
		if !ok {
			return story.StoryQuery{}, invalidQuery(paramModuleID, v, nil)
		}
		q.ModuleID = &id
	}
	if v := vals.Get(paramAssignedTo); v != "" {
		id, ok := int64Param(v, paramAssignedTo)
		if !ok {
			return story.StoryQuery{}, invalidQuery(paramAssignedTo, v, nil)
		}
		q.AssignedToID = &id
	}
	if v := vals.Get(paramOpenedBy); v != "" {
		id, ok := int64Param(v, paramOpenedBy)
		if !ok {
			return story.StoryQuery{}, invalidQuery(paramOpenedBy, v, nil)
		}
		q.OpenedByID = &id
	}
	return q, nil
}

// intParam 解析可选整数参数；缺省返回 0（由领域层 Normalize 收敛）。
func intParam(vals url.Values, name string) (int, error) {
	raw := vals.Get(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalidQuery(name, raw, nil)
	}
	return n, nil
}

// int64Param 解析正整数字符串。
func int64Param(raw, name string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// parseSort 解析 "a,-b" 形式的排序参数；空串返回 nil（由领域层填默认）。
func parseSort(raw string) ([]story.SortKey, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := splitList(raw)
	keys := make([]story.SortKey, 0, len(parts))
	for _, p := range parts {
		field, desc := p, false
		if strings.HasPrefix(p, "-") {
			field, desc = p[1:], true
		} else if strings.HasPrefix(p, "+") {
			field = p[1:]
		}
		keys = append(keys, story.SortKey{Field: field, Desc: desc})
	}
	return keys, nil
}

// splitList 按逗号切分并去空、去空白。
func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// statusesOf 把字符串切片转为状态切片（非法值留给领域层 Validate 拒绝）。
func statusesOf(vals []string) []story.Status {
	out := make([]story.Status, 0, len(vals))
	for _, v := range vals {
		out = append(out, story.Status(v))
	}
	return out
}

// stagesOf 把字符串切片转为阶段切片（非法值留给领域层 Validate 拒绝）。
func stagesOf(vals []string) []story.Stage {
	out := make([]story.Stage, 0, len(vals))
	for _, v := range vals {
		out = append(out, story.Stage(v))
	}
	return out
}

// BuildListLinks 生成分页导航链接（next / prev）。
// 链接回带当前全部筛选条件并覆盖 page/per_page，客户端可直接取用无需自行拼装。
func BuildListLinks(r *http.Request, page shared.Page, total int64) dto.ListLinks {
	var links dto.ListLinks
	if page.Page > 1 {
		links.Prev = linkWithPage(r, page.PerPage, page.Page-1)
	}
	if int64(page.Offset()+page.PerPage) < total {
		links.Next = linkWithPage(r, page.PerPage, page.Page+1)
	}
	return links
}

// linkWithPage 复制当前查询串并把 page/per_page 改写为指定值。
func linkWithPage(r *http.Request, perPage, page int) string {
	q := make(map[string][]string, len(r.URL.Query()))
	for k, v := range r.URL.Query() {
		q[k] = append([]string(nil), v...)
	}
	q[paramPage] = []string{strconv.Itoa(page)}
	q[paramPerPage] = []string{strconv.Itoa(perPage)}
	return r.URL.Path + "?" + url.Values(q).Encode()
}

// invalidQuery 构造 400 INVALID_QUERY 错误（meta 中带上字段名与允许值）。
func invalidQuery(field, value string, allowed []string) error {
	meta := map[string]any{"field": field, "value": value}
	if allowed != nil {
		meta["allowed"] = allowed
	}
	return shared.New("INVALID_QUERY", "invalid query parameter "+field+": "+value, meta)
}

// ---- 通用查询辅助（用例域等复用；领域中性，避免每个域复制一遍） ----

// SortField 是中性的排序键（handler 再映射到各领域的 SortKey）。
type SortField struct {
	Field string
	Desc  bool
}

// PageParams 从请求读取分页参数（page/per_page，非法值回落默认，超上限截断）。
func PageParams(r *http.Request) (page, perPage int) {
	p, _ := intParam(r.URL.Query(), "page")
	pp, _ := intParam(r.URL.Query(), "per_page")
	if p < 1 {
		p = 1
	}
	if pp < 1 {
		pp = shared.DefaultPerPage
	}
	if pp > shared.MaxPerPage {
		pp = shared.MaxPerPage
	}
	return p, pp
}

// ParseSortFields 解析 sort 参数（如 "-created_at,title"）为中性排序键。
// 白名单外字段返回 INVALID_QUERY（与 story 域行为一致）。
func ParseSortFields(r *http.Request, allowed []string) ([]SortField, error) {
	raw := r.URL.Query().Get("sort")
	if raw == "" {
		return nil, nil
	}
	var out []SortField
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		desc := false
		if strings.HasPrefix(part, "-") {
			desc = true
			part = part[1:]
		}
		ok := false
		for _, a := range allowed {
			if a == part {
				ok = true
				break
			}
		}
		if !ok {
			return nil, invalidQuery("sort", part, allowed)
		}
		out = append(out, SortField{Field: part, Desc: desc})
	}
	return out, nil
}

// Int64Param 导出版：解析 int64 查询参数（失败返回 false，由调用方转 400）。
func Int64Param(raw, name string) (int64, bool) { return int64Param(raw, name) }

// InvalidQuery 导出版：构造 400 INVALID_QUERY 错误。
func InvalidQuery(field, value string, allowed []string) error {
	return invalidQuery(field, value, allowed)
}
