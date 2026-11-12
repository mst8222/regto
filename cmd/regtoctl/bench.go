package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mst8222/regto/internal/adapter/persistence/postgres"
	"github.com/mst8222/regto/internal/domain/audit"
	"github.com/mst8222/regto/internal/domain/report"
	"github.com/mst8222/regto/internal/platform/config"
	"github.com/mst8222/regto/internal/platform/db"
)

// benchCmd 处理 `regtoctl bench ...`（SPEC-PERF-001）。
//
// 为什么要有这个命令：报表的 NFR 是「10 万行下 P95 < 500ms」，
// 只靠集成测试的手算 golden 验不出延迟；而延迟又必须在真实数据量下量，
// 不能靠 EXPLAIN 估算（估算会撒谎）。所以做成运维命令，
// 任何时候都能在准生产库上跑一遍拿真实分位数。
func benchCmd(args []string) error {
	if len(args) == 0 {
		fmt.Print(`用法：
  regtoctl bench report [--id 报表ID | --all] [--product N] [--iterations 20] [--threshold 500] [--json]
                                            统计每张报表的 P50/P95/max（毫秒）
  regtoctl bench report --list                  列出可测量的报表
  regtoctl bench audit [--id 审计报表ID | --all] [--iterations 20] [--threshold 500] [--json]
                                            统计每张审计报表的 P50/P95/max（毫秒）
  regtoctl bench http --path /api/v1/stories [--qps 100] [--duration 30s] \
             [--threshold 200] [--token JWT | --api-key KEY] [--concurrency N] [--json]
                                            固定 QPS 打读接口，输出分位数与错误率
`)
		return nil
	}
	switch args[0] {
	case "report":
		return benchReportCmd(args[1:])
	case "http":
		return benchHTTPCmd(args[1:])
	case "audit":
		return benchAuditCmd(args[1:])
	default:
		return fmt.Errorf("未知 bench 子命令 %q（目前只有 report / audit / http）", args[0])
	}
}

// benchReportCmd 跑报表延迟分位数。
func benchReportCmd(args []string) error {
	fs := flag.NewFlagSet("bench report", flag.ContinueOnError)
	id := fs.String("id", "", "报表 ID；为空或 --all 表示跑全部")
	all := fs.Bool("all", false, "跑全部 6 张报表")
	productID := fs.Int64("product", 0, "产品 ID；0 表示不限定产品（全局口径）")
	iterations := fs.Int("iterations", 20, "每张报表的采样次数")
	threshold := fs.Int("threshold", 500, "P95 门槛（毫秒）；超门槛以退出码 1 结束")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	list := fs.Bool("list", false, "列出可测量的报表并退出")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *list {
		for _, m := range report.All() {
			scope := "全局"
			if m.Scoped {
				scope = "按产品"
			}
			fmt.Printf("%-22s %-12s %s\n", m.ID, scope, m.Title)
		}
		return nil
	}
	if *iterations <= 0 {
		return fmt.Errorf("--iterations 必须为正数，当前 %d", *iterations)
	}

	ids := []report.ID{}
	if *id != "" && !*all {
		if !report.IsValid(*id) {
			return fmt.Errorf("未知报表 ID %q（用 --list 查看）", *id)
		}
		ids = append(ids, report.ID(*id))
	} else {
		for _, m := range report.All() {
			ids = append(ids, m.ID)
		}
	}

	ctx := context.Background()
	cfg, err := config.Load(0)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer pool.Close()

	repo := postgres.NewReportRepo(pool)
	var pids *int64
	if *productID > 0 {
		p := *productID
		pids = &p
	}

	type result struct {
		ID    string  `json:"id"`
		Rows  int     `json:"rows"`
		P50MS float64 `json:"p50_ms"`
		P95MS float64 `json:"p95_ms"`
		MaxMS float64 `json:"max_ms"`
		OK    bool    `json:"ok"`
	}
	results := make([]result, 0, len(ids))
	failed := 0

	// 先跑一次预热：让 PG 的计划缓存与共享缓冲进入稳定态，避免第一次的冷启动
	// 污染 P95（SPEC-PERF-001#DEC-02：预热不计入采样）。
	for _, rid := range ids {
		if _, err := repo.Query(ctx, rid, pids); err != nil {
			return fmt.Errorf("预热报表 %s 失败: %w", rid, err)
		}
	}

	for _, rid := range ids {
		samples := make([]float64, 0, *iterations)
		var rows int
		for i := 0; i < *iterations; i++ {
			start := time.Now()
			r, err := repo.Query(ctx, rid, pids)
			if err != nil {
				return fmt.Errorf("跑报表 %s 失败: %w", rid, err)
			}
			samples = append(samples, float64(time.Since(start).Microseconds())/1000.0)
			rows = len(r.Rows)
		}
		sort.Float64s(samples)
		p50 := percentile(samples, 0.50)
		p95 := percentile(samples, 0.95)
		max := samples[len(samples)-1]
		ok := p95 < float64(*threshold)
		if !ok {
			failed++
		}
		results = append(results, result{ID: string(rid), Rows: rows,
			P50MS: round1(p50), P95MS: round1(p95), MaxMS: round1(max), OK: ok})
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"iterations": *iterations,
			"threshold":  *threshold,
			"product_id": *productID,
			"results":    results,
		})
	}

	fmt.Printf("报表延迟（每张 %d 次采样，门槛 P95 < %dms，产品过滤=%s）\n",
		*iterations, *threshold, productDesc(*productID))
	fmt.Printf("%-22s %8s %10s %10s %10s %6s\n", "报表", "行数", "P50(ms)", "P95(ms)", "max(ms)", "判定")
	for _, r := range results {
		verdict := "PASS"
		if !r.OK {
			verdict = "FAIL"
		}
		fmt.Printf("%-22s %8d %10.1f %10.1f %10.1f %6s\n",
			r.ID, r.Rows, r.P50MS, r.P95MS, r.MaxMS, verdict)
	}
	if failed > 0 {
		return fmt.Errorf("%d 张报表 P95 超过 %dms 门槛", failed, *threshold)
	}
	return nil
}

