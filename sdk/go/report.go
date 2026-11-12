package regto

import (
	"context"
	"net/url"
	"strconv"
)

// ReportService 提供固定报表接口。
type ReportService struct{ c *Client }

// ReportMeta 是可用报表的元信息（对应 dto.ReportMeta）。
type ReportMeta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Scoped      bool   `json:"scoped"`
}

// Report 是单张报表的结果：列定义 + 行（行的键随报表而变，所以是 map）。
type Report struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	GeneratedAt string           `json:"generated_at"`
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
	ProductID   *int64           `json:"product_id,omitempty"`
}

// List 列出全部可用报表（6 张，字典序）。
func (s *ReportService) List(ctx context.Context) ([]ReportMeta, error) {
	var out struct {
		Data []ReportMeta `json:"data"`
	}
	if err := s.c.do(ctx, "GET", apiPath("/reports"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Run 跑指定报表；productID 为 0 表示不限定产品（对非 scoped 报表本就忽略）。
// 报表 ID 不在注册表时返回 *Error：StatusCode=400、Code=UNKNOWN_REPORT。
func (s *ReportService) Run(ctx context.Context, id string, productID int64) (*Report, error) {
	q := url.Values{}
	if productID > 0 {
		q.Set("product_id", strconv.FormatInt(productID, 10))
	}
	var out Report
	if err := s.c.do(ctx, "GET", apiPath("/reports/"+url.PathEscape(id)), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
