package rabbitmq

import (
	"context"
	"encoding/json"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Handler func(context.Context, TaskMessage) error

func (c *Client) ConsumeTasks(ctx context.Context, h Handler) error {
	// 手动 ack（autoAck=false），由 handler 成功/失败决定 ack/nack
	msgs, err := c.Channel.Consume("tasks", "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	go func() {
		// 用 goroutine 持续消费，不阻塞启动流程
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-msgs:
				if !ok {
					return
				}
				c.handleDelivery(ctx, d, h)
			}
		}
	}()

	return nil
}

func (c *Client) handleDelivery(ctx context.Context, d amqp.Delivery, h Handler) {
	// 先把消息体反序列化成结构化任务
	var msg TaskMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		_ = d.Nack(false, false)
		return
	}

	// handler 失败时 Nack（不重回队列）；后续可扩展为重试/死信策略
	if err := h(ctx, msg); err != nil {
		log.Printf("task handler failed: task_id=%s err=%v", msg.TaskID, err)
		_ = d.Nack(false, false)
		return
	}
	// 成功后 ack，避免重复消费
	_ = d.Ack(false)
}
