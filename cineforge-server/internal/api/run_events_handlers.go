package api

// P4d run-events SSE：GET /projects/{project_id}/run-events。
// 对齐 legacy app/api/routes/streaming.py 的线上契约：
//   - 首帧 `retry: 5000` + `event: snapshot` + `data: {compact json}` + 空行
//   - 15s 心跳 `: heartbeat\n\n`
//   - Redis 订阅 cineforge:project-events:v1:{project_id}，收到 agent_run/workflow_run/project_status
//     失效事件则重载快照再发 snapshot
//   - Redis 不可用 → 503 {detail:{code:"project_event_stream_unavailable",...,fallback:"polling"}} + Retry-After:5
//   - 中断 → `event: stream_error` + fallback:polling 后关闭

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	projectEventsHeartbeat        = 15 * time.Second
	projectEventsRetryMilliseconds = 5_000
	projectEventsRedisTimeout     = time.Second
	projectEventsChannelPrefix    = "cineforge:project-events:v1:"
)

// invalidationResources 对齐 legacy streaming._INVALIDATION_RESOURCES。
var invalidationResources = map[string]bool{
	"agent_run":      true,
	"workflow_run":   true,
	"project_status": true,
}

func (s *Server) handleRunEvents(c *gin.Context) {
	user := userFromContext(c)
	projectID := c.Param("project_id")
	ctx := c.Request.Context()

	snapshot, err := s.projects.RunEventSnapshot(ctx, user.ID, user.Role, projectID)
	if err != nil {
		writeServiceError(c, "项目运行事件", err)
		return
	}

	client, pubsub, err := s.openProjectSubscription(ctx, projectID)
	if err != nil {
		c.Header("Retry-After", "5")
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"detail": gin.H{
			"code":      "project_event_stream_unavailable",
			"message":   "Project event stream is unavailable",
			"fallback":  "polling",
		}})
		return
	}
	defer s.closeProjectSubscription(client, pubsub)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache, no-transform")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	// 首帧带 retry（对齐 legacy _sse_message(retry_milliseconds=5000)）
	if err := writeSSEFrame(c, "snapshot", snapshot, projectEventsRetryMilliseconds); err != nil {
		return
	}

	messages := pubsub.Channel()
	ticker := time.NewTicker(projectEventsHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-messages:
			if !ok {
				return // pubsub 关闭
			}
			if !isInvalidationEvent(msg.Payload, projectID) {
				continue
			}
			refreshed, err := s.projects.RunEventSnapshot(ctx, user.ID, user.Role, projectID)
			if err != nil {
				writeSSEStreamError(c, "project_event_snapshot_failed", projectID)
				return
			}
			if err := writeSSEFrame(c, "snapshot", refreshed, 0); err != nil {
				return
			}
		case <-ticker.C:
			if _, err := c.Writer.WriteString(": heartbeat\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// openProjectSubscription 建立 Redis 订阅；任一步超时/失败返回 err → handler 转 503。
func (s *Server) openProjectSubscription(ctx context.Context, projectID string) (*redis.Client, *redis.PubSub, error) {
	opts, err := redis.ParseURL(s.cfg.Redis.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse redis url: %w", err)
	}
	opts.DialTimeout = projectEventsRedisTimeout
	// ReadTimeout=-1：pubsub 阻塞读不设 deadline，避免空闲期因读超时反复重连（心跳由 handler 的 ticker 驱动）。
	opts.ReadTimeout = -1
	client := redis.NewClient(opts)
	pingCtx, pingCancel := context.WithTimeout(ctx, projectEventsRedisTimeout)
	err = client.Ping(pingCtx).Err()
	pingCancel()
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("redis ping: %w", err)
	}
	channel := projectEventsChannelPrefix + projectID
	subCtx, subCancel := context.WithTimeout(ctx, projectEventsRedisTimeout)
	pubsub := client.Subscribe(subCtx, channel)
	if _, err := pubsub.Receive(subCtx); err != nil {
		subCancel()
		_ = pubsub.Close()
		_ = client.Close()
		return nil, nil, fmt.Errorf("redis subscribe: %w", err)
	}
	subCancel()
	return client, pubsub, nil
}

// closeProjectSubscription 幂等关闭 pubsub 与连接。
func (s *Server) closeProjectSubscription(client *redis.Client, pubsub *redis.PubSub) {
	if pubsub != nil {
		_ = pubsub.Close()
	}
	if client != nil {
		_ = client.Close()
	}
}

// isInvalidationEvent 对齐 legacy _event_payload + 校验（cross-project 忽略、非 invalidation 忽略）。
func isInvalidationEvent(payload, projectID string) bool {
	var p struct {
		Event     string `json:"event"`
		ProjectID string `json:"project_id"`
		Resource  string `json:"resource"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return false
	}
	return p.Event == "invalidation" && p.ProjectID == projectID && invalidationResources[p.Resource]
}

// writeSSEFrame 写一个 SSE 帧并 flush。retryMs>0 时带 retry 行（仅首帧）。
func writeSSEFrame(c *gin.Context, event string, data any, retryMs int) error {
	var sb strings.Builder
	if retryMs > 0 {
		fmt.Fprintf(&sb, "retry: %d\n", retryMs)
	}
	sb.WriteString("event: ")
	sb.WriteString(event)
	sb.WriteString("\ndata: ")
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	sb.Write(raw)
	sb.WriteString("\n\n")
	if _, err := c.Writer.WriteString(sb.String()); err != nil {
		return err
	}
	c.Writer.Flush()
	return nil
}

// writeSSEStreamError 发 stream_error 帧（对齐 legacy code + fallback:polling）后返回 false。
func writeSSEStreamError(c *gin.Context, code, projectID string) bool {
	err := writeSSEFrame(c, "stream_error", gin.H{
		"type":       "stream_error",
		"code":       code,
		"project_id": projectID,
		"fallback":   "polling",
	}, projectEventsRetryMilliseconds)
	return err == nil
}