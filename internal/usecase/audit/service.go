// Package audit 提供审计报表的用例编排（SPEC-AUDIT-001）。
package audit

import (
	"context"

	"github.com/mst8222/regto/internal/domain/audit"
	"github.com/mst8222/regto/internal/domain/shared"
)

// CodeUnknownAuditReport 是未知审计报表 id 的业务码。
//
// 沿用报表域的 UNKNOWN_REPORT（problem.go 已映射 400）：对调用方来说
// 「报表 id 写错」与「审计报表 id 写错」是同一类错误，多一个码就多一处
// 要记住的分支，而 400 的语义已经足够。
const CodeUnknownAuditReport = "UNKNOWN_REPORT"

// Repo 是审计报表的只读查询口。
type Repo interface {
	Query(ctx context.Context, id audit.ID) (*audit.Report, error)
}

// Service 编排审计报表查询。
type Service struct {
	repo Repo
}

// NewService 构造服务。
func NewService(repo Repo) *Service {
	return &Service{repo: repo}
}

// List 返回可用审计报表清单。
func (s *Service) List() []audit.Meta {
	return audit.All()
}

// Run 执行单张审计报表。id 非法时返回 400（非 422——格式问题而非业务规则）。
func (s *Service) Run(ctx context.Context, id string) (*audit.Report, error) {
	if !audit.IsValid(id) {
		return nil, shared.New(CodeUnknownAuditReport,
			"未知审计报表："+id, map[string]any{"id": id})
	}
	return s.repo.Query(ctx, audit.ID(id))
}
