package handlers

import (
	"github.com/gofiber/fiber/v2"

	"agent_server_go/internal/http/response"
	"agent_server_go/internal/service"
)

type AgentHandler struct {
	Svc *service.Services
}

func NewAgentHandler(svc *service.Services) *AgentHandler {
	return &AgentHandler{Svc: svc}
}

func (h *AgentHandler) Chat(c *fiber.Ctx) error {
	var req service.ChatRequest
	if err := c.BodyParser(&req); err != nil {
		return response.Error(c, fiber.StatusBadRequest, "BAD_REQUEST", err.Error())
	}

	out, err := h.Svc.Chat(c.UserContext(), req)
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, "CHAT_FAILED", err.Error())
	}
	return response.JSON(c, fiber.StatusOK, out)
}

func (h *AgentHandler) Ingest(c *fiber.Ctx) error {
	var req service.IngestRequest
	if err := c.BodyParser(&req); err != nil {
		return response.Error(c, fiber.StatusBadRequest, "BAD_REQUEST", err.Error())
	}

	out, err := h.Svc.Ingest(c.UserContext(), req)
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, "INGEST_FAILED", err.Error())
	}
	return response.JSON(c, fiber.StatusAccepted, out)
}

func (h *AgentHandler) Search(c *fiber.Ctx) error {
	var req service.SearchRequest
	if err := c.BodyParser(&req); err != nil {
		return response.Error(c, fiber.StatusBadRequest, "BAD_REQUEST", err.Error())
	}

	out, err := h.Svc.Search(c.UserContext(), req)
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, "SEARCH_FAILED", err.Error())
	}
	return response.JSON(c, fiber.StatusOK, out)
}

func (h *AgentHandler) GetTask(c *fiber.Ctx) error {
	taskID := c.Params("id")
	out, err := h.Svc.GetTask(c.UserContext(), taskID)
	if err != nil {
		return response.Error(c, fiber.StatusNotFound, "TASK_NOT_FOUND", err.Error())
	}
	return response.JSON(c, fiber.StatusOK, out)
}
