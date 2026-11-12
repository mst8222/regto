// Package agent 实现 AI 授权闭环的应用服务：审批决策与会话运维。
//
// 为什么单独开一个用例包（SPEC-AGENT-002 DESIGN-AGENT-002#DEC-06）：
// M3 的病根是「审批有两条写入路径」——CLI 写真源、server 写副本，
// 中间靠人记得跑 trace sync 缝合。只要还有两条路径，这条缝就会再裂开。
// 把所有决策收敛到 ApprovalService.Decide，HTTP 与 CLI 都只调它，
// 「审批是否即时生效」才从「调用方的自觉」变成「路径的属性」。
package agent

import (
	"context"
	"strings"
	"time"

	dagent "github.com/mst8222/regto/internal/domain/agent"
	"github.com/mst8222/regto/internal/domain/shared"
	dtrace "github.com/mst8222/regto/internal/domain/trace"
)

// 审批相关错误码（HTTP 层映射为 404 / 422 / 409）。
const (
	CodeApprovalNotFound     = "APPROVAL_NOT_FOUND"
	CodeApprovalNotPending   = "APPROVAL_NOT_PENDING"
	CodeAlreadyDecided       = "APPROVAL_ALREADY_DECIDED"
	CodeApprovalTraceInvalid = "INVALID_ID"
)

// approvalRepo 是本服务需要的留痕只读能力（结构化接口，不绑死具体仓储实现）。
type approvalRepo interface {
	// ApprovalState 返回审批令牌指向留痕的状态快照（是否存在 / 待审 / 已批 / 已驳）。
	ApprovalState(ctx context.Context, traceID string) (dagent.Approval, error)
	// PendingApprovals 返回待审批队列（与守卫判定同源）。
	PendingApprovals(ctx context.Context) ([]dtrace.Record, error)
}

// recordSink 是「写一条留痕」的能力，由 usecase/trace.Recorder 实现。
//
// 用 Recorder 而不是直接 Store：Recorder 保证真源先写、副本后补，
// 这正是「批准后立刻生效」的技术前提。
type recordSink interface {
	AppendRecord(ctx context.Context, rec dtrace.Record) (dtrace.Record, error)
}

// ApprovalService 处理人类对 AI 越权申请的决策。
type ApprovalService struct {
	repo approvalRepo
	sink recordSink
	env  string
	now  func() time.Time
}

// NewApprovalService 构造审批服务。env 写入留痕的 env 字段（local / ci / prod）。
func NewApprovalService(repo approvalRepo, sink recordSink, env string) *ApprovalService {
	if env == "" {
		env = "local"
	}
	return &ApprovalService{repo: repo, sink: sink, env: env, now: time.Now}
}

// Pending 返回待审批队列。
func (s *ApprovalService) Pending(ctx context.Context) ([]dtrace.Record, error) {
	return s.repo.PendingApprovals(ctx)
}

// Decide 批准或驳回一条待审批留痕。
//
// 决策本身是**追加一条新留痕**（traces 不可 UPDATE，SPEC-AGENT-001 REQ-05）：
// action=approve|reject、object_type=trace、object_id=被审批的 trace_id。
// 追加成功后副本也写好了，守卫下一次判定即可放行（REQ-08）。
//
// 幂等：已决策（存在引用它的 approve/reject）再批 → 409。
// 已知边界：并发双批存在极窄的竞态窗口（两次检查都通过），
// 结果会落两条决策记录；守卫按「存在 approve」判定，行为仍正确，
// 只是多一条审计噪声。彻底消除需要 DB 侧唯一约束（M5 硬化时评估）。
func (s *ApprovalService) Decide(ctx context.Context, traceID, by string,
	approve bool, comment string) (dtrace.Record, error) {
	traceID = strings.TrimSpace(traceID)
	if len(traceID) != 26 {
		return dtrace.Record{}, shared.New(CodeApprovalTraceInvalid,
			"trace_id 必须是 26 位 ULID", map[string]any{"trace_id": traceID})
	}
	by = strings.TrimSpace(by)
	if by == "" {
		return dtrace.Record{}, shared.New("UNAUTHENTICATED",
			"审批必须由人类主体发起", nil)
	}

	st, err := s.repo.ApprovalState(ctx, traceID)
	if err != nil {
		return dtrace.Record{}, err
	}
	if !st.Found {
		return dtrace.Record{}, shared.New(CodeApprovalNotFound,
			"trace 不存在（若刚由 CLI 写入，先执行 regtoctl trace sync）",
			map[string]any{"trace_id": traceID})
	}
	if st.Approved || st.Rejected {
		prior := "approve"
		if st.Rejected {
			prior = "reject"
		}
		return dtrace.Record{}, shared.New(CodeAlreadyDecided,
			"该留痕已有决策记录，不能重复审批",
			map[string]any{"trace_id": traceID, "prior_decision": prior})
	}
	if !st.Pending {
		return dtrace.Record{}, shared.New(CodeApprovalNotPending,
			"该留痕不是待审批状态", map[string]any{"trace_id": traceID})
	}

	action := dtrace.ActionApprove
	verb := "批准"
	if !approve {
		action = dtrace.ActionReject
		verb = "驳回"
	}
	summary := verb + "：" + st.Action + " " + traceID
	if st.ActorID != "" {
		summary = verb + "：" + st.ActorID + " 申请 " + st.Action
	}
	return s.sink.AppendRecord(ctx, dtrace.Record{
		Action:       action,
		ObjectType:   "trace",
		ObjectID:     traceID,
		ObjectRef:    "trace/" + traceID,
		ActorType:    dtrace.ActorHuman,
		ActorID:      by,
		Summary:      summary,
		Detail:       strings.TrimSpace(comment),
		Outcome:      dtrace.OutcomeSuccess,
		Related:      []dtrace.RelatedRef{{Type: "trace", ID: traceID}},
		Env:          s.env,
		OccurredAtMs: s.now().UTC().UnixMilli(),
	})
}
