package zentao

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mst8222/regto/internal/domain/importpkg"
)

// ErrorRateLimit 是单表错误率闸门（REQ-05：errors/read > 1% 中止该表）。
const ErrorRateLimit = 0.01

// rowSource 是源库读取抽象（MySQL 实现 + 测试假件）。
// 只发 SELECT（NFR-01 源库只读）。
type rowSource interface {
	QueryRows(ctx context.Context, query string, args ...any) ([]map[string]any, error)
}

// mysqlSource 是 rowSource 的 MySQL 实现（禅道标准库）。
type mysqlSource struct {
	db *sql.DB
}

// QueryRows 执行查询并把结果集转为「列名（小写）→ 值」行集合。
func (m mysqlSource) QueryRows(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, 64)
	for rows.Next() {
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(map[string]any, len(cols))
		for i, c := range cols {
			r[strings.ToLower(c)] = holders[i]
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// tableStats 是单表导入统计（NFR-03 可观测）。
type tableStats struct {
	Read     int64
	Written  int64
	Skipped  int64
	Errors   int64
	LastID   int64
	Aborted  bool // 错误率闸门触发
	AbortMsg string
}

// importTable 导入一张表：游标分页 → 映射 → 幂等写入 → 记账。
// 返回的 stats 含中止原因；调用方据此决定是否中止整个 run（REQ-05）。
func (im *Importer) importTable(ctx context.Context, tm tableMap) (tableStats, error) {
	var st tableStats
	var savedWritten, savedSkipped, savedErrors int64
	cursor, err := im.loadCursor(ctx, im.runKey, tm.source)
	if err != nil {
		return st, err
	}
	sel := tm.selectSQL()
	now := time.Now().UTC()

	for {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		rows, err := im.src.QueryRows(ctx, sel, cursor, im.opts.Batch)
		if err != nil {
			return st, fmt.Errorf("读取 %s 失败: %w", tm.source, err)
		}
		if len(rows) == 0 {
			break
		}
		st.Read += int64(len(rows))

		// ---------- 映射（单行失败记 error 不中断，REQ-05） ----------
		mapped := make([]mappedPair, 0, len(rows))
		for _, row := range rows {
			id, _ := toInt64(row["id"])
			st.LastID = id
			out, err := mapRow(ctx, tm, row, im.res, rowCtx{
				table: tm.source, srcID: id, runKey: im.runKey,
			}, now)
			if err != nil {
				st.Errors++
				im.recordRowError(ctx, tm, id, row, err)
				continue
			}
			// 登记批内 overlay：同批后续行引用本行时可解析（父先于子）。
			im.res.addPending(tm.source, id)
			mapped = append(mapped, mappedPair{srcID: id, out: out})
		}

		// ---------- 幂等判定：已在本 run 映射过的源 ID 直接跳过（AC-03） ----------
		ids := make([]int64, 0, len(mapped))
		for _, p := range mapped {
			ids = append(ids, p.srcID)
		}
		already, err := im.mappedIDs(ctx, tm.source, ids)
		if err != nil {
			return st, err
		}
		fresh := make([]map[string]any, 0, len(mapped))
		var freshIDs []int64
		for _, p := range mapped {
			if already[p.srcID] {
				st.Skipped++
				continue
			}
			fresh = append(fresh, p.out)
			freshIDs = append(freshIDs, p.srcID)
		}

		// ---------- 批量写入（ON CONFLICT (id) DO NOTHING 双保险） ----------
		if len(fresh) > 0 {
			inserted, err := im.insertRows(ctx, tm.target, fresh)
			if err != nil {
				return st, fmt.Errorf("写入 %s 失败: %w", tm.target, err)
			}
			st.Written += inserted
			st.Skipped += int64(len(fresh)) - inserted
			if inserted > 0 {
				im.recordIDMap(ctx, tm.source, freshIDs)
			}
		}

		// ---------- 进度落库（崩溃后可 resume，REQ-04） ----------
		if err := im.saveCursor(ctx, tm, batchCounts{
			read: int64(len(rows)), lastID: st.LastID,
			written: st.Written - savedWritten, skipped: st.Skipped - savedSkipped,
			errors: st.Errors - savedErrors,
		}); err != nil {
			return st, err
		}
		savedWritten, savedSkipped, savedErrors = st.Written, st.Skipped, st.Errors

		// ---------- 错误率闸门（REQ-05：>1% 中止该表） ----------
		if st.Read > 0 && float64(st.Errors)/float64(st.Read) > ErrorRateLimit {
			st.Aborted = true
			st.AbortMsg = fmt.Sprintf("错误率超限：%d/%d = %.2f%% > %.0f%%",
				st.Errors, st.Read, 100*float64(st.Errors)/float64(st.Read), 100*ErrorRateLimit)
			return st, nil
		}
		cursor = st.LastID
	}
	return st, nil
}

// qt 给 PostgreSQL 标识符加双引号（desc/order 等保留字必须包裹）。
func qt(id string) string { return `"` + id + `"` }

// insertRows 多行 INSERT（ON CONFLICT (id) DO NOTHING），返回实际插入行数。
func (im *Importer) insertRows(ctx context.Context, table string, rows []map[string]any) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	// 列集合取并集（同表映射固定，实为同一组）。
	colSet := map[string]bool{}
	for _, r := range rows {
		for c := range r {
			colSet[c] = true
		}
	}
	cols := make([]string, 0, len(colSet))
	for c := range colSet {
		cols = append(cols, c)
	}
	sort.Strings(cols) // 稳定顺序：SQL 可预期、测试可断言

	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(table)
	sb.WriteString(" (")
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = qt(c)
	}
	sb.WriteString(strings.Join(quoted, ", "))
	sb.WriteString(") VALUES ")
	args := make([]any, 0, len(rows)*len(cols))
	for i, r := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for j, c := range cols {
			if j > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "$%d", len(args)+1)
			args = append(args, r[c])
		}
		sb.WriteString(")")
	}
	sb.WriteString(` ON CONFLICT ("id") DO NOTHING`)

	ct, err := im.pool.Exec(ctx, sb.String(), args...)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// recordRowError 记单行失败（REQ-05：含行快照 JSONB）。
func (im *Importer) recordRowError(ctx context.Context, tm tableMap, srcID int64, row map[string]any, cause error) {
	code := "ROW_ERROR"
	var re *rowError
	if errors.As(cause, &re) {
		code = re.code
	}
	payload := map[string]any{}
	for k, v := range row {
		payload[k] = normalizeValue(v)
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		blob = []byte("{}")
	}
	_, _ = im.pool.Exec(ctx, `
		INSERT INTO regto_import.import_errors
		  (run_key, phase, table_name, source_id, error_code, error_message, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		im.runKey, string(tmPhase(tm)), tm.source, srcID, code, cause.Error(), blob)
}

// tmPhase 返回表所属阶段。
func tmPhase(tm tableMap) importpkg.Phase {
	for _, t := range importpkg.Tables {
		if t.Source == tm.source {
			return t.Phase
		}
	}
	return importpkg.PhaseS2
}

// ensurePlaceholders 保证占位用户存在（REQ-02：首次导入自动创建 system）。
func (im *Importer) ensurePlaceholders(ctx context.Context) error {
	_, err := im.pool.Exec(ctx, `
		INSERT INTO users (id, account, password_hash, realname, disabled)
		VALUES (0, 'system', $1, 'system', true)
		ON CONFLICT (id) DO NOTHING`,
		importpkg.NewPasswordPlaceholder("system"))
	return err
}
