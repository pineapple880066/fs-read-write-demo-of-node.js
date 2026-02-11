package rabbitmq

import "time"

type TaskMessage struct {
	TaskID      string    `json:"task_id"`
	TenantID    string    `json:"tenant_id"`
	Type        string    `json:"type"`
	Payload     string    `json:"payload"`
	RetryCount  int       `json:"retry_count"`
	CreatedAt   time.Time `json:"created_at"`
}
