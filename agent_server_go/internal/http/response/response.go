package response

import "github.com/gofiber/fiber/v2"

type SuccessBody struct {
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func JSON(c *fiber.Ctx, status int, data any) error {
	return c.Status(status).JSON(SuccessBody{
		Data:      data,
		RequestID: requestID(c),
	})
}

func Error(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(ErrorBody{
		Code:      code,
		Message:   message,
		RequestID: requestID(c),
	})
}

func requestID(c *fiber.Ctx) string {
	v := c.Locals("request_id")
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
