package middleware

import (
	"fmt"

	"github.com/gofiber/fiber/v2"

	cache "agent_server_go/internal/cache/redis"
	"agent_server_go/internal/http/response"
)

func RateLimit(rdb *cache.Client, rps int, burst int) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if rdb == nil {
			return c.Next()
		}

		userID, _ := c.Locals("user_id").(string)
		if userID == "" {
			userID = c.IP()
		}
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
