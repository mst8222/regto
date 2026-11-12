// cmd/regtoctl：Regto 命令行工具（SpecDD 流水线的操作入口）。
//
//	spec lint [--strict]          校验规格文档完整性（strict 启用「AC 必须有对应用例」硬闸门）
//	spec sync [--dry-run]         把三类文档单向同步进数据库（文件是唯一真源）
//	spec coverage [--spec ID]     打印需求 → 设计 → 用例的可追溯覆盖率
//	spec list [--status S]        规格状态板（按状态分组，只读）
//	case create ...               创建用例（主数据；初始状态 wait）
//	case import-ztf --file F      从 ZTF 文件导入用例（用例域，REQ-05）
//	case export-ztf --id N        导出用例为 ZTF 文本（REQ-06）
//	trace append ...              追加一条留痕（自动分配 seq 与 hash 链）
//	trace verify                  校验留痕 hash 链完整性
//	trace tail [-n N]             查看最近 N 条留痕
//	trace sync [--file F]         把 jsonl 导入数据库只读副本（先整链校验，再分批写入）
//	testtask import-result ...    导入 go test -json 执行结果（测试执行域，REQ-04~07）
//	schema                        打印期望/当前迁移版本（需 DATABASE_URL）
//	version                       打印版本信息
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mst8222/regto/internal/adapter/persistence/postgres"
	dspec "github.com/mst8222/regto/internal/domain/spec"
	domaintrace "github.com/mst8222/regto/internal/domain/trace"
	"github.com/mst8222/regto/internal/platform/config"
	"github.com/mst8222/regto/internal/platform/db"
	"github.com/mst8222/regto/internal/platform/id"
	ucagent "github.com/mst8222/regto/internal/usecase/agent"
	ucspec "github.com/mst8222/regto/internal/usecase/spec"
	uctrace "github.com/mst8222/regto/internal/usecase/trace"
)

// version 与发布 tag 保持一致；也可由构建注入覆盖（-ldflags -X main.version=...）。
var version = "1.0.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "regtoctl: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("缺少子命令")
	}
	_ = config.LoadDotEnv(".env")

	switch args[0] {
	case "version":
		fmt.Printf("regtoctl %s\n", version)
		return nil
	case "spec":
		return specCmd(args[1:])
	case "trace":
		return traceCmd(args[1:])
	case "case":
		return caseCmd(args[1:])
	case "testtask":
		return testtaskCmd(args[1:])
	case "plan":
		return planCmd(args[1:])
	case "release":
		return releaseCmd(args[1:])
	case "build":
		return buildCmd(args[1:])
	case "account":
		return accountCmd(args[1:])
	case "schema":
		return schemaCmd()
	case "bench":
		return benchCmd(args[1:])
	default:
		usage()
		return fmt.Errorf("未知子命令 %q", args[0])
	}
}

