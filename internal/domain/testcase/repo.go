package testcase

import (
	"context"

	"github.com/mst8222/regto/internal/domain/shared"
)

// Repository 是用例域的持久化契约（adapter/persistence/postgres 实现）。
// 与 story 域同构：Save 单事务写聚合 + 步骤 + 事件；查询方法返回聚合或领域错误。
type Repository interface {
	// GetByID 按 ID 加载用例（含全部步骤）；不存在返回 shared.ErrNotFound。
	GetByID(ctx context.Context, id int64) (*Case, error)
	// GetBySourceRef 按 SpecDD 来源引用加载用例（AC-04 冲突检测）。
	// 不存在返回 shared.ErrNotFound。
	GetBySourceRef(ctx context.Context, sourceRef string) (*Case, error)
	// FindByExternalID 按 (product_id, external_id) 查找用例（go test -json 匹配）。
	// 不存在返回 shared.ErrNotFound。
	FindByExternalID(ctx context.Context, productID int64, externalID string) (*Case, error)
	// Save 保存聚合（INSERT 或乐观锁 UPDATE）+ 步骤整体替换（软删 + 复活）。
	// LoadedAtMs 为 0 走 INSERT 分支。
	Save(ctx context.Context, c *Case) error
	// List 分页查询（筛选：产品/模块/状态/类型/关联需求/关键字）。
	List(ctx context.Context, q Query) ([]*Case, int, error)
	// UpdateRunStats 更新冗余执行列（last_runner/last_run_result/last_run_at）。
	// 由测试执行域在写入结果后调用（本域自身不产生这些值）。
	UpdateRunStats(ctx context.Context, id int64, runner, result string, atMs int64) error
	// CreateSuite 创建套件；同产品下同名已存在返回 ErrCaseConflict。
	CreateSuite(ctx context.Context, productID int64, name, desc string, actorID int64) (int64, error)
	// AddToSuite 把用例加入套件；重复加入返回 ErrCaseConflict。
	AddToSuite(ctx context.Context, suiteID, caseID int64, version int) error
}

// Query 是用例列表查询对象（与 story.StoryQuery 同构）。
type Query struct {
	ProductID  int64
	ModuleID   int64
	StoryID    int64
	Status     Status
	Type       CaseType
	Keyword    string
	ExternalID string
	Page       shared.Page
	Sort       []SortKey
}

// Validate 校验查询对象（领域层语义校验，非法值返回 INVALID_FIELD）。
func (q Query) Validate() error {
	if q.Status != "" && !q.Status.Valid() {
		return NewInvalidError("status", "用例状态非法: "+string(q.Status))
	}
	if q.Type != "" && !q.Type.Valid() {
		return NewInvalidError("type", "用例类型非法: "+string(q.Type))
	}
	if q.Page.PerPage > shared.MaxPerPage {
		return NewInvalidError("per_page", "每页条数超过上限")
	}
	return nil
}

// SortKey 排序键。
type SortKey struct {
	Field string
	Desc  bool
}

// AllowedSortFields 是排序白名单（与 story 域一致：防注入 + 可预测）。
var AllowedSortFields = []string{"id", "title", "pri", "status", "type", "created_at", "updated_at"}

// ValidSortField 报告字段是否在白名单内。
func ValidSortField(f string) bool {
	for _, a := range AllowedSortFields {
		if a == f {
			return true
		}
	}
	return false
}
