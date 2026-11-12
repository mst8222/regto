// Package handler 承载 HTTP 处理器：解析请求 → 调用 usecase → 映射响应。
package handler

import (
	"log/slog"
	"net/http"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	drel "github.com/mst8222/regto/internal/domain/release"
	"github.com/mst8222/regto/internal/domain/shared"
	uctrel "github.com/mst8222/regto/internal/usecase/release"
)

// PlanHandler 处理产品计划相关路由（SPEC-RELEASE-001 REQ-01/02/03）。
type PlanHandler struct {
	svc    *uctrel.Service
	logger *slog.Logger
}

// NewPlanHandler 构造处理器。
func NewPlanHandler(svc *uctrel.Service, logger *slog.Logger) *PlanHandler {
	return &PlanHandler{svc: svc, logger: logger}
}

// Create 处理 POST /api/v1/plans。
func (h *PlanHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreatePlanRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	beginMs, err := dto.ParseDateMs("begin", req.Begin)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	endMs, err := dto.ParseDateMs("end", req.End)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())
	p, err := h.svc.CreatePlan(r.Context(), uctrel.CreatePlanCommand{
		ProductID: req.ProductID,
		BranchID:  req.BranchID,
		ParentID:  req.ParentID,
		Title:     req.Title,
		Desc:      req.Desc,
		BeginMs:   beginMs,
		EndMs:     endMs,
		ActorID:   actor.ID,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.PlanFromDomain(p))
}

// Get 处理 GET /api/v1/plans/{id}。
func (h *PlanHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	p, err := h.svc.GetPlan(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.PlanFromDomain(p))
}

// List 处理 GET /api/v1/plans。
func (h *PlanHandler) List(w http.ResponseWriter, r *http.Request) {
	q := uctrel.ListPlansQuery{}
	if v := r.URL.Query().Get("product_id"); v != "" {
		id, ok := httputil.Int64Param(v, "product_id")
		if !ok {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("product_id", v, nil))
			return
		}
		q.ProductID = id
	}
	if v := r.URL.Query().Get("status"); v != "" {
		st := drel.PlanStatus(v)
		if !st.Valid() {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("status", v, planStatusNames()))
			return
		}
		q.Status = st
	}
	q.Keyword = r.URL.Query().Get("keyword")
	q.Page, q.PerPage = httputil.PageParams(r)
	fields, err := httputil.ParseSortFields(r, drel.AllowedPlanSortFields)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	for _, f := range fields {
		q.Sort = append(q.Sort, drel.SortKey{Field: f.Field, Desc: f.Desc})
	}

	plans, total, err := h.svc.ListPlans(r.Context(), q)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	page := shared.Page{Page: q.Page, PerPage: q.PerPage, Total: int64(total),
		HasMore: int64(q.Page*q.PerPage) < int64(total)}
	httputil.WriteJSON(w, http.StatusOK, dto.PlanListResponse{
		Data:  dto.PlansFromDomain(plans),
		Page:  page,
		Links: httputil.BuildListLinks(r, page, int64(total)),
	})
}

// Update 处理 PUT /api/v1/plans/{id}。
func (h *PlanHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.UpdatePlanRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	cmd := uctrel.UpdatePlanCommand{PlanID: id}
	if req.Title != nil {
		cmd.Title = req.Title
	}
	if req.Desc != nil {
		cmd.Desc = req.Desc
	}
	if req.Begin != nil {
		ms, err := dto.ParseDateMs("begin", *req.Begin)
		if err != nil {
			httputil.WriteProblem(w, r, h.logger, err)
			return
		}
		cmd.BeginMs = ms
	}
	if req.End != nil {
		ms, err := dto.ParseDateMs("end", *req.End)
		if err != nil {
			httputil.WriteProblem(w, r, h.logger, err)
			return
		}
		cmd.EndMs = ms
	}
	p, err := h.svc.UpdatePlan(r.Context(), cmd)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.PlanFromDomain(p))
}

// Transition 处理 POST /api/v1/plans/{id}/transitions（REQ-02）。
func (h *PlanHandler) Transition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.PlanTransitionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	action := drel.PlanAction(req.Action)
	if !validPlanAction(action) {
		httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("action", req.Action, planActionNames()))
		return
	}
	p, err := h.svc.TransitionPlan(r.Context(), uctrel.TransitionPlanCommand{
		PlanID: id,
		Action: action,
		Reason: req.Reason,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.PlanFromDomain(p))
}

