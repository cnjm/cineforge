package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cineforge/server/internal/api"
	"cineforge/server/internal/config"
	"cineforge/server/internal/db"
	"cineforge/server/internal/devseed"
)

func main() {
	cfgPath := "config.yaml"
	if v := os.Getenv("CINEFORGE_CONFIG"); v != "" {
		cfgPath = v
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	// 应用级共享 DB pool：repository 与 /ready 共用；DSN 未配置则不建。
	pool, err := db.NewPool(context.Background(), cfg)
	if err != nil {
		slog.Error("init db pool", "err", err)
		os.Exit(1)
	}
	if pool != nil {
		defer pool.Close()
	}

	// 开发账号自动种子（dev 配置启用时）：幂等创建，失败仅告警不阻断启动。
	if pool != nil && cfg.DevSeed.Enabled {
		seedCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := devseed.EnsureDevUser(seedCtx, pool, cfg.DevSeed, slog.Default()); err != nil {
			slog.Warn("devseed: ensure dev account failed", "err", err)
		}
		cancel()
	}

	router := api.NewRouter(cfg, pool)

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("cineforge-server listening", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
