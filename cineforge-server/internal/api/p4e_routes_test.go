package api

// P4e 路由注册守卫：断言 /agent-jobs、materialize、agent 项目 jobs、
// retry-distribution 四个 P4e 端点已注册且路径树可共存（gin 不允许同类冲突）。
// 不依赖 DB：NewRouter 注册路径即抛 panic，运行即验证。

import (
	"testing"

	"cineforge/server/internal/config"
)

func TestP4eRouteRegistration(t *testing.T) {
	eng := NewRouter(&config.Config{}, nil)
	routes := eng.Routes()
	cases := []struct{ method, path string }{
		{"POST", "/api/agent-jobs"},
		{"POST", "/api/projects/:project_id/agents/runs/:run_id/materialize"},
		{"POST", "/api/projects/:project_id/agents/:agent_type/jobs"},
		{"POST", "/api/projects/:project_id/breakdown-steps/prompts/:run_id/retry-distribution"},
		{"POST", "/api/agent-runs/:run_id/feedback"},
		{"GET", "/api/agent-runs/:run_id/feedback"},
		{"GET", "/api/agent-training-samples"},
		{"POST", "/api/agent-training-samples"},
		{"POST", "/api/tasks/:task_id/prompt-jobs"},
	}
	for _, w := range cases {
		found := false
		for _, r := range routes {
			if r.Method == w.method && r.Path == w.path {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("route not registered: %s %s", w.method, w.path)
		}
	}
	t.Logf("total registered routes: %d", len(routes))
}
