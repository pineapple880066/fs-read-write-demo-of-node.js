package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	// OpenAI-compatible 模型网关配置（当前实现单 provider）
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
	// MaxContextTokens 可通过配置覆盖；<=0 时会尝试探测并兜底估算。
	MaxContextTokens int
	// 重试策略：用于处理上游模型偶发超时/5xx/429。
	RetryMax     int
	RetryBackoff time.Duration

	mu                   sync.RWMutex
	resolvedContextToken int
	contextResolved      bool
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
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Model:        model,
		HTTP:         &http.Client{Timeout: 60 * time.Second},
		RetryMax:     5,
		RetryBackoff: 1500 * time.Millisecond,
	}
}

// SetHTTPTimeout 允许外部按环境配置覆盖模型 HTTP 超时。
func (c *Client) SetHTTPTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: timeout}
		return
	}
	c.HTTP.Timeout = timeout
}

// SetRetryPolicy 配置模型调用失败后的重试策略。
func (c *Client) SetRetryPolicy(maxAttempts int, backoff time.Duration) {
	if maxAttempts > 0 {
		c.RetryMax = maxAttempts
	}
	if backoff > 0 {
		c.RetryBackoff = backoff
	}
}

// SetMaxContextTokens 允许通过配置显式指定模型上下文窗口大小。
func (c *Client) SetMaxContextTokens(n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.MaxContextTokens = n
	c.resolvedContextToken = n
	c.contextResolved = true
}

// GetMaxContextTokens 返回模型可用上下文窗口（token 级别）。
// 优先级：
// 1) 显式配置 MaxContextTokens
// 2) 调用 OpenAI-compatible /models 接口探测
// 3) 按模型名保守估算
// 4) 最终兜底 8192
func (c *Client) GetMaxContextTokens(ctx context.Context) int {
	c.mu.RLock()
	if c.contextResolved && c.resolvedContextToken > 0 {
		n := c.resolvedContextToken
		c.mu.RUnlock()
		return n
	}
	override := c.MaxContextTokens
	c.mu.RUnlock()

	if override > 0 {
		c.cacheContextTokens(override)
		return override
	}

	if n, err := c.fetchContextTokens(ctx); err == nil && n > 0 {
		c.cacheContextTokens(n)
		return n
	}

	guess := guessContextTokensByModelName(c.Model)
	if guess <= 0 {
		guess = 8192
	}
	c.cacheContextTokens(guess)
	return guess
}

func (c *Client) cacheContextTokens(n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolvedContextToken = n
	c.contextResolved = true
}

func (c *Client) fetchContextTokens(ctx context.Context) (int, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return 0, fmt.Errorf("missing api key")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return 0, fmt.Errorf("missing base url")
	}

	// 优先查单模型详情；部分 provider 会在该接口带能力元信息。
	if strings.TrimSpace(c.Model) != "" {
		u := strings.TrimRight(c.BaseURL, "/") + "/models/" + url.PathEscape(strings.TrimSpace(c.Model))
		var detail map[string]any
		if err := c.getJSON(ctx, u, &detail); err == nil {
			if n := extractContextTokens(detail); n > 0 {
				return n, nil
			}
		}
	}

	// 回退到模型列表扫描。
	u := strings.TrimRight(c.BaseURL, "/") + "/models"
	var list map[string]any
	if err := c.getJSON(ctx, u, &list); err != nil {
		return 0, err
	}
	if n := extractContextTokensFromModelList(list, c.Model); n > 0 {
		return n, nil
	}
	if n := extractContextTokens(list); n > 0 {
		return n, nil
	}
	return 0, fmt.Errorf("context token limit not found in /models response")
}

func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func extractContextTokens(v any) int {
	preferredKeys := []string{
		"context_window",
		"max_context_tokens",
		"context_length",
		"max_input_tokens",
		"input_token_limit",
		"max_model_len",
		"max_position_embeddings",
	}
	switch vv := v.(type) {
	case map[string]any:
		for _, want := range preferredKeys {
			for k, child := range vv {
				if strings.EqualFold(k, want) {
					if n := toPositiveInt(child); n > 0 {
						return n
					}
				}
			}
		}
		for _, child := range vv {
			if n := extractContextTokens(child); n > 0 {
				return n
			}
		}
	case []any:
		for _, item := range vv {
			if n := extractContextTokens(item); n > 0 {
				return n
			}
		}
	}
	return 0
}

func extractContextTokensFromModelList(list map[string]any, model string) int {
	model = strings.TrimSpace(model)
	if model == "" {
		return 0
	}
	raw, ok := list["data"]
	if !ok {
		return 0
	}
	arr, ok := raw.([]any)
	if !ok {
		return 0
	}
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		if !strings.EqualFold(strings.TrimSpace(id), model) {
			continue
		}
		if n := extractContextTokens(m); n > 0 {
			return n
		}
	}
	return 0
}

func toPositiveInt(v any) int {
	switch n := v.(type) {
	case int:
		if n > 0 {
			return n
		}
	case int32:
		if n > 0 {
			return int(n)
		}
	case int64:
		if n > 0 {
			return int(n)
		}
	case float64:
		if n > 0 {
			return int(n)
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i > 0 {
			return int(i)
		}
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0
		}
		if i, err := strconv.Atoi(s); err == nil && i > 0 {
			return i
		}
	}
	return 0
}

func guessContextTokensByModelName(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "":
		return 8192
	case strings.Contains(m, "gpt-4o"), strings.Contains(m, "gpt-4.1"), strings.Contains(m, "o1"), strings.Contains(m, "o3"):
		return 128000
	case strings.Contains(m, "claude"):
		return 200000
	case strings.Contains(m, "qwen"):
		return 32768
	case strings.Contains(m, "deepseek"):
		return 64000
	case strings.Contains(m, "gemini"):
		return 128000
	default:
		return 8192
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

	attempts := c.RetryMax
	if attempts <= 0 {
		attempts = 1
	}
	backoff := c.RetryBackoff
	if backoff <= 0 {
		backoff = 1500 * time.Millisecond
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		out, retryable, err := c.chatCompletionOnce(ctx, messages, temperature, tools, toolChoice)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retryable || attempt == attempts {
			break
		}
		wait := time.Duration(attempt) * backoff
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ChatResult{}, ctx.Err()
		}
	}
	return ChatResult{}, lastErr
}

func (c *Client) chatCompletionOnce(ctx context.Context, messages []ChatMessage, temperature float64, tools []ToolDefinition, toolChoice any) (ChatResult, bool, error) {
	payload, _ := json.Marshal(chatReq{Model: c.Model, Messages: messages, Temperature: temperature, Tools: tools, ToolChoice: toolChoice})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatResult{}, false, err
	}

	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ChatResult{}, shouldRetryModelError(err), err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		err := fmt.Errorf("llm http status: %d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		return ChatResult{}, shouldRetryStatus(resp.StatusCode), err
	}

	var out chatResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ChatResult{}, false, err
	}
	if len(out.Choices) == 0 {
		return ChatResult{}, false, fmt.Errorf("empty llm choices")
	}
	content := ""
	if out.Choices[0].Message.Content != nil {
		content = *out.Choices[0].Message.Content
	}
	return ChatResult{
		Content:      content,
		ToolCalls:    out.Choices[0].Message.ToolCalls,
		FinishReason: out.Choices[0].FinishReason,
	}, false, nil
}

func shouldRetryStatus(code int) bool {
	if code == http.StatusRequestTimeout || code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500
}

func shouldRetryModelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "timeout") || strings.Contains(text, "temporarily unavailable") || strings.Contains(text, "connection reset")
}
