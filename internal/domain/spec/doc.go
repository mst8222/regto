// Package spec 定义 SpecDD 文档的领域视图与持久化契约。
//
// 原则是「Git 唯一真源，DB 只是索引」（spec/README.md §1）：
// 本包的对象全部由文件派生，同步是单向的（文件 → DB），领域层不感知文件写入。
package spec

// 文档类型（与 spec/README.md §1 的三类文档对应）。
const (
	TypeRequirement = "requirement"
	TypeDesign      = "design"
	TypeTest        = "test"
)

// 追溯链接类型（ROADMAP §4.4）。
const (
	LinkCovers   = "covers"
	LinkVerifies = "verifies"
)

// LinkOriginFile 表示链接来源是规格文件（区别于 AI 推断的 inference）。
const LinkOriginFile = "file"

// Document 是一份规格文档（SPEC / DESIGN / TEST 通用）。
type Document struct {
	DocID       string // SPEC-STORY-002
	DocType     string // requirement | design | test
	Domain      string
	Title       string
	Status      string
	Version     int
	Owner       string
	FilePath    string // 仓库相对路径，同步的唯一键
	ContentHash string // 正文 sha256，变更判定依据（DEC-01）
	Items       []Item
	Cases       []Case
}

// Item 是文档中的一条规格条目（REQ-01 / DEC-02 / …）。
type Item struct {
	ItemKey  string // REQ-01
	ItemType string // REQ | NFR | AC | ASM | DEC
	Title    string
	Body     string
	LineNo   int
}

// Case 是用例（来自 TEST-*.yaml）。
type Case struct {
	CaseKey    string // TC-01
	Title      string
	Suite      string
	Automation string
	Priority   string
	SourceRef  string // spec/.../TEST-*.yaml#TC-01
	SourceHash string
	SpecDocID  string // 所属 TEST 文档 ID
	Covers     []string
	Verifies   []string
	Steps      []Step
}

// Step 是用例步骤（checkPoints 以 JSON 存于 case_steps.checks，省一张表）。
type Step struct {
	Seq    int
	Name   string
	Checks string // JSON 数组
}

// Link 是一条追溯链接。
type Link struct {
	SourceType string // case | item
	SourceRef  string // 全局引用
	LinkType   string // covers | verifies
	TargetType string // item | decision
	TargetRef  string
	Strength   int
	Origin     string // file | inference
}

// SyncResult 是一次同步的结果（供 CLI 以 JSON 输出，便于 AI 解析）。
type SyncResult struct {
	Documents  int   `json:"documents"`
	Items      int   `json:"items"`
	Cases      int   `json:"cases"`
	Steps      int   `json:"steps"`
	Links      int   `json:"links"`
	Changed    int   `json:"changed"`
	DurationMs int64 `json:"duration_ms"`
}
