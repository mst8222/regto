// Package report 提供固定报表的用例服务：校验报表 id、组装 Report 结构。
//
// 报表是只读聚合，本服务不做业务状态变更，只把持久层的查询结果包装成稳定的 Report。
// 未知 id 在「进库之前」就拦下，避免把无效请求打到 SQL 层（fail-fast）。
package report

import (
	"context"

	"github.com/mst8222/regto/internal/domain/report"
	"github.com/mst8222/regto/internal/domain/shared"
)

// CodeUnknownReport 是「报表 id 不在注册表」的错误码，HTTP 层映射为 400。
const CodeUnknownReport = "UNKNOWN_REPORT"

// Repo 是报表数据的读取端口（持久层实现；单测用桩替代）。
type Repo interface {
	Query(ctx context.Context, id report.ID, productID *int64) (*report.Report, error)
}

// Service 报表用例服务。
type Service struct {
	repo Repo
}

// NewService 构造服务。
func NewService(repo Repo) *Service {
	return &Service{repo: repo}
}

// List 返回所有已注册报表的元信息（供列表接口）。
func (s *Service) List() []report.Meta {
	return report.All()
}

// Run 执行指定报表；productID 为 nil 表示不限定产品。
// 未知 id 直接返回 UNKNOWN_REPORT，不进持久层。
func (s *Service) Run(ctx context.Context, id string, productID *int64) (*report.Report, error) {
	if !report.IsValid(id) {
		return nil, shared.New(CodeUnknownReport, "未知报表: "+id, map[string]any{"id": id})
	}
	return s.repo.Query(ctx, report.ID(id), productID)
}
