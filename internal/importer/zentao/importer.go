package zentao

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/importpkg"
)

// Source 是源库读取抽象（MySQL 实现 + 测试假件）。
// 只发 SELECT（NFR-01 源库只读）。
type Source interface {
	QueryRows(ctx context.Context, query string, args ...any) ([]map[string]any, error)
}

// MySQLSource 返回 Source 的 MySQL 实现（禅道标准库）。
func MySQLSource(db *sql.DB) Source { return mysqlSource{db: db} }

// Options 是导入参数（CLI 参数的领域化表达）。
type Options struct {
	Batch   int    // 分批大小（NFR-02，默认 500）
	WithS3  bool   // 是否导入 S3 关联流水（REQ-07，默认跳过）
	DataDir string // 源库附件目录（校验 #10 用；空则该项 WARN）
}

// ErrErrorRate 表示错误率闸门触发（REQ-05；CLI 映射为退出码 4）。
var ErrErrorRate = errors.New("错误率超限")

// Importer 是导入器编排主体（CLI 只负责参数解析与退出码）。
type Importer struct {
	src     Source
	pool    *pgxpool.Pool
	opts    Options
	runKey  string
	dataDir string // 源库附件目录（校验 #10 用；空则 WARN）
	res     resolver
}

// New 装配导入器。
func New(src Source, pool *pgxpool.Pool, opts Options) *Importer {
	if opts.Batch <= 0 {
		opts.Batch = 500
	}
	im := &Importer{src: src, pool: pool, opts: opts, dataDir: opts.DataDir}
	im.res = im.buildResolver()
	return im
}

// Run 执行三阶段导入（REQ-01/04/05）。
// 返回 error 表示整个 run 失败（连接/写入/闸门）；单行错误已入账不返回。
func (im *Importer) Run(ctx context.Context, runKey string) error {
	im.runKey = runKey

	if err := im.ensurePlaceholders(ctx); err != nil {
		return err
	}

	order := importpkg.PhaseOrder(im.opts.WithS3)
	started := time.Now()
	fmt.Printf("import-zentao: run_key=%s batch=%d s3=%v tables=%d\n",
		runKey, im.opts.Batch, im.opts.WithS3, len(order))

	for i, spec := range order {
		tm, ok := findTableMap(spec.Source)
		if !ok {
			// S3 表无映射（REQ-07：仅记账不改业务表）。
			fmt.Printf("  [skip] %s（S3 占位：本期待 M4）\n", spec.Source)
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		fmt.Printf("  [%d/%d] %s → %s ...\n", i+1, len(order), tm.source, tm.target)
		st, err := im.importTable(ctx, tm)
		if err != nil {
			_ = im.finishRun(ctx, "failed", err.Error())
			return err
		}
		fmt.Printf("         read=%d written=%d skipped=%d errors=%d%s\n",
			st.Read, st.Written, st.Skipped, st.Errors, abortSuffix(st))

		if st.Aborted {
			_ = im.finishRun(ctx, "failed", st.AbortMsg)
			return fmt.Errorf("%w：表 %s %s", ErrErrorRate, tm.source, st.AbortMsg)
		}

		// 推进 run 指针到下一张表（顺序导入语义）。
		if i+1 < len(order) {
			if err := im.advanceRun(ctx, order[i+1]); err != nil {
				return err
			}
		}
	}

	if err := im.finishRun(ctx, "done", fmt.Sprintf("耗时 %s", time.Since(started).Round(time.Millisecond))); err != nil {
		return err
	}
	return nil
}

// abortSuffix 是中止标记后缀。
func abortSuffix(st tableStats) string {
	if st.Aborted {
		return "  [ABORTED: " + st.AbortMsg + "]"
	}
	return ""
}

// ReportChecks 输出校验清单（REQ-06 / NFR-03）。
func ReportChecks(results []importpkg.CheckResult) {
	for _, r := range results {
		fmt.Printf("  [%s] %s — %s\n", r.Verdict, r.ID, r.Detail)
	}
	fails := importpkg.ExitCode(results)
	if fails != 0 {
		fmt.Printf("import-zentao: 校验清单存在 FAIL（退出码 %d）\n", fails)
		return
	}
	fmt.Println("import-zentao: 校验清单全部通过（允许 WARN）")
}

// ---------- JSON 序列化辅助（校验快照落盘/比对用） ----------

// jsonLine 把任意值序列化为单行 JSON。
func jsonLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// sortedKeys 返回 map 的排序键（稳定输出）。
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
