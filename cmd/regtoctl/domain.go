// cmd/regtoctl 的域操作子命令：用例域（case）与测试执行域（testtask）。
//
// 设计约定（ROADMAP §8 / AI_AGENT_RULES）：
//   - 所有命令支持 --output json（成功结果与失败的结构化 stderr 均可被 AI 可靠解析）；
//   - 领域错误以 {"code","message","meta"} 结构写 stderr，退出码 1；
//   - --file - 表示从 stdin 读取，便于 `go test -json ./... | regtoctl ...` 管道。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mst8222/regto/internal/adapter/persistence/postgres"
	"github.com/mst8222/regto/internal/domain/shared"
	dtcase "github.com/mst8222/regto/internal/domain/testcase"
	dtt "github.com/mst8222/regto/internal/domain/testtask"
	"github.com/mst8222/regto/internal/platform/clock"
	"github.com/mst8222/regto/internal/platform/config"
	"github.com/mst8222/regto/internal/platform/db"
	"github.com/mst8222/regto/internal/platform/id"
	uctrel "github.com/mst8222/regto/internal/usecase/release"
	uctcase "github.com/mst8222/regto/internal/usecase/testcase"
	ucttask "github.com/mst8222/regto/internal/usecase/testtask"
)

// domainServices 是域命令的运行时装配（用例域 + 测试执行域 + 发布域共享同一连接池）。
type domainServices struct {
	cases    *uctcase.Service
	tasks    *ucttask.Service
	releases *uctrel.Service
	pool     *pgxpool.Pool
}

// close 释放连接池。
func (d *domainServices) close() {
	if d.pool != nil {
		d.pool.Close()
	}
}

// openDomainServices 装配域服务（与 regto-server 同一组装方式，仅取所需子系统）。
func openDomainServices(ctx context.Context) (*domainServices, error) {
	cfg, err := config.Load(0)
	if err != nil {
		return nil, err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	caseRepo := postgres.NewCaseRepo(pool)
	taskRepo := postgres.NewTestTaskRepo(pool)
	releaseRepo := postgres.NewReleaseRepo(pool)
	releases := uctrel.New(releaseRepo, clock.SystemClock{})
	tasks := ucttask.New(taskRepo, caseRepo, clock.SystemClock{}, id.NewSystem())
	tasks.SetBuildChecker(releases.BuildExists) // SPEC-RELEASE-001 REQ-07
	return &domainServices{
		cases:    uctcase.New(caseRepo, clock.SystemClock{}),
		tasks:    tasks,
		releases: releases,
		pool:     pool,
	}, nil
}

// parseDateMs 解析 YYYY-MM-DD 为毫秒时间戳。
func parseDateMs(raw string) (*int64, error) {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, err
	}
	ms := t.UTC().UnixMilli()
	return &ms, nil
}

// formatDateMs 把毫秒时间戳格式化为 YYYY-MM-DD。
func formatDateMs(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}

// parseIDArg 解析位置参数中的正整数 ID。
func parseIDArg(raw, what string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("非法 %s ID %q（必须为正整数）", what, raw)
	}
	return id, nil
}

// osStderr 返回标准错误输出（便于测试桩替换；当前直接返回 os.Stderr）。
func osStderr() *os.File { return os.Stderr }

// readFileArg 读取文件参数；"-" 表示 stdin。
func readFileArg(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// writeDomainError 把领域错误以结构化 JSON 写 stderr（AI 可解析）。
// 非领域错误不拦截，由 main 的统一出口处理。
func writeDomainError(err error) {
	var de *shared.DomainError
	if !errors.As(err, &de) {
		return
	}
	b, mErr := json.Marshal(map[string]any{
		"code":    de.Code,
		"message": de.Message,
		"meta":    de.Meta,
	})
	if mErr != nil {
		return
	}
	fmt.Fprintln(os.Stderr, string(b))
}

// ---------- case ----------

// caseCmd 是用例域子命令组：create | import-ztf | export-ztf（SPEC-CASE-001 范围：API + CLI）。
func caseCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("case 需要子命令：create | import-ztf | export-ztf")
	}
	switch args[0] {
	case "create":
		return caseCreateCmd(args[1:])
	case "import-ztf":
		return caseImportZTFCmd(args[1:])
	case "export-ztf":
		return caseExportZTFCmd(args[1:])
	default:
		return fmt.Errorf("未知 case 子命令 %q", args[0])
	}
}

