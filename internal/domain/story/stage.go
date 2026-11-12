package story

import "github.com/mst8222/regto/internal/domain/shared"

// Stage 是 Story 研发阶段（对应禅道 zt_story.stage）。
// M1 实现 8 个核心态（ASM-02），其余 6 态 M2 扩展。
type Stage string

// 阶段全集（M1，8 态，单向顺序）。
const (
	StageWait       Stage = "wait"       // 未开始
	StagePlanned    Stage = "planned"    // 已计划
	StageProjected  Stage = "projected"  // 研发立项
	StageDeveloping Stage = "developing" // 研发中
	StageDeveloped  Stage = "developed"  // 研发完成
	StageTesting    Stage = "testing"    // 测试中
	StageTested     Stage = "tested"     // 测试完成
	StageReleased   Stage = "released"   // 已发布
)

// stageOrder 定义阶段顺序索引（值越小越靠前）。
var stageOrder = map[Stage]int{
	StageWait:       0,
	StagePlanned:    1,
	StageProjected:  2,
	StageDeveloping: 3,
	StageDeveloped:  4,
	StageTesting:    5,
	StageTested:     6,
	StageReleased:   7,
}

// AllStages 返回全部阶段（按顺序，用于穷举测试）。
func AllStages() []Stage {
	return []Stage{
		StageWait, StagePlanned, StageProjected, StageDeveloping,
		StageDeveloped, StageTesting, StageTested, StageReleased,
	}
}

// Valid 报告阶段是否合法。
func (s Stage) Valid() bool {
	_, ok := stageOrder[s]
	return ok
}

// stageIndex 返回阶段顺序索引；(index, ok)。
func (s Stage) stageIndex() (int, bool) {
	i, ok := stageOrder[s]
	return i, ok
}

// AdvanceStage 推进阶段：可跳级，不可回退，不可原地踏步（REQ-06 / AC-06）。
// 失败返回 ErrStageRegression / ErrValidation，且不产生任何事件。
func (s *Story) AdvanceStage(to Stage) error {
	if !to.Valid() {
		return shared.New("INVALID_STAGE", "unknown stage: "+string(to), map[string]any{"stage": to})
	}
	fromIdx, ok := s.Stage.stageIndex()
	if !ok {
		// 当前 stage 非法数据（导入脏数据防御）。
		return shared.New("INVALID_STAGE", "current stage is invalid: "+string(s.Stage),
			map[string]any{"stage": s.Stage})
	}
	toIdx, _ := to.stageIndex()
	if toIdx < fromIdx {
		return shared.New("STAGE_REGRESSION", "stage cannot regress",
			map[string]any{"from": s.Stage, "to": to})
	}
	if toIdx == fromIdx {
		return shared.New("STAGE_NOT_ADVANCED", "stage already at target",
			map[string]any{"from": s.Stage, "to": to})
	}
	from := s.Stage
	s.Stage = to
	s.emit(Event{
		ObjectType: "story",
		ObjectID:   s.ID,
		Action:     Action("advanceStage"),
		Changes:    []FieldChange{{Field: "stage", From: string(from), To: string(to)}},
		SpecRef:    StageSpecRef,
	})
	return nil
}
