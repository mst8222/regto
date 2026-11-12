package dto

import (
	"strconv"

	"github.com/mst8222/regto/internal/domain/shared"
	dtt "github.com/mst8222/regto/internal/domain/testtask"
)

// TestTaskTransitionRequest 是测试单状态转移的请求体（动作集 start/finish/block）。
type TestTaskTransitionRequest struct {
	Action string `json:"action"`
}

// AddCasesRequest 是把用例加入测试单的请求体。
type AddCasesRequest struct {
	CaseIDs []int64 `json:"case_ids"`
}

// ImportResultRequest 是导入 go test -json 结果的请求体。
type ImportResultRequest struct {
	// Events 是 go test -json 的原始输出（一行一个 JSON 对象）。
	Events string `json:"events"`
	// ProductID 是匹配用例的产品维度（external_id 在该产品内唯一）。
	ProductID int64 `json:"product_id"`
	// ActorType/ActorID 缺省从认证上下文推导（REQ-05）。
	ActorType string `json:"actor_type,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	// TraceID 关联留痕链；缺省服务端生成 ULID。
	TraceID string `json:"trace_id,omitempty"`
	// RunNo 缺省按测试单已有最大 + 1。
	RunNo int `json:"run_no,omitempty"`
	// CreateMissing 未匹配用例时自动建档（REQ-06）。
	CreateMissing bool `json:"create_missing,omitempty"`
	// DryRun 只预览不写库。
	DryRun bool `json:"dry_run,omitempty"`
}

// TestTaskListResponse 是测试单列表信封。
type TestTaskListResponse struct {
	Data  []TestTaskResponse `json:"data"`
	Page  shared.Page        `json:"page"`
	Links ListLinks          `json:"links"`
}

// NewTaskListResponse 构造测试单列表信封。
func NewTaskListResponse(items []TestTaskResponse, page shared.Page, links ListLinks) TestTaskListResponse {
	if items == nil {
		items = []TestTaskResponse{}
	}
	return TestTaskListResponse{Data: items, Page: page, Links: links}
}

// ResultListResponse 是执行结果列表信封。
type ResultListResponse struct {
	Data  []ResultResponse `json:"data"`
	Page  shared.Page      `json:"page"`
	Links ListLinks        `json:"links"`
}

// NewResultListResponse 构造结果列表信封。
func NewResultListResponse(items []ResultResponse, page shared.Page, links ListLinks) ResultListResponse {
	if items == nil {
		items = []ResultResponse{}
	}
	return ResultListResponse{Data: items, Page: page, Links: links}
}

// rfc3339Ptr 把毫秒指针转为 RFC3339；nil 返回空串。
func rfc3339Ptr(ms *int64) string {
	if ms == nil || *ms == 0 {
		return ""
	}
	return rfc3339(*ms)
}

// itoaStr 轻量整数转字符串（dto 内部用）。
func itoaStr(v int64) string { return strconv.FormatInt(v, 10) }

// CreateTestTaskRequest 是创建测试单的请求体。
type CreateTestTaskRequest struct {
	Name      string `json:"name"`
	ProductID int64  `json:"product_id"`
	ProjectID *int64 `json:"project_id,omitempty"`
	BuildID   *int64 `json:"build_id,omitempty"` // 关联构建（SPEC-RELEASE-001 REQ-07）
	Type      string `json:"type,omitempty"`
	OwnerID   int64  `json:"owner_id,omitempty"`
	Pri       int    `json:"pri,omitempty"`
	Desc      string `json:"desc,omitempty"`
}

// TestTaskResponse 是测试单的响应。
type TestTaskResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ProductID  int64  `json:"product_id"`
	ProjectID  *int64 `json:"project_id,omitempty"`
	BuildID    *int64 `json:"build_id,omitempty"`
	Type       string `json:"type,omitempty"`
	OwnerID    int64  `json:"owner_id"`
	Pri        int    `json:"pri"`
	Status     string `json:"status"`
	Desc       string `json:"desc,omitempty"`
	Begin      string `json:"begin,omitempty"`
	End        string `json:"end,omitempty"`
	OpenedByID int64  `json:"opened_by_id"`
	OpenedAt   string `json:"opened_at"`
	UpdatedAt  string `json:"updated_at"`
}

// TestTaskFromDomain 把领域测试单转为响应。
func TestTaskFromDomain(t *dtt.TestTask) TestTaskResponse {
	return TestTaskResponse{
		ID:         t.ID,
		Name:       t.Name,
		ProductID:  t.ProductID,
		ProjectID:  t.ProjectID,
		BuildID:    t.BuildID,
		Type:       t.Type,
		OwnerID:    t.OwnerID,
		Pri:        t.Pri,
		Status:     string(t.Status),
		Desc:       t.Desc,
		Begin:      rfc3339Ptr(t.BeginMs),
		End:        rfc3339Ptr(t.EndMs),
		OpenedByID: t.OpenedByID,
		OpenedAt:   rfc3339(t.OpenedAtMs),
		UpdatedAt:  rfc3339(t.UpdatedAtMs),
	}
}

// TaskCaseResponse 是测试单内用例行的响应。
type TaskCaseResponse struct {
	CaseID        int64  `json:"case_id"`
	CaseVersion   int    `json:"case_version"`
	CaseTitle     string `json:"case_title"`
	Status        string `json:"status"`
	LastRunResult string `json:"last_run_result,omitempty"`
}

// ResultResponse 是执行结果的响应。
type ResultResponse struct {
	ID          int64  `json:"id"`
	CaseID      int64  `json:"case_id"`
	CaseVersion int    `json:"case_version"`
	RunNo       int    `json:"run_no"`
	Result      string `json:"result"`
	DurationMs  int    `json:"duration_ms"`
	ActorType   string `json:"actor_type"`
	ActorID     string `json:"actor_id"`
	TraceID     string `json:"trace_id,omitempty"`
	ExecutedAt  string `json:"executed_at"`
}

// ResultFromDomain 把领域结果转为响应。
func ResultFromDomain(r dtt.RunResult) ResultResponse {
	return ResultResponse{
		ID:          r.ID,
		CaseID:      r.CaseID,
		CaseVersion: r.CaseVersion,
		RunNo:       r.RunNo,
		Result:      string(r.Result),
		DurationMs:  r.DurationMs,
		ActorType:   r.ActorType,
		ActorID:     r.ActorID,
		TraceID:     r.TraceID,
		ExecutedAt:  rfc3339(r.ExecutedAtMs),
	}
}

// ImportResultResponse 是 go test -json 导入的响应。
type ImportResultResponse struct {
	TestTaskID int64            `json:"test_task_id"`
	RunNo      int              `json:"run_no"`
	Total      int              `json:"total"`
	Written    int              `json:"written"`
	Matched    int              `json:"matched"`
	Created    int              `json:"created"`
	ByResult   map[string]int   `json:"by_result"`
	Results    []ResultResponse `json:"results"`
}
