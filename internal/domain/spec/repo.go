package spec

import "context"

// Repository 是 SpecDD 文档的持久化契约（adapter/persistence/postgres 实现）。
// 全部写入以文件派生的对象为输入，不存在「从 DB 写回文件」的方法——
// 单向性由接口形状保证（DESIGN-SPEC-001 DEC-04）。
type Repository interface {
	// WithTx 在单个事务内执行 fn，保证同步的原子性（NFR-01）。
	WithTx(ctx context.Context, fn func(tx Repository) error) error
	// UpsertDocument 写入文档；返回内容哈希是否发生变化。
	UpsertDocument(ctx context.Context, d *Document) (changed bool, err error)
	// ReplaceItems 用文件当前条目整体替换该文档的条目（文件是唯一真源）。
	// 替换语义是「软删缺失项 + 复活重现项」，不使用 DELETE
	// （应用账号无 DELETE 权限，且审计型系统禁止物理删）。
	ReplaceItems(ctx context.Context, docID string, items []Item) error
	// UpsertCase 写入用例并返回其主键（用于写步骤）。
	UpsertCase(ctx context.Context, c *Case) (caseID int64, err error)
	// ReplaceSteps 用文件当前步骤整体替换该用例的步骤（同 ReplaceItems 的软删语义）。
	ReplaceSteps(ctx context.Context, caseID int64, steps []Step) error
	// AddLink 写入追溯链接；重复链接由唯一键忽略（DEC-03）。
	AddLink(ctx context.Context, l Link) error
	// MarkDeletedExcept 把本次未扫到的文档与用例标为软删（文件被删除时索引随之失效）。
	MarkDeletedExcept(ctx context.Context, filePaths, sourceRefs []string) error
}
