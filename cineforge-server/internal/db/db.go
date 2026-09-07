package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/config"
)

// NewPool 创建应用级共享 pgx 连接池。DSN 未配置时返回 (nil, nil)，
// 交由上层决定（骨架阶段 /ready 可返回 503）。
func NewPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	if cfg.Database.DSN == "" {
		return nil, nil
	}
	pool, err := pgxpool.New(ctx, cfg.Database.DSN)
	if err != nil {
		return nil, fmt.Errorf("init db pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return pool, nil
}

// ReadyChecker 基于已建的共享 pool（可能 nil）构造 /ready 检查：
//   - DSN 未配置 pool 为 nil → 返回错误（骨架阶段 503）
//   - 有 pool → ping，超时 2s
func ReadyChecker(pool *pgxpool.Pool) func(ctx context.Context) error {
	if pool == nil {
		return func(context.Context) error {
			return fmt.Errorf("database not configured (CINEFORGE_DB_DSN)")
		}
	}
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return pool.Ping(ctx)
	}
}