// LinkStory 处理 POST /api/v1/plans/{id}/stories（REQ-03，409 重复关联）。
func (h *PlanHandler) LinkStory(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.LinkPlanStoryRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	if err := h.svc.LinkStory(r.Context(), uctrel.LinkStoryCommand{PlanID: id, StoryID: req.StoryID}); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListStories 处理 GET /api/v1/plans/{id}/stories（REQ-03 列表）。
func (h *PlanHandler) ListStories(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	links, err := h.svc.ListPlanStories(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.PlanStoryListResponse{Data: dto.PlanStoriesFromDomain(links)})
}

// ---------- Release ----------

// ReleaseHandler 处理发布相关路由（SPEC-RELEASE-001 REQ-04/05）。
type ReleaseHandler struct {
	svc    *uctrel.Service
	logger *slog.Logger
}

// NewReleaseHandler 构造处理器。
func NewReleaseHandler(svc *uctrel.Service, logger *slog.Logger) *ReleaseHandler {
	return &ReleaseHandler{svc: svc, logger: logger}
}

// Create 处理 POST /api/v1/releases。
func (h *ReleaseHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateReleaseRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	dateMs, err := dto.ParseDateMs("date", req.Date)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	system := drel.System(req.System)
	if req.System != "" && !system.Valid() {
		httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("system", req.System, systemNames()))
		return
	}
	actor := httputil.ActorFromContext(r.Context())
	rel, err := h.svc.CreateRelease(r.Context(), uctrel.CreateReleaseCommand{
		ProductID: req.ProductID,
		BranchID:  req.BranchID,
		Name:      req.Name,
		System:    system,
		Marker:    req.Marker,
		DateMs:    dateMs,
		Desc:      req.Desc,
		ActorID:   actor.ID,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.ReleaseFromDomain(rel))
}

// Get 处理 GET /api/v1/releases/{id}。
func (h *ReleaseHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	rel, err := h.svc.GetRelease(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.ReleaseFromDomain(rel))
}

// List 处理 GET /api/v1/releases。
func (h *ReleaseHandler) List(w http.ResponseWriter, r *http.Request) {
	q := uctrel.ListReleasesQuery{}
	if v := r.URL.Query().Get("product_id"); v != "" {
		id, ok := httputil.Int64Param(v, "product_id")
		if !ok {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("product_id", v, nil))
			return
		}
		q.ProductID = id
	}
	if v := r.URL.Query().Get("status"); v != "" {
		st := drel.ReleaseStatus(v)
		if !st.Valid() {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("status", v, releaseStatusNames()))
			return
		}
		q.Status = st
	}
	q.Keyword = r.URL.Query().Get("keyword")
	q.Page, q.PerPage = httputil.PageParams(r)
	fields, err := httputil.ParseSortFields(r, drel.AllowedReleaseSortFields)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	for _, f := range fields {
		q.Sort = append(q.Sort, drel.SortKey{Field: f.Field, Desc: f.Desc})
	}

	rels, total, err := h.svc.ListReleases(r.Context(), q)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	page := shared.Page{Page: q.Page, PerPage: q.PerPage, Total: int64(total),
		HasMore: int64(q.Page*q.PerPage) < int64(total)}
	httputil.WriteJSON(w, http.StatusOK, dto.ReleaseListResponse{
		Data:  dto.ReleasesFromDomain(rels),
		Page:  page,
		Links: httputil.BuildListLinks(r, page, int64(total)),
	})
}

// Update 处理 PUT /api/v1/releases/{id}。
func (h *ReleaseHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.UpdateReleaseRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	cmd := uctrel.UpdateReleaseCommand{ReleaseID: id, Name: req.Name, Desc: req.Desc, Mailto: req.Mailto, Notify: req.Notify}
	if req.Date != nil {
		ms, err := dto.ParseDateMs("date", *req.Date)
		if err != nil {
			httputil.WriteProblem(w, r, h.logger, err)
			return
		}
		cmd.DateMs = ms
	}
	rel, err := h.svc.UpdateRelease(r.Context(), cmd)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.ReleaseFromDomain(rel))
}

// Transition 处理 POST /api/v1/releases/{id}/transitions（REQ-05）。
func (h *ReleaseHandler) Transition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.ReleaseTransitionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	action := drel.ReleaseAction(req.Action)
	if !validReleaseAction(action) {
		httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("action", req.Action, releaseActionNames()))
		return
	}
	rel, err := h.svc.TransitionRelease(r.Context(), uctrel.TransitionReleaseCommand{ReleaseID: id, Action: action})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.ReleaseFromDomain(rel))
}

