package casbin_test

// casbin enforcer 契约测试：策略来自 migration 00002 种子数据。
// 需要 dev DB（DSN 缺失自动 skip）。

import (
	"context"
	"testing"

	"cineforge/server/internal/casbin"
	"cineforge/server/internal/testutil"
)

func TestEnforceSeededPolicies(t *testing.T) {
	pool := testutil.Pool(t)
	e := casbin.New(pool)

	ctx := context.Background()
	cases := []struct {
		role, obj, act string
		want           bool
	}{
		// 5 角色都能 projects.read / tasks.read / flows.write / tasks.submit
		{"admin", "projects.read", "execute", true},
		{"director", "projects.read", "execute", true},
		{"script_editor", "projects.read", "execute", true},
		{"artist", "tasks.read", "execute", true},
		{"editor", "tasks.submit", "execute", true},
		{"artist", "flows.write", "execute", true},
		// tasks.review 仅 admin/director
		{"admin", "tasks.review", "execute", true},
		{"director", "tasks.review", "execute", true},
		{"script_editor", "tasks.review", "execute", false},
		{"artist", "tasks.review", "execute", false},
		{"editor", "tasks.review", "execute", false},
		// 未知 capability / 未知角色
		{"admin", "nonexistent.cap", "execute", false},
		{"internal_bot", "projects.read", "execute", false},
	}
	for _, c := range cases {
		got, err := e.Enforce(ctx, c.role, c.obj, c.act)
		if err != nil {
			t.Fatalf("Enforce(%s,%s,%s): %v", c.role, c.obj, c.act, err)
		}
		if got != c.want {
			t.Errorf("Enforce(%s,%s,%s)=%v, want %v", c.role, c.obj, c.act, got, c.want)
		}
	}
}
