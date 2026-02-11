package redis

import (
	"context"
	"fmt"
	"time"
)

// Allow checks simple fixed-window rate limit in Redis.
func (c *Client) Allow(ctx context.Context, key string, rps int, burst int) (bool, error) {
	nowBucket := time.Now().Unix()
	fullKey := fmt.Sprintf("rl:%s:%d", key, nowBucket)

	n, err := c.RDB.Incr(ctx, fullKey).Result()
	if err != nil {
		return false, err
	}
	if n == 1 {
		_ = c.RDB.Expire(ctx, fullKey, 2*time.Second).Err()
	}

	limit := int64(rps)
	if burst > rps {
		limit = int64(burst)
	}
	return n <= limit, nil
}
