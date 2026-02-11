package service

import (
	"context"
	"log"
	"time"

	"agent_server_go/internal/mq/rabbitmq"
)

// StartTaskConsumer is week-5 async worker entry.
// Current implementation is a lightweight scaffold and should be replaced
// by real ingest/embedding/reindex pipeline handlers.
func (s *Services) StartTaskConsumer(ctx context.Context) {
	if s.MQ == nil || s.Store == nil {
		return
	}

	err := s.MQ.ConsumeTasks(ctx, func(ctx context.Context, msg rabbitmq.TaskMessage) error {
		log.Printf("consume task: id=%s type=%s tenant=%s", msg.TaskID, msg.Type, msg.TenantID)

		_ = s.Store.UpdateTaskStatus(ctx, msg.TaskID, "running", nil)

		// Simulate task processing.
		time.Sleep(100 * time.Millisecond)

		// Placeholder status update pattern.
		return s.Store.UpdateTaskStatus(ctx, msg.TaskID, "success", nil)
	})
	if err != nil {
		log.Printf("start consumer failed: %v", err)
	}
}
