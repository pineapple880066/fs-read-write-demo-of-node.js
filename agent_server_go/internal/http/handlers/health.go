package handlers

import (
	"github.com/gofiber/fiber/v2"

	"agent_server_go/internal/http/response"
)

func Health(c *fiber.Ctx) error {
	return response.JSON(c, fiber.StatusOK, fiber.Map{"status": "ok"})
}
