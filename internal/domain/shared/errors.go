// Package shared 定义跨领域复用的基础类型：领域错误、分页、对象引用。
package shared

import (
	"errors"
	"fmt"
)

// DomainError 是带机器可读 Code 与结构化 Meta 的领域错误。
// HTTP 层将其映射为 RFC 7807 problem+json（见 AGENTS.md §5）。
type DomainError struct {
	Code    string         // 机器可读错误码，如 INVALID_TRANSITION
	Message string         // 人类可读描述
	Meta    map[string]any // 结构化上下文，如 from/to/allowed
}

// Error 实现 error 接口。
func (e *DomainError) Error() string {
	if e == nil {
		return "<nil domain error>"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New 创建 DomainError。
func New(code, message string, meta map[string]any) *DomainError {
	return &DomainError{Code: code, Message: message, Meta: meta}
}

// Is 支持 errors.Is 按 Code 比较（忽略 Meta）。
func (e *DomainError) Is(target error) bool {
	var t *DomainError
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// 通用哨兵错误（各领域自有哨兵错误定义在各自包内）。
var (
	// ErrNotFound 对象不存在（或已软删）。
	ErrNotFound = New("NOT_FOUND", "resource not found", nil)
	// ErrValidation 通用参数校验失败。
	ErrValidation = New("VALIDATION_FAILED", "validation failed", nil)
	// ErrConflict 唯一约束/乐观锁冲突。
	ErrConflict = New("CONFLICT", "conflict", nil)
	// ErrForbidden 权限不足。
	ErrForbidden = New("FORBIDDEN", "forbidden", nil)
	// ErrUnauthenticated 未认证。
	ErrUnauthenticated = New("UNAUTHENTICATED", "unauthenticated", nil)
)

// CodeOf 提取错误的领域错误码；非 DomainError 返回 INTERNAL。
func CodeOf(err error) string {
	var de *DomainError
	if errors.As(err, &de) {
		return de.Code
	}
	return "INTERNAL"
}