func usage() {
	fmt.Print(`用法：
  regtoctl spec lint [--strict] [--dir spec]        校验规格文档
  regtoctl spec sync [--dir spec] [--dry-run]       同步文档到数据库（单向：文件 → DB）
  regtoctl spec coverage [--spec SPEC-STORY-001]    覆盖率矩阵
  regtoctl spec list [--status planned] [--output text|md|json]
             状态板：按状态分组展示全部 feature（--output md 可重定向到 spec/STATUS.md）
  regtoctl case create --title "..." --product N [--module N] [--story N] \
             [--pri 3] [--type feature] [--external-id TestFoo] [--output json]
  regtoctl case import-ztf --file F.ztf [--product N] [--actor-id N] [--output json]
  regtoctl case export-ztf --id N                   导出用例为 ZTF 文本
  regtoctl trace append --action read --object-type spec --object-id SPEC-STORY-001 \
             --actor-type ai --actor-id regto-coder --summary "..." [--confidence 85]
  regtoctl account matrix [--role qa] [--output json]   打印权限矩阵（无需数据库）
  regtoctl account init-admin --account admin --password ***   建用户 + 绑 admin
  regtoctl trace verify                             校验 hash 链
  regtoctl trace tail [-n 10]                       查看最近留痕
  regtoctl trace sync [--file traces/2026-10/2026-10-25.jsonl]  导入留痕到数据库
  regtoctl testtask create --name "..." --product N [--owner N] [--type regression] [--output json]
  regtoctl testtask add-cases --task N --cases 12,13
  regtoctl testtask import-result --task N --product N --file test.jsonl \
             [--run-no N] [--create-missing] [--dry-run] [--output json]
  regtoctl plan create --product N --title "..." [--begin DATE --end DATE] [--output json]
  regtoctl plan list [--product N --status wait|doing|done|closed] [--output json]
  regtoctl plan show --id N
  regtoctl plan transition --id N --action start|finish|close|activate [--reason TEXT]
  regtoctl plan link-story --plan N --story N
  regtoctl plan stories --id N
  regtoctl release create --product N --name "..." [--system trunk|branch --marker] [--output json]
  regtoctl release list [--product N --status wait|normal|fail|terminate] [--output json]
  regtoctl release show --id N
  regtoctl release transition --id N --action publish|pause|terminate|play
  regtoctl build create --product N --name "..." --builds "1.0.0" [--output json]
  regtoctl build list [--product N] [--output json]
  regtoctl build show --id N
  regtoctl schema                                   打印迁移版本
  regtoctl bench report [--all] [--product N] [--iterations 20] [--threshold 500] [--json]
                                                    报表延迟分位数（性能验收）
  regtoctl version
`)
}

// ---------- spec ----------

func specCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("spec 需要子命令：lint | sync | coverage | list")
	}
	switch args[0] {
	case "lint":
		fs := flag.NewFlagSet("spec lint", flag.ContinueOnError)
		strict := fs.Bool("strict", false, "启用硬闸门：每条 AC-xx 必须有对应用例覆盖")
		dir := fs.String("dir", "spec", "规格根目录")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		docs, err := ucspec.Scan(*dir)
		if err != nil {
			return err
		}
		reg, err := ucspec.LoadRegistry(filepath.Join(*dir, "_registry.yaml"))
		if err != nil {
			return err
		}
		issues := ucspec.LintWithRegistry(docs, &reg, *strict)
		fmt.Printf("扫描 %d 份规格文档，发现 %d 个问题\n", len(docs), len(issues))
		for _, it := range issues {
			fmt.Printf("  [%s] %s: %s\n", it.Level, it.Path, it.Message)
		}
		if *strict && hasError(issues) {
			return errors.New("硬闸门未通过：存在 error 级问题")
		}
		return nil
	case "sync":
		fs := flag.NewFlagSet("spec sync", flag.ContinueOnError)
		dir := fs.String("dir", "spec", "规格根目录")
		output := fs.String("output", "text", "输出格式：text | json")
		dryRun := fs.Bool("dry-run", false, "只扫描不写库")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return specSyncCmd(*dir, *output, *dryRun)
	case "coverage":
		fs := flag.NewFlagSet("spec coverage", flag.ContinueOnError)
		specID := fs.String("spec", "SPEC-STORY-001", "需求规格 ID")
		dir := fs.String("dir", "spec", "规格根目录")
		format := fs.String("format", "text", "输出格式：text | md")
		allow := fs.Bool("allow-uncovered", false, "存在未覆盖需求条目时仍返回 0（仅用于看报告）")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		docs, err := ucspec.Scan(*dir)
		if err != nil {
			return err
		}
		cov := ucspec.Cover(docs, *specID)
		if *format == "md" {
			printCoverageMD(cov)
		} else {
			printCoverageText(cov)
		}
		// ROADMAP M3 验收：未覆盖时退出码 1（硬闸门，可用 --allow-uncovered 豁免）。
		if uncovered := cov.Uncovered(); len(uncovered) > 0 {
			fmt.Fprintf(os.Stderr, "未覆盖的需求条目：%s\n", strings.Join(uncovered, ", "))
			if !*allow {
				return fmt.Errorf("SPEC %s 有 %d 个需求条目无用例覆盖",
					*specID, len(uncovered))
			}
		}
		return nil
	case "list":
		// 状态板：按状态分组展示全部 feature（spec/<领域>/<feature> 布局不变，
		// 状态仍以 front-matter + _registry.yaml 为真源，本命令只读）。
		fs := flag.NewFlagSet("spec list", flag.ContinueOnError)
		dir := fs.String("dir", "spec", "规格根目录")
		status := fs.String("status", "", "按状态过滤：planned|developing|implemented|discarded")
		format := fs.String("output", "text", "输出格式：text | md | json")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		docs, err := ucspec.Scan(*dir)
		if err != nil {
			return err
		}
		reg, err := ucspec.LoadRegistry(filepath.Join(*dir, "_registry.yaml"))
		if err != nil {
			return err
		}
		feats := ucspec.FilterStatus(ucspec.Board(docs, &reg), *status)
		switch *format {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(feats)
		case "md":
			ucspec.PrintBoardMD(os.Stdout, feats)
		default:
			ucspec.PrintBoardText(os.Stdout, feats)
		}
		return nil
	default:
		return fmt.Errorf("未知 spec 子命令 %q", args[0])
	}
}

