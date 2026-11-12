package dto

import (
	"github.com/mst8222/regto/internal/domain/report"
)

// ReportMeta 是报表清单里的单条元信息。
type ReportMeta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Scoped      bool   `json:"scoped"`
}

// ReportMetaFromDomain 把领域元信息转 DTO。
func ReportMetaFromDomain(m report.Meta) ReportMeta {
	return ReportMeta{
		ID:          string(m.ID),
		Title:       m.Title,
		Description: m.Description,
		Category:    m.Category,
		Scoped:      m.Scoped,
	}
}

// ReportListResponse 是报表清单信封。
//
// 为什么也要套信封：项目约定「集合恒定在 data 键下，客户端可泛型解析」
// （见 dto.NewStoryListResponse 的注释）。清单此前直接返回裸数组，
// 与需求/用例列表不一致，客户端不得不为这一个接口写特例——对齐掉。
type ReportListResponse struct {
	Data []ReportMeta `json:"data"`
}

// ReportResponse 是单张报表的返回结构，与 report.Report 一一对应。
type ReportResponse struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	GeneratedAt string           `json:"generated_at"` // RFC3339
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
}

// ReportResponseFromDomain 把领域报表转 DTO。
func ReportResponseFromDomain(r *report.Report) ReportResponse {
	rows := make([]map[string]any, 0, len(r.Rows))
	rows = append(rows, r.Rows...)
	return ReportResponse{
		ID:          string(r.ID),
		Title:       r.Title,
		GeneratedAt: r.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"),
		Columns:     r.Columns,
		Rows:        rows,
	}
}
