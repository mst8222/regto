package dto

import (
	"time"

	drel "github.com/mst8222/regto/internal/domain/release"
	"github.com/mst8222/regto/internal/domain/shared"
)

// dateStr 把毫秒时间戳指针转为 YYYY-MM-DD（DATE 列的传输格式）；
// nil 返回空串。
func dateStr(ms *int64) string {
	if ms == nil || *ms == 0 {
		return ""
	}
	return time.UnixMilli(*ms).UTC().Format("2006-01-02")
}

// ParseDateMs 把 YYYY-MM-DD 字符串解析为毫秒时间戳。
// 空串返回 (nil, nil)（不设置）；非法返回 INVALID_FIELD 错误。
func ParseDateMs(field, raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, shared.New("INVALID_FIELD", "日期格式必须为 YYYY-MM-DD",
			map[string]any{"field": field, "value": raw})
	}
	ms := t.UTC().UnixMilli()
	return &ms, nil
}

// ---------- Plan ----------

// CreatePlanRequest 是创建产品计划的请求体。
type CreatePlanRequest struct {
	ProductID int64  `json:"product_id"`
	BranchID  *int64 `json:"branch_id,omitempty"`
	ParentID  *int64 `json:"parent_id,omitempty"`
	Title     string `json:"title"`
	Desc      string `json:"desc,omitempty"`
	Begin     string `json:"begin,omitempty"` // YYYY-MM-DD
	End       string `json:"end,omitempty"`
}

// UpdatePlanRequest 是修改计划的请求体（全字段可选）。
type UpdatePlanRequest struct {
	Title *string `json:"title,omitempty"`
	Desc  *string `json:"desc,omitempty"`
	Begin *string `json:"begin,omitempty"`
	End   *string `json:"end,omitempty"`
}

// PlanTransitionRequest 是计划状态转移的请求体。
// 与 case/story/testtask 的 transition 各自独立：动作集不同
// （start/finish/close/activate），DecodeJSON 开着 DisallowUnknownFields。
type PlanTransitionRequest struct {
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"` // close 时写入 closed_reason
}

// LinkPlanStoryRequest 是计划关联需求的请求体。
type LinkPlanStoryRequest struct {
	StoryID int64 `json:"story_id"`
}

// PlanResponse 是产品计划的响应。
type PlanResponse struct {
	ID           int64  `json:"id"`
	ProductID    int64  `json:"product_id"`
	BranchID     *int64 `json:"branch_id,omitempty"`
	ParentID     *int64 `json:"parent_id,omitempty"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Desc         string `json:"desc,omitempty"`
	Begin        string `json:"begin,omitempty"`
	End          string `json:"end,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
	ClosedAt     string `json:"closed_at,omitempty"`
	ClosedReason string `json:"closed_reason,omitempty"`
	OpenedByID   int64  `json:"opened_by_id"`
	OpenedAt     string `json:"opened_at"`
	UpdatedAt    string `json:"updated_at"`
}

// PlanFromDomain 把领域计划映射为响应。
func PlanFromDomain(p *drel.Plan) PlanResponse {
	return PlanResponse{
		ID:           p.ID,
		ProductID:    p.ProductID,
		BranchID:     p.BranchID,
		ParentID:     p.ParentID,
		Title:        p.Title,
		Status:       string(p.Status),
		Desc:         p.Desc,
		Begin:        dateStr(p.BeginMs),
		End:          dateStr(p.EndMs),
		FinishedAt:   rfc3339Ptr(p.FinishedAtMs),
		ClosedAt:     rfc3339Ptr(p.ClosedAtMs),
		ClosedReason: p.ClosedReason,
		OpenedByID:   p.OpenedByID,
		OpenedAt:     rfc3339Ptr(&p.OpenedAtMs),
		UpdatedAt:    rfc3339Ptr(&p.UpdatedAtMs),
	}
}

