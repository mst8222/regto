package importpkg

import "fmt"

// Verdict 是单项校验的判定结果（REQ-06）。
type Verdict string

// 判定全集。
const (
	VerdictPass Verdict = "PASS"
	VerdictFail Verdict = "FAIL"
	VerdictWarn Verdict = "WARN" // 不阻塞退出码 0（如附件可达性）
)

// CheckID 是 10 项校验的编号（REQ-06 表格）。
type CheckID int

// 10 项校验编号。
const (
	CheckRowCounts              CheckID = iota + 1 // 1 行数比对
	CheckFKOrphans                                 // 2 外键悬空
	CheckEnumDomain                                // 3 枚举域
	CheckTimeNonEmpty                              // 4 时间非空
	CheckEstimatePrecision                         // 5 工时精度
	CheckSampleDiff                                // 6 抽样 diff
	CheckStatusDistribution                        // 7 状态分布
	CheckTreeIntegrity                             // 8 树完整性
	CheckVersionedIntegrity                        // 9 版本化完整性
	CheckAttachmentReachability                    // 10 附件可达（WARN 级）
)

// AllCheckIDs 返回 10 项编号（固定顺序，报告稳定可测）。
func AllCheckIDs() []CheckID {
	return []CheckID{
		CheckRowCounts, CheckFKOrphans, CheckEnumDomain, CheckTimeNonEmpty,
		CheckEstimatePrecision, CheckSampleDiff, CheckStatusDistribution,
		CheckTreeIntegrity, CheckVersionedIntegrity, CheckAttachmentReachability,
	}
}

// String 返回 "01 行数比对" 形式的标签。
func (c CheckID) String() string {
	names := map[CheckID]string{
		CheckRowCounts:              "行数比对",
		CheckFKOrphans:              "外键悬空",
		CheckEnumDomain:             "枚举域",
		CheckTimeNonEmpty:           "时间非空",
		CheckEstimatePrecision:      "工时精度",
		CheckSampleDiff:             "抽样 diff",
		CheckStatusDistribution:     "状态分布",
		CheckTreeIntegrity:          "树完整性",
		CheckVersionedIntegrity:     "版本化完整性",
		CheckAttachmentReachability: "附件可达",
	}
	return fmt.Sprintf("%02d %s", int(c), names[c])
}

// CheckResult 是单项校验结果（含明细）。
type CheckResult struct {
	ID      CheckID
	Verdict Verdict
	Detail  string // 人类可读明细（哪张表/哪个值不一致）
}

// CountPair 是一张表的源/目标行数（CheckRowCounts 输入）。
type CountPair struct {
	Table  string
	Source int64
	Target int64
}

// RowCountsVerdict 判定行数比对（REQ-06 #1）。
// 删除行单独比对：调用方传入 total 与 live 两组。
func RowCountsVerdict(pairs []CountPair) CheckResult {
	for _, p := range pairs {
		if p.Source != p.Target {
			return CheckResult{ID: CheckRowCounts, Verdict: VerdictFail,
				Detail: fmt.Sprintf("表 %s 行数不一致：源 %d / 目标 %d", p.Table, p.Source, p.Target)}
		}
	}
	return CheckResult{ID: CheckRowCounts, Verdict: VerdictPass, Detail: "全部表行数一致"}
}

// FKOrphan 是一条悬空外键（CheckFKOrphans 输入）。
type FKOrphan struct {
	Table  string
	Column string
	Value  int64
}

// FKOrphansVerdict 判定外键悬空（REQ-06 #2）。任一悬空 → FAIL。
func FKOrphansVerdict(orphans []FKOrphan) CheckResult {
	if len(orphans) == 0 {
		return CheckResult{ID: CheckFKOrphans, Verdict: VerdictPass, Detail: "无悬空外键"}
	}
	o := orphans[0]
	return CheckResult{ID: CheckFKOrphans, Verdict: VerdictFail,
		Detail: fmt.Sprintf("%d 条悬空外键，首条 %s.%s=%d", len(orphans), o.Table, o.Column, o.Value)}
}

// EnumViolation 是一个越界枚举值（CheckEnumDomain 输入）。
type EnumViolation struct {
	Table  string
	Column string
	Value  string
	Legal  []string
}

// EnumDomainVerdict 判定枚举域（REQ-06 #3）。任一越界 → FAIL。
func EnumDomainVerdict(violations []EnumViolation) CheckResult {
	if len(violations) == 0 {
		return CheckResult{ID: CheckEnumDomain, Verdict: VerdictPass, Detail: "全部枚举值在域内"}
	}
	v := violations[0]
	return CheckResult{ID: CheckEnumDomain, Verdict: VerdictFail,
		Detail: fmt.Sprintf("%d 个越界值，首条 %s.%s=%q（合法 %v）", len(violations), v.Table, v.Column, v.Value, v.Legal)}
}

// TimeNull 是一条空时间列（CheckTimeNonEmpty 输入）。
type TimeNull struct {
	Table  string
	Column string
	ID     int64
}

// TimeNonEmptyVerdict 判定时间非空（REQ-06 #4）。
func TimeNonEmptyVerdict(nulls []TimeNull) CheckResult {
	if len(nulls) == 0 {
		return CheckResult{ID: CheckTimeNonEmpty, Verdict: VerdictPass, Detail: "必填时间列全部非空"}
	}
	n := nulls[0]
	return CheckResult{ID: CheckTimeNonEmpty, Verdict: VerdictFail,
		Detail: fmt.Sprintf("%d 条空时间，首条 %s.%s (id=%d)", len(nulls), n.Table, n.Column, n.ID)}
}

// EstimateDelta 是一行工时比对（CheckEstimatePrecision 输入）。
type EstimateDelta struct {
	Table  string
	ID     int64
	Source float64
	Target float64
	Delta  float64
}

