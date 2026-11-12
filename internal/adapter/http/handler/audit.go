package handler

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	ucaudit "github.com/mst8222/regto/internal/usecase/audit"
)

// AuditHandler 处理审计报表列表与单张查询（SPEC-AUDIT-001）。
//
// 为什么单独一组路由而不是挂在 /api/v1/reports 下：ACL 按路由模式判定，
// 审计数据要沿用「留痕可见性」（guest 看不到 trace），只有独立路径才能
// 映射到 ResourceTrace+ActionView（DESIGN-AUDIT-001#DEC-01）。
type AuditHandler struct {
	svc    *ucaudit.Service
	logger *slog.Logger
}

// NewAuditHandler 构造处理器。
func NewAuditHandler(svc *ucaudit.Service, logger *slog.Logger) *AuditHandler {
	return &AuditHandler{svc: svc, logger: logger}
}

// List 处理 GET /api/v1/audit/reports：返回可用审计报表清单。
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	metas := h.svc.List()
	out := make([]dto.AuditReportMeta, 0, len(metas))
	for _, m := range metas {
		out = append(out, dto.AuditReportMetaFromDomain(m))
	}
	httputil.WriteJSON(w, http.StatusOK, dto.AuditReportListResponse{Data: out})
}

// Show 处理 GET /api/v1/audit/reports/{id}：执行单张审计报表。
func (h *AuditHandler) Show(w http.ResponseWriter, r *http.Request) {
	rep, err := h.svc.Run(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.AuditReportResponseFromDomain(rep))
}
