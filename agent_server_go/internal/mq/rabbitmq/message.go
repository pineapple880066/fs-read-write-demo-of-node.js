package rabbitmq

import "time"

type TaskMessage struct {
	// 与计划文档约定的任务消息体字段基本一致
	TaskID     string    `json:"task_id"`
	TenantID   string    `json:"tenant_id"`
	Type       string    `json:"type"`
	Payload    string    `json:"payload"`
	RetryCount int       `json:"retry_count"`
	CreatedAt  time.Time `json:"created_at"`
}
