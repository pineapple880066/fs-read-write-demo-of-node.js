package handlers

import (
	"github.com/gofiber/fiber/v2"

	"agent_server_go/internal/http/response"
)

func Health(c *fiber.Ctx) error {
	// 最小健康检查：只表示 HTTP 进程存活，不代表所有依赖都健康
	return response.JSON(c, fiber.StatusOK, fiber.Map{"status": "ok"})
}
