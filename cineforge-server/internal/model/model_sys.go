// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"time"
)

type SysRole struct {
	RoleId    int64      `db:"role_id" json:"role_id"`
	RoleName  string     `db:"role_name" json:"role_name"`
	Status    string     `db:"status" json:"status"`
	RoleKey   string     `db:"role_key" json:"role_key"`
	RoleSort  int32      `db:"role_sort" json:"role_sort"`
	Flag      string     `db:"flag" json:"flag"`
	Remark    string     `db:"remark" json:"remark"`
	Admin     bool       `db:"admin" json:"admin"`
	DataScope string     `db:"data_scope" json:"data_scope"`
	CreateBy  int64      `db:"create_by" json:"create_by"`
	UpdateBy  int64      `db:"update_by" json:"update_by"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt time.Time  `db:"updated_at" json:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at" json:"deleted_at"`
}

type SysCasbinRule struct {
	ID    int64  `db:"id" json:"id"`
	Ptype string `db:"ptype" json:"ptype"`
	V0    string `db:"v0" json:"v0"`
	V1    string `db:"v1" json:"v1"`
	V2    string `db:"v2" json:"v2"`
	V3    string `db:"v3" json:"v3"`
	V4    string `db:"v4" json:"v4"`
	V5    string `db:"v5" json:"v5"`
}
