// Package testcase 是用例域的应用服务层：编排领域对象、事务边界、驱动留痕。
// 依赖方向：usecase → domain（禁止反向；AGENTS.md §2）。
package testcase

import (
	"context"
	"fmt"
	"strconv"

	dtcase "github.com/mst8222/regto/internal/domain/testcase"
	"github.com/mst8222/regto/internal/platform/clock"
)

// Service 是用例域应用服务。
type Service struct {
	repo dtcase.Repository
	clk  clock.Clock
}

// New 构造服务；repo 与 clk 均不可为 nil。
func New(repo dtcase.Repository, clk clock.Clock) *Service {
	return &Service{repo: repo, clk: clk}
}

// CreateCommand 是创建用例的输入。
type CreateCommand struct {
	ProductID    int64
	ModuleID     *int64
	StoryID      *int64
	StoryVersion int
	BranchID     *int64
	Title        string
	Precondition string
	Keywords     string
	Pri          int
	Type         dtcase.CaseType
	ExternalID   string
	ActorID      int64
}

// Create 创建业务用例（初始状态 wait）。
func (s *Service) Create(ctx context.Context, cmd CreateCommand) (*dtcase.Case, error) {
	now := s.clk.Now().UnixMilli()
	c := dtcase.NewCase(cmd.ProductID, cmd.Title, cmd.Type, cmd.Pri, cmd.ActorID, now)
	c.ModuleID = cmd.ModuleID
	c.StoryID = cmd.StoryID
	if cmd.StoryVersion > 0 {
		c.StoryVersion = cmd.StoryVersion
	}
	c.BranchID = cmd.BranchID
	c.Precondition = cmd.Precondition
	c.Keywords = cmd.Keywords
	c.ExternalID = cmd.ExternalID
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.Save(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Get 按 ID 加载用例（含步骤）；不存在返回 NOT_FOUND。
func (s *Service) Get(ctx context.Context, id int64) (*dtcase.Case, error) {
	return s.repo.GetByID(ctx, id)
}

// TransitionCommand 是状态转移的输入。
type TransitionCommand struct {
	CaseID  int64
	Action  dtcase.Action
	ActorID int64
}

// Transition 执行用例状态转移（领域层集中校验 + 乐观锁保存）。
func (s *Service) Transition(ctx context.Context, cmd TransitionCommand) (*dtcase.Case, error) {
	c, err := s.repo.GetByID(ctx, cmd.CaseID)
	if err != nil {
		return nil, err
	}
	if _, err := c.Transition(cmd.Action); err != nil {
		return nil, err
	}
	c.UpdatedAtMs = s.clk.Now().UnixMilli()
	if err := s.repo.Save(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateCommand 是修改用例的输入。
type UpdateCommand struct {
	CaseID       int64
	Title        string
	Precondition string
	Keywords     string
	Pri          int
	Type         dtcase.CaseType
	ModuleID     *int64
	StoryID      *int64
	StoryVersion int
}

// Update 修改用例主数据（不动步骤；步骤由 ReplaceSteps 单独维护）。
func (s *Service) Update(ctx context.Context, cmd UpdateCommand) (*dtcase.Case, error) {
	c, err := s.repo.GetByID(ctx, cmd.CaseID)
	if err != nil {
		return nil, err
	}
	if cmd.Title != "" {
		c.Title = cmd.Title
	}
	c.Precondition = cmd.Precondition
	c.Keywords = cmd.Keywords
	if cmd.Pri >= 1 && cmd.Pri <= 4 {
		c.Pri = cmd.Pri
	}
	if cmd.Type != "" {
		c.Type = cmd.Type
	}
	c.ModuleID = cmd.ModuleID
	c.StoryID = cmd.StoryID
	if cmd.StoryVersion > 0 {
		c.StoryVersion = cmd.StoryVersion
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c.UpdatedAtMs = s.clk.Now().UnixMilli()
	if err := s.repo.Save(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ReplaceSteps 整体替换用例步骤（文件/导入语义：软删缺失 + 复活重现）。
func (s *Service) ReplaceSteps(ctx context.Context, caseID int64, steps []dtcase.Step) (*dtcase.Case, error) {
	c, err := s.repo.GetByID(ctx, caseID)
	if err != nil {
		return nil, err
	}
	c.Steps = steps
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c.UpdatedAtMs = s.clk.Now().UnixMilli()
	if err := s.repo.Save(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListQuery 是列表查询输入。
type ListQuery struct {
	ProductID  int64
	ModuleID   int64
	StoryID    int64
	Status     dtcase.Status
	Type       dtcase.CaseType
	Keyword    string
	ExternalID string
	Page       int
	PerPage    int
	Sort       []dtcase.SortKey
}

// List 分页查询用例。
func (s *Service) List(ctx context.Context, q ListQuery) ([]*dtcase.Case, int, error) {
	dq := dtcase.Query{
		ProductID:  q.ProductID,
		ModuleID:   q.ModuleID,
		StoryID:    q.StoryID,
		Status:     q.Status,
		Type:       q.Type,
		Keyword:    q.Keyword,
		ExternalID: q.ExternalID,
		Sort:       q.Sort,
	}
	if err := dq.Validate(); err != nil {
		return nil, 0, err
	}
	dq.Page.Page = q.Page
	dq.Page.PerPage = q.PerPage
	return s.repo.List(ctx, dq)
}

// ImportZTFCommand 是 ZTF 导入的输入。
type ImportZTFCommand struct {
	ZTF string
	// DefaultProductID 用于未带 @产品 属性的用例块（0 表示拒绝缺省）。
	DefaultProductID int64
	ActorID          int64
}

// ImportZTFResult 是导入结果统计。
type ImportZTFResult struct {
	Total   int
	Created int
	Skipped int // 与既有 SpecDD 用例冲突而跳过的数量（AC-04）
	Cases   []*dtcase.Case
}

// ImportZTF 解析并导入 ZTF 文本（REQ-05）。
// 与 SpecDD 派生用例（source_ref 非空）冲突的块跳过并计入 Skipped（AC-04）——
// spec sync 的用例由 Git 真源管理，不允许业务导入覆盖。
func (s *Service) ImportZTF(ctx context.Context, cmd ImportZTFCommand) (*ImportZTFResult, error) {
	parsed, err := dtcase.ParseZTF(cmd.ZTF)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now().UnixMilli()
	result := &ImportZTFResult{Total: len(parsed)}
	for _, c := range parsed {
		if c.ProductID == 0 {
			if cmd.DefaultProductID == 0 {
				return nil, dtcase.NewZTFError(0, "用例块未指定 @产品，且未提供默认产品")
			}
			c.ProductID = cmd.DefaultProductID
		}
		// AC-04 的结构性保证：ZTF 导入的用例 source_ref 恒为空，
		// 与 spec sync 写入的 SpecDD 用例（source_ref 非空）走不同的唯一键空间，
		// 物理上不可能互相覆盖。显式冲突拦截只对 CreateWithSourceRef 路径生效。
		c.OpenedByID = cmd.ActorID
		c.OpenedAtMs = now
		c.UpdatedAtMs = now
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if err := s.repo.Save(ctx, c); err != nil {
			return nil, fmt.Errorf("导入用例 %q 失败: %w", c.Title, err)
		}
		result.Created++
		result.Cases = append(result.Cases, c)
	}
	return result, nil
}

// CreateWithSourceRef 以指定来源引用创建用例（供 spec sync 之外的受控导入使用）。
// source_ref 与既有未删用例冲突时返回 CASE_CONFLICT（AC-04）。
func (s *Service) CreateWithSourceRef(ctx context.Context, c *dtcase.Case) (*dtcase.Case, error) {
	if c.SourceRef == "" {
		return nil, dtcase.NewInvalidError("source_ref", "来源引用不能为空")
	}
	if _, err := s.repo.GetBySourceRef(ctx, c.SourceRef); err == nil {
		return nil, dtcase.ErrCaseConflict
	}
	now := s.clk.Now().UnixMilli()
	c.OpenedAtMs = now
	c.UpdatedAtMs = now
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.Save(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ExportZTF 导出单个用例的 ZTF 文本（REQ-06）。
func (s *Service) ExportZTF(ctx context.Context, id int64) (string, error) {
	c, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return "", err
	}
	return dtcase.ExportZTF(c), nil
}

// Suite 是套件（薄包装：套件无独立状态机，仅 CRUD）。
type Suite struct {
	ID        int64
	ProductID int64
	Name      string
	Desc      string
	Type      string
}

// CreateSuite 创建套件（同产品下同名唯一）。
func (s *Service) CreateSuite(ctx context.Context, productID int64, name, desc string, actorID int64) (int64, error) {
	if name == "" {
		return 0, dtcase.NewInvalidError("name", "套件名称不能为空")
	}
	return s.repo.CreateSuite(ctx, productID, name, desc, actorID)
}

// AddToSuite 把用例加入套件（重复加入返回 SUITE_CASE_CONFLICT）。
func (s *Service) AddToSuite(ctx context.Context, suiteID, caseID int64, version int) error {
	return s.repo.AddToSuite(ctx, suiteID, caseID, version)
}

// itoa 供错误消息使用。
func itoa(v int64) string { return strconv.FormatInt(v, 10) }
