package redis

import (
	"context"
	"encoding/json"
	"time"
)

func (c *Client) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	// 常用缓存写入：结构体 -> JSON 字符串
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.RDB.Set(ctx, key, b, ttl).Err()
}

func (c *Client) GetJSON(ctx context.Context, key string, out any) (bool, error) {
	// 返回 bool 表示“是否命中”，调用方可区分 miss 和反序列化错误
	v, err := c.RDB.Get(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(v), out); err != nil {
		return false, err
	}
	return true, nil
}
