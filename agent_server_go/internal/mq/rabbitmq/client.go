package rabbitmq

import (
	"encoding/json"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	// Connection 管理 TCP 连接；Channel 管理发布/消费操作
	Conn    *amqp.Connection
	Channel *amqp.Channel
}

func New(url string) (*Client, error) {
	// New 建立 RabbitMQ 连接与 channel，供发布/消费任务使用。
	// 建立连接与 channel；如果 channel 创建失败，要记得关闭连接
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &Client{Conn: conn, Channel: ch}, nil
}

func (c *Client) EnsureQueues() error {
	// EnsureQueues 声明本项目需要的队列（tasks / tasks.dlq）。
	// 声明主队列 tasks（持久化）
	_, err := c.Channel.QueueDeclare("tasks", true, false, false, false, nil)
	if err != nil {
		return err
	}
	// 预留死信队列 tasks.dlq（当前尚未接入死信绑定）
	_, err = c.Channel.QueueDeclare("tasks.dlq", true, false, false, false, nil)
	return err
}

func (c *Client) PublishTask(msg TaskMessage) error {
	// PublishTask 把任务消息发布到 tasks 队列。
	// 当前未处理 json.Marshal 错误（简单骨架）；后续可补显式错误返回
	b, _ := json.Marshal(msg)
	return c.Channel.Publish("", "tasks", false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        b,
	})
}

func (c *Client) Close() {
	// Close 关闭 MQ 资源（channel + connection）。
	// 按 channel -> connection 顺序关闭
	if c.Channel != nil {
		_ = c.Channel.Close()
	}
	if c.Conn != nil {
		_ = c.Conn.Close()
	}
}
