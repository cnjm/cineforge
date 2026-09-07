package casbin

// 授权判定契约（对齐 legacy Go system-core authorization）：
//
//   - 策略唯一事实源：sys_casbin_rule 表（ptype='p'，v0=role_key, v1=object, v2=action）
//   - exact-match 三元组判定 (role, object, action)，无模型文件
//   - 种子策略由 migration 00002 写入：5 平台角色 × {projects.read, tasks.read,
//     flows.write, tasks.submit} × execute；tasks.review 仅 admin/director
//   - 单进程 monolith 用内存缓存 + 60s 刷新；seed 数据只在 migration 变更，
//     不提供运行时写 API

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	reloadInterval = time.Minute
	policyTable    = `sys_casbin_rule`
)

// Enforcer 从 sys_casbin_rule 加载策略并在内存中执行 exact-match 判定。
type Enforcer struct {
	pool     *pgxpool.Pool
	mu       sync.RWMutex
	rules    map[string]map[string][]string // role -> object -> actions
	loadedAt time.Time
}

// AuthorizeError 表示授权判定服务不可用（对应 legacy AUTHZ_UNAVAILABLE / 503）。
type AuthorizeError struct{ err error }

func (e *AuthorizeError) Error() string { return "authorization unavailable: " + e.err.Error() }
func (e *AuthorizeError) Unwrap() error { return e.err }

// New 创建基于共享 pool 的 enforcer（pool 为空时 Enforce 返回 AuthorizeError）。
func New(pool *pgxpool.Pool) *Enforcer {
	return &Enforcer{pool: pool, rules: map[string]map[string][]string{}}
}

// Enforce 判定 subject(role) 是否可以对 object 执行 action。
func (e *Enforcer) Enforce(ctx context.Context, subject, object, action string) (bool, error) {
	if e.pool == nil {
		return false, &AuthorizeError{err: fmt.Errorf("database not configured")}
	}
	if err := e.ensureLoaded(ctx); err != nil {
		return false, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, a := range e.rules[subject][object] {
		if a == action {
			return true, nil
		}
	}
	return false, nil
}

// ensureLoaded 每 reloadInterval 至少重载一次策略。
func (e *Enforcer) ensureLoaded(ctx context.Context) error {
	e.mu.RLock()
	stale := e.rules == nil || time.Since(e.loadedAt) >= reloadInterval
	e.mu.RUnlock()
	if !stale {
		return nil
	}
	return e.reload(ctx)
}

func (e *Enforcer) reload(ctx context.Context) error {
	rows, err := e.pool.Query(ctx,
		`SELECT v0, v1, v2 FROM `+policyTable+` WHERE ptype = 'p' ORDER BY v0, v1, v2`)
	if err != nil {
		return &AuthorizeError{err: fmt.Errorf("load casbin policies: %w", err)}
	}
	defer rows.Close()

	next := map[string]map[string][]string{}
	for rows.Next() {
		var role, obj, act string
		if err := rows.Scan(&role, &obj, &act); err != nil {
			return &AuthorizeError{err: fmt.Errorf("scan casbin policy: %w", err)}
		}
		if next[role] == nil {
			next[role] = map[string][]string{}
		}
		next[role][obj] = append(next[role][obj], act)
	}
	if err := rows.Err(); err != nil {
		return &AuthorizeError{err: fmt.Errorf("iterate casbin policies: %w", err)}
	}

	e.mu.Lock()
	e.rules = next
	e.loadedAt = time.Now()
	e.mu.Unlock()
	return nil
}
