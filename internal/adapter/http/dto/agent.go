package dto

import (
	dagent "github.com/mst8222/regto/internal/domain/agent"
	dtrace "github.com/mst8222/regto/internal/domain/trace"
)

// CreateAgentSessionRequest 是 POST /api/v1/agent/sessions 的请求体。
type CreateAgentSessionRequest struct {
	AgentID        string            `json:"agent_id"`
	DisplayName    string            `json:"display_name"`
	Model          string            `json:"model"`
	Capabilities   []string          `json:"capabilities"`
	ApprovalPolicy map[string]string `json:"approval_policy"`
	OwnerID        int64             `json:"owner_id"`
	ExpiresAtMs    int64             `json:"expires_at_ms"`
}

// AgentSessionResponse 是会话的对外表示。
type AgentSessionResponse struct {
	ID             int64             `json:"id"`
	AgentID        string            `json:"agent_id"`
	DisplayName    string            `json:"display_name"`
	Model          string            `json:"model,omitempty"`
	Capabilities   []string          `json:"capabilities"`
	ApprovalPolicy map[string]string `json:"approval_policy,omitempty"`
	OwnerID        int64             `json:"owner_id,omitempty"`
	Status         string            `json:"status"`
	CreatedAtMs    int64             `json:"created_at_ms"`
	ExpiresAtMs    int64             `json:"expires_at_ms,omitempty"`
}

// AgentSessionFromDomain 把领域视图转为响应体。
func AgentSessionFromDomain(s dagent.RegisteredSession) AgentSessionResponse {
	caps := s.Capabilities
	if caps == nil {
		caps = []string{}
	}
	return AgentSessionResponse{
		ID:             s.ID,
		AgentID:        s.AgentID,
		DisplayName:    s.DisplayName,
		Model:          s.Model,
		Capabilities:   caps,
		ApprovalPolicy: s.ApprovalPolicy,
		OwnerID:        s.OwnerID,
		Status:         string(s.Status),
		CreatedAtMs:    s.CreatedAtMs,
		ExpiresAtMs:    s.ExpiresAtMs,
	}
}

// ApprovalDecisionRequest 是审批接口的请求体（意见可选）。
type ApprovalDecisionRequest struct {
	Comment string `json:"comment"`
}

// PendingApprovalResponse 是队列里的一条待审批留痕。
type PendingApprovalResponse struct {
	TraceID    string `json:"trace_id"`
	Seq        int64  `json:"seq"`
	Action     string `json:"action"`
	ActorType  string `json:"actor_type"`
	ActorID    string `json:"actor_id"`
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	ObjectRef  string `json:"object_ref,omitempty"`
	Summary    string `json:"summary"`
	OccurredMs int64  `json:"occurred_at_ms"`
}

// PendingApprovalFromDomain 把留痕记录转为队列项。
func PendingApprovalFromDomain(r dtrace.Record) PendingApprovalResponse {
	return PendingApprovalResponse{
		TraceID:    r.TraceID,
		Seq:        r.Seq,
		Action:     r.Action,
		ActorType:  string(r.ActorType),
		ActorID:    r.ActorID,
		ObjectType: r.ObjectType,
		ObjectID:   r.ObjectID,
		ObjectRef:  r.ObjectRef,
		Summary:    r.Summary,
		OccurredMs: r.OccurredAtMs,
	}
}

// ApprovalDecisionResponse 是审批结果的对外表示。
type ApprovalDecisionResponse struct {
	TraceID string `json:"trace_id"`
	Seq     int64  `json:"seq"`
	Action  string `json:"action"`
	Target  string `json:"target_trace_id"`
}
