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
	// NewAgentHandler 创建 HTTP handler，并注入 service 层依赖。
	return &AgentHandler{Svc: svc}
}

func (h *AgentHandler) Chat(c *fiber.Ctx) error {
	// Chat 处理 POST /v1/chat：解析请求、调用 service.Chat、返回统一响应。
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
	// Ingest 处理 POST /v1/ingest：创建异步导入任务并返回 task_id。
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
	// Search 处理 POST /v1/search：返回检索结果（优先查租户已导入 chunks，再回退 TS RAG/占位）。
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
	// GetTask 处理 GET /v1/tasks/:id：查询异步任务状态。
	// 路径参数示例：/v1/tasks/:id
	taskID := c.Params("id")
	out, err := h.Svc.GetTask(c.UserContext(), taskID)
	if err != nil {
		return response.Error(c, fiber.StatusNotFound, "TASK_NOT_FOUND", err.Error())
	}
	return response.JSON(c, fiber.StatusOK, out)
}
