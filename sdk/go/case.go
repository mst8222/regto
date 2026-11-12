package regto

import (
	"context"
	"net/url"
)

// CaseService 提供用例（case）相关接口。
type CaseService struct{ c *Client }

// Case 是用例视图（对应 dto 的用例响应）。
type Case struct {
	ID           int64  `json:"id"`
	ProductID    int64  `json:"product_id"`
	ModuleID     *int64 `json:"module_id,omitempty"`
	StoryID      *int64 `json:"story_id,omitempty"`
	Title        string `json:"title"`
	Pri          int    `json:"pri"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	Precondition string `json:"precondition,omitempty"`
	Keywords     string `json:"keywords,omitempty"`
	Version      int    `json:"version"`
	ExternalID   string `json:"external_id,omitempty"`
	OpenedByID   int64  `json:"opened_by_id,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// CaseQuery 是用例列表查询参数。
type CaseQuery struct {
	ProductID int64
	ModuleID  int64
	SuiteID   int64
	StoryID   int64
	Status    string
	Keyword   string
	Page      int
	PerPage   int
}

func (q CaseQuery) values() url.Values {
	v := url.Values{}
	queryInt(v, "product_id", q.ProductID)
	queryInt(v, "module_id", q.ModuleID)
	queryInt(v, "suite_id", q.SuiteID)
	queryInt(v, "story_id", q.StoryID)
	queryString(v, "status", q.Status)
	queryString(v, "keyword", q.Keyword)
	queryInt(v, "page", int64(q.Page))
	queryInt(v, "per_page", int64(q.PerPage))
	return v
}

// CaseList 是列表信封。
type CaseList struct {
	Data  []Case `json:"data"`
	Page  Page   `json:"page"`
	Links ListLinks
}

// List 拉用例列表。
func (s *CaseService) List(ctx context.Context, q CaseQuery) (*CaseList, error) {
	var out CaseList
	if err := s.c.do(ctx, "GET", apiPath("/cases"), q.values(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get 按 ID 取用例。
func (s *CaseService) Get(ctx context.Context, id int64) (*Case, error) {
	var out Case
	if err := s.c.do(ctx, "GET", apiPath("/cases/"+itoa(id)), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateCaseInput 是创建用例的入参。
type CreateCaseInput struct {
	ProductID    int64  `json:"product_id"`
	ModuleID     *int64 `json:"module_id,omitempty"`
	StoryID      *int64 `json:"story_id,omitempty"`
	Title        string `json:"title"`
	Pri          int    `json:"pri,omitempty"`
	Type         string `json:"type,omitempty"`
	Precondition string `json:"precondition,omitempty"`
	Keywords     string `json:"keywords,omitempty"`
	ExternalID   string `json:"external_id,omitempty"`
}

// Create 建用例。
func (s *CaseService) Create(ctx context.Context, in CreateCaseInput) (*Case, error) {
	var out Case
	if err := s.c.do(ctx, "POST", apiPath("/cases"), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CaseTransitionInput 是用例状态转移入参。
type CaseTransitionInput struct {
	Action  string `json:"action"`
	Comment string `json:"comment,omitempty"`
}

// Transition 对用例做状态转移。
func (s *CaseService) Transition(ctx context.Context, id int64, in CaseTransitionInput) (*Case, error) {
	var out Case
	if err := s.c.do(ctx, "POST", apiPath("/cases/"+itoa(id)+"/transitions"), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
