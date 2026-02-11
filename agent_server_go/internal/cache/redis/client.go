package redis

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type Client struct {
	RDB *redis.Client
}

func New(addr, pass string) *Client {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: pass, DB: 0})
	return &Client{RDB: rdb}
}

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.RDB.Ping(ctx).Err()
}
