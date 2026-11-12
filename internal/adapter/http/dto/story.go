// Package dto 定义 HTTP 层的请求/响应结构（与领域模型隔离，避免领域被传输格式污染）。
package dto

import (
	"strconv"
	"time"

	"github.com/mst8222/regto/internal/domain/shared"
	"github.com/mst8222/regto/internal/domain/story"
)

// CreateStoryRequest 是创建需求的请求体。
type CreateStoryRequest struct {
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

// SpecInput 是规格输入（change 动作使用）。
type SpecInput struct {
	Title    string   `json:"title,omitempty"`
	Spec     string   `json:"spec,omitempty"`
	Verify   string   `json:"verify,omitempty"`
	Pri      int      `json:"pri,omitempty"`
	Estimate *float64 `json:"estimate,omitempty"`
}

// ToDomain 转领域输入类型；nil 返回 nil。
func (in *SpecInput) ToDomain() *story.SpecInput {
	if in == nil {
		return nil
	}
	return &story.SpecInput{
		Title:    in.Title,
		Spec:     in.Spec,
		Verify:   in.Verify,
		Pri:      in.Pri,
		Estimate: in.Estimate,
	}
}

// TransitionRequest 是状态转移请求体（统一入口：ROADMAP §3.4 关键差异 1）。
type TransitionRequest struct {
	Action        string     `json:"action"`
	Spec          *SpecInput `json:"spec,omitempty"`
	CloseReason   string     `json:"close_reason,omitempty"`
	ReviewResults []string   `json:"review_results,omitempty"`
	Reviewers     []int64    `json:"reviewers,omitempty"`
	Comment       string     `json:"comment,omitempty"`
	ReviewerID    int64      `json:"reviewer_id,omitempty"`
}

// AdvanceStageRequest 是推进研发阶段的请求体。
type AdvanceStageRequest struct {
	Stage string `json:"stage"`
}

// SpecVersionResponse 是规格版本摘要。
type SpecVersionResponse struct {
	Version int    `json:"version"`
	Title   string `json:"title"`
	Spec    string `json:"spec,omitempty"`
	Verify  string `json:"verify,omitempty"`
}

// StoryResponse 是需求响应体（时间统一 RFC3339 UTC，ROADMAP §3.4）。
type StoryResponse struct {
	ID           int64                `json:"id"`
	ProductID    int64                `json:"product_id"`
	ModuleID     *int64               `json:"module_id,omitempty"`
	Title        string               `json:"title"`
	Type         string               `json:"type"`
	Pri          int                  `json:"pri"`
	Estimate     *float64             `json:"estimate,omitempty"`
	Status       string               `json:"status"`
	Stage        string               `json:"stage"`
	SpecVersion  int                  `json:"spec_version"`
	ClosedReason string               `json:"closed_reason,omitempty"`
	Reviewers    []int64              `json:"reviewers,omitempty"`
	OpenedByID   int64                `json:"opened_by_id"`
	CreatedAt    string               `json:"created_at"`
	UpdatedAt    string               `json:"updated_at"`
	CurrentSpec  *SpecVersionResponse `json:"current_spec,omitempty"`
}

// StoryFromDomain 把领域聚合映射为响应体。
func StoryFromDomain(s *story.Story) StoryResponse {
	resp := StoryResponse{
		ID:           s.ID,
		ProductID:    s.ProductID,
		ModuleID:     s.ModuleID,
		Title:        s.Title,
		Type:         s.Type,
		Pri:          s.Pri,
		Estimate:     s.Estimate,
		Status:       string(s.Status),
		Stage:        string(s.Stage),
		SpecVersion:  s.SpecVersion,
		ClosedReason: s.ClosedReason,
		Reviewers:    s.Reviewers,
		OpenedByID:   s.OpenedByID,
		CreatedAt:    rfc3339(s.CreatedAtMs),
		UpdatedAt:    rfc3339(s.UpdatedAtMs),
	}
	if sp := s.CurrentSpec(); sp != nil {
		resp.CurrentSpec = &SpecVersionResponse{
			Version: sp.Version,
			Title:   sp.Title,
			Spec:    sp.Spec,
			Verify:  sp.Verify,
		}
	}
	return resp
}

// StoryListItemResponse 是列表项响应体（DESIGN-STORY-002 DEC-04：不含规格版本链）。
type StoryListItemResponse struct {
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
	Deleted      bool   `json:"deleted,omitempty"`
}

// ListLinks 是分页信封的导航链接（ROADMAP §3.4 关键差异 2）。
type ListLinks struct {
	Next string `json:"next,omitempty"`
	Prev string `json:"prev,omitempty"`
}

// StoryListResponse 是列表响应信封：集合恒定在 data 键下，客户端可泛型解析。
type StoryListResponse struct {
	Data  []StoryListItemResponse `json:"data"`
	Page  shared.Page             `json:"page"`
	Links ListLinks               `json:"links"`
}

// NewStoryListResponse 由领域投影构造列表信封（data 恒为数组，空结果也是 []）。
func NewStoryListResponse(items []story.StoryListItem, total int64, page shared.Page, links ListLinks) StoryListResponse {
	data := make([]StoryListItemResponse, 0, len(items))
	for i := range items {
		it := &items[i]
		row := StoryListItemResponse{
			ID:           it.ID,
			ProductID:    it.ProductID,
			ModuleID:     it.ModuleID,
			Title:        it.Title,
			Type:         it.Type,
			Pri:          it.Pri,
			Status:       string(it.Status),
			Stage:        string(it.Stage),
			SpecVersion:  it.SpecVersion,
			ClosedReason: it.ClosedReason,
			AssignedToID: it.AssignedToID,
			OpenedByID:   it.OpenedByID,
			CreatedAt:    rfc3339(it.CreatedAtMs),
			UpdatedAt:    rfc3339(it.UpdatedAtMs),
		}
		if it.DeletedAtMs != nil {
			row.Deleted = true
		}
		data = append(data, row)
	}
	page.Total = total
	page.HasMore = int64(page.Offset()+page.PerPage) < total
	return StoryListResponse{Data: data, Page: page, Links: links}
}

// rfc3339 把 unix 毫秒转为 RFC3339 UTC（0 视为空串）。
func rfc3339(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// ParseInt64 解析正整数；非法返回 0。
func ParseInt64(raw string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
