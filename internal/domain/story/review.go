package story

import "github.com/mst8222/regto/internal/domain/shared"

// Review 是一次评审记录（对应 story_reviews 表）。
// 与 Event 的区别：Event 是内存中的领域事件（落 activities），Review 是业务评审单据（落 story_reviews）。
type Review struct {
	ID           int64
	StoryID      int64
	Version      int // 被评审的规格版本
	ReviewerID   int64
	Result       ReviewResult
	Comment      string
	ReviewedAtMs int64
}

// ReviewResult 是单个评审人的评审结果（对应禅道 storyreview.result）。
type ReviewResult string

// 评审结果全集（4 种，REQ-02）。
const (
	ReviewPass    ReviewResult = "pass"    // 通过
	ReviewClarify ReviewResult = "clarify" // 待澄清
	ReviewRevert  ReviewResult = "revert"  // 回退版本
	ReviewReject  ReviewResult = "reject"  // 拒绝
)

// AllReviewResults 返回全部评审结果（用于校验与穷举测试）。
func AllReviewResults() []ReviewResult {
	return []ReviewResult{ReviewPass, ReviewClarify, ReviewRevert, ReviewReject}
}

// Valid 报告评审结果是否合法。
func (r ReviewResult) Valid() bool {
	switch r {
	case ReviewPass, ReviewClarify, ReviewRevert, ReviewReject:
		return true
	}
	return false
}

// AggregateReviewResults 聚合多人评审结果（REQ-02 聚合规则）：
//   - 任一 reject → reject
//   - 无 reject 且任一 clarify → clarify
//   - 唯一结果 revert → revert（多人含 revert 时报 ErrRevertOnlySingle）
//   - 全部 pass → pass
func AggregateReviewResults(results []ReviewResult) (ReviewResult, error) {
	if len(results) == 0 {
		return "", shared.New("EMPTY_REVIEW_RESULTS", "no review results to aggregate", nil)
	}
	hasReject, hasClarify, revertCount := false, false, 0
	for _, r := range results {
		if !r.Valid() {
			return "", shared.New("INVALID_REVIEW_RESULT", "invalid review result: "+string(r),
				map[string]any{"result": r})
		}
		switch r {
		case ReviewReject:
			hasReject = true
		case ReviewClarify:
			hasClarify = true
		case ReviewRevert:
			revertCount++
		}
	}
	switch {
	case hasReject:
		return ReviewReject, nil
	case revertCount > 1:
		return "", shared.New("REVERT_ONLY_SINGLE", "revert requires a single reviewer result",
			map[string]any{"revert_count": revertCount})
	case revertCount == 1:
		// revert 仅在单人唯一时可用；与其他结果混合视为非法组合。
		if len(results) > 1 {
			return "", shared.New("REVERT_ONLY_SINGLE", "revert cannot be mixed with other results",
				map[string]any{"count": len(results)})
		}
		return ReviewRevert, nil
	case hasClarify:
		return ReviewClarify, nil
	default:
		return ReviewPass, nil
	}
}