// percentile 取线性插值分位数（样本已升序）。
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(pos)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := pos - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

func productDesc(id int64) string {
	if id <= 0 {
		return "全局"
	}
	return fmt.Sprintf("%d", id)
}

// benchAuditCmd 跑审计报表的延迟分位数（SPEC-AUDIT-001#NFR-01）。
//
// 与 bench report 分开：审计报表不接受 product_id（数据来自 traces，与产品无关），
// 混进同一个命令只会多一个「传了也被忽略」的参数。
func benchAuditCmd(args []string) error {
	fs := flag.NewFlagSet("bench audit", flag.ContinueOnError)
	id := fs.String("id", "", "审计报表 ID；为空或 --all 表示跑全部")
	all := fs.Bool("all", false, "跑全部 3 张审计报表")
	iterations := fs.Int("iterations", 20, "每张报表的采样次数")
	threshold := fs.Int("threshold", 500, "P95 门槛（毫秒）；超门槛以退出码 1 结束")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	list := fs.Bool("list", false, "列出可测量的审计报表并退出")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *list {
		for _, m := range audit.All() {
			fmt.Printf("%-24s %-8s %s\n", m.ID, m.Category, m.Title)
		}
		return nil
	}
	if *iterations <= 0 {
		return fmt.Errorf("--iterations 必须为正数，当前 %d", *iterations)
	}

	ids := []audit.ID{}
	if *id != "" && !*all {
		if !audit.IsValid(*id) {
			return fmt.Errorf("未知审计报表 ID %q（用 --list 查看）", *id)
		}
		ids = append(ids, audit.ID(*id))
	} else {
		for _, m := range audit.All() {
			ids = append(ids, m.ID)
		}
	}

	ctx := context.Background()
	cfg, err := config.Load(0)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer pool.Close()

	repo := postgres.NewAuditRepo(pool)
	// 预热一次（同 bench report：冷启动污染 P95）。
	for _, rid := range ids {
		if _, err := repo.Query(ctx, rid); err != nil {
			return fmt.Errorf("预热审计报表 %s 失败: %w", rid, err)
		}
	}

	type result struct {
		ID    string  `json:"id"`
		Rows  int     `json:"rows"`
		P50MS float64 `json:"p50_ms"`
		P95MS float64 `json:"p95_ms"`
		MaxMS float64 `json:"max_ms"`
		OK    bool    `json:"ok"`
	}
	results := make([]result, 0, len(ids))
	failed := 0
	for _, rid := range ids {
		samples := make([]float64, 0, *iterations)
		var rows int
		for i := 0; i < *iterations; i++ {
			start := time.Now()
			r, err := repo.Query(ctx, rid)
			if err != nil {
				return fmt.Errorf("跑审计报表 %s 失败: %w", rid, err)
			}
			samples = append(samples, float64(time.Since(start).Microseconds())/1000.0)
			rows = len(r.Rows)
		}
		sort.Float64s(samples)
		p50 := percentile(samples, 0.50)
		p95 := percentile(samples, 0.95)
		maxv := samples[len(samples)-1]
		ok := p95 < float64(*threshold)
		if !ok {
			failed++
		}
		results = append(results, result{ID: string(rid), Rows: rows,
			P50MS: round1(p50), P95MS: round1(p95), MaxMS: round1(maxv), OK: ok})
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"iterations": *iterations,
			"threshold":  *threshold,
			"results":    results,
		})
	}

	fmt.Printf("审计报表延迟（每张 %d 次采样，门槛 P95 < %dms）\n", *iterations, *threshold)
	fmt.Printf("%-24s %8s %10s %10s %10s %6s\n", "审计报表", "行数", "P50(ms)", "P95(ms)", "max(ms)", "判定")
	for _, r := range results {
		fmt.Printf("%-24s %8d %10.1f %10.1f %10.1f %6s\n",
			r.ID, r.Rows, r.P50MS, r.P95MS, r.MaxMS, verdict(r.OK))
	}
	if failed > 0 {
		return fmt.Errorf("%d 张审计报表 P95 超过 %dms 门槛", failed, *threshold)
	}
	return nil
}

