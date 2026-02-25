package middleware

// 中间件:为每个 请求 设置一个 request_id
// 如果请求没有 request id，就生成一个；然后把它存起来并回给客户端，再继续处理请求
import (
	"crypto/rand"  // 安全生成随机数
	"encoding/hex" // 进制转换，二进制转化为十六进制字符串

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
