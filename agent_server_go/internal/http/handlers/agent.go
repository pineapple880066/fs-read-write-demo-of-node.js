package handlers

import (
	"github.com/gofiber/fiber/v2"

	"agent_server_go/internal/http/response"
	"agent_server_go/internal/service"
)

type AgentHandler struct {
	// handler 只负责 HTTP 层协议转换，业务逻辑放在 service
	Svc *service.Services
}

func NewAgentHandler(svc *service.Services) *AgentHandler {
	return &AgentHandler{Svc: svc}
}

func (h *AgentHandler) Chat(c *fiber.Ctx) error {
	// 1) 解析 JSON 请求体到 service 层请求结构
	var req service.ChatRequest
	if err := c.BodyParser(&req); err != nil {
		return response.Error(c, fiber.StatusBadRequest, "BAD_REQUEST", err.Error())
	}

	// 2) 调用业务层
	out, err := h.Svc.Chat(c.UserContext(), req)
	if err != nil {
		return response.Error(c, fiber.StatusBadRequest, "CHAT_FAILED", err.Error())
	}
	// 3) 返回统一成功体
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
	// /search 是最容易单独调试的接口，用来验证检索链路
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
	// 路径参数示例：/v1/tasks/:id
	taskID := c.Params("id")
	out, err := h.Svc.GetTask(c.UserContext(), taskID)
	if err != nil {
		return response.Error(c, fiber.StatusNotFound, "TASK_NOT_FOUND", err.Error())
	}
	return response.JSON(c, fiber.StatusOK, out)
}
