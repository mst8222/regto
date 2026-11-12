// cmd/regtoctl 的发布域子命令：产品计划（plan）、发布（release）、构建（build）。
//
// 设计约定同域操作（ROADMAP §8 / AI_AGENT_RULES）：
//   - 所有命令支持 --output json（成功结果与失败的结构化 stderr 均可被 AI 可靠解析）；
//   - 领域错误以 {"code","message","meta"} 结构写 stderr，退出码 1。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	drel "github.com/mst8222/regto/internal/domain/release"
	uctrel "github.com/mst8222/regto/internal/usecase/release"
)

// ---------- plan ----------

// planCmd 是产品计划子命令组：create | list | show | transition | link-story | stories。
func planCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("plan 需要子命令：create | list | show | transition | link-story | stories")
	}
	switch args[0] {
	case "create":
		return planCreateCmd(args[1:])
	case "list":
		return planListCmd(args[1:])
	case "show":
		return planShowCmd(args[1:])
	case "transition":
		return planTransitionCmd(args[1:])
	case "link-story":
		return planLinkStoryCmd(args[1:])
	case "stories":
		return planStoriesCmd(args[1:])
	default:
		return fmt.Errorf("未知 plan 子命令 %q", args[0])
	}
}

// planCreateCmd 实现 `regtoctl plan create`（REQ-01；初始状态 wait）。
func planCreateCmd(args []string) error {
	fs := flag.NewFlagSet("plan create", flag.ContinueOnError)
	product := fs.Int64("product", 0, "所属产品 ID（必填）")
	title := fs.String("title", "", "计划标题（必填）")
	desc := fs.String("desc", "", "描述")
	begin := fs.String("begin", "", "开始日期 YYYY-MM-DD")
	end := fs.String("end", "", "结束日期 YYYY-MM-DD")
	parent := fs.Int64("parent", 0, "父计划 ID（0=顶层）")
	actor := fs.Int64("actor-id", 1, "执行主体账号 ID（缺省 1=系统管理员；opened_by_id NOT NULL）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *product <= 0 {
		return errors.New("缺少 --product（所属产品 ID）")
	}
	if *title == "" {
		return errors.New("缺少 --title（计划标题）")
	}
	beginMs, err := parseDateArg(*begin, "begin")
	if err != nil {
		return err
	}
	endMs, err := parseDateArg(*end, "end")
	if err != nil {
		return err
	}
	var parentID *int64
	if *parent > 0 {
		parentID = parent
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	p, err := svcs.releases.CreatePlan(ctx, uctrel.CreatePlanCommand{
		ProductID: *product, Title: *title, Desc: *desc, ParentID: parentID,
		BeginMs: beginMs, EndMs: endMs, ActorID: *actor,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	printPlan(*output, p)
	return nil
}

// planListCmd 实现 `regtoctl plan list`（分页/筛选）。
func planListCmd(args []string) error {
	fs := flag.NewFlagSet("plan list", flag.ContinueOnError)
	product := fs.Int64("product", 0, "按产品筛选")
	status := fs.String("status", "", "按状态筛选：wait|doing|done|closed")
	keyword := fs.String("keyword", "", "标题模糊匹配")
	page := fs.Int("page", 1, "页码")
	perPage := fs.Int("per-page", 20, "每页条数")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *status != "" && !drel.PlanStatus(*status).Valid() {
		return fmt.Errorf("非法 --status %q（合法值：wait|doing|done|closed）", *status)
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	plans, total, err := svcs.releases.ListPlans(ctx, uctrel.ListPlansQuery{
		ProductID: *product, Status: drel.PlanStatus(*status), Keyword: *keyword,
		Page: *page, PerPage: *perPage,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		items := make([]map[string]any, 0, len(plans))
		for _, p := range plans {
			items = append(items, planJSON(p))
		}
		b, err := json.Marshal(map[string]any{"data": items, "total": total})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	for _, p := range plans {
		printPlan("text", p)
	}
	fmt.Printf("共 %d 条（第 %d 页）\n", total, *page)
	return nil
}

// planShowCmd 实现 `regtoctl plan show ID`。
func planShowCmd(args []string) error {
	fs := flag.NewFlagSet("plan show", flag.ContinueOnError)
	id := fs.Int64("id", 0, "计划 ID（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return errors.New("缺少 --id（计划 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	p, err := svcs.releases.GetPlan(ctx, *id)
	if err != nil {
		writeDomainError(err)
		return err
	}
	printPlan(*output, p)
	return nil
}

// planTransitionCmd 实现 `regtoctl plan transition ID --action ...`（REQ-02）。
func planTransitionCmd(args []string) error {
	fs := flag.NewFlagSet("plan transition", flag.ContinueOnError)
	id := fs.Int64("id", 0, "计划 ID（必填）")
	action := fs.String("action", "", "转移动作：start|finish|close|activate（必填）")
	reason := fs.String("reason", "", "关闭原因（close 时写入 closed_reason）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return errors.New("缺少 --id（计划 ID）")
	}
	if *action == "" {
		return errors.New("缺少 --action（start|finish|close|activate）")
	}
	act := drel.PlanAction(*action)
	if !validPlanActionCLI(act) {
		return fmt.Errorf("非法 --action %q（合法值：start|finish|close|activate）", *action)
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	p, err := svcs.releases.TransitionPlan(ctx, uctrel.TransitionPlanCommand{
		PlanID: *id, Action: act, Reason: *reason,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	printPlan(*output, p)
	return nil
}

// planLinkStoryCmd 实现 `regtoctl plan link-story --plan ID --story ID`（REQ-03）。
func planLinkStoryCmd(args []string) error {
	fs := flag.NewFlagSet("plan link-story", flag.ContinueOnError)
	plan := fs.Int64("plan", 0, "计划 ID（必填）")
	story := fs.Int64("story", 0, "需求 ID（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *plan <= 0 {
		return errors.New("缺少 --plan（计划 ID）")
	}
	if *story <= 0 {
		return errors.New("缺少 --story（需求 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	if err := svcs.releases.LinkStory(ctx, uctrel.LinkStoryCommand{PlanID: *plan, StoryID: *story}); err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		b, err := json.Marshal(map[string]any{"plan_id": *plan, "story_id": *story, "linked": true})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("已关联：计划 #%d ← 需求 #%d\n", *plan, *story)
	return nil
}

// planStoriesCmd 实现 `regtoctl plan stories ID`（REQ-03 列表）。
func planStoriesCmd(args []string) error {
	fs := flag.NewFlagSet("plan stories", flag.ContinueOnError)
	id := fs.Int64("id", 0, "计划 ID（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return errors.New("缺少 --id（计划 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	links, err := svcs.releases.ListPlanStories(ctx, *id)
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		items := make([]map[string]any, 0, len(links))
		for _, l := range links {
			items = append(items, map[string]any{
				"id": l.ID, "plan_id": l.PlanID, "story_id": l.StoryID, "order": l.Order,
			})
		}
		b, err := json.Marshal(map[string]any{"data": items, "total": len(items)})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	for _, l := range links {
		fmt.Printf("计划 #%d ← 需求 #%d（order=%d）\n", l.PlanID, l.StoryID, l.Order)
	}
	fmt.Printf("共 %d 条\n", len(links))
	return nil
}

// printPlan 输出计划（text 一行摘要 / json 全字段）。
func printPlan(output string, p *drel.Plan) {
	if output == "json" {
		b, err := json.Marshal(planJSON(p))
		if err != nil {
			fmt.Fprintln(osStderr(), err)
			return
		}
		fmt.Println(string(b))
		return
	}
	fmt.Printf("计划 #%d %s（product=%d status=%s）\n", p.ID, p.Title, p.ProductID, p.Status)
}

// planJSON 把计划映射为 CLI 的 JSON 视图。
func planJSON(p *drel.Plan) map[string]any {
	return map[string]any{
		"id": p.ID, "product_id": p.ProductID, "title": p.Title,
		"status": string(p.Status), "desc": p.Desc,
		"begin": dateArgOf(p.BeginMs), "end": dateArgOf(p.EndMs),
	}
}

// ---------- release ----------

// releaseCmd 是发布子命令组：create | list | show | transition。
func releaseCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("release 需要子命令：create | list | show | transition")
	}
	switch args[0] {
	case "create":
		return releaseCreateCmd(args[1:])
	case "list":
		return releaseListCmd(args[1:])
	case "show":
		return releaseShowCmd(args[1:])
	case "transition":
		return releaseTransitionCmd(args[1:])
	default:
		return fmt.Errorf("未知 release 子命令 %q", args[0])
	}
}

// releaseCreateCmd 实现 `regtoctl release create`（REQ-04；初始状态 wait）。
func releaseCreateCmd(args []string) error {
	fs := flag.NewFlagSet("release create", flag.ContinueOnError)
	product := fs.Int64("product", 0, "所属产品 ID（必填）")
	name := fs.String("name", "", "发布名称（必填）")
	system := fs.String("system", "trunk", "系统类型：trunk|branch")
	marker := fs.Bool("marker", false, "是否里程碑发布")
	date := fs.String("date", "", "计划发布日期 YYYY-MM-DD")
	desc := fs.String("desc", "", "描述")
	actor := fs.Int64("actor-id", 1, "执行主体账号 ID（缺省 1=系统管理员；opened_by_id NOT NULL）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *product <= 0 {
		return errors.New("缺少 --product（所属产品 ID）")
	}
	if *name == "" {
		return errors.New("缺少 --name（发布名称）")
	}
	sys := drel.System(*system)
	if !sys.Valid() {
		return fmt.Errorf("非法 --system %q（合法值：trunk|branch）", *system)
	}
	dateMs, err := parseDateArg(*date, "date")
	if err != nil {
		return err
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	rel, err := svcs.releases.CreateRelease(ctx, uctrel.CreateReleaseCommand{
		ProductID: *product, Name: *name, System: sys, Marker: *marker,
		DateMs: dateMs, Desc: *desc, ActorID: *actor,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	printRelease(*output, rel)
	return nil
}

// releaseListCmd 实现 `regtoctl release list`（分页/筛选）。
func releaseListCmd(args []string) error {
	fs := flag.NewFlagSet("release list", flag.ContinueOnError)
	product := fs.Int64("product", 0, "按产品筛选")
	status := fs.String("status", "", "按状态筛选：wait|normal|fail|terminate")
	keyword := fs.String("keyword", "", "名称模糊匹配")
	page := fs.Int("page", 1, "页码")
	perPage := fs.Int("per-page", 20, "每页条数")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *status != "" && !drel.ReleaseStatus(*status).Valid() {
		return fmt.Errorf("非法 --status %q（合法值：wait|normal|fail|terminate）", *status)
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	rels, total, err := svcs.releases.ListReleases(ctx, uctrel.ListReleasesQuery{
		ProductID: *product, Status: drel.ReleaseStatus(*status), Keyword: *keyword,
		Page: *page, PerPage: *perPage,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		items := make([]map[string]any, 0, len(rels))
		for _, r := range rels {
			items = append(items, releaseJSON(r))
		}
		b, err := json.Marshal(map[string]any{"data": items, "total": total})
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	for _, r := range rels {
		printRelease("text", r)
	}
	fmt.Printf("共 %d 条（第 %d 页）\n", total, *page)
	return nil
}

// releaseShowCmd 实现 `regtoctl release show ID`。
func releaseShowCmd(args []string) error {
	fs := flag.NewFlagSet("release show", flag.ContinueOnError)
	id := fs.Int64("id", 0, "发布 ID（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return errors.New("缺少 --id（发布 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	rel, err := svcs.releases.GetRelease(ctx, *id)
	if err != nil {
		writeDomainError(err)
		return err
	}
	printRelease(*output, rel)
	return nil
}

// releaseTransitionCmd 实现 `regtoctl release transition ID --action ...`（REQ-05）。
func releaseTransitionCmd(args []string) error {
	fs := flag.NewFlagSet("release transition", flag.ContinueOnError)
	id := fs.Int64("id", 0, "发布 ID（必填）")
	action := fs.String("action", "", "转移动作：publish|pause|terminate|play（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return errors.New("缺少 --id（发布 ID）")
	}
	if *action == "" {
		return errors.New("缺少 --action（publish|pause|terminate|play）")
	}
	act := drel.ReleaseAction(*action)
	if !validReleaseActionCLI(act) {
		return fmt.Errorf("非法 --action %q（合法值：publish|pause|terminate|play）", *action)
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	rel, err := svcs.releases.TransitionRelease(ctx, uctrel.TransitionReleaseCommand{ReleaseID: *id, Action: act})
	if err != nil {
		writeDomainError(err)
		return err
	}
	printRelease(*output, rel)
	return nil
}

// printRelease 输出发布（text 一行摘要 / json 全字段）。
func printRelease(output string, r *drel.Release) {
	if output == "json" {
		b, err := json.Marshal(releaseJSON(r))
		if err != nil {
			fmt.Fprintln(osStderr(), err)
			return
		}
		fmt.Println(string(b))
		return
	}
	fmt.Printf("发布 #%d %s（product=%d status=%s system=%s）\n", r.ID, r.Name, r.ProductID, r.Status, r.System)
}

// releaseJSON 把发布映射为 CLI 的 JSON 视图。
func releaseJSON(r *drel.Release) map[string]any {
	return map[string]any{
		"id": r.ID, "product_id": r.ProductID, "name": r.Name,
		"status": string(r.Status), "system": string(r.System), "marker": r.Marker,
		"desc": r.Desc, "date": dateArgOf(r.DateMs),
	}
}

// ---------- build ----------

// buildCmd 是构建子命令组：create | list | show。
func buildCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("build 需要子命令：create | list | show")
	}
	switch args[0] {
	case "create":
		return buildCreateCmd(args[1:])
	case "list":
		return buildListCmd(args[1:])
	case "show":
		return buildShowCmd(args[1:])
	default:
		return fmt.Errorf("未知 build 子命令 %q", args[0])
	}
}

// buildCreateCmd 实现 `regtoctl build create`（REQ-06；无状态机）。
func buildCreateCmd(args []string) error {
	fs := flag.NewFlagSet("build create", flag.ContinueOnError)
	product := fs.Int64("product", 0, "所属产品 ID（必填）")
	name := fs.String("name", "", "构建名称（必填）")
	builds := fs.String("builds", "", "构建号（必填，产品内唯一）")
	system := fs.String("system", "trunk", "系统类型：trunk|branch")
	date := fs.String("date", "", "构建日期 YYYY-MM-DD")
	builder := fs.String("builder", "", "构建人")
	desc := fs.String("desc", "", "描述")
	actor := fs.Int64("actor-id", 1, "执行主体账号 ID（缺省 1=系统管理员；opened_by_id NOT NULL）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *product <= 0 {
		return errors.New("缺少 --product（所属产品 ID）")
	}
	if *name == "" {
		return errors.New("缺少 --name（构建名称）")
	}
	if *builds == "" {
		return errors.New("缺少 --builds（构建号）")
	}
	sys := drel.System(*system)
	if !sys.Valid() {
		return fmt.Errorf("非法 --system %q（合法值：trunk|branch）", *system)
	}
	dateMs, err := parseDateArg(*date, "date")
	if err != nil {
		return err
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	b, err := svcs.releases.CreateBuild(ctx, uctrel.CreateBuildCommand{
		ProductID: *product, Name: *name, Builds: *builds, System: sys,
		DateMs: dateMs, Builder: *builder, Desc: *desc, ActorID: *actor,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	printBuild(*output, b)
	return nil
}

// buildListCmd 实现 `regtoctl build list`（分页/筛选）。
func buildListCmd(args []string) error {
	fs := flag.NewFlagSet("build list", flag.ContinueOnError)
	product := fs.Int64("product", 0, "按产品筛选")
	keyword := fs.String("keyword", "", "名称模糊匹配")
	page := fs.Int("page", 1, "页码")
	perPage := fs.Int("per-page", 20, "每页条数")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	builds, total, err := svcs.releases.ListBuilds(ctx, uctrel.ListBuildsQuery{
		ProductID: *product, Keyword: *keyword, Page: *page, PerPage: *perPage,
	})
	if err != nil {
		writeDomainError(err)
		return err
	}
	if *output == "json" {
		items := make([]map[string]any, 0, len(builds))
		for _, b := range builds {
			items = append(items, buildJSON(b))
		}
		out, err := json.Marshal(map[string]any{"data": items, "total": total})
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	for _, b := range builds {
		printBuild("text", b)
	}
	fmt.Printf("共 %d 条（第 %d 页）\n", total, *page)
	return nil
}

// buildShowCmd 实现 `regtoctl build show ID`。
func buildShowCmd(args []string) error {
	fs := flag.NewFlagSet("build show", flag.ContinueOnError)
	id := fs.Int64("id", 0, "构建 ID（必填）")
	output := fs.String("output", "text", "输出格式：text | json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id <= 0 {
		return errors.New("缺少 --id（构建 ID）")
	}

	ctx := context.Background()
	svcs, err := openDomainServices(ctx)
	if err != nil {
		return err
	}
	defer svcs.close()

	b, err := svcs.releases.GetBuild(ctx, *id)
	if err != nil {
		writeDomainError(err)
		return err
	}
	printBuild(*output, b)
	return nil
}

// printBuild 输出构建（text 一行摘要 / json 全字段）。
func printBuild(output string, b *drel.Build) {
	if output == "json" {
		out, err := json.Marshal(buildJSON(b))
		if err != nil {
			fmt.Fprintln(osStderr(), err)
			return
		}
		fmt.Println(string(out))
		return
	}
	fmt.Printf("构建 #%d %s（product=%d builds=%s system=%s）\n", b.ID, b.Name, b.ProductID, b.Builds, b.System)
}

// buildJSON 把构建映射为 CLI 的 JSON 视图。
func buildJSON(b *drel.Build) map[string]any {
	return map[string]any{
		"id": b.ID, "product_id": b.ProductID, "name": b.Name, "builds": b.Builds,
		"system": string(b.System), "builder": b.Builder, "desc": b.Desc,
		"date": dateArgOf(b.DateMs),
	}
}

// ---------- 共用辅助 ----------

// parseDateArg 解析 YYYY-MM-DD 日期参数（空串返回 nil）。
func parseDateArg(raw, field string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	ms, err := parseDateMs(raw)
	if err != nil {
		return nil, fmt.Errorf("非法 --%s %q（格式必须为 YYYY-MM-DD）", field, raw)
	}
	return ms, nil
}

// dateArgOf 把毫秒指针转为 YYYY-MM-DD（nil 返回空串）。
func dateArgOf(ms *int64) string {
	if ms == nil || *ms == 0 {
		return ""
	}
	return formatDateMs(*ms)
}

// validPlanActionCLI 报告计划动作是否合法。
func validPlanActionCLI(a drel.PlanAction) bool {
	for _, x := range drel.AllPlanActions() {
		if x == a {
			return true
		}
	}
	return false
}

// validReleaseActionCLI 报告发布动作是否合法。
func validReleaseActionCLI(a drel.ReleaseAction) bool {
	for _, x := range drel.AllReleaseActions() {
		if x == a {
			return true
		}
	}
	return false
}
