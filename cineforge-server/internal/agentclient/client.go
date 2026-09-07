package agentclient

// Python agent 子模块（:8091 bridge）HTTP 客户端。
//
// 对齐 legacy orchestrator.submit_background 的投递路径：
// POST /api/runs/{run_id}/enqueue（task_id=run_id 幂等入队）+ GET /health（探活）。
// 入队失败由调用方按 legacy 语义把 agent_run/workflow_run 置为 failed。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 持有 agent bridge 的 base URL 与可注入 Doer（测试替换点）。
type Client struct {
	base string
	hc   *http.Client
	// Do 是可注入的请求执行器；默认走 hc.Do。
	Do func(*http.Request) (*http.Response, error)
}

// New 创建 agent 客户端。addr 空时按 unreachable 处理（EnqueueRun 直接报错）。
func New(addr string) *Client {
	c := &Client{
		base: strings.TrimSuffix(addr, "/"),
		hc:   &http.Client{Timeout: 5 * time.Second},
	}
	c.Do = c.hc.Do
	return c
}

// EnqueueRun 把已落库的 queued agent_run 投递给 Celery。
// 返回错误时调用方按 legacy 语义将 run/workflow 置为 failed 但仍返回 run。
func (c *Client) EnqueueRun(ctx context.Context, runID string) error {
	if c.base == "" {
		return fmt.Errorf("agent 服务未配置地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/runs/"+runID+"/enqueue", bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("agent 队列入队不可达：%w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("agent 队列入队失败：%s", detail)
	}
	return nil
}

// Health agent bridge /health 探活（P4e hermes 真实探活的数据源之一）。
type Health struct {
	Status        string `json:"status"`
	HermesEnabled bool   `json:"hermes_enabled"`
	HermesModel   string `json:"hermes_model"`
}

// Health 查询 agent 子模块存活状态。
func (c *Client) Health(ctx context.Context) (Health, error) {
	var out Health
	if c.base == "" {
		return out, fmt.Errorf("agent 服务未配置地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/health", nil)
	if err != nil {
		return out, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return out, fmt.Errorf("agent 探活不可达：%w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return out, fmt.Errorf("agent 探活 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	return out, nil
}
