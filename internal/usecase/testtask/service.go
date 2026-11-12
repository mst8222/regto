// Package testtask 是测试执行域的应用服务层。
// 依赖方向：usecase → domain（禁止反向；AGENTS.md §2）。
package testtask

import (
	"context"
	"fmt"

	"github.com/mst8222/regto/internal/domain/shared"
	dtcase "github.com/mst8222/regto/internal/domain/testcase"
	dtt "github.com/mst8222/regto/internal/domain/testtask"
	"github.com/mst8222/regto/internal/platform/clock"
	"github.com/mst8222/regto/internal/platform/id"
)

// dttUnmatchedError 构造 UNMATCHED_TESTS 错误（DEC-06：默认失败 + 可操作信息）。
// meta.unmatched 列出全部未匹配测试名，调用方据此补 external_id。
func dttUnmatchedError(taskID int64, unmatched []string) error {
	return shared.New("UNMATCHED_TESTS",
		fmt.Sprintf("%d 个测试未匹配到用例（可用 --create-missing 自动建档）", len(unmatched)),
		map[string]any{"test_task_id": taskID, "unmatched": unmatched})
}

// DefaultActorType/ActorID 是导入的缺省执行主体（REQ-05：M2 期导入主要来自 AI 流水线）。
const (
	DefaultActorType = "ai"
	DefaultActorID   = "regto-coder"
)

// Service 是测试执行域应用服务。
type Service struct {
	tasks dtt.Repository
	cases dtcase.Repository
	clk   clock.Clock
	ids   *id.Generator

	// buildChecker 校验 test_tasks.build_id 指向的构建存在（SPEC-RELEASE-001
	// REQ-07 / DESIGN-RELEASE-001 DEC-07）。nil 表示未装配发布域（测试桩），
	// 跳过存在性校验——保持 M2-A 既有调用方零改动。
	buildChecker func(ctx context.Context, id int64) (bool, error)
}

// New 构造服务；全部依赖不可为 nil。
func New(tasks dtt.Repository, cases dtcase.Repository, clk clock.Clock, ids *id.Generator) *Service {
	return &Service{tasks: tasks, cases: cases, clk: clk, ids: ids}
}

// SetBuildChecker 装配构建存在性校验器（发布域装配后调用；
// 签名与 uctrelease.Service.BuildExists 一致）。
func (s *Service) SetBuildChecker(fn func(ctx context.Context, id int64) (bool, error)) {
	s.buildChecker = fn
}

// CreateCommand 是创建测试单的输入。
type CreateCommand struct {
	Name      string
	ProductID int64
	ProjectID *int64
	BuildID   *int64 // 关联构建（SPEC-RELEASE-001 REQ-07；nil=不关联）
	Type      string
	OwnerID   int64
	Pri       int
	Desc      string
	ActorID   int64
}

