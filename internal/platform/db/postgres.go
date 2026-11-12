// Package db 封装 pgxpool 连接池与连通性检查。
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open 建立连接池并立即探活，避免进程带着坏连接启动。
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("解析 DATABASE_URL 失败: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}
	if err := Ping(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Ping 探活（超时由 ctx 控制）。
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("pool is nil")
	}
	return pool.Ping(ctx)
}
