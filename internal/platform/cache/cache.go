// Package cache 提供可插拔的缓存抽象。
//
// 设计要点（DESIGN-REDIS-001）：
//   - 业务代码只依赖 Store 接口，不感知 Redis 是否存在；
//   - 未配置 Redis 时注入 Noop()，行为与「无缓存」等价；
//   - 缓存故障一律降级：读失败视为未命中，写失败仅记日志（NFR-02）。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// KeyPrefix 是全部缓存键的统一前缀（含版本段，DESIGN-REDIS-001 DEC-06）。
// 版本段允许将来整体切换到 v2，让旧键自然过期，无需运维手工清理。
const KeyPrefix = "regto:v1"

// Store 是缓存抽象。所有实现必须满足：故障不返回错误给业务（除 Get 命中判定外）。
type Store interface {
	// Enabled 报告缓存是否真实可用（false 表示空实现，无需等待）。
	Enabled() bool
	// Get 读取键并反序列化到 dst；返回 false 表示未命中（含故障降级）。
	Get(ctx context.Context, key string, dst any) (bool, error)
	// Set 写入键值并设 TTL。ttl <= 0 时拒绝写入（NFR-03：禁止无过期键）。
	Set(ctx context.Context, key string, val any, ttl time.Duration) error
	// Delete 删除键（幂等）。
	Delete(ctx context.Context, keys ...string) error
	// SetNX 在键不存在时设置并返回 true（用于写节流闸门）。
	// 出错时返回 (true, err)：让调用方「照常执行被节流的动作」，避免吞掉业务。
	SetNX(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// Compile-time 保证：两种实现都满足 Store。
var (
	_ Store = (*redisStore)(nil)
	_ Store = noopStore{}
)

// ErrNoTTL 表示调用方试图写入没有过期时间的键（NFR-03 硬约束）。
var ErrNoTTL = errors.New("cache: 缓存键必须带 TTL")

// NewRedis 基于 go-redis 客户端构造缓存；rdb 为 nil 时返回空实现。
// 日志走 slog.Default()（DEC-07：缓存故障记日志但不传播）。
func NewRedis(rdb *goredis.Client) Store {
	if rdb == nil {
		return Noop()
	}
	return &redisStore{rdb: rdb, log: slog.Default()}
}

// NewRedisWithLogger 同 NewRedis，但显式指定日志器（便于测试断言降级行为）。
func NewRedisWithLogger(rdb *goredis.Client, log *slog.Logger) Store {
	if rdb == nil {
		return Noop()
	}
	if log == nil {
		log = slog.Default()
	}
	return &redisStore{rdb: rdb, log: log}
}

type redisStore struct {
	rdb *goredis.Client
	log *slog.Logger
}

func (s *redisStore) Enabled() bool { return s != nil && s.rdb != nil }

func (s *redisStore) Get(ctx context.Context, key string, dst any) (bool, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, goredis.Nil) {
			s.warn("缓存读取失败，降级回源", key, err)
		}
		return false, nil // 未命中或故障降级（NFR-02）
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		// 值损坏（版本切换残留）：按未命中处理，让业务回源并覆写。
		s.warn("缓存值反序列化失败，降级回源", key, err)
		return false, nil
	}
	return true, nil
}

func (s *redisStore) Set(ctx context.Context, key string, val any, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrNoTTL
	}
	b, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("cache: 序列化 %s 失败: %w", key, err)
	}
	if err := s.rdb.Set(ctx, key, b, ttl).Err(); err != nil {
		s.warn("缓存写入失败", key, err)
		return err
	}
	return nil
}

func (s *redisStore) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
		s.warn("缓存删除失败", keyOf(keys), err)
		return err
	}
	return nil
}

func (s *redisStore) SetNX(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return true, ErrNoTTL
	}
	ok, err := s.rdb.SetNX(ctx, key, 1, ttl).Result()
	if err != nil {
		// 故障时必须让调用方照常执行被节流的动作（DEC-04：不能吞掉使用记录）。
		return true, err
	}
	return ok, nil
}

// warn 统一记录缓存降级日志（不传播给业务）。
func (s *redisStore) warn(msg, key string, err error) {
	if s.log == nil {
		return
	}
	s.log.Warn(msg, slog.String("key", key), slog.String("err", err.Error()))
}

// keyOf 把多键拼接为单个日志字段。
func keyOf(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	out := keys[0]
	for _, k := range keys[1:] {
		out += "," + k
	}
	return out
}

// Noop 返回空实现：恒未命中、写入恒成功——业务代码无需判 nil。
func Noop() Store { return noopStore{} }

type noopStore struct{}

func (noopStore) Enabled() bool                                         { return false }
func (noopStore) Get(context.Context, string, any) (bool, error)        { return false, nil }
func (noopStore) Set(context.Context, string, any, time.Duration) error { return nil }
func (noopStore) Delete(context.Context, ...string) error               { return nil }
func (noopStore) SetNX(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

// Key 按 `regto:v1:<域>:<标识...>` 拼接缓存键（REQ-06）。
func Key(domain string, parts ...string) string {
	key := KeyPrefix + ":" + domain
	for _, p := range parts {
		key += ":" + p
	}
	return key
}
