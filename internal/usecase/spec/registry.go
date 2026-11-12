package spec

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RegistryDoc 是 _registry.yaml 中一条文档登记项（lint 只关心这几个字段）。
type RegistryDoc struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	Domain  string `yaml:"domain"`
	Status  string `yaml:"status"`
	Version int    `yaml:"version"`
	File    string `yaml:"file"`
}

// RegistryDomain 是域名下的序号水位。
//
// NextItemSeq 的语义是「该域下一个可用条目号」，所以它必须**大于**域内已用的最大号：
// 两者相等就意味着下一次分配会撞上已有条目（README §3「序号域内单调、永不复用」）。
type RegistryDomain struct {
	Description string         `yaml:"description"`
	NextDocSeq  int            `yaml:"next_doc_seq"`
	NextItemSeq map[string]int `yaml:"next_item_seq"`
}

// LintRegistry 校验「文件 ↔ 登记表」双向一致，以及各域序号水位是否还够用。
//
// 这是 AGENTS.md §1.3「改 status 要 registry + 文档 front-matter 两处同步」的机器执行：
// 两处都写、都靠人记，必然漂（本项目实测已漂过 10 处 status/version）。
func LintRegistry(docs []Doc, reg *Registry) []Issue {
	if reg == nil || len(reg.ids) == 0 {
		return nil
	}
	var issues []Issue
	seen := make(map[string]bool, len(docs))

	for _, d := range docs {
		seen[d.ID] = true
		rd, ok := reg.Doc(d.ID)
		if !ok {
			issues = append(issues, Issue{"error", d.Path,
				fmt.Sprintf("文档 %s 未在 _registry.yaml 登记", d.ID)})
			continue
		}
		if rd.Status != d.Status {
			issues = append(issues, Issue{"error", d.Path,
				fmt.Sprintf("status 漂移：文件=%q 登记表=%q（两处必须同步，AGENTS.md §1.3）",
					d.Status, rd.Status)})
		}
		if rd.Version != d.Version {
			issues = append(issues, Issue{"error", d.Path,
				fmt.Sprintf("version 漂移：文件=%d 登记表=%d", d.Version, rd.Version)})
		}
		if rd.File != "" && !strings.HasSuffix(filepath.ToSlash(d.Path), rd.File) {
			issues = append(issues, Issue{"error", d.Path,
				fmt.Sprintf("登记表 file 字段=%q 与实际路径不符（文档挪位置后忘了改登记表）", rd.File)})
		}
	}

	// 反向：登记了但磁盘上没有——通常是删文件没删登记，或 file 路径写错。
	for id := range reg.ids {
		if !seen[id] {
			rd, _ := reg.Doc(id)
			issues = append(issues, Issue{"error", "spec/_registry.yaml",
				fmt.Sprintf("登记表声明了 %s（%s），但扫描不到对应文档", id, rd.File)})
		}
	}

	issues = append(issues, lintSeqWatermark(docs, reg)...)
	return issues
}

// lintSeqWatermark 检查域内序号水位是否已被用完（next <= 已用最大号 即撞号）。
//
// 只看 REQ/NFR/AC/ASM/DEC/TC：CON（约束）目前无文档使用，水位缺失不算问题。
func lintSeqWatermark(docs []Doc, reg *Registry) []Issue {
	// 域内聚合已用最大号：条目来自 SPEC/DESIGN 正文与 front-matter，用例来自 TEST 的 cases[]。
	maxUsed := map[string]map[string]int{}
	bump := func(domain, kind string, n int) {
		if domain == "" {
			return
		}
		if maxUsed[domain] == nil {
			maxUsed[domain] = map[string]int{}
		}
		if n > maxUsed[domain][kind] {
			maxUsed[domain][kind] = n
		}
	}
	for _, d := range docs {
		for _, it := range d.Items {
			if kind, n, ok := splitItemID(it.ID); ok {
				bump(d.Domain, kind, n)
			}
		}
		for _, dec := range d.Decisions {
			if kind, n, ok := splitItemID(dec.ID); ok {
				bump(d.Domain, kind, n)
			}
		}
		for _, c := range d.Cases {
			if kind, n, ok := splitItemID(c.ID); ok {
				bump(d.Domain, kind, n)
			}
		}
	}

	var issues []Issue
	for domain, kinds := range maxUsed {
		rd, ok := reg.Domain(domain)
		if !ok {
			issues = append(issues, Issue{"warning", "spec/_registry.yaml",
				fmt.Sprintf("域 %s 有文档但登记表没有该域的 next_item_seq 水位", domain)})
			continue
		}
		for kind, mx := range kinds {
			next, ok := rd.NextItemSeq[kind]
			if !ok {
				issues = append(issues, Issue{"warning", "spec/_registry.yaml",
					fmt.Sprintf("域 %s 缺 next_item_seq[%s]（文档已用到 %s-%02d）", domain, kind, kind, mx)})
				continue
			}
			if next <= mx {
				issues = append(issues, Issue{"error", "spec/_registry.yaml",
					fmt.Sprintf("域 %s 的 next_item_seq[%s]=%d，但文档已用到 %s-%02d——下一个新条目会撞号",
						domain, kind, next, kind, mx)})
			}
		}
	}
	return issues
}

// splitItemID 把 "REQ-03" 拆成 ("REQ", 3, true)。
func splitItemID(id string) (string, int, bool) {
	i := strings.LastIndexByte(id, '-')
	if i <= 0 || i == len(id)-1 {
		return "", 0, false
	}
	kind := id[:i]
	switch kind {
	case "REQ", "NFR", "AC", "ASM", "DEC", "CON", "TC":
	default:
		return "", 0, false
	}
	n := 0
	for _, c := range id[i+1:] {
		if c < '0' || c > '9' {
			return "", 0, false
		}
		n = n*10 + int(c-'0')
	}
	return kind, n, true
}
