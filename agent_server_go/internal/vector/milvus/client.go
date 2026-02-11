package milvus

import "context"

type Client struct {
	Addr string
}

func New(addr string) *Client {
	return &Client{Addr: addr}
}

// Placeholder methods for week-6 vector retrieval.
func (c *Client) UpsertEmbedding(_ context.Context, _ string, _ []float32, _ map[string]string) error {
	return nil
}

func (c *Client) Search(_ context.Context, _ []float32, topK int) ([]string, error) {
	return make([]string, 0, topK), nil
}
