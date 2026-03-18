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
	"agent_server_go/internal/retrieval"
	"agent_server_go/internal/service"
	"agent_server_go/internal/store/mysql"
	"agent_server_go/internal/vector/milvus"
)

func main() {
	// 1) 读取环境变量配置（端口、DSN、模型配置等）
	cfg := config.Load() // 从环境变量读取配置，并填充默认值
	// 2) 初始化日志、指标、Tracing（当前 tracing 是最小骨架）
	logger := obs.NewLogger()                  // 创建统一日志器（带前缀 [agent-server]）
	metrics := obs.NewMetrics()                // 创建 Prometheus 指标注册器与指标对象
	shutdownTracer := obs.InitTracerProvider() // 初始化 OTel tracer，并返回关闭函数
	defer func() {
		// 进程退出前关闭 tracer provider，避免资源泄漏
		_ = shutdownTracer(context.Background())
	}()

	ctx := context.Background()

	// 3) 初始化 MySQL（允许失败：失败时服务仍可启动，只是相关能力降级）
	var store *mysql.Store
	if cfg.MySQLDSN != "" {
		s, err := mysql.New(cfg.MySQLDSN) // 创建 MySQL 连接池（不是立刻强连通）
		if err != nil {
			logger.Printf("mysql init failed: %v", err)
		} else {
			if err := s.Ping(ctx); err != nil { // 主动探活，确认数据库可连通
				logger.Printf("mysql ping failed: %v", err)
			} else {
				store = s
			}
		}
	}
	if store != nil {
		defer store.Close() // 进程退出时关闭数据库连接池
	}

	// 4) 初始化 Redis（失败时限流/缓存会退化，但服务仍可运行）
	var redisClient *cache.Client
	if cfg.RedisAddr != "" {
		rdb := cache.New(cfg.RedisAddr, cfg.RedisPass) // 创建 Redis 客户端
		if err := rdb.Ping(ctx); err != nil {          // 检查 Redis 是否可达
			logger.Printf("redis ping failed, disable redis-backed features: %v", err)
		} else {
			redisClient = rdb
		}
	}

	// 5) 初始化 RabbitMQ（失败时异步任务能力不可用，但接口骨架仍可工作）
	var mq *rabbitmq.Client
	if cfg.RabbitMQURL != "" {
		m, err := rabbitmq.New(cfg.RabbitMQURL) // 建立 RabbitMQ 连接和 channel
		if err != nil {
			logger.Printf("rabbitmq init failed: %v", err)
		} else {
			mq = m
			if err := mq.EnsureQueues(); err != nil { // 声明任务队列（tasks / tasks.dlq）
				logger.Printf("rabbitmq queue setup failed: %v", err)
			}
		}
	}
	if mq != nil {
		defer mq.Close() // 退出时关闭 MQ channel 与连接
	}

	// 6) 初始化模型网关与 service 层（聚合业务依赖）
	model := modelgateway.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)             // 创建 OpenAI-compatible 模型客户端
	model.SetHTTPTimeout(time.Duration(cfg.LLMHTTPTimeoutSeconds) * time.Second)       // edit/tool-calling 场景通常比普通 chat 更慢
	model.SetMaxContextTokens(cfg.LLMMaxContextTokens)                                 // 可选：环境变量显式覆盖上下文窗口
	svc := service.New(store, redisClient, mq, model, cfg.RateLimitRPS, cfg.RateBurst) // 组装业务服务（service 层）
	svc.SetMemoryOptions(cfg.ChatMemoryMaxMessages, cfg.ChatMemoryTTLSeconds)          // 配置会话短期记忆窗口与 TTL
	svc.SetVectorClient(milvus.New(cfg.MilvusAddr))                                    // 接入真实 Milvus REST 客户端
	// 可选：桥接到已有 TypeScript RAG（agent/dist/retrieve.js）；失败时 service 会自动回退占位检索。
	svc.SetTSBridge(retrieval.NewTSBridge(retrieval.TSBridgeConfig{
		NodeBin:       cfg.TSRAGNodeBin,
		ScriptPath:    cfg.TSRAGScriptPath,
		AgentDistDir:  cfg.TSRAGAgentDistDir,
		TargetRootDir: cfg.TSRAGTargetRoot,
	}))
	// 启动异步任务消费者（当前为占位实现）
	svc.StartTaskConsumer(ctx) // 后台消费 MQ 中的任务消息，并更新任务状态

	// 7) 初始化 HTTP 服务器并注册路由/中间件
	srv := server.NewServer(svc, redisClient, cfg.JWTSecret, cfg.RateLimitRPS, cfg.RateBurst, metrics) // 创建 Fiber App + 路由 + 中间件

	go func() {
		addr := ":" + cfg.AppPort
		logger.Printf("%s start on %s", cfg.AppName, addr)
		if err := srv.App.Listen(addr); err != nil { // 启动 HTTP 服务并阻塞监听端口
			logger.Printf("fiber listen exited: %v", err)
		}
	}()

	// 8) 阻塞等待退出信号（Ctrl+C 或容器停止）
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM) // 把系统退出信号转发到 channel
	<-quit

	// 9) 优雅关闭：给在途请求一个超时时间
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // 创建超时上下文，限制关闭等待时间
	defer cancel()
	if err := srv.App.ShutdownWithContext(shutdownCtx); err != nil { // 停止接收新请求，等待在途请求结束
		log.Printf("server shutdown failed: %v", err)
	}
}
