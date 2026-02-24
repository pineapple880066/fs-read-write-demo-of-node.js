package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	cache "agent_server_go/internal/cache/redis"
	"agent_server_go/internal/config"
	server "agent_server_go/internal/http"
	"agent_server_go/internal/modelgateway"
	"agent_server_go/internal/mq/rabbitmq"
	"agent_server_go/internal/obs"
	"agent_server_go/internal/service"
	"agent_server_go/internal/store/mysql"
)

func main() {
	// 1) 读取环境变量配置（端口、DSN、模型配置等）
	cfg := config.Load()
	// 2) 初始化日志、指标、Tracing（当前 tracing 是最小骨架）
	logger := obs.NewLogger()
	metrics := obs.NewMetrics()
	shutdownTracer := obs.InitTracerProvider()
	defer func() {
		// 进程退出前关闭 tracer provider，避免资源泄漏
		_ = shutdownTracer(context.Background())
	}()

	ctx := context.Background()

	// 3) 初始化 MySQL（允许失败：失败时服务仍可启动，只是相关能力降级）
	var store *mysql.Store
	if cfg.MySQLDSN != "" {
		s, err := mysql.New(cfg.MySQLDSN)
		if err != nil {
			logger.Printf("mysql init failed: %v", err)
		} else {
			if err := s.Ping(ctx); err != nil {
				logger.Printf("mysql ping failed: %v", err)
			} else {
				store = s
			}
		}
	}
	if store != nil {
		defer store.Close()
	}

	// 4) 初始化 Redis（失败时限流/缓存会退化，但服务仍可运行）
	redisClient := cache.New(cfg.RedisAddr, cfg.RedisPass)
	if err := redisClient.Ping(ctx); err != nil {
		logger.Printf("redis ping failed: %v", err)
	}

	// 5) 初始化 RabbitMQ（失败时异步任务能力不可用，但接口骨架仍可工作）
	var mq *rabbitmq.Client
	if cfg.RabbitMQURL != "" {
		m, err := rabbitmq.New(cfg.RabbitMQURL)
		if err != nil {
			logger.Printf("rabbitmq init failed: %v", err)
		} else {
			mq = m
			if err := mq.EnsureQueues(); err != nil {
				logger.Printf("rabbitmq queue setup failed: %v", err)
			}
		}
	}
	if mq != nil {
		defer mq.Close()
	}

	// 6) 初始化模型网关与 service 层（聚合业务依赖）
	model := modelgateway.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	svc := service.New(store, redisClient, mq, model, cfg.RateLimitRPS, cfg.RateBurst)
	// 启动异步任务消费者（当前为占位实现）
	svc.StartTaskConsumer(ctx)

	// 7) 初始化 HTTP 服务器并注册路由/中间件
	srv := server.NewServer(svc, redisClient, cfg.JWTSecret, cfg.RateLimitRPS, cfg.RateBurst, metrics)

	go func() {
		addr := ":" + cfg.AppPort
		logger.Printf("%s start on %s", cfg.AppName, addr)
		if err := srv.App.Listen(addr); err != nil {
			logger.Printf("fiber listen exited: %v", err)
		}
	}()

	// 8) 阻塞等待退出信号（Ctrl+C 或容器停止）
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// 9) 优雅关闭：给在途请求一个超时时间
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.App.ShutdownWithContext(shutdownCtx); err != nil {
		log.Printf("server shutdown failed: %v", err)
	}
}
