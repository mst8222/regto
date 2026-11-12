// Package handler 承载 HTTP 处理器：解析请求 → 调用 usecase → 映射响应。
package handler

import (
	"log/slog"
	"net/http"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	"github.com/mst8222/regto/internal/domain/shared"
	dtcase "github.com/mst8222/regto/internal/domain/testcase"
	uctcase "github.com/mst8222/regto/internal/usecase/testcase"
)

// CaseHandler 处理用例相关路由。
type CaseHandler struct {
	svc    *uctcase.Service
	logger *slog.Logger
}

// NewCaseHandler 构造处理器。
func NewCaseHandler(svc *uctcase.Service, logger *slog.Logger) *CaseHandler {
	return &CaseHandler{svc: svc, logger: logger}
}

// Create 处理 POST /api/v1/cases。
func (h *CaseHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateCaseRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())

	c, err := h.svc.Create(r.Context(), uctcase.CreateCommand{
		ProductID:    req.ProductID,
		ModuleID:     req.ModuleID,
		StoryID:      req.StoryID,
		StoryVersion: req.StoryVersion,
		BranchID:     req.BranchID,
		Title:        req.Title,
		Precondition: req.Precondition,
		Keywords:     req.Keywords,
		Pri:          req.Pri,
		Type:         dtcase.CaseType(req.Type),
		ExternalID:   req.ExternalID,
		ActorID:      actor.ID,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.CaseFromDomain(c))
}

// Get 处理 GET /api/v1/cases/{id}。
func (h *CaseHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	c, err := h.svc.Get(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.CaseFromDomain(c))
}

// List 处理 GET /api/v1/cases（分页/筛选/排序）。
func (h *CaseHandler) List(w http.ResponseWriter, r *http.Request) {
	q := uctcase.ListQuery{}
	if v := r.URL.Query().Get("product_id"); v != "" {
		id, ok := httputil.Int64Param(v, "product_id")
		if !ok {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("product_id", v, nil))
			return
		}
		q.ProductID = id
	}
	if v := r.URL.Query().Get("module_id"); v != "" {
		id, ok := httputil.Int64Param(v, "module_id")
		if !ok {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("module_id", v, nil))
			return
		}
		q.ModuleID = id
	}
	if v := r.URL.Query().Get("story_id"); v != "" {
		id, ok := httputil.Int64Param(v, "story_id")
		if !ok {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("story_id", v, nil))
			return
		}
		q.StoryID = id
	}
	if v := r.URL.Query().Get("status"); v != "" {
		st := dtcase.Status(v)
		if !st.Valid() {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("status", v, dtcaseStatusNames()))
			return
		}
		q.Status = st
	}
	if v := r.URL.Query().Get("type"); v != "" {
		ct := dtcase.CaseType(v)
		if !ct.Valid() {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("type", v, dtcaseTypeNames()))
			return
		}
		q.Type = ct
	}
	q.Keyword = r.URL.Query().Get("keyword")
	q.ExternalID = r.URL.Query().Get("external_id")
	q.Page, q.PerPage = httputil.PageParams(r)
	fields, err := httputil.ParseSortFields(r, dtcase.AllowedSortFields)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	for _, f := range fields {
		q.Sort = append(q.Sort, dtcase.SortKey{Field: f.Field, Desc: f.Desc})
	}

	cases, total, err := h.svc.List(r.Context(), q)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	items := make([]dto.CaseResponse, 0, len(cases))
	for _, c := range cases {
		items = append(items, dto.CaseFromDomain(c))
	}
	page := shared.Page{Page: q.Page, PerPage: q.PerPage, Total: int64(total),
		HasMore: int64(q.Page*q.PerPage) < int64(total)}
	httputil.WriteJSON(w, http.StatusOK,
		dto.NewCaseListResponse(items, total, page, httputil.BuildListLinks(r, page, int64(total))))
}

// Transition 处理 POST /api/v1/cases/{id}/transitions。
func (h *CaseHandler) Transition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.CaseTransitionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	action := dtcase.Action(req.Action)
	c, err := h.svc.Transition(r.Context(), uctcase.TransitionCommand{
		CaseID: id,
		Action: action,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.CaseFromDomain(c))
}

// Update 处理 PUT /api/v1/cases/{id}。
func (h *CaseHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.UpdateCaseRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	c, err := h.svc.Update(r.Context(), uctcase.UpdateCommand{
		CaseID:       id,
		Title:        req.Title,
		Precondition: req.Precondition,
		Keywords:     req.Keywords,
		Pri:          req.Pri,
		Type:         dtcase.CaseType(req.Type),
		ModuleID:     req.ModuleID,
		StoryID:      req.StoryID,
		StoryVersion: req.StoryVersion,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.CaseFromDomain(c))
}

// ImportZTF 处理 POST /api/v1/cases/import-ztf（REQ-05）。
func (h *CaseHandler) ImportZTF(w http.ResponseWriter, r *http.Request) {
	var req dto.ImportZTFRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())
	res, err := h.svc.ImportZTF(r.Context(), uctcase.ImportZTFCommand{
		ZTF:              req.ZTF,
		DefaultProductID: req.DefaultProductID,
		ActorID:          actor.ID,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.ImportZTFResponse{
		Total:   res.Total,
		Created: res.Created,
		Cases:   dto.CasesFromDomain(res.Cases),
	})
}

// ExportZTF 处理 GET /api/v1/cases/{id}/export-ztf（REQ-06，text/plain）。
func (h *CaseHandler) ExportZTF(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	text, err := h.svc.ExportZTF(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(text))
}

// pageOf 构造分页信息（页号非法已由 PageParams 收敛）。
func pageOf(page, perPage, total int) shared.Page {
	return shared.Page{
		Page:    page,
		PerPage: perPage,
		Total:   int64(total),
		HasMore: int64(page*perPage) < int64(total),
	}
}

// dtcaseStatusNames 返回全部用例状态名（错误提示用）。
func dtcaseStatusNames() []string {
	all := dtcase.AllStatuses()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

// dtcaseTypeNames 返回全部用例类型名（错误提示用）。
func dtcaseTypeNames() []string {
	return []string{
		string(dtcase.TypeFeature), string(dtcase.TypePerformance),
		string(dtcase.TypeConfig), string(dtcase.TypeInstall),
	}
}
