package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/spec"
)

// SpecRepo 实现 spec.Repository（PostgreSQL）。
// 它只提供「文件派生对象 → DB」的写入路径，没有任何从 DB 回写文件的能力
// （单向性由接口形状保证，DESIGN-SPEC-001 DEC-04）。
type SpecRepo struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

// NewSpecRepo 构造仓储。
func NewSpecRepo(pool *pgxpool.Pool) *SpecRepo { return &SpecRepo{pool: pool} }

// db 返回当前执行器：事务内用 tx，否则用 pool。
func (r *SpecRepo) db() dbExecutor {
	if r.tx != nil {
		return r.tx
	}
	return r.pool
}

// WithTx 在单个事务内执行 fn（NFR-01：任一步失败整体回滚）。
func (r *SpecRepo) WithTx(ctx context.Context, fn func(tx spec.Repository) error) error {
	if r.tx != nil {
		return fn(r) // 已在事务中：直接复用，避免嵌套事务
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(&SpecRepo{pool: r.pool, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// UpsertDocument 写入文档；内容哈希变化返回 changed=true（DEC-01）。
func (r *SpecRepo) UpsertDocument(ctx context.Context, d *spec.Document) (bool, error) {
	var oldHash string
	err := r.db().QueryRow(ctx,
		`SELECT content_sha256 FROM spec_documents WHERE file_path = $1`, d.FilePath,
	).Scan(&oldHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("查询文档哈希失败: %w", err)
	}
	changed := oldHash != d.ContentHash

	_, err = r.db().Exec(ctx, `
		INSERT INTO spec_documents (doc_id, doc_type, domain, title, status, version,
			owner, file_path, content_sha256, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now(), now())
		ON CONFLICT (file_path) DO UPDATE SET
			doc_id = EXCLUDED.doc_id, doc_type = EXCLUDED.doc_type, domain = EXCLUDED.domain,
			title = EXCLUDED.title, status = EXCLUDED.status, version = EXCLUDED.version,
			owner = EXCLUDED.owner, content_sha256 = EXCLUDED.content_sha256,
			updated_at = now(), deleted_at = NULL`,
		d.DocID, d.DocType, d.Domain, d.Title, d.Status, d.Version,
		nullString(d.Owner), d.FilePath, d.ContentHash)
	if err != nil {
		return false, fmt.Errorf("写入 spec_documents 失败: %w", err)
	}
	return changed, nil
}

// ReplaceItems 用文件当前条目整体替换（文件是唯一真源，不做增量合并）。
//
// 替换用「软删 + 复活」而非 DELETE：应用账号没有 DELETE 权限，
// 且审计型系统要求删除可追溯（000011 迁移给表加了 deleted_at）。
// 步骤：先把该文档全部条目软删，再逐条 UPSERT（重现的条目自动复活）。
func (r *SpecRepo) ReplaceItems(ctx context.Context, docID string, items []spec.Item) error {
	if _, err := r.db().Exec(ctx, `
		UPDATE spec_items SET deleted_at = now(), updated_at = now()
		 WHERE doc_id = $1 AND deleted_at IS NULL`, docID); err != nil {
		return fmt.Errorf("软删 spec_items 失败: %w", err)
	}
	for _, it := range items {
		_, err := r.db().Exec(ctx, `
			INSERT INTO spec_items (doc_id, doc_version, item_key, item_type, ref, title, body, line_no)
			VALUES ($1,1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (ref) WHERE deleted_at IS NULL DO UPDATE SET
				title = EXCLUDED.title, body = EXCLUDED.body, line_no = EXCLUDED.line_no,
				updated_at = now(), deleted_at = NULL`,
			docID, it.ItemKey, it.ItemType, docID+"#"+it.ItemKey, it.Title, it.Body, it.LineNo)
		if err != nil {
			return fmt.Errorf("写入 spec_items(%s) 失败: %w", it.ItemKey, err)
		}
	}
	return nil
}

// UpsertCase 写入用例并返回主键。
func (r *SpecRepo) UpsertCase(ctx context.Context, c *spec.Case) (int64, error) {
	var id int64
	err := r.db().QueryRow(ctx, `
		INSERT INTO cases (title, source_ref, source_hash, suite, automation, priority,
			spec_doc_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7, now(), now())
		-- 冲突目标必须与 000012 的部分唯一索引谓词一致（source_ref 非空且未删），
		-- 否则 PostgreSQL 报「no unique or exclusion constraint matching」（DESIGN-CASE-001 R1）。
		ON CONFLICT (source_ref) WHERE source_ref IS NOT NULL AND deleted_at IS NULL DO UPDATE SET
			title = EXCLUDED.title, source_hash = EXCLUDED.source_hash,
			suite = EXCLUDED.suite, automation = EXCLUDED.automation,
			priority = EXCLUDED.priority, updated_at = now(), deleted_at = NULL
		RETURNING id`,
		c.Title, c.SourceRef, c.SourceHash, nullString(c.Suite),
		nullString(c.Automation), nullString(c.Priority), nullString(c.SpecDocID),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("写入 cases(%s) 失败: %w", c.SourceRef, err)
	}
	return id, nil
}

// ReplaceSteps 用文件当前步骤整体替换（与 ReplaceItems 同为软删 + 复活语义）。
func (r *SpecRepo) ReplaceSteps(ctx context.Context, caseID int64, steps []spec.Step) error {
	if _, err := r.db().Exec(ctx, `
		UPDATE case_steps SET deleted_at = now(), updated_at = now()
		 WHERE case_id = $1 AND deleted_at IS NULL`, caseID); err != nil {
		return fmt.Errorf("软删 case_steps 失败: %w", err)
	}
	for _, st := range steps {
		var checks any
		if st.Checks != "" {
			checks = st.Checks
		}
		_, err := r.db().Exec(ctx, `
			INSERT INTO case_steps (case_id, seq, name, checks)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (case_id, seq) WHERE deleted_at IS NULL DO UPDATE SET
				name = EXCLUDED.name, checks = EXCLUDED.checks,
				updated_at = now(), deleted_at = NULL`,
			caseID, st.Seq, st.Name, checks)
		if err != nil {
			return fmt.Errorf("写入 case_steps(seq=%d) 失败: %w", st.Seq, err)
		}
	}
	return nil
}

// MarkDeletedExcept 把本次未扫到的文档与用例标为软删（文件删除 → 索引失效）。
func (r *SpecRepo) MarkDeletedExcept(ctx context.Context, filePaths, sourceRefs []string) error {
	if _, err := r.db().Exec(ctx, `
		UPDATE spec_documents SET deleted_at = now()
		 WHERE deleted_at IS NULL AND file_path <> ALL($1)`, filePaths); err != nil {
		return fmt.Errorf("软删失效文档失败: %w", err)
	}
	if len(sourceRefs) == 0 {
		return nil
	}
	if _, err := r.db().Exec(ctx, `
		UPDATE cases SET deleted_at = now()
		 WHERE deleted_at IS NULL AND source_ref <> ALL($1)`, sourceRefs); err != nil {
		return fmt.Errorf("软删失效用例失败: %w", err)
	}
	return nil
}

// AddLink 写入追溯链接；重复链接由唯一键忽略（DEC-03）。
func (r *SpecRepo) AddLink(ctx context.Context, l spec.Link) error {
	_, err := r.db().Exec(ctx, `
		INSERT INTO trace_links (source_type, source_ref, link_type, target_type, target_ref,
			strength, origin)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (source_type, source_ref, link_type, target_type, target_ref) DO NOTHING`,
		l.SourceType, l.SourceRef, l.LinkType, l.TargetType, l.TargetRef, l.Strength, l.Origin)
	if err != nil {
		return fmt.Errorf("写入 trace_links 失败: %w", err)
	}
	return nil
}

// dbExecutor 抽象 pool 与 tx 的公共操作，便于事务内外复用同一套 SQL。
type dbExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
