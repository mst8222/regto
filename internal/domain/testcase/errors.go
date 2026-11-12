package testcase

import "github.com/mst8222/regto/internal/domain/shared"

// 本包哨兵错误（领域层零外部依赖，错误码与 DESIGN-CASE-001「错误与边界」一致）。
var (
	// ErrInvalidTransition 非法用例状态转移。Meta: from/to/allowed
	ErrInvalidTransition = shared.New("INVALID_TRANSITION", "invalid case status transition", nil)
	// ErrInvalidField 字段校验失败。Meta: field/reason
	ErrInvalidField = shared.New("INVALID_FIELD", "invalid field", nil)
	// ErrInvalidZTF ZTF 语法错误。Meta: line/reason
	ErrInvalidZTF = shared.New("INVALID_ZTF", "invalid ztf syntax", nil)
	// ErrCaseConflict 与既有 SpecDD 派生用例的 source_ref 冲突（AC-04）。
	ErrCaseConflict = shared.New("CASE_CONFLICT", "source_ref conflicts with an existing spec-derived case", nil)
)

// NewInvalidError 构造字段级校验错误（Meta 带 field/reason）。
func NewInvalidError(field, reason string) error {
	return shared.New("INVALID_FIELD", reason, map[string]any{"field": field})
}

// NewZTFError 构造 ZTF 语法错误（Meta 带 line/reason，AC-03）。
func NewZTFError(line int, reason string) error {
	return shared.New("INVALID_ZTF", reason, map[string]any{"line": line})
}
