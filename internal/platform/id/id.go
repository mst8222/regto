// Package id 提供全局唯一标识生成。
// 使用 ULID（字典序可排序、含毫秒时间戳、26 字符 Crockford Base32），
// 满足 traces 的 seq 时间有序性与人类可读性。
package id

import (
	"crypto/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Generator 是 ULID 生成器，并发安全。
// entropy 源为 crypto/rand；单调性由 ulid 库的 Monotonic 保证（同一毫秒内递增）。
type Generator struct {
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
	now     func() time.Time
}

// New 创建生成器。now 注入便于测试。
func New(now func() time.Time) *Generator {
	seed := uint64(time.Now().UnixNano())
	return &Generator{
		entropy: ulid.Monotonic(rand.Reader, seed),
		now:     now,
	}
}

// NewSystem 创建使用系统时钟的生成器。
func NewSystem() *Generator { return New(time.Now) }

// NewString 生成一个新的 ULID 字符串（26 字符）。
func (g *Generator) NewString() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ulid.MustNew(ulid.Timestamp(g.now()), g.entropy).String()
}

// NewTime 生成并返回 ULID 及其时间戳。
func (g *Generator) NewTime() (string, time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.now()
	id := ulid.MustNew(ulid.Timestamp(t), g.entropy)
	return id.String(), t
}

// IsValid 校验字符串是否为合法 ULID。
func IsValid(s string) bool {
	_, err := ulid.ParseStrict(s)
	return err == nil
}
