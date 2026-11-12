// Package handler 承载 HTTP 处理器：解析请求 → 调用 usecase → 映射响应。
package handler

import (
	"log/slog"
	"net/http"

	"github.com/mst8222/regto/internal/adapter/http/httputil"
	uctcase "github.com/mst8222/regto/internal/usecase/testcase"
)

// SuiteHandler 处理用例套件路由（REQ-03）。
type SuiteHandler struct {
	svc    *uctcase.Service
	logger *slog.Logger
}

// NewSuiteHandler 构造处理器。
func NewSuiteHandler(svc *uctcase.Service, logger *slog.Logger) *SuiteHandler {
	return &SuiteHandler{svc: svc, logger: logger}
}

// CreateSuiteRequest 是创建套件的请求体。
type CreateSuiteRequest struct {
	ProductID int64  `json:"product_id"`
	Name      string `json:"name"`
	Desc      string `json:"desc,omitempty"`
}

// Create 处理 POST /api/v1/suites（同产品下同名唯一，冲突 409）。
func (h *SuiteHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateSuiteRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())
	id, err := h.svc.CreateSuite(r.Context(), req.ProductID, req.Name, req.Desc, actor.ID)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"product_id": req.ProductID,
		"name":       req.Name,
		"desc":       req.Desc,
	})
}

// AddCasesRequest 是把用例加入套件的请求体。
type AddCasesRequest struct {
	CaseID  int64 `json:"case_id"`
	Version int   `json:"version,omitempty"`
}

// AddCases 处理 POST /api/v1/suites/{id}/cases（重复加入 409 SUITE_CASE_CONFLICT）。
// version 缺省取用例当前版本（套件记录加入时的版本快照）。
func (h *SuiteHandler) AddCases(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req AddCasesRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	version := req.Version
	if version <= 0 {
		c, err := h.svc.Get(r.Context(), req.CaseID)
		if err != nil {
			httputil.WriteProblem(w, r, h.logger, err)
			return
		}
		version = c.Version
	}
	if err := h.svc.AddToSuite(r.Context(), id, req.CaseID, version); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]any{
		"suite_id": id,
		"case_id":  req.CaseID,
		"version":  version,
	})
}
