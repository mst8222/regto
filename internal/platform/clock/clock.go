// Package clock 提供可注入的时间源，保证领域层可无 I/O 测试（NFR-01）。
package clock

import (
	"sync"
	"time"
)

// Clock 抽象时间读取。
type Clock interface {
	// Now 返回当前时间（UTC）。
	Now() time.Time
}

// SystemClock 使用系统时钟。
type SystemClock struct{}

// Now 实现 Clock。
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FakeClock 测试用可控时钟，并发安全。
type FakeClock struct {
	mu   sync.RWMutex
	when time.Time
}

// NewFake 创建固定时间的假时钟。
func NewFake(when time.Time) *FakeClock { return &FakeClock{when: when} }

// Now 实现 Clock。
func (c *FakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.when
}

// Advance 向前推进时钟。
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.when = c.when.Add(d)
}

// Set 设置时钟到指定时间。
func (c *FakeClock) Set(when time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.when = when
}
