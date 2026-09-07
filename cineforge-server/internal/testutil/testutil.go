// Package testutil 提供跨包契约测试共享的数据库辅助。
// 契约测试需要真实 dev DB：未配置 DSN 时自动 t.Skip（生产构建可跑空测试）。
package testutil

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DSN 返回测试 DSN：优先 CINEFORGE_DB_DSN，否则解析服务根 .env 中的值；
// 都没有时返回 ""（调用方 t.Skip）。
func DSN(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("CINEFORGE_DB_DSN"); v != "" {
		return v
	}
	// go test 的工作目录是包目录：internal/testutil → ../../.env
	p := filepath.Join("..", "..", ".env")
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "CINEFORGE_DB_DSN=") {
			return strings.TrimPrefix(line, "CINEFORGE_DB_DSN=")
		}
	}
	return ""
}

// Pool 返回测试连接池；DSN 不可用或不可达时 t.Skip。
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := DSN(t)
	if dsn == "" {
		t.Skip("未配置 CINEFORGE_DB_DSN，跳过数据库契约测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("数据库不可达，跳过契约测试: %v", err)
	}
	return pool
}

// EnvMap 解析服务根 .env 的全部 KEY=VALUE（# 注释忽略）。
func EnvMap(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	p := filepath.Join("..", "..", ".env")
	f, err := os.Open(p)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
