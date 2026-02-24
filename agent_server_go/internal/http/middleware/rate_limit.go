package middleware

import (
	"fmt"

	"github.com/gofiber/fiber/v2"

	cache "agent_server_go/internal/cache/redis"
	"agent_server_go/internal/http/response"
)

func RateLimit(rdb *cache.Client, rps int, burst int) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// 未配置 Redis 时直接跳过，方便本地纯内存调试
		if rdb == nil {
			return c.Next()
		}

		// 优先按 user_id 限流；未登录用户则退化为按 IP 限流
		userID, _ := c.Locals("user_id").(string)
		if userID == "" {
			userID = c.IP()
		}
		// 把 path 纳入 key，避免一个接口流量影响另一个接口
		key := fmt.Sprintf("%s:%s", c.Path(), userID)

		ok, err := rdb.Allow(c.UserContext(), key, rps, burst)
		if err != nil {
			return response.Error(c, fiber.StatusInternalServerError, "RATE_LIMIT_ERROR", err.Error())
		}
		if !ok {
			return response.Error(c, fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "rate limit exceeded")
		}

		return c.Next()
	}
}
