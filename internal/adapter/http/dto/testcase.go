package dto

import (
	"github.com/mst8222/regto/internal/domain/shared"
	dtcase "github.com/mst8222/regto/internal/domain/testcase"
)

// CreateCaseRequest 是创建用例的请求体。
type CreateCaseRequest struct {
	ProductID    int64  `json:"product_id"`
	ModuleID     *int64 `json:"module_id,omitempty"`
	StoryID      *int64 `json:"story_id,omitempty"`
	StoryVersion int    `json:"story_version,omitempty"`
	BranchID     *int64 `json:"branch_id,omitempty"`
	Title        string `json:"title"`
	Precondition string `json:"precondition,omitempty"`
	Keywords     string `json:"keywords,omitempty"`
	Pri          int    `json:"pri,omitempty"`
	Type         string `json:"type,omitempty"`
	ExternalID   string `json:"external_id,omitempty"`
}

// UpdateCaseRequest 是修改用例的请求体（全字段可选，空值不覆盖）。
type UpdateCaseRequest struct {
	Title        string `json:"title,omitempty"`
	Precondition string `json:"precondition,omitempty"`
	Keywords     string `json:"keywords,omitempty"`
	Pri          int    `json:"pri,omitempty"`
	Type         string `json:"type,omitempty"`
	ModuleID     *int64 `json:"module_id,omitempty"`
	StoryID      *int64 `json:"story_id,omitempty"`
	StoryVersion int    `json:"story_version,omitempty"`
}

// CaseTransitionRequest 是用例状态转移的请求体。
// 与 story 域的 TransitionRequest 各自独立：动作集不同（activate/block/
// investigate/resume），且 DecodeJSON 开着 DisallowUnknownFields，
// 复用会让客户端把 story 专属字段（spec/reviewers）静默带进来。
type CaseTransitionRequest struct {
	Action string `json:"action"`
}

// ImportZTFRequest 是 ZTF 导入的请求体。
type ImportZTFRequest struct {
	ZTF              string `json:"ztf"`
	DefaultProductID int64  `json:"default_product_id,omitempty"`
}

// ImportZTFResponse 是 ZTF 导入的响应。
type ImportZTFResponse struct {
	Total   int            `json:"total"`
	Created int            `json:"created"`
	Cases   []CaseResponse `json:"cases"`
}

// StepResponse 是用例步骤的响应。
type StepResponse struct {
	Seq      int    `json:"seq"`
	Name     string `json:"name"`
	Expect   string `json:"expect,omitempty"`
	StepType string `json:"step_type"`
}

// CaseResponse 是用例的响应。
type CaseResponse struct {
	ID            int64          `json:"id"`
	ProductID     int64          `json:"product_id"`
	ModuleID      *int64         `json:"module_id,omitempty"`
	StoryID       *int64         `json:"story_id,omitempty"`
	StoryVersion  int            `json:"story_version"`
	BranchID      *int64         `json:"branch_id,omitempty"`
	Title         string         `json:"title"`
	Precondition  string         `json:"precondition,omitempty"`
	Keywords      string         `json:"keywords,omitempty"`
	Pri           int            `json:"pri"`
	Type          string         `json:"type"`
	Status        string         `json:"status"`
	Version       int            `json:"version"`
	SourceRef     string         `json:"source_ref,omitempty"`
	ExternalID    string         `json:"external_id,omitempty"`
	Suite         string         `json:"suite,omitempty"`
	LastRunResult string         `json:"last_run_result,omitempty"`
	LastRunner    string         `json:"last_runner,omitempty"`
	OpenedByID    int64          `json:"opened_by_id"`
	OpenedAt      string         `json:"opened_at"`
	UpdatedAt     string         `json:"updated_at"`
	Steps         []StepResponse `json:"steps,omitempty"`
}

// CaseFromDomain 把领域用例转为响应（步骤随详情返回）。
func CaseFromDomain(c *dtcase.Case) CaseResponse {
	out := CaseResponse{
		ID:            c.ID,
		ProductID:     c.ProductID,
		ModuleID:      c.ModuleID,
		StoryID:       c.StoryID,
		StoryVersion:  c.StoryVersion,
		BranchID:      c.BranchID,
		Title:         c.Title,
		Precondition:  c.Precondition,
		Keywords:      c.Keywords,
		Pri:           c.Pri,
		Type:          string(c.Type),
		Status:        string(c.Status),
		Version:       c.Version,
		SourceRef:     c.SourceRef,
		ExternalID:    c.ExternalID,
		Suite:         c.Suite,
		LastRunResult: c.LastRunResult,
		LastRunner:    c.LastRunner,
		OpenedByID:    c.OpenedByID,
		OpenedAt:      rfc3339(c.OpenedAtMs),
		UpdatedAt:     rfc3339(c.UpdatedAtMs),
	}
	for _, s := range c.Steps {
		out.Steps = append(out.Steps, StepResponse{
			Seq:      s.Seq,
			Name:     s.Name,
			Expect:   s.Expect,
			StepType: string(s.StepType),
		})
	}
	return out
}

// CasesFromDomain 批量转换。
func CasesFromDomain(cases []*dtcase.Case) []CaseResponse {
	out := make([]CaseResponse, 0, len(cases))
	for _, c := range cases {
		out = append(out, CaseFromDomain(c))
	}
	return out
}

// CaseListResponse 是用例列表响应（信封 {data,page,links}，与 story 域同构）。
type CaseListResponse struct {
	Data  []CaseResponse `json:"data"`
	Page  shared.Page    `json:"page"`
	Links ListLinks      `json:"links"`
}

// NewCaseListResponse 构造用例列表信封。空数据返回 [] 而非 null（与 story 域一致）。
func NewCaseListResponse(items []CaseResponse, total int, page shared.Page, links ListLinks) CaseListResponse {
	if items == nil {
		items = []CaseResponse{}
	}
	return CaseListResponse{Data: items, Page: page, Links: links}
}
