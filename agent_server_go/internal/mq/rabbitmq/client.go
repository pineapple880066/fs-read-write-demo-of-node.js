package rabbitmq

import (
	"encoding/json"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Client struct {
	Conn    *amqp.Connection
	Channel *amqp.Channel
}

func New(url string) (*Client, error) {
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
	_, err := c.Channel.QueueDeclare("tasks", true, false, false, false, nil)
	if err != nil {
		return err
	}
	_, err = c.Channel.QueueDeclare("tasks.dlq", true, false, false, false, nil)
	return err
}

func (c *Client) PublishTask(msg TaskMessage) error {
	b, _ := json.Marshal(msg)
	return c.Channel.Publish("", "tasks", false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        b,
	})
}

func (c *Client) Close() {
	if c.Channel != nil {
		_ = c.Channel.Close()
	}
	if c.Conn != nil {
		_ = c.Conn.Close()
	}
}