// caseCreateCmd 实现 `regtoctl case create`（REQ-01：主数据创建；初始状态 wait）。
func caseCreateCmd(args []string) error {
	fs := flag.NewFlagSet("case create", flag.ContinueOnError)
	title := fs.String("title", "", "用例标题（必填）")
	product := fs.Int64("product", 0, "所属产品 ID（必填）")
	module := fs.Int64("module", 0, "模块 ID（0=无）")
	story := fs.Int64("story", 0, "关联需求 ID（0=无）")
	storyVer := fs.Int("story-version", 0, "关联需求锁定的版本（0=默认 1）")
	precondition := fs.String("precondition", "", "前置条件")
	keywords := fs.String("keywords", "", "关键词")
	pri := fs.Int("pri", 3, "优先级 1-4")
	typ := fs.String("type", "feature", "用例类型：feature|performance|config|install")
	externalID := fs.String("external-id", "", "CI 侧标识（如 go 测试函数名），go test -json 导入的匹配键")
	actor := fs.Int64("actor-id", 0, "执行主体账号 ID（0=系统/未知，写 NULL）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *title == "" {
		return errors.New("缺少 --title（用例标题）")
	}
	if *product <= 0 {
		return errors.New("缺少 --product（所属产品 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	cmd := uctcase.CreateCommand{
		ProductID:    *product,
		Title:        *title,
		Precondition: *precondition,
		Keywords:     *keywords,
		Pri:          *pri,
		Type:         dtcase.CaseType(*typ),
		StoryVersion: *storyVer,
		ExternalID:   *externalID,
		ActorID:      *actor,
	}
	if *module > 0 {
		cmd.ModuleID = module
	}
	if *story > 0 {
		cmd.StoryID = story
	}
	c, err := svcs.cases.Create(ctx, cmd)
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		b, err := json.Marshal(map[string]any{
			"id": c.ID, "title": c.Title, "product_id": c.ProductID,
			"pri": c.Pri, "type": string(c.Type), "status": string(c.Status),
			"external_id": c.ExternalID,
		})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("用例已创建：#%d %s（product=%d pri=%d type=%s status=%s）\n",
		c.ID, c.Title, c.ProductID, c.Pri, c.Type, c.Status)
	return nil
}

// caseImportZTFCmd 实现 `regtoctl case import-ztf`（REQ-05）。
func caseImportZTFCmd(args []string) error {
	fs := flag.NewFlagSet("case import-ztf", flag.ContinueOnError)
	file := fs.String("file", "", "ZTF 文件路径（必填；- 表示 stdin）")
	product := fs.Int64("product", 0, "用例块未带 @产品 时的默认产品 ID")
	actor := fs.Int64("actor-id", 0, "执行主体账号 ID（0=系统/未知，写 NULL）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("缺少 --file（ZTF 文件路径；- 表示 stdin）")
	}
	raw, err := readFileArg(*file)
	if err != nil {
		return fmt.Errorf("读取 ZTF 文件失败: %w", err)
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	res, err := svcs.cases.ImportZTF(ctx, uctcase.ImportZTFCommand{
		ZTF:              string(raw),
		DefaultProductID: *product,
		ActorID:          *actor,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		type item struct {
			ID      int64  `json:"id"`
			Title   string `json:"title"`
			Product int64  `json:"product_id"`
			Pri     int    `json:"pri"`
			Type    string `json:"type"`
		}
		items := make([]item, 0, len(res.Cases))
		for _, c := range res.Cases {
			items = append(items, item{c.ID, c.Title, c.ProductID, c.Pri, string(c.Type)})
		}
		b, err := json.Marshal(map[string]any{
			"total": res.Total, "created": res.Created, "cases": items,
		})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("ZTF 导入完成：解析 %d 个用例块，创建 %d 个用例\n", res.Total, res.Created)
	for _, c := range res.Cases {
		fmt.Printf("  #%d %s（product=%d pri=%d type=%s）\n", c.ID, c.Title, c.ProductID, c.Pri, c.Type)
	}
	return nil
}

// caseExportZTFCmd 实现 `regtoctl case export-ztf`（REQ-06，默认原样输出 ZTF 文本）。
func caseExportZTFCmd(args []string) error {
	fs := flag.NewFlagSet("case export-ztf", flag.ContinueOnError)
	id := fs.Int64("id", 0, "用例 ID（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return errors.New("缺少 --id（用例 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	text, err := svcs.cases.ExportZTF(ctx, *id)
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		b, err := json.Marshal(map[string]any{"id": *id, "ztf": text})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	// text 模式直接写 ZTF 原文（无换行附加）：可重定向到文件做 diff
	fmt.Print(text)
	return nil
}

// ---------- testtask ----------

// testtaskCmd 是测试执行域子命令组：create | add-cases | import-result
// （SPEC-TESTTASK-001；create/add-cases 补全 ROADMAP §222「每域 CRUD + 动作命令」）。
func testtaskCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("testtask 需要子命令：create | add-cases | import-result")
	}
	switch args[0] {
	case "create":
		return testtaskCreateCmd(args[1:])
	case "add-cases":
		return testtaskAddCasesCmd(args[1:])
	case "import-result":
		return testtaskImportResultCmd(args[1:])
	default:
		return fmt.Errorf("未知 testtask 子命令 %q", args[0])
	}
}

// testtaskCreateCmd 实现 `regtoctl testtask create`（REQ-01；初始状态 wait）。
func testtaskCreateCmd(args []string) error {
	fs := flag.NewFlagSet("testtask create", flag.ContinueOnError)
	name := fs.String("name", "", "测试单名称（必填）")
	product := fs.Int64("product", 0, "所属产品 ID（必填）")
	owner := fs.Int64("owner", 0, "负责人账号 ID（0=取执行主体）")
	typ := fs.String("type", "", "测试单类型（如 regression/smoke）")
	pri := fs.Int("pri", 3, "优先级 1-4")
	desc := fs.String("desc", "", "描述")
	actor := fs.Int64("actor-id", 0, "执行主体账号 ID（0=系统/未知）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("缺少 --name（测试单名称）")
	}
	if *product <= 0 {
		return errors.New("缺少 --product（所属产品 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	t, err := svcs.tasks.Create(ctx, ucttask.CreateCommand{
		Name: *name, ProductID: *product, OwnerID: *owner,
		Type: *typ, Pri: *pri, Desc: *desc, ActorID: *actor,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		b, err := json.Marshal(map[string]any{
			"id": t.ID, "name": t.Name, "product_id": t.ProductID,
			"owner_id": t.OwnerID, "pri": t.Pri, "status": string(t.Status),
		})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("测试单已创建：#%d %s（product=%d status=%s）\n", t.ID, t.Name, t.ProductID, t.Status)
	return nil
}

// testtaskAddCasesCmd 实现 `regtoctl testtask add-cases`（锁定当前版本快照）。
// 用例 ID 逗号分隔；已加入的不重复计（幂等）。
func testtaskAddCasesCmd(args []string) error {
	fs := flag.NewFlagSet("testtask add-cases", flag.ContinueOnError)
	task := fs.Int64("task", 0, "测试单 ID（必填）")
	caseIDs := fs.String("cases", "", "用例 ID 列表，逗号分隔（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *task <= 0 {
		return errors.New("缺少 --task（测试单 ID）")
	}
	ids, err := parseIDList(*caseIDs)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return errors.New("缺少 --cases（用例 ID 列表，逗号分隔）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	added, err := svcs.tasks.AddCases(ctx, *task, ids)
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		b, err := json.Marshal(map[string]any{"test_task_id": *task, "added": added})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("已加入测试单 #%d：新增 %d 个用例（共提交 %d 个）\n", *task, added, len(ids))
	return nil
}

// parseIDList 解析逗号分隔的正整数列表（拒绝空段与非数字）。
func parseIDList(raw string) ([]int64, error) {
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("非法用例 ID: %q（须为正整数，逗号分隔）", part)
		}
		out = append(out, id)
	}
	return out, nil
}

// testtaskImportResultCmd 实现 `regtoctl testtask import-result`（REQ-04~07）。
func testtaskImportResultCmd(args []string) error {
	fs := flag.NewFlagSet("testtask import-result", flag.ContinueOnError)
	task := fs.Int64("task", 0, "测试单 ID（必填）")
	product := fs.Int64("product", 0, "匹配用例的产品 ID（必填）")
	file := fs.String("file", "", "go test -json 输出文件（必填；- 表示 stdin）")
	actorType := fs.String("actor-type", ucttask.DefaultActorType, "执行主体类型：human|ai|system")
	actorID := fs.String("actor-id", ucttask.DefaultActorID, "执行主体 ID（AI=agent_id，人类=登录名）")
	traceID := fs.String("trace-id", "", "关联留痕 trace_id；缺省生成 ULID")
	runNo := fs.Int("run-no", 0, "执行轮次；缺省取该测试单已有最大 +1")
	createMissing := fs.Bool("create-missing", false, "未匹配用例时按测试名自动建档")
	dryRun := fs.Bool("dry-run", false, "只解析与匹配，不写库")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *task <= 0 {
		return errors.New("缺少 --task（测试单 ID）")
	}
	if *product <= 0 {
		return errors.New("缺少 --product（匹配用例的产品 ID）")
	}
	if *file == "" {
		return errors.New("缺少 --file（go test -json 输出文件；- 表示 stdin）")
	}
	raw, err := readFileArg(*file)
	if err != nil {
		return fmt.Errorf("读取测试输出文件失败: %w", err)
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	res, err := svcs.tasks.ImportGoTestJSON(ctx, ucttask.ImportCommand{
		TestTaskID:    *task,
		ProductID:     *product,
		Events:        string(raw),
		ActorType:     *actorType,
		ActorID:       *actorID,
		TraceID:       *traceID,
		RunNo:         *runNo,
		CreateMissing: *createMissing,
		DryRun:        *dryRun,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		type item struct {
			CaseID     int64  `json:"case_id"`
			Result     string `json:"result"`
			DurationMs int    `json:"duration_ms"`
		}
		items := make([]item, 0, len(res.Results))
		for _, r := range res.Results {
			items = append(items, item{r.CaseID, string(r.Result), r.DurationMs})
		}
		b, err := json.Marshal(map[string]any{
			"test_task_id": res.TestTaskID,
			"run_no":       res.RunNo,
			"total":        res.Total,
			"written":      res.Written,
			"matched":      res.Matched,
			"created":      res.Created,
			"by_result":    res.ByResult,
			"dry_run":      *dryRun,
			"results":      items,
		})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	verb := "导入"
	if *dryRun {
		verb = "预演（dry-run）"
	}
	fmt.Printf("%s完成：测试单 #%d 轮次 %d，解析 %d 个测试，写入 %d（匹配 %d / 新建 %d）\n",
		verb, res.TestTaskID, res.RunNo, res.Total, res.Written, res.Matched, res.Created)
	fmt.Printf("  结果分布：%s\n", formatByResult(res.ByResult))
	return nil
}

// formatByResult 按固定顺序格式化结果分布（输出稳定可测）。
func formatByResult(m map[dtt.Result]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[dtt.Result(k)]))
	}
	if len(parts) == 0 {
		return "（无）"
	}
	return strings.Join(parts, " / ")
}
