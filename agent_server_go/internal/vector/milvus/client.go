package milvus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type EmbeddingRow struct {
	ChunkID   string
	TenantID  string
	RelPath   string
	Embedding []float32
}

type SearchResult struct {
	ChunkID string
	Score   float64
	RelPath string
}

type Client struct {
	Addr           string
	Token          string
	CollectionName string
	HTTP           *http.Client

	mu        sync.Mutex
	ensured   bool
	dimension int
}

func New(addr string) *Client {
	addr = strings.TrimSpace(addr)
	if addr != "" && !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return &Client{
		Addr:           addr,
		CollectionName: "agent_chunks",
		HTTP:           &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && strings.TrimSpace(c.Addr) != ""
}

func (c *Client) EnsureCollection(ctx context.Context, dim int) error {
	if !c.Enabled() {
		return fmt.Errorf("milvus not configured")
	}
	if dim <= 0 {
		return fmt.Errorf("invalid milvus dimension")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ensured && c.dimension == dim {
		return nil
	}

	var listResp struct {
		Code    int      `json:"code"`
		Data    []string `json:"data"`
		Message string   `json:"message"`
	}
	if err := c.post(ctx, "/v2/vectordb/collections/list", map[string]any{}, &listResp); err != nil {
		return err
	}
	exists := false
	for _, name := range listResp.Data {
		if name == c.CollectionName {
			exists = true
			break
		}
	}

	if !exists {
		createReq := map[string]any{
			"collectionName":     c.CollectionName,
			"dimension":          dim,
			"primaryFieldName":   "chunk_id",
			"vectorFieldName":    "embedding",
			"idType":             "VarChar",
			"autoID":             false,
			"metricType":         "COSINE",
			"enableDynamicField": true,
			"params": map[string]any{
				"max_length": "128",
			},
		}
		var createResp struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		if err := c.post(ctx, "/v2/vectordb/collections/create", createReq, &createResp); err != nil {
			return err
		}
	}

	var loadResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := c.post(ctx, "/v2/vectordb/collections/load", map[string]any{
		"collectionName": c.CollectionName,
	}, &loadResp); err != nil {
		return err
	}

	c.ensured = true
	c.dimension = dim
	return nil
}

func (c *Client) UpsertEmbeddings(ctx context.Context, rows []EmbeddingRow) error {
	if !c.Enabled() || len(rows) == 0 {
		return nil
	}
	if err := c.EnsureCollection(ctx, len(rows[0].Embedding)); err != nil {
		return err
	}

	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{
			"chunk_id":  row.ChunkID,
			"tenant_id": row.TenantID,
			"rel_path":  row.RelPath,
			"embedding": row.Embedding,
		})
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	return c.post(ctx, "/v2/vectordb/entities/upsert", map[string]any{
		"collectionName": c.CollectionName,
		"data":           data,
	}, &resp)
}

func (c *Client) Search(ctx context.Context, tenantID string, vector []float32, topK int) ([]SearchResult, error) {
	if !c.Enabled() || len(vector) == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 8
	}
	if err := c.EnsureCollection(ctx, len(vector)); err != nil {
		return nil, err
	}

	req := map[string]any{
		"collectionName": c.CollectionName,
		"data":           []any{vector},
		"limit":          topK,
		"outputFields":   []string{"chunk_id", "tenant_id", "rel_path"},
	}
	if strings.TrimSpace(tenantID) != "" {
		req["filter"] = fmt.Sprintf("tenant_id == \"%s\"", escapeFilterValue(tenantID))
	}

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    []struct {
			ID       any     `json:"id"`
			Distance float64 `json:"distance"`
			ChunkID  string  `json:"chunk_id"`
			RelPath  string  `json:"rel_path"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/v2/vectordb/entities/search", req, &resp); err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(resp.Data))
	for _, item := range resp.Data {
		chunkID := strings.TrimSpace(item.ChunkID)
		if chunkID == "" && item.ID != nil {
			chunkID = fmt.Sprintf("%v", item.ID)
		}
		if chunkID == "" {
			continue
		}
		out = append(out, SearchResult{
			ChunkID: chunkID,
			Score:   item.Distance,
			RelPath: item.RelPath,
		})
	}
	return out, nil
}

func (c *Client) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Addr, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("milvus http status: %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}

	// 通用 code/message 约定：code=0 表示成功
	if m, ok := out.(interface{ GetCode() int }); ok {
		if m.GetCode() != 0 {
			return fmt.Errorf("milvus business code: %d", m.GetCode())
		}
	}

	b, _ := json.Marshal(out)
	var probe struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &probe); err == nil && probe.Code != 0 {
		msg := strings.TrimSpace(probe.Message)
		if msg == "" {
			msg = fmt.Sprintf("milvus business code: %d", probe.Code)
		}
		return fmt.Errorf(msg)
	}
	return nil
}

func escapeFilterValue(s string) string {
	return strings.ReplaceAll(s, "\"", "\\\"")
}
