package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	"github.com/mst8222/regto/internal/domain/shared"
	ucreport "github.com/mst8222/regto/internal/usecase/report"
)

// ReportHandler 处理报表列表与单张报表查询（SPEC-REPORT-001）。
type ReportHandler struct {
	svc    *ucreport.Service
	logger *slog.Logger
}

// NewReportHandler 构造处理器。
func NewReportHandler(svc *ucreport.Service, logger *slog.Logger) *ReportHandler {
	return &ReportHandler{svc: svc, logger: logger}
}

// List 处理 GET /api/v1/reports：返回可用报表清单。
func (h *ReportHandler) List(w http.ResponseWriter, r *http.Request) {
	metas := h.svc.List()
	out := make([]dto.ReportMeta, 0, len(metas))
	for _, m := range metas {
		out = append(out, dto.ReportMetaFromDomain(m))
	}
	httputil.WriteJSON(w, http.StatusOK, dto.ReportListResponse{Data: out})
}

// Show 处理 GET /api/v1/reports/{id}：执行单张报表，支持 ?product_id= 过滤。
func (h *ReportHandler) Show(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var productID *int64
	if raw := r.URL.Query().Get("product_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			httputil.WriteProblem(w, r, h.logger,
				shared.New("INVALID_QUERY", "product_id 必须是整数", map[string]any{"product_id": raw}))
			return
		}
		productID = &n
	}

	rep, err := h.svc.Run(r.Context(), id, productID)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.ReportResponseFromDomain(rep))
}
