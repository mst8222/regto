// Package spec 扫描仓库内 SpecDD 三类文档，提供 lint 与覆盖率计算。
// 这是「AC 必须有对应用例」硬闸门的落地处（ROADMAP §4.5）。
package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// 状态机取值（spec/README.md）。
var validStatuses = []string{"planned", "developing", "implemented", "discarded"}

// itemHeader 匹配 Markdown 中的规格条目标题，如 "### REQ-01 xxx"、"### DEC-01 xxx"。
var itemHeader = regexp.MustCompile(`(?m)^#{2,4}\s+((?:REQ|NFR|AC|ASM|DEC)-\d+)\s*(.*)$`)

// Item 是一条规格条目（REQ/NFR/AC/ASM/DEC）。
type Item struct {
	ID    string
	Title string
	Body  string // 条目正文（到下一个条目标题或章节标题为止）
	// LineNo 是条目在**剥离 front-matter 后的正文**中的行号（1 起）。
	LineNo int
}

// maxItemBody 是条目正文入库前的截断长度（防止整篇正文塞满 spec_items）。
const maxItemBody = 4000

// Decision 是设计规格中的一条决策（DESIGN front-matter 的 decisions[]）。
type Decision struct {
	ID     string   `yaml:"id"`
	Title  string   `yaml:"title"`
	Covers []string `yaml:"covers"`
}

// CheckPoint 是用例步骤中的一个校验点（与 ZTF 的 checkPoints 同构）。
type CheckPoint struct {
	Expect string `yaml:"expect"`
	Actual string `yaml:"actual"`
}

// Step 是用例的一个步骤（ROADMAP §4.4：steps[] → case_steps）。
type Step struct {
	Name        string       `yaml:"name"`
	Status      string       `yaml:"status"`
	CheckPoints []CheckPoint `yaml:"checkPoints"`
}

// Case 是用例（TEST 文档中的 cases 项）。
type Case struct {
	ID           string   `yaml:"id"`
	Title        string   `yaml:"title"`
	Covers       []string `yaml:"covers"`
	Verifies     []string `yaml:"verifies"`
	Priority     string   `yaml:"priority"`
	Precondition string   `yaml:"precondition"`
	Steps        []Step   `yaml:"steps"`
}

// Doc 是一份规格文档（SPEC / DESIGN / TEST 三类通用）。
type Doc struct {
	ID         string     `yaml:"id"`
	Type       string     `yaml:"type"`
	Domain     string     `yaml:"domain"`
	Title      string     `yaml:"title"`
	Status     string     `yaml:"status"`
	Version    int        `yaml:"version"`
	Owner      string     `yaml:"owner"`
	SpecID     string     `yaml:"spec_id"`
	DesignID   string     `yaml:"design_id"`
	Suite      string     `yaml:"suite"`
	Automation string     `yaml:"automation"`
	Cases      []Case     `yaml:"cases"`
	Decisions  []Decision `yaml:"decisions"`

	// 非 YAML 字段（解析后填充）
	Path  string
	Items []Item
}

// Scan 递归扫描 root 目录，解析全部 SPEC-*.md / DESIGN-*.md / TEST-*.yaml。
// 跳过 _templates（模板不是真规格）与 _registry / README 等非规格文件。
func Scan(root string) ([]Doc, error) {
	var docs []Doc
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "_templates" || d.Name() == "plans" || d.Name() == "anchor" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		base := strings.ToUpper(name)
		isSpec := strings.HasPrefix(base, "SPEC-") && strings.HasSuffix(base, ".MD")
		isDesign := strings.HasPrefix(base, "DESIGN-") && strings.HasSuffix(base, ".MD")
		isTest := strings.HasPrefix(base, "TEST-") && (strings.HasSuffix(base, ".YAML") || strings.HasSuffix(base, ".YML"))
		if !isSpec && !isDesign && !isTest {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", path, err)
		}
		content := string(raw)
		if isTest {
			doc, err := parseYAMLDoc(content)
			if err != nil {
				return fmt.Errorf("解析 %s 失败: %w", path, err)
			}
			doc.Path = path
			docs = append(docs, doc)
			return nil
		}
		fm, body := splitFrontMatter(content)
		doc, err := parseYAMLDoc(fm)
		if err != nil {
			return fmt.Errorf("解析 %s 的 front-matter 失败: %w", path, err)
		}
		doc.Path = path
		doc.Items = extractItems(body)
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

func parseYAMLDoc(s string) (Doc, error) {
	var d Doc
	if strings.TrimSpace(s) == "" {
		return d, nil
	}
	if err := yaml.Unmarshal([]byte(s), &d); err != nil {
		return d, err
	}
	return d, nil
}

// splitFrontMatter 分离 Markdown 的 YAML front-matter 与正文。
func splitFrontMatter(content string) (string, string) {
	if !strings.HasPrefix(content, "---") {
		return "", content
	}
	rest := content[3:]
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", content
	}
	return rest[:end], rest[end+4:]
}

