package api

// P4d run-events SSE 帧格式单元测试（内部包：直接用未导出 writeSSEFrame/writeSSEStreamError）。

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRunEventsFraming 帧格式：首帧 `retry: 5000` + `event:` + `data:` + 双换行。
func TestRunEventsFraming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	snapshot := map[string]any{
		"project_id": "p1",
		"workflows":  []any{},
		"agent_runs": []any{},
		"emitted_at": "2026-09-03T00:00:00Z",
	}
	if err := writeSSEFrame(c, "snapshot", snapshot, 5000); err != nil {
		t.Fatal(err)
	}
	got := w.Body.String()
	wantPrefix := "retry: 5000\nevent: snapshot\ndata: "
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("frame prefix=%q want=%q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("frame missing blank line: %q", got)
	}
	var payload map[string]any
	rest := strings.TrimSuffix(strings.TrimPrefix(got, wantPrefix), "\n\n")
	if err := json.Unmarshal([]byte(rest), &payload); err != nil {
		t.Fatalf("data 行非法 JSON: %v (%q)", err, rest)
	}
	if payload["project_id"] != "p1" {
		t.Fatalf("payload=%v", payload)
	}

	// 心跳帧与 stream_error 帧
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	if _, err := c2.Writer.WriteString(": heartbeat\n\n"); err != nil {
		t.Fatal(err)
	}
	if w2.Body.String() != ": heartbeat\n\n" {
		t.Fatalf("heartbeat=%q", w2.Body.String())
	}
	if !writeSSEStreamError(c2, "project_event_snapshot_failed", "p1") {
		t.Fatal("stream_error write failed")
	}
	frame := w2.Body.String()
	if !strings.Contains(frame, "event: stream_error") || !strings.Contains(frame, `"fallback":"polling"`) {
		t.Fatalf("stream_error frame=%q", frame)
	}
}
