package trace

import (
	"context"
	"log/slog"
	"sync"

	dtrace "github.com/mst8222/regto/internal/domain/trace"
)

// Recorder 是守卫的留痕器：一次写入，两处落地。
//
// 为什么必须双写（SPEC-AGENT-001 REQ-03 / NFR-02）：
//   - 真源是 traces/*.jsonl（AGENTS.md 铁律 2），记录必须落在这里才不可抵赖；
//   - 守卫自己的判定却要读 DB 副本（截获重复入队、校验审批令牌都靠 SQL 查询）。
//     只写真源 → 本进程刚写的 pending 在库里查不到：NFR-02 的「不重复入队」失效
//     （Agent 每次重试都新刷一条），人类 approve 后也永远带不动令牌。
//     只写副本 → 留痕脱离真源，等于把防篡改的根交给了数据库。
//
// 顺序不可颠倒：
//  1. 先 Append 到 jsonl。失败即返回错误 —— 守卫据此 503 拒绝请求，
//     绝不出现「越权但没留痕」（DEC-03）。
//  2. 再 InsertBatch 进副本。失败只告警、不影响返回值：记录确实已经存在
//     （在真源里），只是暂时查不到；下一次 `regtoctl trace sync` 会补齐。
//     代价是这段时间内同一动作可能重复入队，这是可接受的降级。
//
// 并发：Store.Append 要先读链尾再追加，两个并发请求会拿到同一个 seq，
// 造成链上 seq 重复、sync 时整链校验失败。这里用进程内 mutex 串行化。
// 与 CLI 的跨进程竞争仍是已知缺口（M4 引入文件锁，届时同步收口）。
type Recorder struct {
	store *Store
	repo  dtrace.Repository
	log   *slog.Logger
	mu    sync.Mutex
}

// NewRecorder 构造留痕器。repo 为 nil 时退化为「只写真源」。
func NewRecorder(store *Store, repo dtrace.Repository, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{store: store, repo: repo, log: logger}
}

// Append 写入一条留痕并返回其 trace_id（签名对齐 httputil.Tracer）。
func (r *Recorder) Append(ctx context.Context, rec dtrace.Record) (string, error) {
	saved, err := r.AppendRecord(ctx, rec)
	if err != nil {
		return "", err
	}
	return saved.TraceID, nil
}

// AppendRecord 与 Append 相同，但返回落盘后的完整记录（含 seq / hash / recorded_at）。
//
// 为什么需要这个变体（SPEC-AGENT-002 REQ-08）：审批接口要把「新决策记录的 seq」
// 回显给调用方，否则人类批完只知道自己批了，不知道凭据落在链上哪一格。
// 语义与 Append 完全一致（真源先写、副本后补），只是多返回一点信息。
func (r *Recorder) AppendRecord(ctx context.Context, rec dtrace.Record) (dtrace.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	saved, err := r.store.Append(rec)
	if err != nil {
		return saved, err
	}
	if r.repo == nil {
		return saved, nil
	}
	if _, _, ierr := r.repo.InsertBatch(ctx, []dtrace.Record{saved}); ierr != nil {
		r.log.WarnContext(ctx, "留痕副本写入失败：真源已落盘，执行 trace sync 可补齐",
			slog.String("trace_id", saved.TraceID),
			slog.Int64("seq", saved.Seq),
			slog.String("error", ierr.Error()))
	}
	return saved, nil
}
