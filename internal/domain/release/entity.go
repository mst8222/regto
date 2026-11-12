// Package release 实现发布域：产品计划（plans）、发布（releases）、
// 构建（builds）与计划-需求关联（plan_stories）。
//
// 本包是纯领域逻辑，零外部依赖（SPEC-RELEASE-001 NFR-01）：
//   - 禁止 import adapter/、platform/、usecase/、数据库与 HTTP 库；
//   - 两个状态机（plan / release）由 state.go 与 release_state.go 的显式
//     转移表驱动，可被表驱动测试穷举（与 story/case/testtask 三域同构）；
//   - 构建无状态机：禅道 zt_build 无 status 列，是记录型实体（DEC-06）。
//
// 规格来源：spec/release/SPEC-RELEASE-001.md（DESIGN-RELEASE-001 DEC-01~07）。
package release

// System 是发布/构建的系统类型（禅道 zt_release.system / zt_build.system：
// 0=trunk 主干，>0=branch 分支 ID；Regto 规范为 trunk|branch 两值）。
type System string

// 系统类型全集。
const (
	SystemTrunk  System = "trunk"  // 主干
	SystemBranch System = "branch" // 分支
)

// Valid 报告系统类型是否合法。
func (s System) Valid() bool {
	return s == SystemTrunk || s == SystemBranch
}

// ---------- Plan（产品计划，禅道 zt_productplan） ----------

// Plan 是产品计划聚合根。
// 字段与 migrations/000017 的 plans 列一一对应（DESIGN-RELEASE-001 §2）。
type Plan struct {
	ID        int64
	ProductID int64
	BranchID  *int64
	ParentID  *int64
	Title     string
	Status    PlanStatus
	Desc      string
	BeginMs   *int64 // 计划开始日期（DATE，毫秒精度；nil=未设置）
	EndMs     *int64 // 计划结束日期

	FinishedAtMs *int64
	ClosedAtMs   *int64
	ClosedReason string

	OpenedByID  int64
	OpenedAtMs  int64
	UpdatedAtMs int64
	DeletedAtMs *int64
	LoadedAtMs  int64 // 乐观锁：持久层加载时的 updated_at
}

// NewPlan 创建产品计划（初始状态 wait）。
func NewPlan(productID int64, title string, openedByID int64, nowMs int64) *Plan {
	return &Plan{
		ProductID:   productID,
		Title:       title,
		Status:      PlanStatusWait,
		OpenedByID:  openedByID,
		OpenedAtMs:  nowMs,
		UpdatedAtMs: nowMs,
	}
}

// Validate 校验计划不变式（领域层语义校验，脱离 HTTP 也可单测）。
func (p *Plan) Validate() error {
	if p.Title == "" {
		return NewInvalidError("title", "计划标题不能为空")
	}
	if p.ProductID <= 0 {
		return NewInvalidError("product_id", "计划必须归属产品")
	}
	if !p.Status.Valid() {
		return NewInvalidError("status", "计划状态非法: "+string(p.Status))
	}
	if p.BeginMs != nil && p.EndMs != nil && *p.BeginMs > *p.EndMs {
		return NewInvalidError("end", "计划结束日期不能早于开始日期")
	}
	return nil
}

// ---------- PlanStory（计划-需求关联，禅道 zt_planstory） ----------

// PlanStory 是计划-需求关联（有序归属，DEC-03）。
type PlanStory struct {
	ID      int64
	PlanID  int64
	StoryID int64
	Order   int
}

// ---------- Release（发布，禅道 zt_release） ----------

// Release 是发布聚合根。
// story_ids/bug_ids/build_ids 保留禅道逗号串形式（DEC-04：快照语义）。
type Release struct {
	ID           int64
	ProductID    int64
	BranchID     *int64
	Name         string
	System       System
	Marker       bool // 里程碑发布
	Status       ReleaseStatus
	DateMs       *int64 // 计划发布日期（DATE）
	ReleasedAtMs *int64

	StoryIDs string // 逗号串（快照）
	BugIDs   string // 逗号串（快照；bug 域不在本期，仅透传）
	BuildIDs string // 逗号串（快照）

	Mailto string // 逗号串（NFR-03：只存不发）
	Notify string
	Desc   string

	OpenedByID  int64
	OpenedAtMs  int64
	UpdatedAtMs int64
	DeletedAtMs *int64
	LoadedAtMs  int64
}

// NewRelease 创建发布（初始状态 wait，system 默认 trunk）。
func NewRelease(productID int64, name string, openedByID int64, nowMs int64) *Release {
	return &Release{
		ProductID:   productID,
		Name:        name,
		System:      SystemTrunk,
		Status:      ReleaseStatusWait,
		OpenedByID:  openedByID,
		OpenedAtMs:  nowMs,
		UpdatedAtMs: nowMs,
	}
}

// Validate 校验发布不变式。
func (r *Release) Validate() error {
	if r.Name == "" {
		return NewInvalidError("name", "发布名称不能为空")
	}
	if r.ProductID <= 0 {
		return NewInvalidError("product_id", "发布必须归属产品")
	}
	if !r.System.Valid() {
		return NewInvalidError("system", "发布系统类型非法: "+string(r.System))
	}
	if !r.Status.Valid() {
		return NewInvalidError("status", "发布状态非法: "+string(r.Status))
	}
	return nil
}

// ---------- Build（构建，禅道 zt_build；无状态机，记录型实体） ----------

// Build 是构建记录（DEC-06：无生命周期）。
type Build struct {
	ID        int64
	ProductID int64
	ProjectID *int64 // 所属迭代（禅道 execution）
	Name      string
	Builds    string // 构建号（如 1.0.0），产品内唯一（AC-04）
	System    System
	SCMPath   string
	FilePath  string
	DateMs    *int64 // 构建日期（DATE）
	Builder   string

	StoryIDs string // 逗号串（快照）
	BugIDs   string

	Desc string

	CreatedByID int64
	CreatedAtMs int64
	UpdatedAtMs int64
	DeletedAtMs *int64
	LoadedAtMs  int64
}

// NewBuild 创建构建记录。
func NewBuild(productID int64, name, builds string, createdByID int64, nowMs int64) *Build {
	return &Build{
		ProductID:   productID,
		Name:        name,
		Builds:      builds,
		System:      SystemTrunk,
		CreatedByID: createdByID,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}
}

// Validate 校验构建不变式。
func (b *Build) Validate() error {
	if b.Name == "" {
		return NewInvalidError("name", "构建名称不能为空")
	}
	if b.ProductID <= 0 {
		return NewInvalidError("product_id", "构建必须归属产品")
	}
	if b.Builds == "" {
		return NewInvalidError("builds", "构建号不能为空")
	}
	if !b.System.Valid() {
		return NewInvalidError("system", "构建系统类型非法: "+string(b.System))
	}
	return nil
}
