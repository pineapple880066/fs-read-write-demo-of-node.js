package response

// 做统一的返回格式
// 统一成功响应格式
// 返回成：{ data, request_id }
// 统一错误响应格式
// 返回成：{ code, message, request_id }
import (
	"github.com/gofiber/fiber/v2"
)

type SuccessBody struct {
	// data 承载真实业务返回
	Data any `json:"data"`
	// request_id 用于排查问题、串联日志
	RequestID string `json:"request_id"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func JSON(c *fiber.Ctx, status int, data any) error {
	// 统一成功响应格式，避免每个 handler 手写一遍结构
	return c.Status(status).JSON(SuccessBody{
		Data:      data,
		RequestID: requestID(c),
	})
}

func Error(c *fiber.Ctx, status int, code, message string) error {
	// 统一错误响应格式，前端/调用方更容易做兼容处理
	return c.Status(status).JSON(ErrorBody{
		Code:      code,
		Message:   message,
		RequestID: requestID(c),
	})
}

func requestID(c *fiber.Ctx) string {
	// RequestID 中间件已写入 Locals；这里集中读取
	v := c.Locals("request_id")
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
