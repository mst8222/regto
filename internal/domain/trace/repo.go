package trace

import "context"

// Repository 是留痕只读副本的持久化契约。
// 接口刻意只提供写入与链尾查询：本表是 jsonl 的副本，不提供 UPDATE/DELETE 方法，
// 与库级权限（仅 INSERT/SELECT）形成双重防线（DESIGN-TRACE-001 DEC-04）。
type Repository interface {
	// InsertBatch 批量插入留痕；已存在（按 trace_id）的记录被跳过。
	// 返回实际写入条数与跳过条数。
	InsertBatch(ctx context.Context, recs []Record) (imported, skipped int, err error)

	// Head 返回库内当前链尾（seq 最大记录的 seq 与 hash）；空库返回 (0, "", nil)。
	// 供分片文件导入前的衔接校验使用（SPEC-TRACE-001 REQ-05 / DESIGN-TRACE-001 DEC-06）。
	Head(ctx context.Context) (seq int64, hash string, err error)
}
