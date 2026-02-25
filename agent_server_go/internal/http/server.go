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
	app := fiber.New(fiber.Config{ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}) // 创建 Fiber 应用实例

	// 全局恢复中间件：防止 panic 直接把进程打崩
	app.Use(recover.New()) // 捕获 panic，返回 500，避免服务进程崩溃
	// 全局 request_id：后续日志/响应体会复用
	app.Use(middleware.RequestID()) // 给每个请求注入 request_id，并写入响应头
	// 统一记录基础 HTTP 指标（请求数、耗时）
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()                          // 继续执行后续中间件/handler
		status := c.Response().StatusCode()      // 读取 handler 执行后的最终状态码
		pathLabel := strings.Clone(c.Path())     // 复制 path，避免底层缓冲复用导致指标标签异常
		methodLabel := strings.Clone(c.Method()) // 复制 method，原因同上
		statusLabel := stringFromStatus(status)
		metrics.HTTPRequestsTotal.WithLabelValues(pathLabel, methodLabel, statusLabel).Inc()                              // 请求计数 +1
		metrics.HTTPDurationMs.WithLabelValues(pathLabel, methodLabel).Observe(float64(time.Since(start).Milliseconds())) // 记录耗时直方图
		return err
	})

	h := handlers.NewAgentHandler(svc) // 创建 HTTP handler（内部调用 service 层）

	// 基础探针与 Prometheus 指标端点（不做鉴权，方便监控系统抓取）
	app.Get("/healthz", handlers.Health)                                                                    // 健康检查端点
	app.Get("/metrics", adaptor.HTTPHandler(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))) // 显示 Prometheus 指标（标准 net/http handler 适配到 Fiber）

	// 业务 API 统一挂在 /v1 下(分组)，并在分组层做鉴权/限流
	v1 := app.Group("/v1")                                        // 创建 API v1 路由分组
	v1.Use(middleware.JWTAuth(jwtSecret))                         // 给整个分组加 JWT 鉴权(JSON Web Token)
	v1.Use(middleware.RateLimit(redisClient, rateRPS, rateBurst)) // 给整个分组加限流
	v1.Post("/chat", h.Chat)                                      // 注册 POST /v1/chat
	v1.Post("/ingest", h.Ingest)                                  // 注册 POST /v1/ingest
	v1.Post("/search", h.Search)                                  // 注册 POST /v1/search
	v1.Get("/tasks/:id", h.GetTask)                               // 注册 GET /v1/tasks/:id

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

// 常见 http status codem
// 2xx 成功
// 200：成功（最常见）
// 201：创建成功（常用于新建资源）
// 202：已接收，异步处理中（你 /ingest 很适合用这个）
// 3xx 重定向
// 301：永久重定向
// 302：临时重定向
// 4xx 客户端请求有问题
// 400：请求参数错误
// 401：未登录/未授权（你 JWT 失败就是这个）
// 403：禁止访问
// 404：路由不存在 / 资源不存在
// 429：请求太频繁（限流）
// 5xx 服务端错误
// 500：服务器内部错误（代码异常、panic 等）
// 502/503/504：网关或上游服务问题（常见于微服务场景）
