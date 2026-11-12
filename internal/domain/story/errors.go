package story

import "github.com/mst8222/regto/internal/domain/shared"

// 本包哨兵错误（领域层零外部依赖，错误码与 DESIGN-STORY-001「错误与边界」一致）。
var (
	// ErrInvalidTransition 非法状态转移。Meta: from/to/allowed
	ErrInvalidTransition = shared.New("INVALID_TRANSITION", "invalid status transition", nil)
	// ErrReviewersRequired 提交评审但评审人集合为空。
	ErrReviewersRequired = shared.New("REVIEWERS_REQUIRED", "reviewers required for submitReview", nil)
	// ErrInvalidCloseReason 关闭原因缺失或非法。
	ErrInvalidCloseReason = shared.New("INVALID_CLOSE_REASON", "invalid close reason", nil)
	// ErrEmptySpec 变更时规格内容为空。
	ErrEmptySpec = shared.New("EMPTY_SPEC", "spec content is empty", nil)
	// ErrNoPreviousVersion revert 时不存在上一版本。
	ErrNoPreviousVersion = shared.New("NO_PREVIOUS_VERSION", "no previous spec version to revert", nil)
	// ErrStageRegression 阶段回退。
	ErrStageRegression = shared.New("STAGE_REGRESSION", "stage cannot regress", nil)
	// ErrVersionOverflow 版本号溢出。
	ErrVersionOverflow = shared.New("VERSION_OVERFLOW", "spec version overflow", nil)
	// ErrInvalidReviewResult 评审结果非法。
	ErrInvalidReviewResult = shared.New("INVALID_REVIEW_RESULT", "invalid review result", nil)
	// ErrRevertOnlySingle revert 仅允许单人评审唯一结果。
	ErrRevertOnlySingle = shared.New("REVERT_ONLY_SINGLE", "revert requires a single reviewer result", nil)
	// ErrInvalidTitle 标题为空。
	ErrInvalidTitle = shared.New("INVALID_TITLE", "title is required", nil)
)

// maxSpecVersion 与 int32 上限对齐（AC/边界：版本号溢出）。
const maxSpecVersion = 1<<31 - 1
