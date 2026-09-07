package main

import (
	"database/sql"
	"flag"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx sql 驱动
	"github.com/pressly/goose/v3"
)

// migrate 命令行：对单库 cineforge 执行 goose 迁移。
//
// 用法：
//
//	cd cineforge-server
//	CINEFORGE_DB_DSN='postgres://cineforge:***@127.0.0.1:5432/cineforge' \
//	  go run ./cmd/migrate -action up
//	go run ./cmd/migrate -action status        # 查看当前版本
//	go run ./cmd/migrate -action down step:1   # 回滚 1 步
func main() {
	var (
		action string
		dsn    string
		dir    string
	)
	flag.StringVar(&action, "action", "up", "goose 动作：up | down | status | version | up-by-one | redo")
	flag.StringVar(&dsn, "dsn", "", "PostgreSQL DSN（缺省读 CINEFORGE_DB_DSN）")
	flag.StringVar(&dir, "dir", "migrations", "goose 迁移目录")
	flag.Parse()

	if dsn == "" {
		dsn = os.Getenv("CINEFORGE_DB_DSN")
	}
	if dsn == "" {
		log.Fatal("缺少数据库 DSN：-dsn 参数或环境变量 CINEFORGE_DB_DSN")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("set dialect: %v", err)
	}

	args := flag.Args()
	switch action {
	case "up":
		err = goose.Up(db, dir)
	case "up-by-one":
		err = goose.UpByOne(db, dir)
	case "status":
		err = goose.Status(db, dir)
	case "version":
		err = goose.Version(db, dir)
	case "down":
		err = goose.Down(db, dir)
	case "redo":
		err = goose.Redo(db, dir)
	default:
		log.Fatalf("未知 action: %s", action)
	}
	if err != nil {
		log.Fatalf("goose %s: %v", action, err)
	}
	_ = args
	log.Printf("goose %s ok", action)
}
