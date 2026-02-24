package redis

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type Client struct {
	// 包一层是为了隔离第三方库，方便后续替换/扩展
	RDB *redis.Client
}

func New(addr, pass string) *Client {
	// 当前固定使用 DB 0；多租户方案可扩展 namespace/key 前缀
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: pass, DB: 0})
	return &Client{RDB: rdb}
}

func (c *Client) Ping(ctx context.Context) error {
	// 单独加 2s 超时，避免启动检查无限等待
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.RDB.Ping(ctx).Err()
}
