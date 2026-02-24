package mysql

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type Store struct {
	// Store 是 MySQL 访问入口，后续 repository 方法挂在它上面
	DB *sql.DB
}

func New(dsn string) (*Store, error) {
	// sql.Open 不会立刻建立连接，真正连通性在 Ping 阶段验证
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	// 设置连接池参数，避免默认值在高并发下失控
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(15 * time.Minute)
	return &Store{DB: db}, nil
}

func (s *Store) Ping(ctx context.Context) error {
	// 启动时健康检查使用
	return s.DB.PingContext(ctx)
}

func (s *Store) Close() error {
	return s.DB.Close()
}
