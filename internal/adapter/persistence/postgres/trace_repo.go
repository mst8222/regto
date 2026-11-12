package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/agent"
	"github.com/mst8222/regto/internal/domain/trace"
)

// TraceRepo 实现 trace.Repository（PostgreSQL）。
// 只做批量 INSERT：本包不提供任何 UPDATE/DELETE traces 的方法，
// 与库级权限（regto_trace 仅 INSERT/SELECT）共同构成物理防篡改（DEC-04）。
type TraceRepo struct {
	pool *pgxpool.Pool
}

// NewTraceRepo 构造留痕仓储。
func NewTraceRepo(pool *pgxpool.Pool) *TraceRepo { return &TraceRepo{pool: pool} }

// traceColumns 是插入列清单（顺序与 traceValues 一致）。
const traceColumns = `seq, trace_id, schema_version, prev_hash, hash, occurred_at, recorded_at,
	duration_ms, action, outcome, actor_type, actor_id, actor_model, actor_version,
	actor_session_id, object_type, object_id, object_ref, related, from_state, to_state,
	summary, detail, evidence, prompt_digest, confidence, needs_human_review, review_status,
	reviewed_by, reviewed_at, repo, commit_sha, pr, env, tags`

// traceColumnCount 是每行的列数（用于生成 $n 占位符）。
const traceColumnCount = 35

// InsertBatch 批量写入留痕；按 trace_id 幂等（DEC-02）。
func (r *TraceRepo) InsertBatch(ctx context.Context, recs []trace.Record) (int, int, error) {
	if len(recs) == 0 {
		return 0, 0, nil
	}
	var (
		placeholders []string
		args         []any
	)
	for i, rec := range recs {
		base := i * traceColumnCount
		var row strings.Builder
		for c := 0; c < traceColumnCount; c++ {
			if c > 0 {
				row.WriteByte(',')
			}
			fmt.Fprintf(&row, "$%d", base+c+1)
		}
		placeholders = append(placeholders, "("+row.String()+")")
		args = append(args, traceValues(rec)...)
	}

	sql := `INSERT INTO traces (` + traceColumns + `) VALUES ` +
		strings.Join(placeholders, ",") + ` ON CONFLICT (trace_id) DO NOTHING`
	ct, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("批量写入 traces 失败: %w", err)
	}
	imported := int(ct.RowsAffected())
	return imported, len(recs) - imported, nil
}

