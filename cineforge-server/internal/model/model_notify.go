// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"encoding/json"
	"time"
)

type Notifications struct {
	ID        string    `db:"id" json:"id"`
	UserId    string    `db:"user_id" json:"user_id"`
	Title     string    `db:"title" json:"title"`
	Content   string    `db:"content" json:"content"`
	Type      string    `db:"type" json:"type"`
	IsRead    bool      `db:"is_read" json:"is_read"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

type OperationLogs struct {
	ID         string          `db:"id" json:"id"`
	OperatorId *string         `db:"operator_id" json:"operator_id"`
	ProjectId  *string         `db:"project_id" json:"project_id"`
	TargetType string          `db:"target_type" json:"target_type"`
	TargetId   *string         `db:"target_id" json:"target_id"`
	Action     string          `db:"action" json:"action"`
	Detail     json.RawMessage `db:"detail" json:"detail"`
	CreatedAt  time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at" json:"updated_at"`
}
