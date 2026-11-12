package story

import "context"

// Repository 是 Story 聚合的持久化契约（adapter/persistence/postgres 实现）。
// Save 必须与 events 在同一事务落库：先写 stories/specs，再写 activities（NFR-03）。
type Repository interface {
	// GetByID 按 ID 加载聚合（含全部规格版本）；不存在返回 shared.ErrNotFound。
	GetByID(ctx context.Context, id int64) (*Story, error)
	// Save 以乐观方式保存聚合、其领域事件、以及事件上挂载的评审单据，三者在同一事务（DEC-09）。
	Save(ctx context.Context, s *Story, events []Event) error
	// List 按条件返回列表投影与满足条件的总数。
	// 与 GetByID 分开：列表不加载规格版本链，避免 N+1（DESIGN-STORY-002 DEC-04）。
	List(ctx context.Context, q StoryQuery) ([]StoryListItem, int64, error)
}
