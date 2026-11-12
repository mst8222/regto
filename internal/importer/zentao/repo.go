package zentao

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/mst8222/regto/internal/domain/importpkg"
)

// findTableMap 按源表名查映射。
func findTableMap(source string) (tableMap, bool) {
	for _, tm := range tableMaps {
		if tm.source == source {
			return tm, true
		}
	}
	return tableMap{}, false
}

// loadCursor 返回该表在本 run 的断点；表未开始（run 指针在别的表）返回 0。
// 顺序导入语义：import_runs 的 (table_name, cursor_last_id) 始终指向当前表。
func (im *Importer) loadCursor(ctx context.Context, runKey, sourceTable string) (int64, error) {
	var tbl string
	var cursor int64
	err := im.pool.QueryRow(ctx,
		`SELECT table_name, cursor_last_id FROM regto_import.import_runs WHERE run_key = $1`,
		runKey).Scan(&tbl, &cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取断点失败: %w", err)
	}
	if tbl != sourceTable {
		return 0, nil // 该表尚未开始（或已完成）
	}
	return cursor, nil
}

// batchCounts 是一批的增量统计（run 总数按增量累加）。
type batchCounts struct {
	read, written, skipped, errors int64
	lastID                         int64
}

// saveCursor 把批次增量与断点落库（REQ-04：崩溃后可 resume）。
func (im *Importer) saveCursor(ctx context.Context, tm tableMap, c batchCounts) error {
	_, err := im.pool.Exec(ctx, `
		INSERT INTO regto_import.import_runs
		  (run_key, phase, table_name, cursor_last_id, status,
		   total_read, total_written, total_skipped, total_errors, updated_at)
		VALUES ($1, $2, $3, $4, 'running', $5, $6, $7, $8, now())
		ON CONFLICT (run_key) DO UPDATE SET
		  phase = EXCLUDED.phase,
		  table_name = EXCLUDED.table_name,
		  cursor_last_id = EXCLUDED.cursor_last_id,
		  total_read = regto_import.import_runs.total_read + EXCLUDED.total_read,
		  total_written = regto_import.import_runs.total_written + EXCLUDED.total_written,
		  total_skipped = regto_import.import_runs.total_skipped + EXCLUDED.total_skipped,
		  total_errors = regto_import.import_runs.total_errors + EXCLUDED.total_errors,
		  updated_at = now()`,
		im.runKey, string(tmPhase(tm)), tm.source, c.lastID,
		c.read, c.written, c.skipped, c.errors)
	if err != nil {
		return fmt.Errorf("保存断点失败: %w", err)
	}
	return nil
}

// advanceRun 把 run 指针推进到下一张表（cursor 归零）。
func (im *Importer) advanceRun(ctx context.Context, next importpkg.TableSpec) error {
	_, err := im.pool.Exec(ctx, `
		UPDATE regto_import.import_runs
		   SET phase = $2, table_name = $3, cursor_last_id = 0, updated_at = now()
		 WHERE run_key = $1`,
		im.runKey, string(next.Phase), next.Source)
	return err
}

// finishRun 标记 run 终态（done / failed）。
func (im *Importer) finishRun(ctx context.Context, status, msg string) error {
	_, err := im.pool.Exec(ctx, `
		UPDATE regto_import.import_runs
		   SET status = $2, finished_at = now(), updated_at = now()
		 WHERE run_key = $1`,
		im.runKey, status)
	if err != nil {
		return err
	}
	if msg != "" {
		fmt.Printf("  run %s: %s\n", status, msg)
	}
	return nil
}

