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
	// 统一消息结构：兼容普通 chat 与 tool-calling 两种请求。
	// tool-calling 场景下 assistant 消息可能携带 tool_calls，tool 消息会带 tool_call_id。
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolDefinition 对应 OpenAI-compatible 的 tools[] 定义。
type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction 是 function-calling 的工具 schema。
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ToolCall 是模型返回的单次工具调用请求（assistant.tool_calls[]）。
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction 描述模型要调用的函数名和 JSON 参数字符串。
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatReq/chatResp 是与上游模型接口交互时使用的内部结构体。
type chatReq struct {
	Model       string           `json:"model"`
	Messages    []ChatMessage    `json:"messages"`
	Temperature float64          `json:"temperature"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  any              `json:"tool_choice,omitempty"`
}

type chatResp struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   *string    `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

// ChatResult 统一返回一次 chat/completions 的首条消息结果（文本 + 工具调用）。
type ChatResult struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

func New(baseURL, apiKey, model string) *Client {
	// New 创建模型网关客户端（OpenAI-compatible HTTP 调用封装）。
	// 内置 HTTP 超时，避免模型接口超时拖垮请求
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: 25 * time.Second},
	}
}

func (c *Client) Chat(ctx context.Context, messages []ChatMessage, temperature float64) (string, error) {
	// Chat 调用上游模型的 /chat/completions，并返回第一条文本结果。
	out, err := c.ChatCompletion(ctx, messages, temperature, nil, nil)
	if err != nil {
		return "", err
	}
	return out.Content, nil
}

// ChatCompletion 是底层统一调用入口：支持普通聊天和 tools/function-calling。
func (c *Client) ChatCompletion(ctx context.Context, messages []ChatMessage, temperature float64, tools []ToolDefinition, toolChoice any) (ChatResult, error) {
	// ChatCompletion 调用上游 /chat/completions，并返回首条消息的文本/工具调用。
	// 未配置 API Key 时显式报错，方便本地排查
	if c.APIKey == "" {
		return ChatResult{}, fmt.Errorf("missing LLM api key")
	}

	// 1) 组装请求体
	payload, _ := json.Marshal(chatReq{Model: c.Model, Messages: messages, Temperature: temperature, Tools: tools, ToolChoice: toolChoice}) // 序列化模型请求体（可携带 tools）
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))                   // 创建带超时/取消能力的 HTTP 请求
	if err != nil {
		return ChatResult{}, err
	}

	// 2) 设置鉴权与内容类型
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	// 3) 发起 HTTP 请求
	resp, err := c.HTTP.Do(req) // 发起 HTTP 请求到模型网关
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()

	// 只接受 2xx 响应；错误体当前未细分解析
	if resp.StatusCode/100 != 2 {
		return ChatResult{}, fmt.Errorf("llm http status: %d", resp.StatusCode)
	}

	// 4) 解析响应并提取第一条内容
	var out chatResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { // 解析 JSON 响应
		return ChatResult{}, err
	}
	if len(out.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("empty llm choices")
	}
	content := ""
	if out.Choices[0].Message.Content != nil {
		content = *out.Choices[0].Message.Content
	}
	return ChatResult{
		Content:      content,
		ToolCalls:    out.Choices[0].Message.ToolCalls,
		FinishReason: out.Choices[0].FinishReason,
	}, nil // 返回第一条候选（文本 + tool_calls）
}
