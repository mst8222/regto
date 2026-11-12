package release

import "github.com/mst8222/regto/internal/domain/shared"

// 本包哨兵错误（领域层零外部依赖，错误码与 DESIGN-RELEASE-001「错误场景」一致）。
var (
	// ErrPlanConflict 计划冲突（当前保留：同产品同标题等业务唯一）。
	ErrPlanConflict = shared.New("PLAN_CONFLICT", "plan conflicts with an existing record", nil)
	// ErrReleaseConflict 发布冲突。
	ErrReleaseConflict = shared.New("RELEASE_CONFLICT", "release conflicts with an existing record", nil)
	// ErrBuildConflict 同产品下构建号重复（AC-04）。
	ErrBuildConflict = shared.New("BUILD_CONFLICT", "build number already exists for this product", nil)
	// ErrPlanStoryConflict 计划重复关联同一需求（AC-03）。
	ErrPlanStoryConflict = shared.New("PLAN_STORY_CONFLICT", "story already linked to this plan", nil)
)

// NewInvalidError 构造字段级校验错误（Meta 带 field/reason）。
func NewInvalidError(field, reason string) error {
	return shared.New("INVALID_FIELD", reason, map[string]any{"field": field})
}