// mappedIDs 返回本 run 已映射的源 ID 集合（AC-03 幂等判定）。
func (im *Importer) mappedIDs(ctx context.Context, sourceTable string, ids []int64) (map[int64]bool, error) {
	if len(ids) == 0 {
		return map[int64]bool{}, nil
	}
	rows, err := im.pool.Query(ctx, `
		SELECT source_id FROM regto_import.import_id_map
		 WHERE run_key = $1 AND table_name = $2 AND source_id = ANY($3)`,
		im.runKey, sourceTable, ids)
	if err != nil {
		return nil, fmt.Errorf("查询幂等映射失败: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// recordIDMap 批量记录源 ID → 目标 ID（ID 保留语义：两者相等）。
func (im *Importer) recordIDMap(ctx context.Context, sourceTable string, ids []int64) {
	if len(ids) == 0 {
		return
	}
	_, err := im.pool.Exec(ctx, `
		INSERT INTO regto_import.import_id_map (run_key, table_name, source_id, target_id)
		SELECT $1, $2, u.source_id, u.source_id FROM unnest($3::bigint[]) AS u(source_id)
		ON CONFLICT (run_key, table_name, source_id) DO NOTHING`,
		im.runKey, sourceTable, ids)
	if err != nil {
		fmt.Printf("  [warn] 记录 id_map 失败（%s）：%v\n", sourceTable, err)
	}
}

// buildResolver 构造映射期依赖：外键查 id_map（批内 overlay + 跨 run 回退
// 目标表存在性），account 查 users（缺失回退 system 并记 MISSING_ACCOUNT，REQ-02）。
//
// 批内 overlay 解决「整批先映射后插入」的前向依赖：父行（更低源 ID）在
// 同批先映射成功即登记，子行随后即可解析（zentao 父先于子创建，ID 有序）。
func (im *Importer) buildResolver() resolver {
	var mu sync.Mutex
	acctCache := map[string]int64{}
	pending := map[string]int64{}
	return resolver{
		addPending: func(srcTable string, srcID int64) {
			mu.Lock()
			pending[srcTable+"#"+fmt.Sprint(srcID)] = srcID // ID 保留语义
			mu.Unlock()
		},
		fk: func(ctx context.Context, srcTable string, srcID int64) (int64, bool, error) {
			key := srcTable + "#" + fmt.Sprint(srcID)
			mu.Lock()
			overlaid, ok := pending[key]
			mu.Unlock()
			if ok {
				return overlaid, true, nil
			}
			var target int64
			err := im.pool.QueryRow(ctx, `
				SELECT target_id FROM regto_import.import_id_map
				 WHERE run_key = $1 AND table_name = $2 AND source_id = $3`,
				im.runKey, srcTable, srcID).Scan(&target)
			if err == nil {
				return target, true, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return 0, false, err
			}
			// 回退：ID 保留语义下目标表已有该行（往次 run 或手工导入），
			// 视为已导入，直接采用源 ID。
			tm, ok := findTableMap(srcTable)
			if !ok {
				return 0, false, nil
			}
			var dummy int64
			err = im.pool.QueryRow(ctx,
				fmt.Sprintf("SELECT id FROM %s WHERE id = $1", tm.target), srcID).Scan(&dummy)
			if err == nil {
				return srcID, true, nil
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, false, nil
			}
			return 0, false, err
		},
		account: func(ctx context.Context, rc rowCtx, name string) (int64, error) {
			mu.Lock()
			if id, ok := acctCache[name]; ok {
				mu.Unlock()
				return id, nil
			}
			mu.Unlock()

			var id int64
			err := im.pool.QueryRow(ctx,
				`SELECT id FROM users WHERE account = $1 AND deleted_at IS NULL`, name).Scan(&id)
			if err == nil {
				mu.Lock()
				acctCache[name] = id
				mu.Unlock()
				return id, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return 0, err
			}
			// MISSING_ACCOUNT：回退占位用户 system（REQ-02）。
			_, _ = im.pool.Exec(ctx, `
				INSERT INTO regto_import.import_errors
				  (run_key, phase, table_name, source_id, error_code, error_message)
				VALUES ($1, $2, $3, $4, 'MISSING_ACCOUNT', $5)`,
				im.runKey, string(tmPhaseBySource(rc.table)), rc.table, rc.srcID,
				fmt.Sprintf("account %q 不存在，回退 system", name))
			mu.Lock()
			acctCache[name] = systemUserID
			mu.Unlock()
			return systemUserID, nil
		},
	}
}

// tmPhaseBySource 按源表名查阶段。
func tmPhaseBySource(source string) importpkg.Phase {
	for _, t := range importpkg.Tables {
		if t.Source == source {
			return t.Phase
		}
	}
	return importpkg.PhaseS2
}

// systemUserID 是占位用户 ID（REQ-02）。
const systemUserID = 0

// normalizeAccount 归一 account（去空格；zentao account 无大小写区分约定）。
func normalizeAccount(s string) string { return strings.TrimSpace(s) }

// mappedPair 是一行映射结果（源 ID + 目标列值）。
type mappedPair struct {
	srcID int64
	out   map[string]any
}
