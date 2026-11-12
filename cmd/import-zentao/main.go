// cmd/import-zentao：禅道存量导入器 CLI（M2-C）。
//
// 用法：
//
//	import-zentao --source "user:pass@tcp(host:3306)/zentao" \
//	              --target "postgres://regto_app@127.0.0.1:5432/regto" \
//	              [--run-key KEY] [--batch 500] [--verify-only] \
//	              [--resume KEY] [--with-s3] [--data-dir DIR]
//
// 设计（SPEC-IMPORT-001 / DESIGN-IMPORT-001）：
//   - 三阶段按外键拓扑：S1 主数据 → S2 业务主表（S3 默认跳过，REQ-07）；
//   - 幂等 + 断点续传：regto_import 三表记账（import_runs/import_id_map/import_errors）；
//   - 源库只读（NFR-01）；错误率 >1% 中止该表（REQ-05）。
//
// 本文件只负责参数解析、连接与退出码；导入逻辑在 internal/importer/zentao。
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/domain/importpkg"
	"github.com/mst8222/regto/internal/importer/zentao"
	"github.com/mst8222/regto/internal/platform/config"
)

// 退出码（DESIGN-IMPORT-001 §5）。
const (
	exitOK         = 0
	exitGeneric    = 1
	exitVerifyFail = 2
	exitConnect    = 3
	exitErrorRate  = 4
)

// options 是导入器的命令行参数。
type options struct {
	SourceDSN  string
	TargetDSN  string
	RunKey     string
	Batch      int
	VerifyOnly bool
	Resume     string
	WithS3     bool
	DataDir    string // 源库附件目录（校验 #10 用；空则该项 WARN）
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "import-zentao: %v\n", err)
		os.Exit(exitCodeOf(err))
	}
}

// 哨兵错误：把失败原因映射为退出码。
var (
	verifyFailure  = errors.New("校验清单存在 FAIL 项")
	connectFailure = errors.New("连接失败")
)

// exitCodeOf 从错误提取退出码。
func exitCodeOf(err error) int {
	switch {
	case errors.Is(err, zentao.ErrErrorRate):
		return exitErrorRate
	case errors.Is(err, verifyFailure):
		return exitVerifyFail
	case errors.Is(err, connectFailure):
		return exitConnect
	}
	return exitGeneric
}

// run 是导入器主流程。
func run(args []string) error {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}

	// SIGINT/SIGTERM：置取消标志，让批次边界安全停止（cursor 已落库，可 resume）。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ---------- 连接 ----------
	src, err := openSource(ctx, opts.SourceDSN)
	if err != nil {
		return fmt.Errorf("%w：源库 %v", connectFailure, err)
	}
	defer func() { _ = src.Close() }()
	if err := src.PingContext(ctx); err != nil {
		return fmt.Errorf("%w：源库不可达 %v", connectFailure, err)
	}

	pool, err := openTarget(ctx, opts.TargetDSN)
	if err != nil {
		return fmt.Errorf("%w：目标库 %v", connectFailure, err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("%w：目标库不可达 %v", connectFailure, err)
	}

	im := zentao.New(zentao.MySQLSource(src), pool, zentao.Options{
		Batch: opts.Batch, WithS3: opts.WithS3, DataDir: opts.DataDir,
	})

	// ---------- --verify-only：零写入，只读比对 ----------
	if opts.VerifyOnly {
		results := im.Verify(ctx)
		zentao.ReportChecks(results)
		if importpkg.ExitCode(results) != 0 {
			return verifyFailure
		}
		return nil
	}

	// ---------- 导入 ----------
	runKey := opts.Resume
	if runKey == "" {
		runKey = opts.RunKey
	}
	if runKey == "" {
		runKey = fmt.Sprintf("run-%s", time.Now().UTC().Format("20060102T150405"))
	}
	return im.Run(ctx, runKey)
}

// parseFlags 解析命令行参数（手写解析：避免 flag 包的位置参数限制）。
func parseFlags(args []string) (*options, error) {
	opts := &options{Batch: 500}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("参数 %s 缺少数值", a)
			}
			return args[i], nil
		}
		switch a {
		case "--source":
			v, err := next()
			if err != nil {
				return nil, err
			}
			opts.SourceDSN = v
		case "--target":
			v, err := next()
			if err != nil {
				return nil, err
			}
			opts.TargetDSN = v
		case "--run-key":
			v, err := next()
			if err != nil {
				return nil, err
			}
			opts.RunKey = v
		case "--resume":
			v, err := next()
			if err != nil {
				return nil, err
			}
			opts.Resume = v
		case "--batch":
			v, err := next()
			if err != nil {
				return nil, err
			}
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
				return nil, fmt.Errorf("--batch 必须为正整数，得到 %q", v)
			}
			opts.Batch = n
		case "--verify-only":
			opts.VerifyOnly = true
		case "--with-s3":
			opts.WithS3 = true
		case "--data-dir":
			v, err := next()
			if err != nil {
				return nil, err
			}
			opts.DataDir = v
		default:
			return nil, fmt.Errorf("未知参数 %q", a)
		}
	}
	if opts.SourceDSN == "" {
		return nil, errors.New("缺少 --source（禅道 MySQL DSN）")
	}
	if opts.TargetDSN == "" {
		return nil, errors.New("缺少 --target（Regto PostgreSQL DSN）")
	}
	if opts.Resume != "" && opts.VerifyOnly {
		return nil, errors.New("--resume 与 --verify-only 互斥（校验是只读全量比对）")
	}
	return opts, nil
}

// openSource 连接源库（MySQL；只读用途，NFR-01）。
func openSource(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn+"?parseTime=true&loc=UTC&readTimeout=60s")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, nil
}

// openTarget 连接目标库（PostgreSQL）。
func openTarget(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		cfg, err := config.Load(0)
		if err != nil {
			return nil, err
		}
		dsn = cfg.DatabaseURL
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return pool, nil
}
