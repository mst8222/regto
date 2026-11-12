// Package httputil 提供 HTTP 层可复用工具：错误映射（RFC 7807）、执行主体识别、中间件。
// 独立成包是为了打破 http ↔ handler 的循环依赖（两者都要用到这些工具）。
package httputil

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/mst8222/regto/internal/domain/shared"
)

// Problem 是 RFC 7807 application/problem+json 错误体（ROADMAP §3.4）。
type Problem struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Status    int            `json:"status"`
	Detail    string         `json:"detail"`
	Code      string         `json:"code,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// statusFor 把领域错误码映射为 HTTP 状态码。
// 规格要求：非法转移等规则性拒绝统一 422（TEST-STORY-001 负向用例断言 422）。
func statusFor(err error) int {
	code := shared.CodeOf(err)
	switch code {
	case "NOT_FOUND":
		return http.StatusNotFound
	case "CONFLICT", "APPROVAL_ALREADY_DECIDED":
		// 重复审批是「状态冲突」而不是「参数错」：同一个批文不能批两次。
		return http.StatusConflict
	case "APPROVAL_NOT_FOUND":
		return http.StatusNotFound
	case "FORBIDDEN", "ORIGIN_NOT_ALLOWED":
		// ORIGIN_NOT_ALLOWED 是 CSRF 防线（SPEC-WEB-001 REQ-04）：来源不被允许属于
		// "拒绝服务这个请求"，不是参数错（400），也不是业务规则（422）。单列一个码
		// 而不是复用 FORBIDDEN，是为了让前端在 403 里能区分「权限不足」与「跨站伪造」。
		return http.StatusForbidden
	case "UNAUTHENTICATED":
		return http.StatusUnauthorized
	case "INVALID_QUERY", "INVALID_JSON", "INVALID_ID", "INVALID_USER", "UNKNOWN_REPORT":
		// 请求格式问题（查询参数、请求体、路径参数）是 400，业务规则拒绝才是 422。
		// 契约 api/openapi/v1.yaml 的 BadRequest 响应即对应这三类；
		// UNKNOWN_REPORT（报表 id 不在注册表）同理属于「请求参数非法」。
		return http.StatusBadRequest
	}
	if code == "INTERNAL" {
		return http.StatusInternalServerError
	}
	// 其余 DomainError 均为业务规则拒绝：422 Unprocessable Entity。
	if isDomainError(err) {
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadRequest
}

// isDomainError 判断是否为领域错误（区别于驱动/网络错误）。
func isDomainError(err error) bool {
	var de *shared.DomainError
	return errors.As(err, &de)
}

// WriteProblem 统一写错误响应。内部错误不回显详情（防信息泄漏）。
func WriteProblem(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status := statusFor(err)
	code := shared.CodeOf(err)
	p := Problem{
		Type:      "https://regto.local/errors/" + code,
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    err.Error(),
		Code:      code,
		RequestID: RequestID(r.Context()),
	}
	var de *shared.DomainError
	if errors.As(err, &de) {
		p.Meta = de.Meta
		if status >= 500 {
			p.Detail = "internal error"
		}
	} else if status >= 500 {
		p.Detail = "internal error"
		if logger != nil {
			logger.Error("internal error", slog.String("err", err.Error()),
				slog.String("request_id", p.RequestID))
		}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteJSON 写成功响应；data 为 nil 时写 204。
func WriteJSON(w http.ResponseWriter, status int, data any) {
	if data == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// DecodeJSON 解析请求体；非法 JSON 返回 400 语义错误。
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return shared.New("INVALID_JSON", "request body is not valid JSON: "+err.Error(), nil)
	}
	return nil
}

// PathID 解析路径参数中的正整数 ID。
func PathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, shared.New("INVALID_ID", "invalid path parameter "+name+": "+raw,
			map[string]any{"value": raw})
	}
	return id, nil
}
