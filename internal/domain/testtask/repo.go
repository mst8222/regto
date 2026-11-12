package testtask

import "context"

// Repository 是测试执行域的持久化契约（adapter/persistence/postgres 实现）。
type Repository interface {
	// GetByID 加载测试单；不存在返回 shared.ErrNotFound。
	GetByID(ctx context.Context, id int64) (*TestTask, error)
	// Save 保存测试单（INSERT 或乐观锁 UPDATE）。
	Save(ctx context.Context, t *TestTask) error
	// List 分页查询测试单。
	List(ctx context.Context, q TaskQuery) ([]*TestTask, int, error)
	// AddCases 把用例加入测试单（锁定 case_version）；重复加入由唯一键忽略。
	// 返回实际新增数（不含已存在）。
	AddCases(ctx context.Context, taskID int64, caseIDs []int64, caseVersions []int) (added int, err error)
	// ListCases 列出测试单内的用例及其实执行状态。
	ListCases(ctx context.Context, taskID int64) ([]TaskCase, error)
	// NextRunNo 返回该测试单下一个可用的 run_no（已有最大 + 1；无结果时 1）。
	// 用于导入缺省轮次（DEC-07）。
	NextRunNo(ctx context.Context, taskID int64) (int, error)
	// SaveResult 写入执行结果（幂等键 task_id+case_id+run_no，ON CONFLICT DO UPDATE）。
	// 同时把 test_task_cases.last_run_result 更新为最新值。
	SaveResult(ctx context.Context, r RunResult) error
	// ListResults 分页查询测试单的执行结果。
	ListResults(ctx context.Context, taskID int64, page Page) ([]RunResult, int, error)
	// CountResults 统计测试单内各结果的条数（报表用）。
	CountResults(ctx context.Context, taskID int64) (map[Result]int, error)
}

// TaskCase 是测试单内的用例行（含锁定的版本与执行状态）。
type TaskCase struct {
	ID            int64
	TestTaskID    int64
	CaseID        int64
	CaseVersion   int
	CaseTitle     string
	Status        CaseRunStatus
	LastRunResult Result
}

// RunResult 是一次执行结果记录（与 Result 枚举区分：记录 vs 取值）。
type RunResult struct {
	ID           int64
	TestTaskID   int64
	CaseID       int64
	CaseVersion  int
	RunNo        int
	Result       Result // 结果取值
	StepResults  string // JSONB 原文（[{seq,result,actual},...]）
	DurationMs   int
	ActorType    string // human | ai | system
	ActorID      string
	TraceID      string
	RawOutput    string
	ExecutedAtMs int64
}

// Page 是结果查询的分页参数（与 shared.Page 对齐的轻量结构）。
type Page struct {
	Page    int
	PerPage int
}

// TaskQuery 是测试单列表查询对象。
type TaskQuery struct {
	ProductID int64
	Status    Status
	Keyword   string
	Page      Page
}
