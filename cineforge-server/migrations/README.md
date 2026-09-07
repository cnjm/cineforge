# goose 迁移目录

- 本目录存放 CineForge 单库（`cineforge`）的 schema 演进 SQL，文件名格式：`NNNNN_描述.sql`，内含 `-- +goose Up` / `-- +goose Down`。
- **schema 只能通过本目录迁移**：禁止启动建表、禁止手工 DDL、禁止改动已上线的版本文件。
- 执行（在 cineforge-server 根目录）：

```bash
export CINEFORGE_DB_DSN='postgres://cineforge:CHANGE_ME@127.0.0.1:5432/cineforge'
go run ./cmd/migrate -action up
go run ./cmd/migrate -action status
go run ./cmd/migrate -action down step:1   # 回滚 1 步
```

- P2b 阶段将根据「旧表 → 新 schema 单库映射」定稿后生成 `00001_...` 起的新迁移。