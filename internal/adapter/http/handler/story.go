// Package handler 承载 HTTP 处理器：解析请求 → 调用 usecase → 映射响应。
package handler

import (
	"log/slog"
	"net/http"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	"github.com/mst8222/regto/internal/domain/story"
	ucstory "github.com/mst8222/regto/internal/usecase/story"
)

// StoryHandler 处理需求相关路由。
type StoryHandler struct {
	svc    *ucstory.Service
	logger *slog.Logger
}

// NewStoryHandler 构造处理器。
func NewStoryHandler(svc *ucstory.Service, logger *slog.Logger) *StoryHandler {
	return &StoryHandler{svc: svc, logger: logger}
}

// Create 处理 POST /api/v1/stories。
func (h *StoryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateStoryRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())

	st, err := h.svc.Create(r.Context(), ucstory.CreateCommand{
		ProductID:  req.ProductID,
		ModuleID:   req.ModuleID,
		Title:      req.Title,
		Type:       req.Type,
		Pri:        req.Pri,
		Estimate:   req.Estimate,
		Spec:       story.SpecInput{Title: req.Title, Spec: req.Spec, Verify: req.Verify, Pri: req.Pri},
		Reviewers:  req.Reviewers,
		SourceNote: req.SourceNote,
		AsDraft:    req.AsDraft,
		Actor:      actor,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.StoryFromDomain(st))
}

// List 处理 GET /api/v1/stories（分页/筛选/排序，见 SPEC-STORY-002）。
func (h *StoryHandler) List(w http.ResponseWriter, r *http.Request) {
	q, err := httputil.StoryQueryFromRequest(r)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	q = q.Normalize()
	items, total, err := h.svc.List(r.Context(), q)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	links := httputil.BuildListLinks(r, q.Page, total)
	httputil.WriteJSON(w, http.StatusOK, dto.NewStoryListResponse(items, total, q.Page, links))
}

// Get 处理 GET /api/v1/stories/{id}。
func (h *StoryHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	st, err := h.svc.Get(r.Context(), id)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.StoryFromDomain(st))
}

// Transition 处理 POST /api/v1/stories/{id}/transitions（统一状态转移入口）。
func (h *StoryHandler) Transition(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.TransitionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())

	results := make([]story.ReviewResult, 0, len(req.ReviewResults))
	for _, v := range req.ReviewResults {
		results = append(results, story.ReviewResult(v))
	}

	st, err := h.svc.Transition(r.Context(), ucstory.TransitionCommand{
		ID:            id,
		Action:        story.Action(req.Action),
		Spec:          req.Spec.ToDomain(),
		CloseReason:   req.CloseReason,
		ReviewResults: results,
		Reviewers:     req.Reviewers,
		Comment:       req.Comment,
		ReviewerID:    req.ReviewerID,
		Actor:         actor,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.StoryFromDomain(st))
}

// AdvanceStage 处理 POST /api/v1/stories/{id}/stage。
func (h *StoryHandler) AdvanceStage(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.PathID(r, "id")
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	var req dto.AdvanceStageRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	actor := httputil.ActorFromContext(r.Context())
	st, err := h.svc.AdvanceStage(r.Context(), ucstory.AdvanceStageCommand{
		ID: id, Stage: story.Stage(req.Stage), Actor: actor,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.StoryFromDomain(st))
}
