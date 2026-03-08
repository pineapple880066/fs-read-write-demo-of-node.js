package handlers

import (
	"embed"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"

	"agent_server_go/internal/http/response"
)

//go:embed ui/*
var uiAssets embed.FS

type WebHandler struct {
	JWTSecret string
}

func NewWebHandler(jwtSecret string) *WebHandler {
	return &WebHandler{JWTSecret: jwtSecret}
}

func (h *WebHandler) Index(c *fiber.Ctx) error {
	return sendEmbeddedAsset(c, "ui/index.html", "html")
}

func (h *WebHandler) AppCSS(c *fiber.Ctx) error {
	return sendEmbeddedAsset(c, "ui/app.css", "css")
}

func (h *WebHandler) AppJS(c *fiber.Ctx) error {
	return sendEmbeddedAsset(c, "ui/app.js", "js")
}

func (h *WebHandler) Bootstrap(c *fiber.Ctx) error {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "local-ui",
		"user_id":   "desktop-user",
		"iat":       now.Unix(),
		"exp":       now.Add(24 * time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(h.JWTSecret))
	if err != nil {
		return response.Error(c, fiber.StatusInternalServerError, "BOOTSTRAP_FAILED", err.Error())
	}

	return response.JSON(c, fiber.StatusOK, fiber.Map{
		"token":     signed,
		"tenant_id": "local-ui",
		"user_id":   "desktop-user",
		"app_name":  "Local Codebase Agent",
	})
}

func sendEmbeddedAsset(c *fiber.Ctx, name, fileType string) error {
	body, err := uiAssets.ReadFile(name)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, fmt.Sprintf("asset not found: %s", name))
	}
	c.Type(fileType, "utf-8")
	return c.Send(body)
}
