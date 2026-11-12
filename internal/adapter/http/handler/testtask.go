// Package handler 承载 HTTP 处理器：解析请求 → 调用 usecase → 映射响应。
package handler

import (
	"log/slog"
	"net/http"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	dtt "github.com/mst8222/regto/internal/domain/testtask"
	ucttask "github.com/mst8222/regto/internal/usecase/testtask"
)

// TestTaskHandler 处理测试单与执行结果路由。
type TestTaskHandler struct {
	svc    *ucttask.Service
	logger *slog.Logger
}

// NewTestTaskHandler 构造处理器。
func NewTestTaskHandler(svc *ucttask.Service, logger *slog.Logger) *TestTaskHandler {
	return &TestTaskHandler{svc: svc, logger: logger}
}

// Create 处理 POST /api/v1/test-tasks。
func (h *TestTaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateTestTaskRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())
	owner := req.OwnerID
	if owner == 0 {
		owner = actor.ID // 缺省取当前主体（人类即用户 ID）
	}
	t, err := h.svc.Create(r.Context(), ucttask.CreateCommand{
		Name:      req.Name,
		ProductID: req.ProductID,
		ProjectID: req.ProjectID,
		BuildID:   req.BuildID,
		Type:      req.Type,
		OwnerID:   owner,
		Pri:       req.Pri,
		Desc:      req.Desc,
		ActorID:   actor.ID,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.TestTaskFromDomain(t))
}

// Get 处理 GET /api/v1/test-tasks/{id}。
func (h *TestTaskHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.TestTaskFromDomain(t))
}

// List 处理 GET /api/v1/test-tasks。
func (h *TestTaskHandler) List(w http.ResponseWriter, r *http.Request) {
	q := ucttask.ListQuery{}
	if v := r.URL.Query().Get("product_id"); v != "" {
		id, ok := httputil.Int64Param(v, "product_id")
		if !ok {
			httputil.WriteProblem(w, r, h.logger, httputil.InvalidQuery("product_id", v, nil))
			return
		}
		q.ProductID = id
	}
	if v := r.URL.Query().Get("status"); v != "" {
		st := dtt.Status(v)
		if !st.Valid() {
			httputil.WriteProblem(w, r, h.logger,
				httputil.InvalidQuery("status", v, dttStatusNames()))
			return
		}
		q.Status = st
	}
	q.Keyword = r.URL.Query().Get("keyword")
	q.Page, q.PerPage = httputil.PageParams(r)

	tasks, total, err := h.svc.List(r.Context(), q)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	items := make([]dto.TestTaskResponse, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, dto.TestTaskFromDomain(t))
	}
	page := pageOf(q.Page, q.PerPage, total)
	httputil.WriteJSON(w, http.StatusOK, dto.NewTaskListResponse(items, page,
		httputil.BuildListLinks(r, page, int64(total))))
}

// Transition 处理 POST /api/v1/test-tasks/{id}/transitions。
func (h *TestTaskHandler) Transition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.TestTaskTransitionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	t, err := h.svc.Transition(r.Context(), id, dtt.Action(req.Action))
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.TestTaskFromDomain(t))
}

// AddCases 处理 POST /api/v1/test-tasks/{id}/cases（锁定版本快照）。
func (h *TestTaskHandler) AddCases(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.AddCasesRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	added, err := h.svc.AddCases(r.Context(), id, req.CaseIDs)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]any{"added": added})
}

// ListCases 处理 GET /api/v1/test-tasks/{id}/cases。
func (h *TestTaskHandler) ListCases(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	cases, err := h.svc.ListCases(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	items := make([]dto.TaskCaseResponse, 0, len(cases))
	for _, c := range cases {
		items = append(items, dto.TaskCaseResponse{
			CaseID:        c.CaseID,
			CaseVersion:   c.CaseVersion,
			CaseTitle:     c.CaseTitle,
			Status:        string(c.Status),
			LastRunResult: string(c.LastRunResult),
		})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"data": items})
}

// ImportResult 处理 POST /api/v1/test-tasks/{id}/import-result（go test -json）。
func (h *TestTaskHandler) ImportResult(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.ImportResultRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	// 主体：认证上下文优先（客户端无法自报，DESIGN-AUTHN-001 DEC-02）；
	// AI 主体用 agent_id（如 regto-coder），人类用登录名，均只作展示与追溯。
	sub, _ := httputil.SubjectFromContext(r.Context())
	actorType := req.ActorType
	actorID := req.ActorID
	if actorType == "" {
		if sub.Kind != "" {
			actorType = sub.Kind
		} else {
			actorType = ucttask.DefaultActorType
		}
	}
	if actorID == "" {
		switch {
		case sub.AgentID != "":
			actorID = sub.AgentID
		case sub.Account != "":
			actorID = sub.Account
		default:
			actorID = ucttask.DefaultActorID
		}
	}
	res, err := h.svc.ImportGoTestJSON(r.Context(), ucttask.ImportCommand{
		TestTaskID:    id,
		ProductID:     req.ProductID,
		Events:        req.Events,
		ActorType:     actorType,
		ActorID:       actorID,
		TraceID:       req.TraceID,
		RunNo:         req.RunNo,
		CreateMissing: req.CreateMissing,
		DryRun:        req.DryRun,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	out := dto.ImportResultResponse{
		TestTaskID: res.TestTaskID,
		RunNo:      res.RunNo,
		Total:      res.Total,
		Written:    res.Written,
		Matched:    res.Matched,
		Created:    res.Created,
		ByResult:   map[string]int{},
	}
	for k, v := range res.ByResult {
		out.ByResult[string(k)] = v
	}
	for _, rr := range res.Results {
		out.Results = append(out.Results, dto.ResultFromDomain(rr))
	}
	httputil.WriteJSON(w, http.StatusCreated, out)
}

// ListResults 处理 GET /api/v1/test-tasks/{id}/results。
func (h *TestTaskHandler) ListResults(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	page, perPage := httputil.PageParams(r)
	results, total, err := h.svc.ListResults(r.Context(), id, page, perPage)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	items := make([]dto.ResultResponse, 0, len(results))
	for _, rr := range results {
		items = append(items, dto.ResultFromDomain(rr))
	}
	pg := pageOf(page, perPage, total)
	httputil.WriteJSON(w, http.StatusOK, dto.NewResultListResponse(items, pg,
		httputil.BuildListLinks(r, pg, int64(total))))
}

// Summary 处理 GET /api/v1/test-tasks/{id}/summary（各结果计数）。
func (h *TestTaskHandler) Summary(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	counts, err := h.svc.CountResults(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	out := map[string]int{}
	for k, v := range counts {
		out[string(k)] = v
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

// dttStatusNames 返回全部测试单状态名（错误提示用）。
func dttStatusNames() []string {
	all := dtt.AllStatuses()
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, string(s))
	}
	return out
}
