package redis

import (
	"context"
	"fmt"
	"time"
)

// Allow 使用 Redis 实现一个简单固定窗口限流（fixed window）。
// 这个版本实现简单、便于教学，但边界处会有突刺问题（burst spike）。
func (c *Client) Allow(ctx context.Context, key string, rps int, burst int) (bool, error) {
	nowBucket := time.Now().Unix()
	// 把时间戳按秒分桶：同一秒内的请求落在同一个 key 上累加
	fullKey := fmt.Sprintf("rl:%s:%d", key, nowBucket)

	n, err := c.RDB.Incr(ctx, fullKey).Result()
	if err != nil {
		return false, err
	}
	if n == 1 {
		// 设置短 TTL，防止限流 key 无限制堆积
		_ = c.RDB.Expire(ctx, fullKey, 2*time.Second).Err()
	}

	limit := int64(rps)
	if burst > rps {
		// 若配置了更高 burst，则临时放宽阈值
		limit = int64(burst)
	}
	return n <= limit, nil
}
