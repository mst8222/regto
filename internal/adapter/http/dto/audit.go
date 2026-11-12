package dto

import (
	"github.com/mst8222/regto/internal/domain/audit"
)

// AuditReportMeta 是审计报表清单里的单条元信息。
type AuditReportMeta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// AuditReportMetaFromDomain 把领域元信息转 DTO。
func AuditReportMetaFromDomain(m audit.Meta) AuditReportMeta {
	return AuditReportMeta{
		ID:          string(m.ID),
		Title:       m.Title,
		Description: m.Description,
		Category:    m.Category,
	}
}

// AuditReportListResponse 是审计报表清单信封（集合恒定在 data 键下）。
type AuditReportListResponse struct {
	Data []AuditReportMeta `json:"data"`
}

// AuditReportResponse 是单张审计报表的返回结构。
type AuditReportResponse struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	GeneratedAt string           `json:"generated_at"` // RFC3339
	Columns     []string         `json:"columns"`
	Rows        []map[string]any `json:"rows"`
}

// AuditReportResponseFromDomain 把领域审计报表转 DTO。
func AuditReportResponseFromDomain(r *audit.Report) AuditReportResponse {
	rows := make([]map[string]any, 0, len(r.Rows))
	rows = append(rows, r.Rows...)
	return AuditReportResponse{
		ID:          string(r.ID),
		Title:       r.Title,
		GeneratedAt: r.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"),
		Columns:     r.Columns,
		Rows:        rows,
	}
}