// specSyncCmd 把 spec/ 目录的三类文档单向同步进数据库（文件是唯一真源）。
func specSyncCmd(dir, format string, dryRun bool) error {
	ctx := context.Background()
	cfg, err := config.Load(0)
	if err != nil {
		return err
	}
	docs, err := ucspec.Scan(dir)
	if err != nil {
		return err
	}
	reg, err := ucspec.LoadRegistry(filepath.Join(dir, "_registry.yaml"))
	if err != nil {
		return err
	}
	if dryRun {
		return printSyncResult(dspec.SyncResult{
			Documents: len(docs),
			Items:     countItems(docs),
			Cases:     countCases(docs),
			Links:     countLinks(docs),
		}, format)
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	result, err := ucspec.NewSyncer(postgres.NewSpecRepo(pool)).Sync(ctx, dir, reg)
	if err != nil {
		return err
	}
	return printSyncResult(result, format)
}

// printSyncResult 输出结果；json 格式供 AI/脚本解析（AI_AGENT_RULES）。
func printSyncResult(r dspec.SyncResult, format string) error {
	if format == "json" {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("同步完成：文档 %d，条目 %d，用例 %d，步骤 %d，链接 %d，变更 %d（%dms）\n",
		r.Documents, r.Items, r.Cases, r.Steps, r.Links, r.Changed, r.DurationMs)
	return nil
}

func countItems(docs []ucspec.Doc) int {
	n := 0
	for _, d := range docs {
		n += len(d.Items)
	}
	return n
}

func countCases(docs []ucspec.Doc) int {
	n := 0
	for _, d := range docs {
		n += len(d.Cases)
	}
	return n
}

func countLinks(docs []ucspec.Doc) int {
	n := 0
	for _, d := range docs {
		for _, c := range d.Cases {
			n += len(c.Covers) + len(c.Verifies)
		}
	}
	return n
}

func hasError(issues []ucspec.Issue) bool {
	for _, it := range issues {
		if it.Level == "error" {
			return true
		}
	}
	return false
}

func printCoverageText(cov ucspec.Coverage) {
	fmt.Printf("覆盖率报告：%s\n\n", cov.SpecID)
	fmt.Printf("%-10s %-40s %-14s %s\n", "条目", "标题", "用例", "设计")
	for _, ic := range cov.Items {
		cases := "—"
		if len(ic.Cases) > 0 {
			cases = strings.Join(ic.Cases, ",")
		}
		design := "—"
		if len(ic.Designs) > 0 {
			design = strings.Join(ic.Designs, ",")
		}
		title := ic.Item.Title
		if len([]rune(title)) > 38 {
			title = string([]rune(title)[:38]) + "…"
		}
		fmt.Printf("%-10s %-40s %-14s %s\n", ic.Item.ID, title, cases, design)
	}
	fmt.Printf("\n设计覆盖 %.0f%%   用例覆盖 %.0f%%\n", cov.DesignRate, cov.CaseRate)
}

func printCoverageMD(cov ucspec.Coverage) {
	fmt.Printf("| 条目 | 标题 | 设计覆盖 | 用例覆盖 |\n|---|---|---|---|\n")
	for _, ic := range cov.Items {
		cases := "—"
		if len(ic.Cases) > 0 {
			cases = strings.Join(ic.Cases, ", ")
		}
		design := "—"
		if len(ic.Designs) > 0 {
			design = strings.Join(ic.Designs, ", ")
		}
		fmt.Printf("| %s | %s | %s | %s |\n", ic.Item.ID, ic.Item.Title, design, cases)
	}
	fmt.Printf("\n设计覆盖 %.0f%%   用例覆盖 %.0f%%\n", cov.DesignRate, cov.CaseRate)
}

// ---------- trace ----------

// traceRecorder 在能连上库时返回「真源 + 副本」双写器，连不上时退化为只写真源。
//
// 为什么 CLI 也必须双写（2026-10-27 备份演练实测发现的漂移）：
//
//	`regtoctl trace append` 原先用 uctrace.Store 只写 jsonl 真源，库副本要等人
//	记得跑 `trace sync` —— 实测就是这么悄悄少了 3 条（seq 78/79/80）。
//	而审计报表与守卫查询读的是**副本**，等于审计口径比真源少三条，且没人会发现：
//	真源能自证，所以没人怀疑；副本少几条，也不会报错。
//
// 真源仍是权威（铁律不变）：副本写失败只告警、不阻断，下一次 sync 补齐。
func traceRecorder(store *uctrace.Store) (*uctrace.Recorder, func()) {
	cfg, err := config.Load(0)
	if err != nil || cfg.DatabaseURL == "" {
		slog.Warn("未配置 REGTO_DATABASE_URL：本次留痕只写真源，副本需 trace sync 补齐")
		return uctrace.NewRecorder(store, nil, slog.Default()), func() {}
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Warn("连不上库：本次留痕只写真源，副本需 trace sync 补齐",
			slog.String("error", err.Error()))
		return uctrace.NewRecorder(store, nil, slog.Default()), func() {}
	}
	rec := uctrace.NewRecorder(store, postgres.NewTraceRepo(pool), slog.Default())
	return rec, func() { pool.Close() }
}

func traceCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("trace 需要子命令：append | verify | tail | sync | pending | approve | reject")
	}
	store := uctrace.NewStore("traces", id.NewSystem())
	recorder, closeDB := traceRecorder(store)
	defer closeDB()

	switch args[0] {
	case "append":
		fs := flag.NewFlagSet("trace append", flag.ContinueOnError)
		action := fs.String("action", "", "动作：read|review|implement|test|fix|regress|status_change|...")
		objType := fs.String("object-type", "", "对象类型：spec|design|test|story|task|bug|case|commit|...")
		objID := fs.String("object-id", "", "对象 ID")
		objRef := fs.String("object-ref", "", "对象引用路径，如 spec/story/.../SPEC-STORY-001.md#REQ-01")
		actorType := fs.String("actor-type", domaintrace.ActorAI, "执行主体：human|ai|system")
		actorID := fs.String("actor-id", "regto-coder", "执行主体 ID（人类=account，AI=agent_id）")
		summary := fs.String("summary", "", "一句话摘要（必填）")
		detail := fs.String("detail", "", "正文（评审意见全文等）")
		confidence := fs.Int("confidence", 0, "AI 自评置信度 0-100")
		needsReview := fs.Bool("needs-human-review", false, "是否需要人工复核")
		reviewStatus := fs.String("review-status", "", "复核状态：pending | approved | rejected（进入审批队列时填 pending）")
		commit := fs.String("commit-sha", "", "关联提交 SHA")
		session := fs.String("session-id", "", "Agent 会话 ID")
		model := fs.String("model", "", "模型名，如 hy4-preview")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		rec := domaintrace.Record{
			Action:           *action,
			ObjectType:       *objType,
			ObjectID:         *objID,
			ObjectRef:        *objRef,
			ActorType:        *actorType,
			ActorID:          *actorID,
			ActorModel:       *model,
			ActorSessionID:   *session,
			Summary:          *summary,
			Detail:           *detail,
			Confidence:       *confidence,
			NeedsHumanReview: *needsReview,
			ReviewStatus:     *reviewStatus,
			CommitSHA:        *commit,
			Env:              envOr("REGTO_ENV", "local"),
			OccurredAtMs:     time.Now().UTC().UnixMilli(),
		}
		out, err := recorder.AppendRecord(context.Background(), rec)
		if err != nil {
			return err
		}
		fmt.Printf("留痕已写入：seq=%d trace_id=%s hash=%s\n", out.Seq, out.TraceID, out.Hash[:16]+"…")
		return nil

	case "verify":
		n, err := store.Verify()
		if err != nil {
			fmt.Printf("留痕校验失败（共 %d 条）：%v\n", n, err)
			return err
		}
		fmt.Printf("留痕校验通过：%d 条记录，hash 链完整\n", n)
		return nil

	case "tail":
		fs := flag.NewFlagSet("trace tail", flag.ContinueOnError)
		n := fs.Int("n", 10, "显示条数")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		recs, err := store.All()
		if err != nil {
			return err
		}
		start := 0
		if len(recs) > *n {
			start = len(recs) - *n
		}
		for _, r := range recs[start:] {
			fmt.Printf("#%d [%s/%s] %s %s:%s — %s\n", r.Seq, r.ActorType, r.Action, r.TraceID,
				r.ObjectType, r.ObjectID, r.Summary)
		}
		return nil

	case "sync":
		fs := flag.NewFlagSet("trace sync", flag.ContinueOnError)
		file := fs.String("file", "", "导入指定 jsonl 文件；缺省导入整个留痕根目录")
		dir := fs.String("dir", "traces", "留痕根目录")
		format := fs.String("output", "text", "输出格式：text | json")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return traceSyncCmd(*file, *dir, *format)

	case "pending":
		// 审批队列：review_status=pending 且尚未被 approve/reject 记录引用的留痕
		// （SPEC-AGENT-001 REQ-05：traces 不可 UPDATE，状态变迁以追加表达）。
		fs := flag.NewFlagSet("trace pending", flag.ContinueOnError)
		all := fs.Bool("all", false, "显示全部（含已被审批/驳回的）")
		format := fs.String("output", "text", "输出格式：text | json")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		recs, err := store.All()
		if err != nil {
			return err
		}
		queue := pendingApprovals(recs)
		if *all {
			queue = recs
		}
		if *format == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(queue)
		}
		if len(queue) == 0 {
			fmt.Println("审批队列为空")
			return nil
		}
		for _, r := range queue {
			fmt.Printf("#%d %s [%s] %s %s:%s — %s\n", r.Seq, r.TraceID, r.Action,
				r.ActorID, r.ObjectType, r.ObjectID, r.Summary)
		}
		return nil

	case "approve", "reject":
		// 批准/驳回是**新增一条留痕**，不改动被审批的记录（DEC-04）。
		// 与 HTTP 接口走同一个用例（SPEC-AGENT-002 REQ-08）：
		// 真源先写、副本后补，跑完这条命令守卫立刻生效，不再需要 trace sync。
		verb := args[0]
		fs := flag.NewFlagSet("trace "+verb, flag.ContinueOnError)
		by := fs.String("by", "admin", "审批人（人类 account）")
		comment := fs.String("comment", "", "审批意见")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		target := fs.Arg(0)
		if target == "" {
			return fmt.Errorf("trace %s 需要 trace_id 参数", verb)
		}

		ctx := context.Background()
		cfg, err := config.Load(0)
		if err != nil {
			return err
		}
		pool, err := db.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("连接数据库失败（审批需要读副本判定，缺库无法确认状态）: %w", err)
		}
		defer pool.Close()

		traceRepo := postgres.NewTraceRepo(pool)
		recorder := uctrace.NewRecorder(store, traceRepo,
			slog.New(slog.NewTextHandler(io.Discard, nil)))
		svc := ucagent.NewApprovalService(traceRepo, recorder, envOr("REGTO_ENV", "local"))

		saved, err := svc.Decide(ctx, target, *by, verb == "approve", *comment)
		if err != nil {
			return err
		}
		fmt.Printf("已%s：%s → 新增留痕 seq=%d trace_id=%s\n", verb, target, saved.Seq, saved.TraceID)
		fmt.Println("副本已同写，守卫无需 trace sync 即可生效")
		return nil

	default:
		return fmt.Errorf("未知 trace 子命令 %q", args[0])
	}
}

