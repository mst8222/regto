package regto

import (
	"context"
	"net/url"
)

// AuditService 提供审计报表接口（SPEC-AUDIT-001）。
//
// 与 ReportService 分开：审计数据来自留痕、可见性归 trace.view，
// 路径与权限都不同，合成一个服务会让调用方误以为两者可互换。
type AuditService struct{ c *Client }

// AuditReportMeta 是可用审计报表的元信息。
type AuditReportMeta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

// AuditReport 是单张审计报表的结果。
type AuditReport struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	GeneratedAt string           `json:"generated_at"`
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
}

// List 列出全部可用审计报表（3 张，字典序）。
func (s *AuditService) List(ctx context.Context) ([]AuditReportMeta, error) {
	var out struct {
		Data []AuditReportMeta `json:"data"`
	}
	if err := s.c.do(ctx, "GET", apiPath("/audit/reports"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Run 跑指定审计报表；id 不在注册表时返回 *Error：StatusCode=400、Code=UNKNOWN_REPORT。
func (s *AuditService) Run(ctx context.Context, id string) (*AuditReport, error) {
	var out AuditReport
	if err := s.c.do(ctx, "GET", apiPath("/audit/reports/"+url.PathEscape(id)), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
