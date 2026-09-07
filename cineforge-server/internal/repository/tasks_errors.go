package repository

// P3e task 域类型化错误：对齐 legacy 的 KeyError / PermissionError / ValueError，
// 路由层按类型映射 404/403/400，detail 字符串由路由层或 ValueError 自带决定。

import (
	"errors"
	"fmt"
)

// KeyError 对齐 legacy KeyError（→ 404，detail 由路由层决定）。
type KeyError struct {
	Arg any
}

func (e *KeyError) Error() string { return fmt.Sprintf("key error: %v", e.Arg) }

// PermissionError 对齐 legacy PermissionError（→ 403，detail 由路由层决定）。
type PermissionError struct {
	Arg any
}

func (e *PermissionError) Error() string { return fmt.Sprintf("permission error: %v", e.Arg) }

// ValueError 对齐 legacy ValueError（→ 400，Detail 原样透传）。
type ValueError struct {
	Detail string
}

func (e *ValueError) Error() string { return e.Detail }

// IsNotFoundError 判定 err（含包装链）是否为 KeyError。
func IsNotFoundError(err error) bool {
	var e *KeyError
	return errors.As(err, &e)
}

// IsForbiddenError 判定 err（含包装链）是否为 PermissionError。
func IsForbiddenError(err error) bool {
	var e *PermissionError
	return errors.As(err, &e)
}

// BadRequestDetail 提取 ValueError 消息；非 ValueError 返回 ok=false。
func BadRequestDetail(err error) (string, bool) {
	var e *ValueError
	if errors.As(err, &e) {
		return e.Detail, true
	}
	return "", false
}

func notFoundError(arg any) error  { return &KeyError{Arg: arg} }
func forbiddenError(arg any) error { return &PermissionError{Arg: arg} }
func valueError(detail string) error {
	return &ValueError{Detail: detail}
}