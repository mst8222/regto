// Package trace 实现留痕的 append-only 文件存储（ROADMAP §5.3 载体 ②）。
// 与内部 domain/trace 的关系：本包负责「落地与校验」，领域包负责「结构与 hash 计算」。
package trace

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	domaintrace "github.com/mst8222/regto/internal/domain/trace"
	"github.com/mst8222/regto/internal/platform/id"
)

// Store 是 jsonl 留痕存储（按月分目录，按天分片）。
type Store struct {
	root string // traces/ 根目录
	now  func() time.Time
	ids  *id.Generator
	// lockTimeout / lockStale 控制跨进程写锁的等待上限与陈旧判定（见 lock.go）。
	lockTimeout time.Duration
	lockStale   time.Duration
}

// StoreOptions 可覆盖默认锁参数；零值表示用默认（5s 等待 / 30s 陈旧）。
type StoreOptions struct {
	LockTimeout time.Duration
	LockStale   time.Duration
}

// NewStore 创建存储；root 不存在时自动创建。gen 为 nil 时使用系统 ULID 生成器。
func NewStore(root string, gen *id.Generator) *Store {
	return NewStoreWithOptions(root, gen, StoreOptions{})
}

// NewStoreWithOptions 创建可调锁参数的存储（测试用短超时，生产用默认）。
func NewStoreWithOptions(root string, gen *id.Generator, opt StoreOptions) *Store {
	if gen == nil {
		gen = id.NewSystem()
	}
	if opt.LockTimeout <= 0 {
		opt.LockTimeout = defaultLockTimeout
	}
	if opt.LockStale <= 0 {
		opt.LockStale = defaultLockStale
	}
	return &Store{root: root, now: time.Now, ids: gen,
		lockTimeout: opt.LockTimeout, lockStale: opt.LockStale}
}

// Root 返回留痕根目录（供日志与测试使用）。
func (s *Store) Root() string { return s.root }

// dayFile 返回指定日期的分片路径：traces/YYYY-MM/YYYY-MM-DD.jsonl。
func (s *Store) dayFile(t time.Time) string {
	month := t.Format("2006-01")
	day := t.Format("2006-01-02")
	return filepath.Join(s.root, month, day+".jsonl")
}

// Last 返回当前链尾记录（seq 最大的一条）；空链返回 nil。
func (s *Store) Last() (*domaintrace.Record, error) {
	recs, err := s.All()
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return &recs[len(recs)-1], nil
}

// All 读取全部留痕记录（按 seq 升序）。
func (s *Store) All() ([]domaintrace.Record, error) {
	var files []string
	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			// anchor/ 是每日锚定摘要，不参与主链。
			if d.Name() == "anchor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	var out []domaintrace.Record
	for _, f := range files {
		recs, err := readFile(f)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func readFile(path string) ([]domaintrace.Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []domaintrace.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024) // 评审意见可能较长
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var r domaintrace.Record
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, fmt.Errorf("%s:%d 解析失败: %w", path, line, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// Append 追加一条留痕：自动分配 seq、衔接 prev_hash、计算 hash。
// 返回落盘后的完整记录（含 hash）。
func (s *Store) Append(r domaintrace.Record) (domaintrace.Record, error) {
	if !domaintrace.ValidAction(r.Action) {
		return r, fmt.Errorf("非法 action: %q（允许：%v）", r.Action, domaintrace.AllActions())
	}
	if !domaintrace.ValidActorType(r.ActorType) {
		return r, fmt.Errorf("非法 actor_type: %q（允许 human/ai/system）", r.ActorType)
	}
	if strings.TrimSpace(r.Summary) == "" {
		return r, errors.New("summary 不可为空")
	}
	if r.ObjectType == "" || r.ObjectID == "" {
		return r, errors.New("object_type 与 object_id 不可为空")
	}

	// 跨进程临界区从这里开始：seq 分配依赖「读链尾」，而 CLI 与 server 是两个进程，
	// 只靠进程内 mutex 挡不住。锁在整个「读-算-写」窗口内持有（DEC-08）。
	lock, err := acquireLock(filepath.Join(s.root, lockFileName), s.lockTimeout, s.lockStale)
	if err != nil {
		return r, err
	}
	defer lock.release()

	last, err := s.Last()
	if err != nil {
		return r, err
	}
	now := s.now().UTC()
	if r.TraceID == "" {
		r.TraceID = s.ids.NewString()
	}
	r.Seq = 1
	r.PrevHash = domaintrace.GenesisPrevHash
	if last != nil {
		r.Seq = last.Seq + 1
		r.PrevHash = last.Hash
	}
	r.SchemaVersion = domaintrace.SchemaVersion
	if r.OccurredAtMs == 0 {
		r.OccurredAtMs = now.UnixMilli()
	}
	r.RecordedAtMs = now.UnixMilli()
	if err := domaintrace.Recompute(&r); err != nil {
		return r, err
	}

	path := s.dayFile(now)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return r, fmt.Errorf("创建留痕目录失败: %w", err)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return r, fmt.Errorf("序列化留痕失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return r, fmt.Errorf("打开留痕文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return r, fmt.Errorf("写入留痕失败: %w", err)
	}
	return r, nil
}

// Verify 校验整条 hash 链：seq 连续、prev_hash 衔接、hash 可重算。
func (s *Store) Verify() (int, error) {
	recs, err := s.All()
	if err != nil {
		return 0, err
	}
	return len(recs), domaintrace.VerifyChain(recs)
}
