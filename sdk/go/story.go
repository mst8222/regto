package regto

import (
	"context"
	"net/url"
)

// StoryService 提供需求（story）相关接口。
type StoryService struct{ c *Client }

// Story 是需求的完整视图（对应 dto.StoryResponse）。
type Story struct {
	ID           int64    `json:"id"`
	ProductID    int64    `json:"product_id"`
	ModuleID     *int64   `json:"module_id,omitempty"`
	Title        string   `json:"title"`
	Type         string   `json:"type"`
	Pri          int      `json:"pri"`
	Estimate     *float64 `json:"estimate,omitempty"`
	Status       string   `json:"status"`
	Stage        string   `json:"stage"`
	SpecVersion  int      `json:"spec_version"`
	ClosedReason string   `json:"closed_reason,omitempty"`
	Reviewers    []int64  `json:"reviewers,omitempty"`
	OpenedByID   int64    `json:"opened_by_id"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// StoryItem 是列表项（比 Story 少一些字段，服务端为列表做了投影）。
type StoryItem struct {
	ID           int64  `json:"id"`
	ProductID    int64  `json:"product_id"`
	ModuleID     *int64 `json:"module_id,omitempty"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Pri          int    `json:"pri"`
	Status       string `json:"status"`
	Stage        string `json:"stage"`
	SpecVersion  int    `json:"spec_version"`
	ClosedReason string `json:"closed_reason,omitempty"`
	AssignedToID *int64 `json:"assigned_to_id,omitempty"`
	OpenedByID   int64  `json:"opened_by_id"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// StoryQuery 是列表查询参数（与服务端 StoryQueryFromRequest 接受的键对齐）。
type StoryQuery struct {
	ProductID int64
	ModuleID  int64
	Status    string
	Stage     string
	Keyword   string
	Page      int
	PerPage   int
}

func (q StoryQuery) values() url.Values {
	v := url.Values{}
	queryInt(v, "product_id", q.ProductID)
	queryInt(v, "module_id", q.ModuleID)
	queryString(v, "status", q.Status)
	queryString(v, "stage", q.Stage)
	queryString(v, "keyword", q.Keyword)
	queryInt(v, "page", int64(q.Page))
	queryInt(v, "per_page", int64(q.PerPage))
	return v
}

// StoryList 是列表信封：集合恒定在 data 键下。
type StoryList struct {
	Data  []StoryItem `json:"data"`
	Page  Page        `json:"page"`
	Links ListLinks   `json:"links"`
}

// ListLinks 是上一页/下一页链接（服务端生成，可能为空串）。
type ListLinks struct {
	Next string `json:"next,omitempty"`
	Prev string `json:"prev,omitempty"`
}

// List 拉需求列表。
func (s *StoryService) List(ctx context.Context, q StoryQuery) (*StoryList, error) {
	var out StoryList
	if err := s.c.do(ctx, "GET", apiPath("/stories"), q.values(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get 按 ID 取需求详情。
func (s *StoryService) Get(ctx context.Context, id int64) (*Story, error) {
	var out Story
	if err := s.c.do(ctx, "GET", apiPath("/stories/"+itoa(id)), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateStoryInput 是创建需求的入参（对应 dto.CreateStoryRequest）。
type CreateStoryInput struct {
	ProductID  int64    `json:"product_id"`
	ModuleID   *int64   `json:"module_id,omitempty"`
	Title      string   `json:"title"`
	Type       string   `json:"type,omitempty"`
	Pri        int      `json:"pri,omitempty"`
	Estimate   *float64 `json:"estimate,omitempty"`
	Spec       string   `json:"spec,omitempty"`
	Verify     string   `json:"verify,omitempty"`
	Reviewers  []int64  `json:"reviewers,omitempty"`
	SourceNote string   `json:"source_note,omitempty"`
	AsDraft    bool     `json:"as_draft"`
}

// Create 建需求。
func (s *StoryService) Create(ctx context.Context, in CreateStoryInput) (*Story, error) {
	var out Story
	if err := s.c.do(ctx, "POST", apiPath("/stories"), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TransitionInput 是状态转移入参（服务端统一入口：所有动作走 action 字段）。
type TransitionInput struct {
	Action      string `json:"action"`
	CloseReason string `json:"close_reason,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

// Transition 对需求做状态转移（assign / review / develop / close 等）。
// 非法转移会返回 *Error 且 StatusCode=422、Code=INVALID_TRANSITION。
func (s *StoryService) Transition(ctx context.Context, id int64, in TransitionInput) (*Story, error) {
	var out Story
	if err := s.c.do(ctx, "POST", apiPath("/stories/"+itoa(id)+"/transitions"), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
