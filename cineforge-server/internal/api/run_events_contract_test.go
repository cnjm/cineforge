package api_test

// P4d run-events 契约测试：/projects/{project_id}/run-events。
// 本文件把 Redis 指向死端口，保证「Redis 不可用 → 503 + Retry-After + polling fallback」契约
// 不依赖开发机 Redis（避免 6379 上恰有实例时测试挂起不返回）。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// disconnectRedis 把测试环境的 Redis 指向死端口，使 run-events 订阅必然失败。
func disconnectRedis(e *testEnv) {
	e.cfg.Redis.URL = "redis://127.0.0.1:1/0"
}

func TestRunEventsAuthAndFallback(t *testing.T) {
	e := newEnv(t)
	disconnectRedis(e)

	director := e.seedUserAs(t, uniquePhone("942"), "director", "secret123")
	outsider := e.seedUserAs(t, uniquePhone("943"), "artist", "secret123")
	projectID := e.seedProject(t, "事件流契约", "EVT", seedProjectOpts{scriptText: "第一幕。\n小明：【去看。】\n"})

	// 未认证 → 401
	code, body := e.doJSON(t, http.MethodGet, "/api/projects/"+projectID+"/run-events", nil, "")
	assertStatus(t, code, http.StatusUnauthorized, body)

	// 幽灵项目 → 404（先于 Redis，subscriber 不触碰）
	code, body = e.doJSON(t, http.MethodGet,
		"/api/projects/00000000-0000-0000-0000-000000000000/run-events", nil, director)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")

	// 无项目权限 → 403（Project permission denied 逐字）
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/"+projectID+"/run-events", nil, outsider)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Project permission denied")

	// director + Redis 不可用 → 503 + Retry-After:5 + {code,message,fallback}
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/run-events", nil)
	req.Header.Set("Authorization", "Bearer "+director)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After=%q", got)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body: %v", err)
	}
	detailObj, _ := out["detail"].(map[string]any)
	if detailObj["code"] != "project_event_stream_unavailable" ||
		detailObj["message"] != "Project event stream is unavailable" ||
		detailObj["fallback"] != "polling" {
		t.Fatalf("detail=%v", out["detail"])
	}
}

// TestRunEventsSnapshotPathRegistry 路由已注册（`/:project_id/run-events` 存在，避免 gin 404 树冲突误伤）。
func TestRunEventsSnapshotPathRegistered(t *testing.T) {
	e := newEnv(t)
	disconnectRedis(e)
	director := e.seedUserAs(t, uniquePhone("944"), "director", "secret123")
	projectID := e.seedProject(t, "事件流路由", "EV2", seedProjectOpts{scriptText: "第一幕。\n小明：【看。】\n"})
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/run-events", nil)
	req.Header.Set("Authorization", "Bearer "+director)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	// 非 404 即路由生效（正常行为是 503 fallback，因为测试 Redis 不可用）
	if w.Code == http.StatusNotFound {
		t.Fatalf("run-events 路由未注册: %d %s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d (want 503 fallback)", w.Code)
	}
}