// extractItems 从正文提取规格条目及其正文片段。
// 每条条目的正文取「本标题之后 → 下一个条目标题或更上层章节标题」之间的文本，
// 使 `spec_items.body` 可被检索与追溯（条目不只是标题）。
func extractItems(body string) []Item {
	locs := itemHeader.FindAllStringSubmatchIndex(body, -1)
	out := make([]Item, 0, len(locs))
	for i, loc := range locs {
		start := nextLineStart(body, loc[1])
		end := len(body)
		if i+1 < len(locs) && locs[i+1][0] < end {
			end = locs[i+1][0]
		}
		// 遇到更上层章节标题（## 或 #）时提前结束，避免把下一段内容算进来。
		if k := strings.Index(body[start:end], "\n##"); k >= 0 {
			end = start + k
		}
		text := strings.TrimSpace(body[start:end])
		if len(text) > maxItemBody {
			text = text[:maxItemBody]
		}
		out = append(out, Item{
			ID:     body[loc[2]:loc[3]],
			Title:  strings.TrimSpace(body[loc[4]:loc[5]]),
			Body:   text,
			LineNo: strings.Count(body[:loc[0]], "\n") + 1,
		})
	}
	return out
}

// nextLineStart 返回 pos 之后下一行的起始下标（跳过当前行剩余内容）。
func nextLineStart(s string, pos int) int {
	if i := strings.IndexByte(s[pos:], '\n'); i >= 0 {
		return pos + i + 1
	}
	return len(s)
}

// Issue 是一条 lint 问题。
type Issue struct {
	Level   string // error | warning
	Path    string
	Message string
}

// Lint 校验规格文档的完整性与一致性。strict=true 时启用硬闸门：
// 每条 AC-xx 必须至少被一条用例 covers（ROADMAP §4.5）。
//
// 不带登记表调用时跳过「文件 ↔ 登记表」一致性校验（单元测试用短文档时不必造一份登记表）；
// 真正跑闸门请用 LintWithRegistry。
func Lint(docs []Doc, strict bool) []Issue {
	return LintWithRegistry(docs, nil, strict)
}

// LintWithRegistry 在 Lint 基础上追加「文件 ↔ _registry.yaml」双向一致性与序号水位校验。
func LintWithRegistry(docs []Doc, reg *Registry, strict bool) []Issue {
	var issues []Issue
	byID := map[string]Doc{}
	for _, d := range docs {
		byID[d.ID] = d
	}
	for _, d := range docs {
		if !validStatus(d.Status) {
			issues = append(issues, Issue{"error", d.Path,
				fmt.Sprintf("status %q 非法，允许值：%v", d.Status, validStatuses)})
		}
		if d.Type == "requirement" {
			if _, ok := byID[strings.Replace(d.ID, "SPEC-", "DESIGN-", 1)]; !ok {
				issues = append(issues, Issue{"warning", d.Path, "缺少对应的 DESIGN 文档"})
			}
			if _, ok := byID[strings.Replace(d.ID, "SPEC-", "TEST-", 1)]; !ok {
				issues = append(issues, Issue{"error", d.Path, "缺少对应的 TEST 文档"})
			}
		}
		if d.Type == "design" && strict {
			// 设计覆盖率（spec coverage）只认 front-matter 的 decisions[].covers。
			// 决策写在正文里而 front-matter 空着，覆盖率会静默算成 0——本项目实测有
			// 9 份 DESIGN 栽在这里，所以把它做成闸门而不是文档建议。
			if len(d.Decisions) == 0 {
				issues = append(issues, Issue{"error", d.Path,
					"DESIGN 的 front-matter 缺少 decisions[]（正文写了决策也要登记，否则覆盖率恒为 0）"})
			}
		}
		if strict && d.Type == "requirement" {
			for _, it := range d.Items {
				if !strings.HasPrefix(it.ID, "AC-") {
					continue
				}
				ref := d.ID + "#" + it.ID
				if countCovering(docs, ref) == 0 {
					issues = append(issues, Issue{"error", d.Path,
						fmt.Sprintf("验收标准 %s 无任何用例覆盖（硬闸门）", ref)})
				}
			}
		}
	}
	// AGENTS.md §1.2 联动：SPEC 一旦 developing/implemented，其 DESIGN 必须已 implemented
	// （「SPEC 进入 developing 前，其 DESIGN 必须已 implemented」）。
	if strict {
		for _, d := range docs {
			if d.Type != "requirement" || (d.Status != "developing" && d.Status != "implemented") {
				continue
			}
			des, ok := byID[strings.Replace(d.ID, "SPEC-", "DESIGN-", 1)]
			if ok && des.Status != "implemented" {
				issues = append(issues, Issue{"error", d.Path,
					fmt.Sprintf("SPEC 已是 %s，但 %s 仍是 %s（联动规则要求 DESIGN 先 implemented）",
						d.Status, des.ID, des.Status)})
			}
		}
	}
	issues = append(issues, LintRegistry(docs, reg)...)
	return issues
}