// PlansFromDomain 批量映射。
func PlansFromDomain(ps []*drel.Plan) []PlanResponse {
	out := make([]PlanResponse, 0, len(ps))
	for _, p := range ps {
		out = append(out, PlanFromDomain(p))
	}
	return out
}

// PlanStoryResponse 是计划-需求关联的响应。
type PlanStoryResponse struct {
	ID      int64 `json:"id"`
	PlanID  int64 `json:"plan_id"`
	StoryID int64 `json:"story_id"`
	Order   int   `json:"order"`
}

// PlanStoriesFromDomain 批量映射。
func PlanStoriesFromDomain(pss []drel.PlanStory) []PlanStoryResponse {
	out := make([]PlanStoryResponse, 0, len(pss))
	for _, ps := range pss {
		out = append(out, PlanStoryResponse{ID: ps.ID, PlanID: ps.PlanID, StoryID: ps.StoryID, Order: ps.Order})
	}
	return out
}

// ---------- Release ----------

// CreateReleaseRequest 是创建发布的请求体。
type CreateReleaseRequest struct {
	ProductID int64  `json:"product_id"`
	BranchID  *int64 `json:"branch_id,omitempty"`
	Name      string `json:"name"`
	System    string `json:"system,omitempty"` // trunk|branch，默认 trunk
	Marker    bool   `json:"marker,omitempty"`
	Date      string `json:"date,omitempty"` // YYYY-MM-DD
	Desc      string `json:"desc,omitempty"`
}

// UpdateReleaseRequest 是修改发布的请求体（全字段可选）。
type UpdateReleaseRequest struct {
	Name   *string `json:"name,omitempty"`
	Date   *string `json:"date,omitempty"`
	Desc   *string `json:"desc,omitempty"`
	Mailto *string `json:"mailto,omitempty"`
	Notify *string `json:"notify,omitempty"`
}

// ReleaseTransitionRequest 是发布状态转移的请求体
// （动作集 publish/pause/terminate/play，与 plan 不同）。
type ReleaseTransitionRequest struct {
	Action string `json:"action"`
}

