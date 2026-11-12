package trace

import (
	"context"
	"fmt"
	"sort"
	"time"

	dtrace "github.com/mst8222/regto/internal/domain/trace"
)

// BatchSize 是每批提交的事务粒度（DEC-05：崩溃时最多丢最后一批，可重入补齐）。
const BatchSize = 500

// SyncResult 是一次导入的结果（供 CLI 以 JSON 输出，便于 AI 解析）。
type SyncResult struct {
	Imported   int    `json:"imported"`
	Skipped    int    `json:"skipped"`
	Total      int    `json:"total"`
	FirstSeq   int64  `json:"first_seq"`
	LastSeq    int64  `json:"last_seq"`
	HeadHash   string `json:"head_hash"`
	DurationMs int64  `json:"duration_ms"`
}

// SyncFile 把单个 jsonl 文件导入数据库。
func SyncFile(ctx context.Context, repo dtrace.Repository, path string) (SyncResult, error) {
	recs, err := readFile(path)
	if err != nil {
		return SyncResult{}, err
	}
	return syncRecords(ctx, repo, recs)
}

// SyncDir 把留痕根目录下全部分片导入数据库（按月分目录、按天分片）。
func SyncDir(ctx context.Context, repo dtrace.Repository, root string) (SyncResult, error) {
	recs, err := NewStore(root, nil).All()
	if err != nil {
		return SyncResult{}, err
	}
	return syncRecords(ctx, repo, recs)
}

// syncRecords 是导入主流程：内部链自洽 → 库内链尾衔接 → 按批写入。
//
// 顺序不可颠倒（DESIGN-TRACE-001 DEC-03/DEC-06）：
//  1. VerifyChain 先校验文件内部自洽——边写边验会在发现篡改时已写入部分数据，
//     而「被篡改的文件」必须整体拒绝；
//  2. VerifyShardLink 再校验与库内链尾的衔接（REQ-05）——按天分片后单文件是链中段，
//     不衔接说明文件缺失/被换/库内不完整，同样整体拒绝；
//  3. 两步都通过才允许 InsertBatch。
func syncRecords(ctx context.Context, repo dtrace.Repository, recs []dtrace.Record) (SyncResult, error) {
	start := time.Now()
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Seq < recs[j].Seq })

	if err := dtrace.VerifyChain(recs); err != nil {
		return SyncResult{}, err
	}

	headSeq, headHash, err := repo.Head(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("查询库内链尾失败: %w", err)
	}
	if err := dtrace.VerifyShardLink(recs, headSeq, headHash); err != nil {
		return SyncResult{}, err
	}

	result := SyncResult{Total: len(recs)}
	for i := 0; i < len(recs); i += BatchSize {
		end := i + BatchSize
		if end > len(recs) {
			end = len(recs)
		}
		imported, skipped, err := repo.InsertBatch(ctx, recs[i:end])
		if err != nil {
			return result, fmt.Errorf("导入 seq %d 批次失败: %w", recs[i].Seq, err)
		}
		result.Imported += imported
		result.Skipped += skipped
	}
	if len(recs) > 0 {
		result.FirstSeq = recs[0].Seq
		result.LastSeq = recs[len(recs)-1].Seq
		result.HeadHash = recs[len(recs)-1].Hash
	}
	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}
