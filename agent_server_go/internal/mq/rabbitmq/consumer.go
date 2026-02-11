package rabbitmq

import (
	"context"
	"encoding/json"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Handler func(context.Context, TaskMessage) error

func (c *Client) ConsumeTasks(ctx context.Context, h Handler) error {
	msgs, err := c.Channel.Consume("tasks", "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	go func() {
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
	var msg TaskMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		_ = d.Nack(false, false)
		return
	}

	if err := h(ctx, msg); err != nil {
		log.Printf("task handler failed: task_id=%s err=%v", msg.TaskID, err)
		_ = d.Nack(false, false)
		return
	}
	_ = d.Ack(false)
}