// Create 创建测试单（初始状态 wait）。
// BuildID 非空时校验构建存在（REQ-07，DEC-07：应用层校验，不存在的构建 → NOT_FOUND）。
func (s *Service) Create(ctx context.Context, cmd CreateCommand) (*dtt.TestTask, error) {
	if cmd.BuildID != nil && s.buildChecker != nil {
		exists, err := s.buildChecker(ctx, *cmd.BuildID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, shared.New("NOT_FOUND",
				fmt.Sprintf("build %d not found", *cmd.BuildID),
				map[string]any{"id": *cmd.BuildID})
		}
	}
	now := s.clk.Now().UnixMilli()
	t := dtt.NewTestTask(cmd.Name, cmd.ProductID, cmd.OwnerID, now)
	t.ProjectID = cmd.ProjectID
	t.BuildID = cmd.BuildID
	t.Type = cmd.Type
	t.Pri = cmd.Pri
	t.Desc = cmd.Desc
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if err := s.tasks.Save(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// Get 加载测试单。
func (s *Service) Get(ctx context.Context, id int64) (*dtt.TestTask, error) {
	return s.tasks.GetByID(ctx, id)
}

// ListQuery 是测试单列表查询输入。
type ListQuery struct {
	ProductID int64
	Status    dtt.Status
	Keyword   string
	Page      int
	PerPage   int
}

// List 分页查询测试单。
func (s *Service) List(ctx context.Context, q ListQuery) ([]*dtt.TestTask, int, error) {
	return s.tasks.List(ctx, dtt.TaskQuery{
		ProductID: q.ProductID,
		Status:    q.Status,
		Keyword:   q.Keyword,
		Page:      dtt.Page{Page: q.Page, PerPage: q.PerPage},
	})
}

// Transition 执行测试单状态转移。
func (s *Service) Transition(ctx context.Context, taskID int64, action dtt.Action) (*dtt.TestTask, error) {
	t, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if _, err := t.Transition(action); err != nil {
		return nil, err
	}
	t.UpdatedAtMs = s.clk.Now().UnixMilli()
	if err := s.tasks.Save(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// AddCases 把用例加入测试单（锁定当前版本快照）。
func (s *Service) AddCases(ctx context.Context, taskID int64, caseIDs []int64) (int, error) {
	if _, err := s.tasks.GetByID(ctx, taskID); err != nil {
		return 0, err
	}
	versions := make([]int, 0, len(caseIDs))
	for _, cid := range caseIDs {
		c, err := s.cases.GetByID(ctx, cid)
		if err != nil {
			return 0, fmt.Errorf("加载用例 %d 失败: %w", cid, err)
		}
		versions = append(versions, c.Version)
	}
	return s.tasks.AddCases(ctx, taskID, caseIDs, versions)
}

// ListCases 列出测试单内的用例及执行状态。
func (s *Service) ListCases(ctx context.Context, taskID int64) ([]dtt.TaskCase, error) {
	return s.tasks.ListCases(ctx, taskID)
}

// ListResults 分页查询执行结果。
func (s *Service) ListResults(ctx context.Context, taskID int64, page, perPage int) ([]dtt.RunResult, int, error) {
	return s.tasks.ListResults(ctx, taskID, dtt.Page{Page: page, PerPage: perPage})
}

// CountResults 统计测试单内各结果条数（只看每用例最新一轮）。
func (s *Service) CountResults(ctx context.Context, taskID int64) (map[dtt.Result]int, error) {
	return s.tasks.CountResults(ctx, taskID)
}

// ImportCommand 是 go test -json 导入的输入。
type ImportCommand struct {
	TestTaskID int64
	ProductID  int64
	// Events 是 go test -json 的原始输出（一行一个 JSON 对象）。
	Events string
	// Actor 三件套（REQ-05）；缺省 ai/regto-coder。
	ActorType string
	ActorID   string
	// TraceID 关联留痕链；缺省生成 ULID。
	TraceID string
	// RunNo 缺省按该测试单已有最大 run_no + 1（DEC-07）。
	RunNo int
	// CreateMissing 未匹配用例时自动建档（REQ-06）。
	CreateMissing bool
	// DryRun 只解析与匹配，不写库。
	DryRun bool
}

// ImportResult 是导入结果统计。
type ImportResult struct {
	// TestTaskID 是目标测试单。
	TestTaskID int64
	// RunNo 是本次执行的轮次。
	RunNo int
	// Total 是解析出的测试数。
	Total int
	// Written 是实际写入的结果数。
	Written int
	// Matched 是按 external_id 命中既有用例的数量。
	Matched int
	// Created 是自动建档的用例数（CreateMissing）。
	Created int
	// Unmatched 是未匹配且未建档的测试名（CreateMissing=false 时非空即失败）。
	Unmatched []string
	// ByResult 是按结果分组的计数。
	ByResult map[dtt.Result]int
	// Results 是写入的结果明细（DryRun 时为预览）。
	Results []dtt.RunResult
}

// ImportGoTestJSON 导入 go test -json 结果为执行结果（REQ-04~07）。
//
// 流程：解析 → 匹配（按 product_id + external_id）→ 未匹配处理 → 写结果。
// 测试单不在 doing 状态时自动 start（导入即视为开始执行，见 DESIGN 错误与边界）。
func (s *Service) ImportGoTestJSON(ctx context.Context, cmd ImportCommand) (*ImportResult, error) {
	agg, err := dtt.ParseGoTestJSON([]byte(cmd.Events))
	if err != nil {
		return nil, err
	}

	actorType := cmd.ActorType
	if actorType == "" {
		actorType = DefaultActorType
	}
	actorID := cmd.ActorID
	if actorID == "" {
		actorID = DefaultActorID
	}
	traceID := cmd.TraceID
	if traceID == "" {
		traceID = s.ids.NewString()
	}

	// 测试单必须存在；不在 doing 则自动 start（语义：导入即开始执行）
	task, err := s.tasks.GetByID(ctx, cmd.TestTaskID)
	if err != nil {
		return nil, err
	}
	if task.Status != dtt.StatusDoing {
		if _, err := task.Transition(dtt.ActionStart); err != nil {
			return nil, err
		}
		task.UpdatedAtMs = s.clk.Now().UnixMilli()
		if !cmd.DryRun {
			if err := s.tasks.Save(ctx, task); err != nil {
				return nil, err
			}
		}
	}

	// run_no 缺省按测试单已有最大 + 1（DEC-07）
	runNo := cmd.RunNo
	if runNo == 0 {
		if cmd.DryRun {
			runNo = 1
		} else {
			runNo, err = s.tasks.NextRunNo(ctx, cmd.TestTaskID)
			if err != nil {
				return nil, err
			}
		}
	}

	now := s.clk.Now().UnixMilli()
	out := &ImportResult{TestTaskID: cmd.TestTaskID, RunNo: runNo, Total: len(agg), ByResult: map[dtt.Result]int{}}

	// 逐测试匹配用例
	type pending struct {
		agg    dtt.AggregateResult
		caseID int64
		ver    int
	}
	var matched, toCreate []pending
	var unmatched []string
	for _, a := range agg {
		c, err := s.cases.FindByExternalID(ctx, cmd.ProductID, a.Test)
		if err == nil {
			matched = append(matched, pending{agg: a, caseID: c.ID, ver: c.Version})
			continue
		}
		if cmd.CreateMissing {
			toCreate = append(toCreate, pending{agg: a})
			continue
		}
		unmatched = append(unmatched, a.Test)
	}
	if len(unmatched) > 0 {
		// 默认失败：不给「以为导入了其实没有」留空间（DEC-06）
		return nil, dttUnmatchedError(cmd.TestTaskID, unmatched)
	}

	// 自动建档（REQ-06）
	for _, p := range toCreate {
		nowMs := s.clk.Now().UnixMilli()
		c := dtcase.NewCase(cmd.ProductID, p.agg.Test, dtcase.TypeFeature, 3, 0, nowMs)
		c.ExternalID = p.agg.Test
		c.OpenedByID = 0
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if err := s.cases.Save(ctx, c); err != nil {
			return nil, fmt.Errorf("自动建档用例 %q 失败: %w", p.agg.Test, err)
		}
		out.Created++
		matched = append(matched, pending{agg: p.agg, caseID: c.ID, ver: c.Version})
	}

	out.Matched = len(matched)
	for _, p := range matched {
		res := dtt.RunResult{
			TestTaskID:   cmd.TestTaskID,
			CaseID:       p.caseID,
			CaseVersion:  p.ver,
			RunNo:        runNo,
			Result:       p.agg.Result,
			DurationMs:   int(p.agg.ElapsedSec * 1000),
			ActorType:    actorType,
			ActorID:      actorID,
			TraceID:      traceID,
			RawOutput:    p.agg.Output,
			ExecutedAtMs: now,
		}
		if !cmd.DryRun {
			if err := s.tasks.SaveResult(ctx, res); err != nil {
				return nil, err
			}
			// 用例冗余执行列同步（展示用：列表页能看到最近一次执行结论）
			_ = s.cases.UpdateRunStats(ctx, p.caseID, actorType+":"+actorID, string(p.agg.Result), now)
		}
		out.Results = append(out.Results, res)
		out.Written++
		out.ByResult[p.agg.Result]++
	}
	return out, nil
}