// ReleaseResponse 是发布的响应。
type ReleaseResponse struct {
	ID         int64  `json:"id"`
	ProductID  int64  `json:"product_id"`
	BranchID   *int64 `json:"branch_id,omitempty"`
	Name       string `json:"name"`
	System     string `json:"system"`
	Marker     bool   `json:"marker"`
	Status     string `json:"status"`
	Date       string `json:"date,omitempty"`
	ReleasedAt string `json:"released_at,omitempty"`
	StoryIDs   string `json:"story_ids,omitempty"`
	BugIDs     string `json:"bug_ids,omitempty"`
	BuildIDs   string `json:"build_ids,omitempty"`
	Mailto     string `json:"mailto,omitempty"`
	Notify     string `json:"notify,omitempty"`
	Desc       string `json:"desc,omitempty"`
	OpenedByID int64  `json:"opened_by_id"`
	OpenedAt   string `json:"opened_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ReleaseFromDomain 把领域发布映射为响应。
func ReleaseFromDomain(r *drel.Release) ReleaseResponse {
	return ReleaseResponse{
		ID:         r.ID,
		ProductID:  r.ProductID,
		BranchID:   r.BranchID,
		Name:       r.Name,
		System:     string(r.System),
		Marker:     r.Marker,
		Status:     string(r.Status),
		Date:       dateStr(r.DateMs),
		ReleasedAt: rfc3339Ptr(r.ReleasedAtMs),
		StoryIDs:   r.StoryIDs,
		BugIDs:     r.BugIDs,
		BuildIDs:   r.BuildIDs,
		Mailto:     r.Mailto,
		Notify:     r.Notify,
		Desc:       r.Desc,
		OpenedByID: r.OpenedByID,
		OpenedAt:   rfc3339Ptr(&r.OpenedAtMs),
		UpdatedAt:  rfc3339Ptr(&r.UpdatedAtMs),
	}
}

// ReleasesFromDomain 批量映射。
func ReleasesFromDomain(rs []*drel.Release) []ReleaseResponse {
	out := make([]ReleaseResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, ReleaseFromDomain(r))
	}
	return out
}

// ---------- Build ----------

// CreateBuildRequest 是创建构建的请求体。
type CreateBuildRequest struct {
	ProductID int64  `json:"product_id"`
	ProjectID *int64 `json:"project_id,omitempty"`
	Name      string `json:"name"`
	Builds    string `json:"builds"`
	System    string `json:"system,omitempty"` // trunk|branch，默认 trunk
	Date      string `json:"date,omitempty"`
	Builder   string `json:"builder,omitempty"`
	Desc      string `json:"desc,omitempty"`
}

// UpdateBuildRequest 是修改构建的请求体（全字段可选）。
type UpdateBuildRequest struct {
	Name     *string `json:"name,omitempty"`
	Builds   *string `json:"builds,omitempty"`
	Date     *string `json:"date,omitempty"`
	Builder  *string `json:"builder,omitempty"`
	SCMPath  *string `json:"scm_path,omitempty"`
	FilePath *string `json:"file_path,omitempty"`
	Desc     *string `json:"desc,omitempty"`
}

// BuildResponse 是构建的响应（无状态字段：记录型实体，DEC-06）。
type BuildResponse struct {
	ID          int64  `json:"id"`
	ProductID   int64  `json:"product_id"`
	ProjectID   *int64 `json:"project_id,omitempty"`
	Name        string `json:"name"`
	Builds      string `json:"builds"`
	System      string `json:"system"`
	SCMPath     string `json:"scm_path,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Date        string `json:"date,omitempty"`
	Builder     string `json:"builder,omitempty"`
	StoryIDs    string `json:"story_ids,omitempty"`
	BugIDs      string `json:"bug_ids,omitempty"`
	Desc        string `json:"desc,omitempty"`
	CreatedByID int64  `json:"created_by_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// BuildFromDomain 把领域构建映射为响应。
func BuildFromDomain(b *drel.Build) BuildResponse {
	return BuildResponse{
		ID:          b.ID,
		ProductID:   b.ProductID,
		ProjectID:   b.ProjectID,
		Name:        b.Name,
		Builds:      b.Builds,
		System:      string(b.System),
		SCMPath:     b.SCMPath,
		FilePath:    b.FilePath,
		Date:        dateStr(b.DateMs),
		Builder:     b.Builder,
		StoryIDs:    b.StoryIDs,
		BugIDs:      b.BugIDs,
		Desc:        b.Desc,
		CreatedByID: b.CreatedByID,
		CreatedAt:   rfc3339Ptr(&b.CreatedAtMs),
		UpdatedAt:   rfc3339Ptr(&b.UpdatedAtMs),
	}
}

// BuildsFromDomain 批量映射。
func BuildsFromDomain(bs []*drel.Build) []BuildResponse {
	out := make([]BuildResponse, 0, len(bs))
	for _, b := range bs {
		out = append(out, BuildFromDomain(b))
	}
	return out
}

// ---------- 列表信封（{data,page,links}，与 story/case 域同构） ----------

// PlanListResponse 是计划列表信封。
type PlanListResponse struct {
	Data  []PlanResponse `json:"data"`
	Page  shared.Page    `json:"page"`
	Links ListLinks      `json:"links"`
}

// PlanStoryListResponse 是计划-需求关联列表信封（无分页：计划下需求有界）。
type PlanStoryListResponse struct {
	Data []PlanStoryResponse `json:"data"`
}

// ReleaseListResponse 是发布列表信封。
type ReleaseListResponse struct {
	Data  []ReleaseResponse `json:"data"`
	Page  shared.Page       `json:"page"`
	Links ListLinks         `json:"links"`
}

// BuildListResponse 是构建列表信封。
type BuildListResponse struct {
	Data  []BuildResponse `json:"data"`
	Page  shared.Page     `json:"page"`
	Links ListLinks       `json:"links"`
}
