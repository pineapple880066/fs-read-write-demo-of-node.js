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
	cfg := config.Load()
	logger := obs.NewLogger()
	metrics := obs.NewMetrics()
	shutdownTracer := obs.InitTracerProvider()
	defer func() {
		_ = shutdownTracer(context.Background())
	}()

	ctx := context.Background()

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

	redisClient := cache.New(cfg.RedisAddr, cfg.RedisPass)
	if err := redisClient.Ping(ctx); err != nil {
		logger.Printf("redis ping failed: %v", err)
	}

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

	model := modelgateway.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	svc := service.New(store, redisClient, mq, model, cfg.RateLimitRPS, cfg.RateBurst)
	svc.StartTaskConsumer(ctx)

	srv := server.NewServer(svc, redisClient, cfg.JWTSecret, cfg.RateLimitRPS, cfg.RateBurst, metrics)

	go func() {
		addr := ":" + cfg.AppPort
		logger.Printf("%s start on %s", cfg.AppName, addr)
		if err := srv.App.Listen(addr); err != nil {
			logger.Printf("fiber listen exited: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.App.ShutdownWithContext(shutdownCtx); err != nil {
		log.Printf("server shutdown failed: %v", err)
	}
}
