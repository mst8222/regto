// Package testcase 实现测试用例域：用例/步骤/套件与 ZTF 转换。
//
// 本包是纯领域逻辑，零外部依赖（SPEC-CASE-001 NFR-02）：
//   - 禁止 import adapter/、platform/、usecase/、数据库与 HTTP 库；
//   - 状态转移由 state.go 显式转移表驱动，可被表驱动测试穷举；
//   - ZTF 解析与导出是纯函数（ztf.go），round-trip 无损由规范形式保证。
//
// 规格来源：spec/case/management/SPEC-CASE-001.md（DESIGN-CASE-001 DEC-01~05）。
package testcase

// CaseType 是用例类型（对应禅道 zt_case.type 的常用值）。
type CaseType string

// 用例类型全集。
const (
	TypeFeature     CaseType = "feature"     // 功能
	TypePerformance CaseType = "performance" // 性能
	TypeConfig      CaseType = "config"      // 配置
	TypeInstall     CaseType = "install"     // 安装
)

// Valid 报告类型是否合法。
func (t CaseType) Valid() bool {
	switch t {
	case TypeFeature, TypePerformance, TypeConfig, TypeInstall:
		return true
	}
	return false
}

// StepType 是步骤类型（对应禅道 zt_casestep.type）。
type StepType string

// 步骤类型全集。
const (
	StepTypeStep  StepType = "step"  // 普通步骤，有期望结果
	StepTypeGroup StepType = "group" // 分组步骤，无期望结果
)

// Valid 报告步骤类型是否合法。
func (t StepType) Valid() bool {
	return t == StepTypeStep || t == StepTypeGroup
}

// Case 是用例聚合根。
// 字段与 migrations/000012 的 cases 列一一对应（DESIGN-CASE-001 数据模型）。
type Case struct {
	ID           int64
	ProductID    int64
	ModuleID     *int64
	StoryID      *int64
	StoryVersion int
	BranchID     *int64
	Title        string
	Precondition string
	Keywords     string
	Pri          int
	Type         CaseType
	Status       Status
	Version      int

	// SourceRef/SourceHash 非空表示 SpecDD 派生用例（TEST yaml 中的 TC-xx）；
	// 业务用例（手工/ZTF 导入）为空。唯一性仅在非空上生效（REQ-07）。
	SourceRef  string
	SourceHash string

	// ExternalID 是 CI 侧标识（如 go 测试函数名），go test -json 导入的匹配键
	// （SPEC-TESTTASK-001 REQ-04）。
	ExternalID string

	// Suite/Automation/Priority/SpecDocID 由 spec sync 使用，保留原语义。
	Suite      string
	Automation string
	Priority   string
	SpecDocID  string

	// LastRun* 是冗余展示列，由测试执行域更新（本域只读）。
	LastRunner    string
	LastRunResult string
	LastRunAtMs   *int64

	OpenedByID  int64
	OpenedAtMs  int64
	UpdatedAtMs int64
	DeletedAtMs *int64
	LoadedAtMs  int64 // 乐观锁：持久层加载时的 updated_at

	Steps []Step // 全部步骤（含历史），按 seq 升序
}

// Step 是用例步骤。期望结果存于 Expect（对应 checks JSONB 首项，DEC-02）。
type Step struct {
	ID       int64
	Seq      int
	Name     string
	Expect   string
	StepType StepType
}

// NewCase 创建业务用例（初始状态 wait，version=1）。
// SpecDD 派生用例不走本构造函数，由 spec sync 的 UpsertCase 直接落库。
func NewCase(productID int64, title string, typ CaseType, pri int, openedByID int64, nowMs int64) *Case {
	return &Case{
		ProductID:    productID,
		Title:        title,
		Type:         typ,
		Pri:          pri,
		Status:       StatusWait,
		Version:      1,
		StoryVersion: 1,
		OpenedByID:   openedByID,
		OpenedAtMs:   nowMs,
		UpdatedAtMs:  nowMs,
	}
}

// Validate 校验用例不变式（领域层语义校验，NFR：脱离 HTTP 也可单测）。
func (c *Case) Validate() error {
	if c.Title == "" {
		return NewInvalidError("title", "用例标题不能为空")
	}
	if c.ProductID <= 0 {
		return NewInvalidError("product_id", "用例必须归属产品")
	}
	if c.Pri < 1 || c.Pri > 4 {
		return NewInvalidError("pri", "优先级必须在 1-4 之间")
	}
	if !c.Type.Valid() {
		return NewInvalidError("type", "用例类型非法: "+string(c.Type))
	}
	for i := range c.Steps {
		if err := c.Steps[i].validate(i + 1); err != nil {
			return err
		}
	}
	return nil
}

// validate 校验单个步骤；seq 为期望序号（1 起）。
func (s Step) validate(wantSeq int) error {
	if s.Seq != wantSeq {
		return NewInvalidError("steps", "步骤序号必须从 1 起连续递增")
	}
	if s.Name == "" {
		return NewInvalidError("steps", "步骤描述不能为空")
	}
	if !s.StepType.Valid() {
		return NewInvalidError("steps", "步骤类型非法: "+string(s.StepType))
	}
	if s.StepType == StepTypeStep && s.Expect == "" {
		return NewInvalidError("steps", "普通步骤必须有期望结果（分组步骤请用 group 类型）")
	}
	if s.StepType == StepTypeGroup && s.Expect != "" {
		return NewInvalidError("steps", "分组步骤不能有期望结果")
	}
	return nil
}

// AddStep 追加步骤并重排序号（领域内聚：序号连续性由实体保证）。
func (c *Case) AddStep(name, expect string, typ StepType) {
	c.Steps = append(c.Steps, Step{
		Seq:      len(c.Steps) + 1,
		Name:     name,
		Expect:   expect,
		StepType: typ,
	})
}
