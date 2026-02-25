package service

import (
	"context"
	"log"
	"time"

	"agent_server_go/internal/mq/rabbitmq"
)

// StartTaskConsumer 启动 MQ 消费者（当前是周5阶段的占位骨架）。
// 后续会在这里替换成真实的 ingest / chunk / embedding / reindex 流程。
func (s *Services) StartTaskConsumer(ctx context.Context) {
	// StartTaskConsumer 启动异步任务消费者（当前仅演示状态流转骨架）。
	// 没有 MQ 或 DB 时直接跳过，避免本地最小环境启动失败
	if s.MQ == nil || s.Store == nil {
		return
	}

	err := s.MQ.ConsumeTasks(ctx, func(ctx context.Context, msg rabbitmq.TaskMessage) error {
		log.Printf("consume task: id=%s type=%s tenant=%s", msg.TaskID, msg.Type, msg.TenantID)

		// 处理前先把任务状态改成 running
		_ = s.Store.UpdateTaskStatus(ctx, msg.TaskID, "running", nil)

		// Simulate task processing.
		time.Sleep(100 * time.Millisecond)

		// 占位处理成功后更新状态；后续需要补 retry / dlq / 错误记录
		return s.Store.UpdateTaskStatus(ctx, msg.TaskID, "success", nil)
	})
	if err != nil {
		log.Printf("start consumer failed: %v", err)
	}
}
