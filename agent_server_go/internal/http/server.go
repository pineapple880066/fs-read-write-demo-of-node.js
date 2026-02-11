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
	app := fiber.New(fiber.Config{ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second})

	app.Use(recover.New())
	app.Use(middleware.RequestID())
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

	app.Get("/healthz", handlers.Health)
	app.Get("/metrics", adaptor.HTTPHandler(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

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
