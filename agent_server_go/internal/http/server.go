package http

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	cache "agent_server_go/internal/cache/redis"
	"agent_server_go/internal/http/handlers"
	"agent_server_go/internal/http/middleware"
	"agent_server_go/internal/obs"
	"agent_server_go/internal/service"
)

type Server struct {
	App *fiber.App
}

func NewServer(svc *service.Services, redisClient *cache.Client, jwtSecret string, rateRPS int, rateBurst int, metrics *obs.Metrics) *Server {
	// 设置读写超时，避免连接长期占用
	app := fiber.New(fiber.Config{ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second})

	// 全局恢复中间件：防止 panic 直接把进程打崩
	app.Use(recover.New())
	// 全局 request_id：后续日志/响应体会复用
	app.Use(middleware.RequestID())
	// 统一记录基础 HTTP 指标（请求数、耗时）
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		status := c.Response().StatusCode()
		pathLabel := strings.Clone(c.Path())
		methodLabel := strings.Clone(c.Method())
		statusLabel := stringFromStatus(status)
		metrics.HTTPRequestsTotal.WithLabelValues(pathLabel, methodLabel, statusLabel).Inc()
		metrics.HTTPDurationMs.WithLabelValues(pathLabel, methodLabel).Observe(float64(time.Since(start).Milliseconds()))
		return err
	})

	h := handlers.NewAgentHandler(svc)

	// 基础探针与 Prometheus 指标端点（不做鉴权，方便监控系统抓取）
	app.Get("/healthz", handlers.Health)
	app.Get("/metrics", adaptor.HTTPHandler(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

	// 业务 API 统一挂在 /v1 下，并在分组层做鉴权/限流
	v1 := app.Group("/v1")
	v1.Use(middleware.JWTAuth(jwtSecret))
	v1.Use(middleware.RateLimit(redisClient, rateRPS, rateBurst))
	v1.Post("/chat", h.Chat)
	v1.Post("/ingest", h.Ingest)
	v1.Post("/search", h.Search)
	v1.Get("/tasks/:id", h.GetTask)

	return &Server{App: app}
}

func stringFromStatus(status int) string {
	// 把具体状态码压缩成状态段，减少指标 label 维度
	if status < 100 {
		return "0"
	}
	if status < 200 {
		return "1xx"
	}
	if status < 300 {
		return "2xx"
	}
	if status < 400 {
		return "3xx"
	}
	if status < 500 {
		return "4xx"
	}
	return "5xx"
}