// EstimatePrecisionVerdict 判定工时精度（REQ-06 #5）。
// EstimateTolerance 是容差 0.05（四舍五入到 0.1 的半误差）。
const EstimateTolerance = 0.05

func EstimatePrecisionVerdict(deltas []EstimateDelta) CheckResult {
	for _, d := range deltas {
		if d.Delta > EstimateTolerance {
			return CheckResult{ID: CheckEstimatePrecision, Verdict: VerdictFail,
				Detail: fmt.Sprintf("表 %s id=%d 工时偏差 %.3f 超容差 %.2f（源 %v 目标 %v）",
					d.Table, d.ID, d.Delta, EstimateTolerance, d.Source, d.Target)}
		}
	}
	return CheckResult{ID: CheckEstimatePrecision, Verdict: VerdictPass, Detail: "工时精度在容差内"}
}

// SampleMismatch 是一条抽样不一致（CheckSampleDiff 输入）。
type SampleMismatch struct {
	Table   string
	Source  int64
	Column  string
	Source2 any
	Target  any
}

// SampleDiffVerdict 判定抽样 diff（REQ-06 #6）。
func SampleDiffVerdict(mismatches []SampleMismatch) CheckResult {
	if len(mismatches) == 0 {
		return CheckResult{ID: CheckSampleDiff, Verdict: VerdictPass, Detail: "抽样逐列一致"}
	}
	m := mismatches[0]
	return CheckResult{ID: CheckSampleDiff, Verdict: VerdictFail,
		Detail: fmt.Sprintf("%d 处不一致，首条 %s(id=%d).%s：源 %v / 目标 %v",
			len(mismatches), m.Table, m.Source, m.Column, m.Source2, m.Target)}
}

// StatusHistogram 是一张表的状态分布（CheckStatusDistribution 输入）。
type StatusHistogram struct {
	Table  string
	Source map[string]int64
	Target map[string]int64
}

// StatusDistributionVerdict 判定状态分布（REQ-06 #7）。
func StatusDistributionVerdict(hists []StatusHistogram) CheckResult {
	for _, h := range hists {
		for k, sv := range h.Source {
			if h.Target[k] != sv {
				return CheckResult{ID: CheckStatusDistribution, Verdict: VerdictFail,
					Detail: fmt.Sprintf("表 %s 状态 %q 计数不一致：源 %d / 目标 %d", h.Table, k, sv, h.Target[k])}
			}
		}
		for k := range h.Target {
			if _, ok := h.Source[k]; !ok {
				return CheckResult{ID: CheckStatusDistribution, Verdict: VerdictFail,
					Detail: fmt.Sprintf("表 %s 目标多出状态 %q", h.Table, k)}
			}
		}
	}
	return CheckResult{ID: CheckStatusDistribution, Verdict: VerdictPass, Detail: "状态分布一致"}
}

// TreeBreak 是一条树断裂（CheckTreeIntegrity 输入：path/grade 与 parent 矛盾）。
type TreeBreak struct {
	Table    string
	ID       int64
	ParentID int64
	Reason   string
}

// TreeIntegrityVerdict 判定树完整性（REQ-06 #8）。
func TreeIntegrityVerdict(breaks []TreeBreak) CheckResult {
	if len(breaks) == 0 {
		return CheckResult{ID: CheckTreeIntegrity, Verdict: VerdictPass, Detail: "父子树无断裂"}
	}
	b := breaks[0]
	return CheckResult{ID: CheckTreeIntegrity, Verdict: VerdictFail,
		Detail: fmt.Sprintf("%d 处断裂，首条 %s(id=%d)：%s", len(breaks), b.Table, b.ID, b.Reason)}
}

// VersionGap 是一处版本跳号（CheckVersionedIntegrity 输入）。
type VersionGap struct {
	Table string
	ID    int64
	Have  int64
	Want  int64
}

// VersionedIntegrityVerdict 判定版本化完整性（REQ-06 #9）：
// cases.version 从 1 连续；test_results.run_no 每 (task,case) 从 1 连续。
func VersionedIntegrityVerdict(gaps []VersionGap) CheckResult {
	if len(gaps) == 0 {
		return CheckResult{ID: CheckVersionedIntegrity, Verdict: VerdictPass, Detail: "版本号连续无跳号"}
	}
	g := gaps[0]
	return CheckResult{ID: CheckVersionedIntegrity, Verdict: VerdictFail,
		Detail: fmt.Sprintf("%d 处跳号，首条 %s(id=%d)：%d（期望 %d）", len(gaps), g.Table, g.ID, g.Have, g.Want)}
}

// AttachmentMissing 是一个缺失附件（CheckAttachmentReachability 输入）。
type AttachmentMissing struct {
	Path string
}

// AttachmentReachabilityVerdict 判定附件可达性（REQ-06 #10，WARN 级：
// 附件文件在源库文件目录缺失不 FAIL——导入只迁数据，文件搬迁是运维动作）。
func AttachmentReachabilityVerdict(missing []AttachmentMissing) CheckResult {
	if len(missing) == 0 {
		return CheckResult{ID: CheckAttachmentReachability, Verdict: VerdictPass, Detail: "附件全部可达"}
	}
	return CheckResult{ID: CheckAttachmentReachability, Verdict: VerdictWarn,
		Detail: fmt.Sprintf("%d 个附件在源目录缺失（WARN：文件搬迁属运维动作）", len(missing))}
}

// ExitCode 汇总校验结果：有 FAIL → 2；仅 WARN/PASS → 0（REQ-06）。
func ExitCode(results []CheckResult) int {
	for _, r := range results {
		if r.Verdict == VerdictFail {
			return 2
		}
	}
	return 0
}
