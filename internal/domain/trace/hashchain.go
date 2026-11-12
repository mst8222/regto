package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ComputeHash 计算一条记录的 hash：sha256(prev_hash ‖ canonical_json(record))。
// canonical_json 剔除 hash 与 recorded_at_ms，保证「同一内容 → 同一 hash」。
func ComputeHash(r Record) (string, error) {
	b, err := r.canonical()
	if err != nil {
		return "", fmt.Errorf("序列化记录失败: %w", err)
	}
	h := sha256.Sum256(append([]byte(r.PrevHash), b...))
	return hex.EncodeToString(h[:]), nil
}

// Recompute 就地重算 Hash 字段（用于 append 前填充）。
func Recompute(r *Record) error {
	if r == nil {
		return errors.New("record is nil")
	}
	h, err := ComputeHash(*r)
	if err != nil {
		return err
	}
	r.Hash = h
	return nil
}

// ErrChainBroken 表示 hash 链校验失败（调用方可用 errors.As 取详情）。
type ErrChainBroken struct {
	Seq     int64  // 断裂处的 seq
	TraceID string // 断裂处的 trace_id
	Reason  string
}

// Error 实现 error 接口。
func (e *ErrChainBroken) Error() string {
	return fmt.Sprintf("hash 链断裂：seq=%d trace_id=%s reason=%s", e.Seq, e.TraceID, e.Reason)
}

// VerifyChain 校验一批留痕记录的**内部自洽性**（SPEC-TRACE-001 REQ-02）：
// action/actor_type 合法、trace_id 非空、seq 相对首条连续、相邻记录 prev_hash 衔接、
// hash 可重算。返回首个断裂点；空切片返回 nil。
//
// 分片语义（DESIGN-TRACE-001 DEC-06）：留痕按天分片，单文件可能是链的中段，
// 因此本函数**不要求首条为 seq=1**：
//   - seq 连续性按「相对首条」判定（records[0].Seq + i）；
//   - i>0 时 prev_hash 必须等于前一条的 hash；
//   - 仅当首条 seq==1（即全链之首）时，才要求 prev_hash 为 GenesisPrevHash。
//
// 分片与库内链尾的**外部衔接**不归本函数管（需要库内状态，领域层零依赖）：
// 由调用方先查库内链尾，再调用 VerifyShardLink 完成（REQ-05）。
func VerifyChain(records []Record) error {
	for i, r := range records {
		if !ValidAction(r.Action) {
			return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID, Reason: "非法 action: " + r.Action}
		}
		if !ValidActorType(r.ActorType) {
			return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID, Reason: "非法 actor_type: " + r.ActorType}
		}
		if r.TraceID == "" {
			return &ErrChainBroken{Seq: r.Seq, TraceID: "", Reason: "trace_id 为空"}
		}
		// seq 相对首条连续：分片文件的首条可以不是 1，但片内必须步进为 1。
		if want := records[0].Seq + int64(i); r.Seq != want {
			return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID,
				Reason: fmt.Sprintf("seq 不连续：期望 %d，实际 %d", want, r.Seq)}
		}
		// prev_hash 衔接：相邻记录必须首尾相接；全链之首（seq=1）必须是创世值。
		if i > 0 {
			if r.PrevHash != records[i-1].Hash {
				return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID, Reason: "prev_hash 与前一条 hash 不一致"}
			}
		} else if r.Seq == 1 && r.PrevHash != GenesisPrevHash {
			return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID,
				Reason: "全链之首（seq=1）的 prev_hash 必须是创世值"}
		}
		got, err := ComputeHash(r)
		if err != nil {
			return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID, Reason: err.Error()}
		}
		if got != r.Hash {
			return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID,
				Reason: fmt.Sprintf("hash 不匹配：记录内 %s，重算 %s", r.Hash, got)}
		}
	}
	return nil
}

// VerifyShardLink 校验一批记录与库内已知链尾的衔接（SPEC-TRACE-001 REQ-05 /
// DESIGN-TRACE-001 DEC-06）。recs 必须已通过 VerifyChain（内部自洽）；
// headSeq/headHash 是库内当前链尾（seq 最大记录），headSeq==0 表示空库。
//
// 规则：
//  1. 空库：首条 seq 必须为 1（genesis prev_hash 已由 VerifyChain 在 seq==1 时强制）。
//  2. 首条 seq > headSeq：必须紧接链尾——seq == headSeq+1 且 prev_hash == headHash。
//  3. 首条 seq <= headSeq（文件与库内重叠）：文件中 seq==headSeq 的记录（若存在）
//     其 hash 必须等于 headHash——内部连续性保证其后的新记录从受信链头延申；
//     文件完全位于过去（不含 headSeq）时全部记录已在库内，视为幂等重导入（REQ-01）。
//
// 其余情形（首条与链尾之间有缺口等）一律拒绝。空记录集返回 nil。
func VerifyShardLink(recs []Record, headSeq int64, headHash string) error {
	if len(recs) == 0 {
		return nil
	}
	first := recs[0]
	if headSeq == 0 { // 空库：只能从创世开始
		if first.Seq != 1 {
			return &ErrChainBroken{Seq: first.Seq, TraceID: first.TraceID,
				Reason: fmt.Sprintf("空库导入时首条 seq 必须为 1，实际 %d", first.Seq)}
		}
		return nil
	}
	if first.Seq > headSeq { // 文件整体在库尾之后：必须紧接
		if first.Seq != headSeq+1 {
			return &ErrChainBroken{Seq: first.Seq, TraceID: first.TraceID,
				Reason: fmt.Sprintf("与库内链尾不衔接：文件首条 seq=%d，库内链尾 seq=%d（期望 %d）",
					first.Seq, headSeq, headSeq+1)}
		}
		if first.PrevHash != headHash {
			return &ErrChainBroken{Seq: first.Seq, TraceID: first.TraceID,
				Reason: "与库内链尾不衔接：文件首条 prev_hash 与库内链尾 hash 不一致"}
		}
		return nil
	}
	// 文件与库内重叠：链尾 seq 必须出现在文件中且 hash 与库内一致
	//（内部连续性已保证其后记录从该 hash 延申）。
	for _, r := range recs {
		if r.Seq == headSeq {
			if r.Hash != headHash {
				return &ErrChainBroken{Seq: r.Seq, TraceID: r.TraceID,
					Reason: fmt.Sprintf("文件中 seq=%d 的记录 hash 与库内链尾不一致", headSeq)}
			}
			return nil
		}
	}
	// 文件完全位于库内过去：全部记录已在库中，幂等重导入。
	return nil
}
