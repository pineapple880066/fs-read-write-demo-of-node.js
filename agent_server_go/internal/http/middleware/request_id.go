package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gofiber/fiber/v2"
)

func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// 优先复用上游传来的 request id，便于跨服务追踪
		rid := c.Get("X-Request-Id")
		if rid == "" {
			rid = genID()
		}
		// 同时写入上下文和响应头：handler/response 包都能取到
		c.Locals("request_id", rid)
		c.Set("X-Request-Id", rid)
		return c.Next()
	}
}

func genID() string {
	// 生成 16 hex 字符的轻量 request id（8 字节随机数）
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
