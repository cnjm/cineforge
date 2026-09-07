// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"time"
)

type Users struct {
	ID           string    `db:"id" json:"id"`
	Username     string    `db:"username" json:"username"`
	Phone        *string   `db:"phone" json:"phone"`
	Name         *string   `db:"name" json:"name"`
	DisplayName  string    `db:"display_name" json:"display_name"`
	Role         string    `db:"role" json:"role"`
	PasswordHash *string   `db:"password_hash" json:"password_hash"`
	IsActive     bool      `db:"is_active" json:"is_active"`
	SyncVersion  int64     `db:"sync_version" json:"sync_version"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type Permissions struct {
	ID             string    `db:"id" json:"id"`
	UserId         string    `db:"user_id" json:"user_id"`
	ProjectId      *string   `db:"project_id" json:"project_id"`
	PermissionType string    `db:"permission_type" json:"permission_type"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time `db:"updated_at" json:"updated_at"`
}
