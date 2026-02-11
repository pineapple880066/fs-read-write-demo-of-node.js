package redis

import (
	"context"
	"encoding/json"
	"time"
)

func (c *Client) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.RDB.Set(ctx, key, b, ttl).Err()
}

func (c *Client) GetJSON(ctx context.Context, key string, out any) (bool, error) {
	v, err := c.RDB.Get(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(v), out); err != nil {
		return false, err
	}
	return true, nil
}
