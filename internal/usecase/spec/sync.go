// Package spec 提供 SpecDD 文档的扫描与单向同步（文件 → DB）。
// 依赖方向：usecase → domain；文件系统是只读输入，绝无回写（DESIGN-SPEC-001 DEC-04）。
package spec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	dspec "github.com/mst8222/regto/internal/domain/spec"
)

// Registry 是 _registry.yaml 的登记表视图（AGENTS.md §1.3）。
// 除了「ID 是否登记」，它还承载 lint 要用的 status/version/水位信息——
// 两处各写一份必然漂，所以只解析一次、由这里统一提供。
type Registry struct {
	ids     map[string]struct{}
	docs    map[string]RegistryDoc
	domains map[string]RegistryDomain
}

// Has 报告文档 ID 是否已登记。
func (r Registry) Has(id string) bool {
	_, ok := r.ids[id]
	return ok
}

// Doc 返回登记项（status/version/file）。
func (r Registry) Doc(id string) (RegistryDoc, bool) {
	d, ok := r.docs[id]
	return d, ok
}

// Domain 返回某个域的序号水位。
func (r Registry) Domain(name string) (RegistryDomain, bool) {
	d, ok := r.domains[name]
	return d, ok
}

// LoadRegistry 加载登记表。登记是写新文档的**前置条件**（NFR-02）。
func LoadRegistry(path string) (Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	var doc struct {
		Domains   map[string]RegistryDomain `yaml:"domains"`
		Documents []struct {
			ID      string `yaml:"id"`
			Type    string `yaml:"type"`
			Domain  string `yaml:"domain"`
			Status  string `yaml:"status"`
			Version int    `yaml:"version"`
			File    string `yaml:"file"`
		} `yaml:"documents"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Registry{}, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	reg := Registry{
		ids:     make(map[string]struct{}, len(doc.Documents)),
		docs:    make(map[string]RegistryDoc, len(doc.Documents)),
		domains: doc.Domains,
	}
	for _, d := range doc.Documents {
		reg.ids[d.ID] = struct{}{}
		reg.docs[d.ID] = RegistryDoc{
			ID: d.ID, Type: d.Type, Domain: d.Domain,
			Status: d.Status, Version: d.Version, File: d.File,
		}
	}
	return reg, nil
}

// Syncer 执行规格同步。
type Syncer struct {
	repo dspec.Repository
}

// NewSyncer 构造同步器。
func NewSyncer(repo dspec.Repository) *Syncer {
	return &Syncer{repo: repo}
}

// Sync 把 root 下的三类文档同步进数据库。
// 全量重扫而非增量：规格文档量在百级，全量成本可忽略，
// 而 mtime 在 clone/CI 环境不可靠（DEC-05）。
func (s *Syncer) Sync(ctx context.Context, root string, reg Registry) (dspec.SyncResult, error) {
	start := time.Now()
	docs, err := Scan(root)
	if err != nil {
		return dspec.SyncResult{}, err
	}

	// 未登记的文档一律拒绝入库：先登记再写文档（NFR-02）。
	for _, d := range docs {
		if d.ID != "" && !reg.Has(d.ID) {
			return dspec.SyncResult{}, fmt.Errorf("文档 %s 未在 %s 登记：请先登记 ID 再创建文档",
				d.ID, filepath.Join(root, "_registry.yaml"))
		}
	}

	domainDocs, err := toDomainDocs(root, docs)
	if err != nil {
		return dspec.SyncResult{}, err
	}

	var result dspec.SyncResult
	err = s.repo.WithTx(ctx, func(tx dspec.Repository) error {
		// 收集本次扫到的全部文件与用例引用，用于把「文件已删除」的索引标为软删。
		filePaths := make([]string, 0, len(domainDocs))
		sourceRefs := make([]string, 0, 16)

		for i := range domainDocs {
			doc := &domainDocs[i]
			filePaths = append(filePaths, doc.FilePath)
			changed, err := tx.UpsertDocument(ctx, doc)
			if err != nil {
				return err
			}
			if changed {
				result.Changed++
			}
			result.Documents++

			if err := tx.ReplaceItems(ctx, doc.DocID, doc.Items); err != nil {
				return err
			}
			result.Items += len(doc.Items)

			for j := range doc.Cases {
				c := &doc.Cases[j]
				caseID, err := tx.UpsertCase(ctx, c)
				if err != nil {
					return err
				}
				sourceRefs = append(sourceRefs, c.SourceRef)
				if err := tx.ReplaceSteps(ctx, caseID, c.Steps); err != nil {
					return err
				}
				result.Cases++
				result.Steps += len(c.Steps)

				for _, ref := range c.Covers {
					if err := tx.AddLink(ctx, dspec.Link{
						SourceType: "case", SourceRef: c.SourceRef,
						LinkType: dspec.LinkCovers, TargetType: "item", TargetRef: ref,
						Strength: 100, Origin: dspec.LinkOriginFile,
					}); err != nil {
						return err
					}
					result.Links++
				}
				for _, ref := range c.Verifies {
					if err := tx.AddLink(ctx, dspec.Link{
						SourceType: "case", SourceRef: c.SourceRef,
						LinkType: dspec.LinkVerifies, TargetType: "decision", TargetRef: ref,
						Strength: 100, Origin: dspec.LinkOriginFile,
					}); err != nil {
						return err
					}
					result.Links++
				}
			}
		}
		return tx.MarkDeletedExcept(ctx, filePaths, sourceRefs)
	})
	if err != nil {
		return dspec.SyncResult{}, err
	}
	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// toDomainDocs 把扫描结果转换为领域文档（含内容哈希与路径规范化）。
func toDomainDocs(root string, docs []Doc) ([]dspec.Document, error) {
	out := make([]dspec.Document, 0, len(docs))
	for _, d := range docs {
		if d.ID == "" {
			continue
		}
		hash, relPath, err := fileDigest(root, d.Path)
		if err != nil {
			return nil, err
		}
		doc := dspec.Document{
			DocID:       d.ID,
			DocType:     docTypeOf(d),
			Domain:      d.Domain,
			Title:       d.Title,
			Status:      d.Status,
			Version:     d.Version,
			Owner:       d.Owner,
			FilePath:    relPath,
			ContentHash: hash,
		}
		for _, it := range d.Items {
			doc.Items = append(doc.Items, dspec.Item{
				ItemKey:  it.ID,
				ItemType: itemTypeOf(it.ID),
				Title:    it.Title,
				Body:     it.Body,
				LineNo:   it.LineNo,
			})
		}
		if d.Type == "test" {
			for _, c := range d.Cases {
				tc := dspec.Case{
					CaseKey:    c.ID,
					Title:      c.Title,
					Suite:      d.Suite,
					Automation: d.Automation,
					Priority:   c.Priority,
					SourceRef:  fmt.Sprintf("%s#%s", relPath, c.ID),
					SourceHash: hash,
					SpecDocID:  d.ID,
					Covers:     c.Covers,
					Verifies:   c.Verifies,
				}
				for i, st := range c.Steps {
					tc.Steps = append(tc.Steps, dspec.Step{
						Seq:    i + 1,
						Name:   st.Name,
						Checks: checksJSON(st.CheckPoints),
					})
				}
				doc.Cases = append(doc.Cases, tc)
			}
		}
		out = append(out, doc)
	}
	return out, nil
}

// fileDigest 计算文件内容的 sha256 与仓库相对路径（只读打开，绝不写入）。
func fileDigest(root, path string) (hash, rel string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	rel, err = filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	return hex.EncodeToString(sum[:]), filepath.ToSlash(rel), nil
}

// docTypeOf 映射扫描结果到领域文档类型。
func docTypeOf(d Doc) string {
	switch d.Type {
	case "test":
		return dspec.TypeTest
	case "design":
		return dspec.TypeDesign
	}
	return dspec.TypeRequirement
}

// itemTypeOf 由条目 ID 前缀判定条目类型。
func itemTypeOf(id string) string {
	switch {
	case strings.HasPrefix(id, "REQ"):
		return "REQ"
	case strings.HasPrefix(id, "NFR"):
		return "NFR"
	case strings.HasPrefix(id, "AC"):
		return "AC"
	case strings.HasPrefix(id, "ASM"):
		return "ASM"
	case strings.HasPrefix(id, "DEC"):
		return "DEC"
	}
	return "REQ"
}

// checksJSON 把校验点序列化为 JSON（存入 case_steps.checks）。
func checksJSON(cps []CheckPoint) string {
	if len(cps) == 0 {
		return ""
	}
	items := make([]map[string]string, 0, len(cps))
	for _, c := range cps {
		items = append(items, map[string]string{"expect": c.Expect, "actual": c.Actual})
	}
	b, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(b)
}
