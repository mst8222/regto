package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/audit"
)

// AuditRepo 执行审计报表的只读聚合。它只查 traces，不碰业务表（DESIGN-AUDIT-001#DEC-03）。
//
// 为什么时间列在 SQL 里就转成字符串：留痕时间每次跑都不同，若让 pgx 返回
// time.Time，golden 比对与手工核对都会对不上；统一 to_char 成 UTC 文本，
// 行内不含随运行变化的值。latency_ms 同理先 ::bigint，避免 numeric 在
// JSON 里的表示形式不稳定。
type AuditRepo struct {
	pool *pgxpool.Pool
}

// NewAuditRepo 构造审计报表仓储。
func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

const tsFormat = "YYYY-MM-DD\"T\"HH24:MI:SSZ"

// Query 按 id 跑对应审计报表。id 合法性由 usecase 用 audit.IsValid 先行校验。
func (r *AuditRepo) Query(ctx context.Context, id audit.ID) (*audit.Report, error) {
	meta, _ := audit.Get(id)
	now := time.Now()

	switch id {
	case audit.Denied:
		rows, err := queryMaps(ctx, r.pool, fmt.Sprintf(`
SELECT action,
       actor_type,
       actor_id,
       count(*)                                                   AS count,
       to_char(max(occurred_at) AT TIME ZONE 'UTC', '%s')         AS last_at
FROM traces
WHERE outcome = 'blocked'
GROUP BY action, actor_type, actor_id
ORDER BY count DESC, action, actor_id
LIMIT 200`, tsFormat))
		if err != nil {
			return nil, err
		}
		return &audit.Report{ID: id, Title: meta.Title, GeneratedAt: now,
			Columns: []string{"action", "actor_type", "actor_id", "count", "last_at"},
			Rows:    rows}, nil

	case audit.ApprovalLat:
		rows, err := queryMaps(ctx, r.pool, fmt.Sprintf(`
SELECT btrim(p.trace_id)                                            AS trace_id,
       p.action                                                     AS action,
       p.actor_id                                                   AS requester,
       to_char(p.occurred_at AT TIME ZONE 'UTC', '%s')              AS requested_at,
       COALESCE(d.action, 'pending')                                AS decision,
       COALESCE(d.actor_id, '')                                     AS reviewer,
       COALESCE(to_char(d.occurred_at AT TIME ZONE 'UTC', '%s'), '') AS decided_at,
       CASE WHEN d.occurred_at IS NULL THEN NULL
            ELSE (EXTRACT(EPOCH FROM (d.occurred_at - p.occurred_at)) * 1000)::bigint
       END                                                          AS latency_ms
FROM traces p
LEFT JOIN traces d
       ON d.object_type = 'trace'
      AND btrim(d.object_id) = btrim(p.trace_id)
      AND d.action IN ('approve', 'reject')
WHERE p.needs_human_review = 1
ORDER BY p.occurred_at, p.trace_id
LIMIT 500`, tsFormat, tsFormat))
		if err != nil {
			return nil, err
		}
		return &audit.Report{ID: id, Title: meta.Title, GeneratedAt: now,
			Columns: []string{"trace_id", "action", "requester", "requested_at",
				"decision", "reviewer", "decided_at", "latency_ms"},
			Rows: rows}, nil

	case audit.TraceGrowth:
		rows, err := queryMaps(ctx, r.pool, `
SELECT to_char(occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')     AS day,
       count(*) FILTER (WHERE actor_type = 'human')              AS human,
       count(*) FILTER (WHERE actor_type = 'ai')                 AS ai,
       count(*) FILTER (WHERE actor_type = 'system')             AS system,
       count(*)                                                  AS total
FROM traces
GROUP BY 1
ORDER BY day
LIMIT 400`)
		if err != nil {
			return nil, err
		}
		return &audit.Report{ID: id, Title: meta.Title, GeneratedAt: now,
			Columns: []string{"day", "human", "ai", "system", "total"},
			Rows:    rows}, nil
	}

	return nil, fmt.Errorf("未实现的审计报表 %q", id)
}
