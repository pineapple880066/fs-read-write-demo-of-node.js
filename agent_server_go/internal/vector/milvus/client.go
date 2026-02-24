package milvus

import "context"

type Client struct {
	// 当前仅保存地址；真实接入时会维护 SDK client/collection 等对象
	Addr string
}

func New(addr string) *Client {
	return &Client{Addr: addr}
}

// Placeholder methods for week-6 vector retrieval.
// 目的是先固定接口，再逐步替换内部实现，减少上层改动。
func (c *Client) UpsertEmbedding(_ context.Context, _ string, _ []float32, _ map[string]string) error {
	return nil
}

func (c *Client) Search(_ context.Context, _ []float32, topK int) ([]string, error) {
	// 返回 chunk_id 列表占位结果；后续替换为真实向量搜索命中
	return make([]string, 0, topK), nil
}
