// Package redis 封装 Redis 缓存/会话连接（可选组件，未配置则整体禁用）。
package redis

import (
	"context"
	"errors"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

// Client 是 Redis 客户端薄封装；未配置时为 nil，调用方需判空。
type Client struct {
	rdb *goredis.Client
}

// Open 建立连接并探活。url 为空返回 (nil, nil) 表示禁用。
func Open(ctx context.Context, url string) (*Client, error) {
	if url == "" {
		return nil, nil
	}
	opt, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("解析 REGTO_REDIS_URL 失败: %w", err)
	}
	c := goredis.NewClient(opt)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("连接 Redis 失败: %w", err)
	}
	return &Client{rdb: c}, nil
}

// Raw 暴露底层 go-redis 客户端，供 platform/cache 构造缓存。
// 这是唯一一处「泄漏驱动」的出口：缓存需要 SetNX 等命令，
// 平台层不为每个命令都包一层方法（DESIGN-REDIS-001 DEC-01）。
func (c *Client) Raw() *goredis.Client {
	if c == nil {
		return nil
	}
	return c.rdb
}

// Ping 探活。
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis 未启用")
	}
	return c.rdb.Ping(ctx).Err()
}

// Close 关闭连接。
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}