// Head 返回库内当前链尾（seq 最大记录的 seq 与 hash）；空库返回 (0, "", nil)。
// 分片文件导入前由 usecase 调用，用于 VerifyShardLink 衔接校验（REQ-05）。
func (r *TraceRepo) Head(ctx context.Context) (int64, string, error) {
	var (
		seq  int64
		hash string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT seq, hash FROM traces ORDER BY seq DESC LIMIT 1`).Scan(&seq, &hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", nil
		}
		return 0, "", fmt.Errorf("查询 traces 链尾失败: %w", err)
	}
	return seq, hash, nil
}

// ApprovalState 返回审批令牌指向留痕的状态快照（SPEC-AGENT-001 REQ-06 的四要素）。
//
// traces 是只读副本（不可 UPDATE），故「已批准 / 已驳回」表现为
// 存在一条 action=approve|reject 且 object_id 指向该 trace 的**新记录**（DEC-04）。
func (r *TraceRepo) ApprovalState(ctx context.Context, traceID string) (agent.Approval, error) {
	const q = `SELECT t.review_status, t.action, t.actor_id,
	                   EXISTS(SELECT 1 FROM traces a
	                           WHERE a.action = 'approve' AND a.object_type = 'trace'
	                             AND a.object_id = t.trace_id) AS approved,
	                   EXISTS(SELECT 1 FROM traces x
	                           WHERE x.action = 'reject' AND x.object_type = 'trace'
	                             AND x.object_id = t.trace_id) AS rejected
	              FROM traces t WHERE t.trace_id = $1`
	var (
		status   pgtype.Text
		action   string
		actorID  string
		approved bool
		rejected bool
	)
	err := r.pool.QueryRow(ctx, q, traceID).Scan(&status, &action, &actorID, &approved, &rejected)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agent.Approval{Found: false}, nil
		}
		return agent.Approval{}, fmt.Errorf("查询审批状态失败: %w", err)
	}
	return agent.Approval{
		Found:    true,
		Pending:  status.Valid && status.String == reviewStatusPending,
		Action:   action,
		ActorID:  actorID,
		Approved: approved,
		Rejected: rejected,
	}, nil
}

// reviewStatusPending 是待审批状态值（与 SPEC-AGENT-001 REQ-03 一致）。
const reviewStatusPending = "pending"

// FindPending 查找同一 Agent + 动作 + 对象引用下尚未被审批/驳回的 pending 记录。
//
// 用途：NFR-02 队列不堆积——Agent 反复重试同一越权动作时复用已有记录，不重复入队。
// 返回空串表示没有可复用的记录。
//
// 排除逻辑是**组级**的：审批/驳回结算的是「这个 Agent 想做这件事」这一意图，
// 而不是某一条具体记录。并发重试可能产生多条同键 pending，
// 人类驳回其中一条后，同组的其余重复项必须一并移出队列，否则队列会漏。
func (r *TraceRepo) FindPending(ctx context.Context, actorID, action, objectRef string) (string, error) {
	const q = `SELECT trace_id FROM traces t
	            WHERE t.actor_id = $1 AND t.action = $2 AND t.object_ref = $3
	              AND t.review_status = 'pending'
	              AND NOT EXISTS (
	                    SELECT 1 FROM traces d
	                     WHERE d.action IN ('approve', 'reject')
	                       AND d.object_type = 'trace' AND d.object_id = t.trace_id)
	              AND NOT EXISTS (
	                    SELECT 1 FROM traces s
	                     WHERE s.actor_id = t.actor_id AND s.action = t.action
	                       AND s.object_ref = t.object_ref
	                       AND EXISTS (
	                             SELECT 1 FROM traces d2
	                              WHERE d2.action IN ('approve', 'reject')
	                                AND d2.object_type = 'trace'
	                                AND d2.object_id = s.trace_id))
	            ORDER BY t.seq LIMIT 1`
	var traceID string
	err := r.pool.QueryRow(ctx, q, actorID, action, objectRef).Scan(&traceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("查询待审批队列失败: %w", err)
	}
	return traceID, nil
}

// traceValues 把领域记录展开为一行绑定参数（顺序与 traceColumns 一致）。
func traceValues(r trace.Record) []any {
	return []any{
		r.Seq, r.TraceID, r.SchemaVersion, r.PrevHash, r.Hash,
		ms(r.OccurredAtMs), ms(r.RecordedAtMs), nullInt64(r.DurationMs),
		r.Action, nullString(string(r.Outcome)),
		r.ActorType, r.ActorID, nullString(r.ActorModel), nullString(r.ActorVersion),
		nullString(r.ActorSessionID),
		r.ObjectType, r.ObjectID, nullString(r.ObjectRef),
		jsonOrNil(r.Related), nullString(r.FromState), nullString(r.ToState),
		r.Summary, nullString(r.Detail), jsonOrNil(r.Evidence), nullString(r.PromptDigest),
		nullInt(r.Confidence), boolToSmallint(r.NeedsHumanReview),
		nullString(r.ReviewStatus), nullString(r.ReviewedBy),
		msOrNilTrace(r.ReviewedAtMs),
		nullString(r.Repo), nullString(r.CommitSHA), nullString(r.PR), nullString(r.Env),
		jsonOrNil(r.Tags),
	}
}

// jsonOrNil 把可空结构序列化为 JSON；空值写 NULL。
func jsonOrNil(v any) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []trace.RelatedRef:
		if len(t) == 0 {
			return nil
		}
	case []trace.Evidence:
		if len(t) == 0 {
			return nil
		}
	case []string:
		if len(t) == 0 {
			return nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// nullInt64 把 0 视为 NULL（可选数值字段）。
func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// nullInt 把 0 视为 NULL（可选数值字段）。
func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// boolToSmallint 把布尔转为 0/1（needs_human_review）。
func boolToSmallint(v bool) any {
	if v {
		return 1
	}
	return 0
}

// msOrNilTrace 把毫秒转为时间参数；0 写 NULL。
func msOrNilTrace(v int64) any {
	if v == 0 {
		return nil
	}
	return ms(v)
}

// PendingApprovals 返回待审批队列（SPEC-AGENT-002 REQ-09）。
//
// 定义与守卫判定严格同源：review_status=pending 且**不存在**引用它的
// approve/reject 记录。不允许在 Go 侧二次过滤——两套真相就是两套 bug。
func (r *TraceRepo) PendingApprovals(ctx context.Context) ([]trace.Record, error) {
	const q = `SELECT t.seq, t.trace_id, t.action, t.actor_type, t.actor_id,
	                  t.object_type, t.object_id, COALESCE(t.object_ref,''),
	                  t.summary, t.occurred_at, COALESCE(t.review_status,'')
	             FROM traces t
	            WHERE t.review_status = 'pending'
	              AND NOT EXISTS (
	                    SELECT 1 FROM traces d
	                     WHERE d.action IN ('approve', 'reject')
	                       AND d.object_type = 'trace' AND d.object_id = t.trace_id)
	            ORDER BY t.seq`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("查询待审批队列失败: %w", err)
	}
	defer rows.Close()

	var out []trace.Record
	for rows.Next() {
		var (
			rec      trace.Record
			occurred time.Time
		)
		if err := rows.Scan(&rec.Seq, &rec.TraceID, &rec.Action, &rec.ActorType, &rec.ActorID,
			&rec.ObjectType, &rec.ObjectID, &rec.ObjectRef, &rec.Summary,
			&occurred, &rec.ReviewStatus); err != nil {
			return nil, fmt.Errorf("扫描待审批队列失败: %w", err)
		}
		rec.OccurredAtMs = occurred.UTC().UnixMilli()
		out = append(out, rec)
	}
	return out, rows.Err()
}
