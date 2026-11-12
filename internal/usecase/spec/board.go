// board.go：规格状态板（spec list）。
//
// 动机（2026-10-28）：spec/ 按 <领域>/<feature>/ 组织，状态藏在 front-matter 里，
// 想回答「现在有哪些 planned」要肉眼翻 69 份文档。本文件把扫描结果整理成
// 按状态分组的单行视图，输出 text / md / json 三种格式。
//
// 真源不变：状态仍以 front-matter + _registry.yaml 为准（lint 双向校验），
// 本文件只读不写。若两处漂移，行上标注 ⚠（lint --strict 会报 error，这里只是展示层兜底）。
package spec

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// Feature 是状态板的一行：一个 feature 的 SPEC 文档（每个 feature 恰好一份 SPEC）。
type Feature struct {
	ID     string // 如 SPEC-STORY-001
	Domain string
	Title  string
	Status string // front-matter 状态（真源之一）
	Path   string // 相对仓库根的路径，统一正斜杠，如 spec/story/state-machine/SPEC-STORY-001.md
	// RegistryStatus 是 _registry.yaml 登记的状态；为空表示未登记（lint 会报，这里不重复标）。
	RegistryStatus string
}

// Drifted 报告 front-matter 与登记表的状态是否漂移（AGENTS.md §1.3 两处必须同步）。
func (f Feature) Drifted() bool {
	return f.RegistryStatus != "" && f.RegistryStatus != f.Status
}

// statusOrder 是状态板展示顺序；未知状态追加在 discarded 之后，避免静默吞掉脏数据。
var statusOrder = []string{"planned", "developing", "implemented", "discarded"}

// Board 把扫描结果整理成按状态分组的 feature 列表（只取 SPEC 文档）。
// reg 允许为 nil（未加载登记表时 RegistryStatus 留空，不标漂移）。
func Board(docs []Doc, reg *Registry) []Feature {
	feats := make([]Feature, 0, len(docs))
	for _, d := range docs {
		if !strings.HasPrefix(d.ID, "SPEC-") {
			continue // DESIGN/TEST 同 feature 有自己的行会重复计组，只取 SPEC
		}
		f := Feature{
			ID:     d.ID,
			Domain: d.Domain,
			Title:  d.Title,
			Status: d.Status,
			Path:   filepath.ToSlash(d.Path),
		}
		if reg != nil {
			if rd, ok := reg.Doc(d.ID); ok {
				f.RegistryStatus = rd.Status
			}
		}
		feats = append(feats, f)
	}
	sort.Slice(feats, func(i, j int) bool {
		if feats[i].Status != feats[j].Status {
			return statusRank(feats[i].Status) < statusRank(feats[j].Status)
		}
		return feats[i].Path < feats[j].Path
	})
	return feats
}

// FilterStatus 返回指定状态的 feature（空串返回全部）。
func FilterStatus(feats []Feature, status string) []Feature {
	if status == "" {
		return feats
	}
	out := make([]Feature, 0, len(feats))
	for _, f := range feats {
		if f.Status == status {
			out = append(out, f)
		}
	}
	return out
}

func statusRank(s string) int {
	for i, v := range statusOrder {
		if v == s {
			return i
		}
	}
	return len(statusOrder) // 未知状态排最后
}

// PrintBoardText 输出人类可读状态板。
func PrintBoardText(w io.Writer, feats []Feature) {
	fmt.Fprintf(w, "规格状态板：共 %d 个 feature\n", len(feats))
	for _, st := range statusOrder {
		group := FilterStatus(feats, st)
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s（%d）\n", st, len(group))
		for _, f := range group {
			mark := ""
			if f.Drifted() {
				mark = fmt.Sprintf("  ⚠ 登记表=%s", f.RegistryStatus)
			}
			fmt.Fprintf(w, "  %-22s %-14s %s%s\n", f.ID, "["+f.Domain+"]", f.Title, mark)
			fmt.Fprintf(w, "    %s\n", f.Path)
		}
	}
	// 未知状态兜底展示。
	unknown := 0
	for _, f := range feats {
		if statusRank(f.Status) == len(statusOrder) {
			if unknown == 0 {
				fmt.Fprintln(w, "\n未知状态（需修复，lint 应报错）")
			}
			unknown++
			fmt.Fprintf(w, "  %-22s %-14s status=%q  %s\n", f.ID, "["+f.Domain+"]", f.Status, f.Path)
		}
	}
}

// PrintBoardMD 输出 Markdown 状态板（可重定向到 spec/STATUS.md，勿手改）。
func PrintBoardMD(w io.Writer, feats []Feature) {
	fmt.Fprintf(w, "# 规格状态板\n\n")
	fmt.Fprintf(w, "> 由 `regtoctl spec list --output md` 生成，勿手改；共 %d 个 feature。\n\n", len(feats))
	fmt.Fprintf(w, "| 状态 | 数量 |\n|---|---|\n")
	for _, st := range statusOrder {
		fmt.Fprintf(w, "| %s | %d |\n", st, len(FilterStatus(feats, st)))
	}
	fmt.Fprintf(w, "\n")
	for _, st := range statusOrder {
		group := FilterStatus(feats, st)
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "## %s（%d）\n\n", st, len(group))
		fmt.Fprintf(w, "| ID | 领域 | 标题 | 路径 |\n|---|---|---|---|\n")
		for _, f := range group {
			title := f.Title
			if f.Drifted() {
				title += fmt.Sprintf(" ⚠ 登记表=%s", f.RegistryStatus)
			}
			fmt.Fprintf(w, "| %s | %s | %s | `%s` |\n", f.ID, f.Domain, title, f.Path)
		}
		fmt.Fprintf(w, "\n")
	}
}
