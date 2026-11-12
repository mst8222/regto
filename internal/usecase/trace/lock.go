package trace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLockTimeout 表示在规定时间内没能拿到留痕写入锁。
//
// 语义上这是「现在不能写」，不是「写失败」：调用方（守卫）应返回 503 而不是静默丢记录。
var ErrLockTimeout = errors.New("获取留痕写入锁超时")

// 默认锁参数。
const (
	// defaultLockTimeout 是单次写留痕愿意等待的上限。
	// 为什么不是无限等：留痕在请求路径上，锁死等于服务假死；
	// 宁可 503（明确失败）也不要挂住（看不见的失败）。
	defaultLockTimeout = 5 * time.Second
	// defaultLockStale 是判定「陈旧锁」的时长：持锁写一条记录是毫秒级，
	// 超过这个时间还挂着，几乎必然是持锁进程已死。
	defaultLockStale = 30 * time.Second
	// lockProbeAfter 是「多久之后才去读锁文件内容做 pid 探测」。
	//
	// 为什么要这道闸：Windows 上 Go 的 OpenFile 不带 FILE_SHARE_DELETE，
	// 只要有人正读着锁文件，释放方的 os.Remove 就会失败（共享冲突）——
	// 锁文件残留 → 后续所有写入都超时。实测 8 并发时必现。
	// 持锁只做一次毫秒级写入，所以「刚创建不到 1 秒的锁」直接当活锁处理，
	// 根本不去读它，把读竞争从热路径里彻底移走。
	lockProbeAfter = 1 * time.Second
	// lockFileName 是锁文件名（放在 traces 根目录，与数据同盘才能保证语义一致）。
	lockFileName = ".trace.lock"
	// lockReleaseRetries 是释放锁的重试次数：偶发的共享冲突会在几十毫秒内消失。
	lockReleaseRetries = 20
)

// lockInfo 是锁文件内容，用于诊断与陈旧锁判定。
type lockInfo struct {
	PID        int    `json:"pid"`
	Host       string `json:"host"`
	AcquiredAt int64  `json:"acquired_at_ms"`
}

// fileLock 是跨进程互斥锁（O_CREATE|O_EXCL 独占创建）。
//
// 为什么不用 OS 文件锁（flock/fcntl）：Windows 上没有等价语义，Go 标准库也不提供；
// 而本仓库的开发机就是 Windows + WSL2 —— 用 flock 会让测试只在 Linux 过。
// 为什么不用数据库 advisory lock：那会把「写文件真源」的能力绑在数据库可用性上，
// 库挂了连留痕都写不了，恰好削弱了审计系统的独立性。
type fileLock struct {
	path string
}

// acquire 阻塞式获取锁：成功返回已持有的锁，超时返回 ErrLockTimeout。
//
// 退避策略：5ms 起步、翻倍、上限 100ms。不用忙等 —— 留痕在请求路径上，
// 忙等会把 CPU 打满，反而拖慢持锁方。
func acquireLock(path string, timeout, staleAfter time.Duration) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建锁目录失败: %w", err)
	}
	deadline := time.Now().Add(timeout)
	backoff := 5 * time.Millisecond
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			writeLockInfo(f)
			_ = f.Close()
			return &fileLock{path: path}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("创建锁文件失败: %w", err)
		}
		// 锁文件存在：先判断是不是「持锁者已死」留下的，是就回收重试。
		if staleLock(path, staleAfter) {
			_ = os.Remove(path)
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, ErrLockTimeout
		}
		time.Sleep(backoff)
		if backoff < 100*time.Millisecond {
			backoff *= 2
		}
	}
}

// release 释放锁（删除锁文件）。
//
// 必须重试：Windows 上若另一进程恰好正在读锁文件（陈旧探测），
// os.Remove 会因共享冲突失败；一次失败就放弃会让锁永久残留。
func (l *fileLock) release() {
	if l == nil {
		return
	}
	for i := 0; i < lockReleaseRetries; i++ {
		if err := os.Remove(l.path); err == nil || errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	// 兜底：仍删不掉就把 mtime 打旧，让下一个进程按「陈旧锁」回收，
	// 而不是让整个留痕系统永久卡死。
	old := time.Now().Add(-2 * defaultLockStale)
	_ = os.Chtimes(l.path, old, old)
}

// writeLockInfo 写入诊断信息；失败不影响加锁结果（锁本身已由 O_EXCL 保证）。
func writeLockInfo(f *os.File) {
	host, _ := os.Hostname()
	b, err := json.Marshal(lockInfo{
		PID:        os.Getpid(),
		Host:       host,
		AcquiredAt: time.Now().UTC().UnixMilli(),
	})
	if err != nil {
		return
	}
	_, _ = f.Write(b)
}

// staleLock 判断锁文件是否为陈旧锁。
//
// 两个条件都基于「持锁期间只做一次毫秒级写入」这一事实：
//  1. 锁文件修改时间早于 staleAfter → 无论持有者是谁都视为陈旧（兜底，任何平台都生效）；
//  2. 同主机且 pid 已不存在 → 立即可回收（更快，但只在支持探测的平台生效）。
func staleLock(path string, staleAfter time.Duration) bool {
	st, err := os.Stat(path)
	if err != nil {
		// 文件刚被删（别的进程释放了）——按「不陈旧」处理，让循环去重试创建。
		return false
	}
	if time.Since(st.ModTime()) > staleAfter {
		return true
	}
	// 新锁（正常持锁毫秒级）不做 pid 探测：见 lockProbeAfter 的注释，
	// 读文件会阻塞持有者删文件。
	if time.Since(st.ModTime()) < lockProbeAfter {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		// 刚创建还没写完内容，或读失败：还不算陈旧，等下一轮。
		return false
	}
	var info lockInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return false
	}
	host, _ := os.Hostname()
	if info.Host != "" && host != "" && info.Host != host {
		// 跨主机（共享盘）时无法可靠探测进程存活，只依赖时间兜底。
		return false
	}
	return !processAlive(info.PID)
}