// countCovering 统计覆盖指定条目引用的用例数。
func countCovering(docs []Doc, ref string) int {
	n := 0
	for _, d := range docs {
		for _, c := range d.Cases {
			for _, cov := range c.Covers {
				if strings.TrimSpace(cov) == ref {
					n++
				}
			}
		}
	}
	return n
}

// Coverage 计算指定 SPEC 的覆盖矩阵：条目 → 设计覆盖 / 用例覆盖。
type Coverage struct {
	SpecID     string
	Items      []ItemCoverage
	DesignRate float64
	CaseRate   float64
}

// ItemCoverage 是单个条目的覆盖情况。
type ItemCoverage struct {
	Item    Item
	Designs []string
	Cases   []string
}

// Cover 生成覆盖报告（ROADMAP §4.5 追溯矩阵）。
func Cover(docs []Doc, specID string) Coverage {
	var spec Doc
	for _, d := range docs {
		if d.ID == specID {
			spec = d
			break
		}
	}
	out := Coverage{SpecID: specID}
	designID := strings.Replace(specID, "SPEC-", "DESIGN-", 1)
	for _, it := range spec.Items {
		ref := specID + "#" + it.ID
		ic := ItemCoverage{Item: it}
		// 设计覆盖：取 DESIGN 文档中 covers 指向本条目的决策（decisions[].covers）。
		for _, d := range docs {
			if d.ID != designID {
				continue
			}
			for _, dec := range d.Decisions {
				for _, cov := range dec.Covers {
					if strings.TrimSpace(cov) == ref {
						ic.Designs = append(ic.Designs, dec.ID)
					}
				}
			}
		}
		for _, d := range docs {
			for _, c := range d.Cases {
				for _, cov := range c.Covers {
					if strings.TrimSpace(cov) == ref {
						ic.Cases = append(ic.Cases, c.ID)
					}
				}
			}
		}
		out.Items = append(out.Items, ic)
	}
	coveredByCase, coveredByDesign := 0, 0
	for _, ic := range out.Items {
		if len(ic.Cases) > 0 {
			coveredByCase++
		}
		if len(ic.Designs) > 0 {
			coveredByDesign++
		}
	}
	if n := len(out.Items); n > 0 {
		out.CaseRate = float64(coveredByCase) / float64(n) * 100
		out.DesignRate = float64(coveredByDesign) / float64(n) * 100
	}
	return out
}

// Uncovered 返回缺少用例覆盖的**需求条目** ID（ROADMAP M3 验收：未覆盖时退出码 1）。
//
// 只约束 REQ：NFR 常无对应决策、ASM/ASM 类条目也不一定有 TC，
// 把它们算进硬闸门会让覆盖率命令永远红（F4 失败信号：lint 常红导致人类绕过）。
func (c Coverage) Uncovered() []string {
	var out []string
	for _, ic := range c.Items {
		if !strings.HasPrefix(ic.Item.ID, "REQ-") {
			continue
		}
		if len(ic.Cases) == 0 {
			out = append(out, ic.Item.ID)
		}
	}
	return out
}

// Complete 报告是否每个需求条目都有用例覆盖。
func (c Coverage) Complete() bool {
	return len(c.Items) > 0 && len(c.Uncovered()) == 0
}

func validStatus(s string) bool {
	for _, v := range validStatuses {
		if v == s {
			return true
		}
	}
	return false
}
