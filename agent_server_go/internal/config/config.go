package config

import (
	"os"
	"strconv"
)

type Config struct {
	AppName      string
	AppEnv       string
	AppPort      string
	MySQLDSN     string
	RedisAddr    string
	RedisPass    string
	RabbitMQURL  string
	MilvusAddr   string
	LLMBaseURL   string
	LLMAPIKey    string
	LLMModel     string
	JWTSecret    string
	RateLimitRPS int
	RateBurst    int
}

func Load() Config {
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
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
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
