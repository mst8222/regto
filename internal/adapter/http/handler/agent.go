package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/mst8222/regto/internal/adapter/http/dto"
	"github.com/mst8222/regto/internal/adapter/http/httputil"
	dagent "github.com/mst8222/regto/internal/domain/agent"
	ucagent "github.com/mst8222/regto/internal/usecase/agent"
)

// ApprovalHandler 处理审批队列与决策（SPEC-AGENT-002 REQ-08 / REQ-09）。
type ApprovalHandler struct {
	svc    *ucagent.ApprovalService
	logger *slog.Logger
}

// NewApprovalHandler 构造处理器。
func NewApprovalHandler(svc *ucagent.ApprovalService, logger *slog.Logger) *ApprovalHandler {
	return &ApprovalHandler{svc: svc, logger: logger}
}

// List 处理 GET /api/v1/agent/approvals。
func (h *ApprovalHandler) List(w http.ResponseWriter, r *http.Request) {
	recs, err := h.svc.Pending(r.Context())
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	out := make([]dto.PendingApprovalResponse, 0, len(recs))
	for _, rec := range recs {
		out = append(out, dto.PendingApprovalFromDomain(rec))
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// Approve 处理 POST /api/v1/agent/approvals/{trace_id}/approve。
func (h *ApprovalHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, true)
}

// Reject 处理 POST /api/v1/agent/approvals/{trace_id}/reject。
func (h *ApprovalHandler) Reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, false)
}

// decide 是批准/驳回的共同实现。
//
// 审批人取认证主体（actor.ID），**不接受请求体自报**：
// 否则任何人都能签别人的名字，审计就失去意义（SPEC-AGENT-002 ASM-05）。
func (h *ApprovalHandler) decide(w http.ResponseWriter, r *http.Request, approve bool) {
	traceID := chi.URLParam(r, "trace_id")
	var req dto.ApprovalDecisionRequest
	// 允许空请求体：审批意见是可选信息。
	if r.ContentLength > 0 {
		if err := httputil.DecodeJSON(r, &req); err != nil {
			httputil.WriteProblem(w, r, h.logger, err)
			return
		}
	}
	rec, err := h.svc.Decide(r.Context(), traceID, humanLabel(r), approve, req.Comment)
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, dto.ApprovalDecisionResponse{
		TraceID: rec.TraceID,
		Seq:     rec.Seq,
		Action:  rec.Action,
		Target:  rec.ObjectID,
	})
}

// humanLabel 取人类主体的可读标识（审批记录的 reviewed_by / actor_id 用）。
//
// 为什么不用数字 id：留痕是给人看的审计材料，"谁批的"要一眼能认，
// 数字 id 还得反查用户表。account 缺失（理论上不会）时退回 id 字符串。
func humanLabel(r *http.Request) string {
	sub, ok := httputil.SubjectFromContext(r.Context())
	if !ok {
		return "unknown"
	}
	if sub.Account != "" {
		return sub.Account
	}
	if sub.ID != 0 {
		return strconv.FormatInt(sub.ID, 10)
	}
	return "unknown"
}

// AgentSessionHandler 处理 Agent 会话运维（SPEC-AGENT-002 REQ-10）。
type AgentSessionHandler struct {
	svc    *ucagent.SessionService
	logger *slog.Logger
}

// NewAgentSessionHandler 构造处理器。
func NewAgentSessionHandler(svc *ucagent.SessionService, logger *slog.Logger) *AgentSessionHandler {
	return &AgentSessionHandler{svc: svc, logger: logger}
}

// List 处理 GET /api/v1/agent/sessions。
func (h *AgentSessionHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.svc.List(r.Context())
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	out := make([]dto.AgentSessionResponse, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, dto.AgentSessionFromDomain(s))
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

// Create 处理 POST /api/v1/agent/sessions。
func (h *AgentSessionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateAgentSessionRequest
	if err := httputil.DecodeJSON(r, &req); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	// 责任人缺省记为当前操作者：会话是「这个人类批的授权」，不能没有主。
	if req.OwnerID == 0 {
		req.OwnerID = httputil.ActorFromContext(r.Context()).ID
	}
	sess, err := h.svc.Create(r.Context(), dagent.CreateSessionInput{
		AgentID:        req.AgentID,
		DisplayName:    req.DisplayName,
		Model:          req.Model,
		Capabilities:   req.Capabilities,
		ApprovalPolicy: req.ApprovalPolicy,
		OwnerID:        req.OwnerID,
		ExpiresAtMs:    req.ExpiresAtMs,
	})
	if err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, dto.AgentSessionFromDomain(sess))
}

// Revoke 处理 POST /api/v1/agent/sessions/{agent_id}/revoke。
func (h *AgentSessionHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agent_id")
	if err := h.svc.Revoke(r.Context(), agentID); err != nil {
		httputil.WriteProblem(w, r, h.logger, err)
		return
	}
	httputil.WriteJSON(w, http.StatusNoContent, nil)
}