// benchHTTPCmd 固定 QPS 打一个读接口（SPEC-HARDEN-001 REQ-02）。
//
// 为什么有了 k6 脚本还要这个：k6 未必装在每台机器上，
// 门槛不能因为工具缺席就没人验。这条命令零依赖，随时可跑，
// 且门槛值与 k6 脚本一致（200ms）——两边不一致就又成了双源真相。
func benchHTTPCmd(args []string) error {
	fs := flag.NewFlagSet("bench http", flag.ContinueOnError)
	path := fs.String("path", "", "要压的路径，例如 /api/v1/stories")
	base := fs.String("base", "http://127.0.0.1:8080", "服务地址")
	qps := fs.Int("qps", 100, "每秒请求数")
	duration := fs.Duration("duration", 30*time.Second, "持续时长")
	threshold := fs.Int("threshold", 200, "P95 门槛（毫秒）")
	maxErrRate := fs.Float64("max-error-rate", 0.01, "错误率上限（0.01 = 1%）")
	token := fs.String("token", "", "JWT（Authorization: Bearer）")
	apiKey := fs.String("api-key", "", "API Key（X-Regto-Api-Key）")
	concurrency := fs.Int("concurrency", 0, "并发 worker 数；0 表示按 qps 自动（每 10 QPS 一个，最少 4）")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("缺少 --path")
	}
	if *qps <= 0 || *duration <= 0 {
		return errors.New("--qps 与 --duration 必须为正")
	}
	if *concurrency <= 0 {
		*concurrency = *qps / 10
		if *concurrency < 4 {
			*concurrency = 4
		}
	}

	// 只压读接口（DEC-03）：写接口压测会往库里灌数据，那是压测噪声。
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(*base, "/")+*path, nil)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if *apiKey != "" {
		req.Header.Set("X-Regto-Api-Key", *apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var (
		mu       sync.Mutex
		samples  []float64
		errCount int
		total    int
		statuses = map[int]int{}
	)
	// 令牌桶用固定间隔发放：比 sleep 累积更稳，不会因为单次请求耗时而漂移。
	interval := time.Second / time.Duration(*qps)
	tickets := make(chan struct{}, *qps*2)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		deadline := time.After(*duration)
		for {
			select {
			case <-deadline:
				close(tickets)
				return
			case <-ticker.C:
				select {
				case tickets <- struct{}{}:
				default:
				}
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range tickets {
				r := req.Clone(context.Background())
				start := time.Now()
				resp, err := client.Do(r)
				ms := float64(time.Since(start).Microseconds()) / 1000.0
				if resp != nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
				mu.Lock()
				total++
				samples = append(samples, ms)
				if err != nil {
					errCount++
				} else {
					statuses[resp.StatusCode]++
					if resp.StatusCode >= 400 {
						errCount++
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	sort.Float64s(samples)
	if total == 0 {
		return errors.New("没有发出任何请求（服务没起？地址错？）")
	}
	p50 := percentile(samples, 0.50)
	p95 := percentile(samples, 0.95)
	maxv := samples[len(samples)-1]
	errRate := float64(errCount) / float64(total)
	ok := p95 < float64(*threshold) && errRate <= *maxErrRate

	statusList := make([]int, 0, len(statuses))
	for k := range statuses {
		statusList = append(statusList, k)
	}
	sort.Ints(statusList)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"path": *path, "qps": *qps, "duration": duration.String(),
			"requests": total, "p50_ms": round1(p50), "p95_ms": round1(p95),
			"max_ms": round1(maxv), "error_rate": errRate, "ok": ok,
			"statuses": statuses,
		})
	}

	fmt.Printf("压测 %s（目标 %d QPS，持续 %s，并发 %d）\n", *path, *qps, duration, *concurrency)
	fmt.Printf("请求数 %d  P50 %.1fms  P95 %.1fms  max %.1fms  错误率 %.2f%%\n",
		total, p50, p95, maxv, errRate*100)
	for _, s := range statusList {
		fmt.Printf("  HTTP %d: %d\n", s, statuses[s])
	}
	fmt.Printf("门槛：P95 < %dms 且错误率 <= %.2f%% → %s\n", *threshold, *maxErrRate*100, verdict(ok))
	if !ok {
		return fmt.Errorf("未达门槛（P95 %.1fms / 错误率 %.2f%%）", p95, errRate*100)
	}
	return nil
}

func verdict(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}