// pendingApprovals 计算待审批队列：review_status=pending 的记录
// 减去已被 approve/reject 记录引用过的那些（SPEC-AGENT-001 REQ-05）。
//
// traces 是只读副本不可 UPDATE，故「已审批」不是改状态，
// 而是追加一条 action=approve|reject 且 object_id 指向原记录的新留痕。
func pendingApprovals(recs []domaintrace.Record) []domaintrace.Record {
	decided := map[string]bool{}
	for _, r := range recs {
		if r.ObjectType != "trace" {
			continue
		}
		if r.Action == domaintrace.ActionApprove || r.Action == domaintrace.ActionReject {
			decided[r.ObjectID] = true
		}
	}
	out := []domaintrace.Record{}
	for _, r := range recs {
		if r.ReviewStatus != "pending" || decided[r.TraceID] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// inQueue 报告 trace_id 是否仍在待审批队列中。
func inQueue(queue []domaintrace.Record, traceID string) bool {
	for _, r := range queue {
		if r.TraceID == traceID {
			return true
		}
	}
	return false
}

// traceSyncCmd 把 jsonl 留痕导入只读副本（导入前必须整链校验通过）。
func traceSyncCmd(file, dir, format string) error {
	ctx := context.Background()
	cfg, err := config.Load(0)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := postgres.NewTraceRepo(pool)

	var result uctrace.SyncResult
	if file != "" {
		result, err = uctrace.SyncFile(ctx, repo, file)
	} else {
		result, err = uctrace.SyncDir(ctx, repo, dir)
	}
	if err != nil {
		return err
	}
	if format == "json" {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("导入完成：新增 %d，跳过 %d，共 %d 条（seq %d-%d，head=%s，%dms）\n",
		result.Imported, result.Skipped, result.Total, result.FirstSeq, result.LastSeq,
		shortHash(result.HeadHash), result.DurationMs)
	return nil
}

// shortHash 截断哈希用于展示（避免刷屏，同时保留可比对性）。
func shortHash(h string) string {
	if len(h) > 16 {
		return h[:16] + "…"
	}
	return h
}

// ---------- schema ----------

// schemaCmd 打印迁移版本并校验 schema 版本契约。
//
// 两个数据源必须一致：
//   - schema_migrations：迁移工具的元数据（应用账号零权限，须用迁移连接串读取）；
//   - schema_meta：业务契约表，由每个迁移文件自己维护（应用只读）。
//
// 二者不一致说明某个迁移漏写了 schema_meta 的版本维护语句——
// 后果是 regto-server 启动校验读到旧版本并拒绝启动。这里显式报出来（DEC-08）。
func schemaCmd() error {
	cfg, err := config.Load(0)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var migrated, contract uint
	var dirty bool

	// 迁移元数据：应用账号无权限，走迁移连接串（超户，仅本命令使用）。
	if mpool, err := db.Open(ctx, cfg.MigrationURL); err == nil {
		err = mpool.QueryRow(ctx,
			`SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1`,
		).Scan(&migrated, &dirty)
		mpool.Close()
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("读取 schema_migrations 失败（库是否已迁移？）: %w", err)
		}
	}

	// 业务契约表：应用账号只读。
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_meta')`,
	).Scan(&exists); err != nil {
		return fmt.Errorf("检查 schema_meta 失败: %w", err)
	}
	if exists {
		if err := pool.QueryRow(ctx,
			`SELECT version FROM schema_meta WHERE singleton`).Scan(&contract); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("读取 schema_meta 失败: %w", err)
		}
	}

	fmt.Printf("schema version=%d dirty=%v contract=%d\n", migrated, dirty, contract)
	if migrated != contract {
		return fmt.Errorf("schema 版本契约不一致：迁移=%d，schema_meta=%d；"+
			"请检查迁移 %d 是否漏写 schema_meta 的版本维护语句", migrated, contract, migrated)
	}
	return nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
