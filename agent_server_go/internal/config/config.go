package config

import (
	"os"
	"strconv"
)

type Config struct {
	// 应用基础信息
	AppName string
	AppEnv  string
	AppPort string
	// 基础设施配置
	MySQLDSN    string
	RedisAddr   string
	RedisPass   string
	RabbitMQURL string
	MilvusAddr  string
	// 模型网关配置
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string
	// 安全与限流配置
	JWTSecret    string
	RateLimitRPS int
	RateBurst    int
}

func Load() Config {
	// 集中在这里做环境变量读取，避免业务层到处直接取 os.Getenv
	return Config{
		AppName:      getEnv("APP_NAME", "agent-server-go"),
		AppEnv:       getEnv("APP_ENV", "dev"),
		AppPort:      getEnv("APP_PORT", "8080"),
		MySQLDSN:     getEnv("MYSQL_DSN", ""),
		RedisAddr:    getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:    getEnv("REDIS_PASSWORD", ""),
		RabbitMQURL:  getEnv("RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:5672/"),
		MilvusAddr:   getEnv("MILVUS_ADDR", "127.0.0.1:19530"),
		LLMBaseURL:   getEnv("LLM_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
		LLMAPIKey:    getEnv("LLM_API_KEY", ""),
		LLMModel:     getEnv("LLM_MODEL", "qwen3-coder-plus"),
		JWTSecret:    getEnv("JWT_SECRET", "change_me"),
		RateLimitRPS: getEnvInt("RATE_LIMIT_RPS", 5),
		RateBurst:    getEnvInt("RATE_LIMIT_BURST", 10),
	}
}

func getEnv(key, fallback string) string {
	// 允许环境变量为空时回退默认值，方便本地快速启动
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	// 整型配置解析失败时回退默认值，避免启动时 panic
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
