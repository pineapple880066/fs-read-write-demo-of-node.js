package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	// OpenAI-compatible 模型网关配置（当前实现单 provider）
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatReq/chatResp 是与上游模型接口交互时使用的内部结构体。
type chatReq struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func New(baseURL, apiKey, model string) *Client {
	// 内置 HTTP 超时，避免模型接口超时拖垮请求
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: 25 * time.Second},
	}
}

func (c *Client) Chat(ctx context.Context, messages []ChatMessage, temperature float64) (string, error) {
	// 未配置 API Key 时显式报错，方便本地排查
	if c.APIKey == "" {
		return "", fmt.Errorf("missing LLM api key")
	}

	// 1) 组装请求体
	payload, _ := json.Marshal(chatReq{Model: c.Model, Messages: messages, Temperature: temperature})                     // 序列化模型请求体
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload)) // 创建带超时/取消能力的 HTTP 请求
	if err != nil {
		return "", err
	}

	// 2) 设置鉴权与内容类型
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	// 3) 发起 HTTP 请求
	resp, err := c.HTTP.Do(req) // 发起 HTTP 请求到模型网关
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// 只接受 2xx 响应；错误体当前未细分解析
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("llm http status: %d", resp.StatusCode)
	}

	// 4) 解析响应并提取第一条内容
	var out chatResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { // 解析 JSON 响应
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty llm choices")
	}
	return out.Choices[0].Message.Content, nil // 返回第一条候选的文本内容
}
