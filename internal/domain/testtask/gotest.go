package testtask

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mst8222/regto/internal/domain/shared"
)

// GoTestEvent 是 `go test -json` 输出中的一行事件。
// 字段与 go tool test2json 的输出结构一一对应（仅取所需子集）。
type GoTestEvent struct {
	// Time 是事件时间戳（RFC3339Nano）。
	Time string `json:"Time,omitempty"`
	// Action ∈ run|pause|cont|pass|fail|skip|bench|output。
	Action string `json:"Action"`
	// Package 是包导入路径（包级事件无 Test 字段）。
	Package string `json:"Package,omitempty"`
	// Test 是测试函数名（含子测试路径，如 TestX/sub）。
	Test string `json:"Test,omitempty"`
	// Elapsed 是耗时（秒，浮点）。
	Elapsed float64 `json:"Elapsed,omitempty"`
	// Output 是测试输出行（含 "--- FAIL: ..." 等摘要行）。
	Output string `json:"Output,omitempty"`
}

// AggregateResult 是一个测试（Package + "/" + Test）的聚合结果。
type AggregateResult struct {
	// Key 是聚合键：Package + "/" + Test。
	Key string
	// Package 是包路径。
	Package string
	// Test 是测试名（不含包路径）。
	Test string
	// Result 是映射后的结果（pass/fail/skip；见 MapGoTestResult）。
	Result Result
	// ElapsedSec 是 go 报告的耗时（秒）。
	ElapsedSec float64
	// Output 是聚合的全部输出行（含换行，供 raw_output 落库）。
	Output string
}

// ParseGoTestJSON 解析 `go test -json` 事件流为聚合结果（纯函数，REQ-04）。
// 规则：
//   - 忽略包级事件（无 Test 字段）——它们不是用例结果（AC-02）；
//   - 同一 Test 的多个结果事件（-count>1）后者覆盖前者（REQ-04.5）；
//   - Action ∈ pass|fail|skip 决定结果；run/pause/cont/output 只累积输出；
//   - 无法解析的行返回错误并带行号（400 INVALID_GOTEST_JSON）。
func ParseGoTestJSON(data []byte) ([]AggregateResult, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var (
		order   []string // 保持首次出现顺序
		byKey   = map[string]*AggregateResult{}
		outputs = map[string][]string{}
	)
	for i, ln := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(ln)
		if line == "" {
			continue
		}
		var ev GoTestEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, shared.New("INVALID_GOTEST_JSON",
				fmt.Sprintf("第 %d 行不是合法 JSON: %v", lineNo, err),
				map[string]any{"line": lineNo})
		}
		if ev.Test == "" {
			continue // 包级事件：不是用例结果（AC-02）
		}
		key := ev.Package + "/" + ev.Test
		agg, ok := byKey[key]
		if !ok {
			agg = &AggregateResult{Key: key, Package: ev.Package, Test: ev.Test}
			byKey[key] = agg
			order = append(order, key)
		}
		if ev.Output != "" {
			outputs[key] = append(outputs[key], ev.Output)
		}
		if ev.Elapsed > 0 {
			agg.ElapsedSec = ev.Elapsed
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			agg.Result = MapGoTestResult(ev.Action)
		}
	}
	out := make([]AggregateResult, 0, len(order))
	for _, k := range order {
		agg := byKey[k]
		agg.Output = strings.Join(outputs[k], "")
		// 只有结果事件（pass/fail/skip）才计入；无结果事件的 Test 视为未完成，跳过
		if agg.Result == "" {
			continue
		}
		out = append(out, *agg)
	}
	return out, nil
}

// MapGoTestResult 把 go test 动作映射为执行结果（REQ-04.3）。
// go 侧没有 blocked/n/a：由人工或 ZTF 执行补充。
func MapGoTestResult(action string) Result {
	switch action {
	case "pass":
		return ResultPass
	case "fail":
		return ResultFail
	case "skip":
		return ResultSkip
	}
	return ""
}

// TestTask 是测试单聚合根。
type TestTask struct {
	ID          int64
	Name        string
	ProductID   int64
	ProjectID   *int64
	BuildID     *int64 // 预留（M2-B 的 build 域），本期不校验存在性
	Type        string
	OwnerID     int64
	Pri         int
	BeginMs     *int64
	EndMs       *int64
	Status      Status
	Desc        string
	OpenedByID  int64
	OpenedAtMs  int64
	UpdatedAtMs int64
	DeletedAtMs *int64
	LoadedAtMs  int64 // 乐观锁
}

// NewTestTask 创建测试单（初始状态 wait）。
func NewTestTask(name string, productID, ownerID int64, nowMs int64) *TestTask {
	return &TestTask{
		Name:        name,
		ProductID:   productID,
		OwnerID:     ownerID,
		Status:      StatusWait,
		OpenedByID:  ownerID,
		OpenedAtMs:  nowMs,
		UpdatedAtMs: nowMs,
	}
}

// Validate 校验测试单不变式。
func (t *TestTask) Validate() error {
	if t.Name == "" {
		return shared.New("INVALID_FIELD", "测试单名称不能为空", map[string]any{"field": "name"})
	}
	if t.ProductID <= 0 {
		return shared.New("INVALID_FIELD", "测试单必须归属产品", map[string]any{"field": "product_id"})
	}
	return nil
}