// ---------- Build ----------

// BuildHandler 处理构建相关路由（SPEC-RELEASE-001 REQ-06）。
type BuildHandler struct {
	svc    *uctrel.Service
	logger *slog.Logger
}

// NewBuildHandler 构造处理器。
func NewBuildHandler(svc *uctrel.Service, logger *slog.Logger) *BuildHandler {
	return &BuildHandler{svc: svc, logger: logger}
}

// Create 处理 POST /api/v1/builds。
func (h *BuildHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateBuildRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	dateMs, err := dto.ParseDateMs("date", req.Date)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	system := drel.System(req.System)
	if req.System != "" && !system.Valid() {
		httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("system", req.System, systemNames()))
		return
	}
	actor := httputil.ActorFromContext(r.Context())
	b, err := h.svc.CreateBuild(r.Context(), uctrel.CreateBuildCommand{
		ProductID: req.ProductID,
		ProjectID: req.ProjectID,
		Name:      req.Name,
		Builds:    req.Builds,
		System:    system,
		DateMs:    dateMs,
		Builder:   req.Builder,
		Desc:      req.Desc,
		ActorID:   actor.ID,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.BuildFromDomain(b))
}

// Get 处理 GET /api/v1/builds/{id}。
func (h *BuildHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	b, err := h.svc.GetBuild(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.BuildFromDomain(b))
}

// List 处理 GET /api/v1/builds。
func (h *BuildHandler) List(w http.ResponseWriter, r *http.Request) {
	q := uctrel.ListBuildsQuery{}
	if v := r.URL.Query().Get("product_id"); v != "" {
		id, ok := httputil.Int64Param(v, "product_id")
		if !ok {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("product_id", v, nil))
			return
		}
		q.ProductID = id
	}
	q.Keyword = r.URL.Query().Get("keyword")
	q.Page, q.PerPage = httputil.PageParams(r)
	fields, err := httputil.ParseSortFields(r, drel.AllowedBuildSortFields)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	for _, f := range fields {
		q.Sort = append(q.Sort, drel.SortKey{Field: f.Field, Desc: f.Desc})
	}

	builds, total, err := h.svc.ListBuilds(r.Context(), q)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	page := shared.Page{Page: q.Page, PerPage: q.PerPage, Total: int64(total),
		HasMore: int64(q.Page*q.PerPage) < int64(total)}
	httputil.WriteJSON(w, http.StatusOK, dto.BuildListResponse{
		Data:  dto.BuildsFromDomain(builds),
		Page:  page,
		Links: httputil.BuildListLinks(r, page, int64(total)),
	})
}

// Update 处理 PUT /api/v1/builds/{id}。
func (h *BuildHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.UpdateBuildRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	cmd := uctrel.UpdateBuildCommand{
		BuildID: id, Name: req.Name, Builds: req.Builds,
		Builder: req.Builder, SCMPath: req.SCMPath, FilePath: req.FilePath, Desc: req.Desc,
	}
	if req.Date != nil {
		ms, err := dto.ParseDateMs("date", *req.Date)
		if err != nil {
			httputil.WriteProblem(w, r, h.logger, err)
			return
		}
		cmd.DateMs = ms
	}
	b, err := h.svc.UpdateBuild(r.Context(), cmd)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.BuildFromDomain(b))
}

// ---------- 枚举名辅助（错误提示与 CLI 共用） ----------

func planStatusNames() []string {
	all := drel.AllPlanStatuses()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func planActionNames() []string {
	all := drel.AllPlanActions()
	out := make([]string, 0, len(all))
	for _, a := range all {
		out = append(out, string(a))
	}
	return out
}

func releaseStatusNames() []string {
	all := drel.AllReleaseStatuses()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}

func releaseActionNames() []string {
	all := drel.AllReleaseActions()
	out := make([]string, 0, len(all))
	for _, a := range all {
		out = append(out, string(a))
	}
	return out
}

func systemNames() []string {
	return []string{string(drel.SystemTrunk), string(drel.SystemBranch)}
}

func validPlanAction(a drel.PlanAction) bool {
	for _, x := range drel.AllPlanActions() {
		if x == a {
			return true
		}
	}
	return false
}

func validReleaseAction(a drel.ReleaseAction) bool {
	for _, x := range drel.AllReleaseActions() {
		if x == a {
			return true
		}
	}
	return false
}
