package middleware

// Redis限流中间件
import (
	"fmt"

	"github.com/gofiber/fiber/v2"

	cache "agent_server_go/internal/cache/redis"
	"agent_server_go/internal/http/response"
)

func RateLimit(rdb *cache.Client, rps int, burst int) fiber.Handler {
	// RateLimit 返回一个中间件：对每个请求做“路径+用户”维度的限流检查。
	return func(c *fiber.Ctx) error {
		// 未配置 Redis 时直接跳过，方便本地纯内存调试
		if rdb == nil {
			return c.Next()
		}

		// 优先按 user_id 限流；未登录用户则退化为按 IP 限流
		userID, _ := c.Locals("user_id").(string) // 取出user_id或者获取IP()
		if userID == "" {
			userID = c.IP()
		}
		// key 是“限流对象标识”，不是数据库主键。
		// 例子：/v1/search:u1 或 /v1/search:127.0.0.1
		// Redis.Allow 内部会再拼上秒级时间桶，形成真正计数 key。
		// 把 path 纳入 key，避免一个接口流量影响另一个接口。
		key := fmt.Sprintf("%s:%s", c.Path(), userID) // 路径 + user_id 才是 key

		ok, err := rdb.Allow(c.UserContext(), key, rps, burst) // 询问 Redis：这次请求是否允许通过
		if err != nil {
			return response.Error(c, fiber.StatusInternalServerError, "RATE_LIMIT_ERROR", err.Error())
		}
		if !ok {
			// 超过阈值返回 429（Too Many Requests）
			return response.Error(c, fiber.StatusTooManyRequests, "TOO_MANY_REQUESTS", "rate limit exceeded")
		}

		return c.Next() // 限流通过，继续执行后续 handler
	}
}
