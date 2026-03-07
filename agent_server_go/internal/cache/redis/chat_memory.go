package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Package redis 提供基于 Redis 的会话短期记忆（session memory）支持。
//
// 设计思想：对于每个租户和会话，使用 Redis 列表保存最近若干条消息，并通过
// LPUSH/LTRIM/EXPIRE 组合实现“最近 N 条 + TTL”的策略。
// 列表中数据为 JSON 编码的 `SessionMessage`，以便在需要时按时间顺序（旧->新）
// 恢复并发送给 LLM 作为上下文历史。

// SessionMessage 表示会话短期记忆中的一条消息。
// 字段含义：
// - Role：消息所属方（例如 "user"、"assistant" 等）。
// - Content：消息文本内容。
// - At：消息的时间戳（Unix 秒），用于调试或必要时排序/展示。
type SessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	At      int64  `json:"at"`
}

// sessionMemoryKey 生成用于存储会话上下文的 Redis 键。
// key 格式：session:<tenantID>:<sessionID>:ctx:v1
func sessionMemoryKey(tenantID, sessionID string) string {
	return fmt.Sprintf("session:%s:%s:ctx:v1", tenantID, sessionID)
}

// AppendSessionMessage 将一条消息追加到会话短期记忆中，并维持“最近 N 条 + TTL”。
// 行为说明：
// 1. 将消息 JSON 编码后执行 LPUSH，消息被插入到列表头部（最新在前）。
// 2. 使用 LTRIM 将列表裁剪到长度不超过 maxTurns（若 <=0 则默认 12）。
// 3. 使用 EXPIRE 设置列表的 TTL（若 <=0 则默认 30 分钟）。
// 以上操作使用 Redis 事务流水线（TxPipeline）一次性提交，确保原子性与性能。
func (c *Client) AppendSessionMessage(ctx context.Context, tenantID, sessionID string, msg SessionMessage, maxTurns int64, ttl time.Duration) error {
	// 默认值处理：最近条数与过期时间
	if maxTurns <= 0 {
		maxTurns = 12
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	// 序列化为 JSON 字符串写入 Redis 列表
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	key := sessionMemoryKey(tenantID, sessionID)
	pipe := c.RDB.TxPipeline()
	// LPUSH 将最新消息放到列表头
	pipe.LPush(ctx, key, string(b))
	// LTRIM 保证列表长度不超过 maxTurns
	pipe.LTrim(ctx, key, 0, maxTurns-1)
	// 续期/设置 TTL
	pipe.Expire(ctx, key, ttl)
	_, err = pipe.Exec(ctx)
	return err
}

// LoadSessionMessages 读取最近 maxTurns 条会话消息，并按时间从旧到新返回。
// 注意：Redis 列表以 "最新在前" 的顺序保存（LPUSH 插入），因此这里需要反向迭代
// 将结果翻转为旧->新的顺序，方便直接作为 LLM 的上下文历史使用。
func (c *Client) LoadSessionMessages(ctx context.Context, tenantID, sessionID string, maxTurns int64) ([]SessionMessage, error) {
	if maxTurns <= 0 {
		maxTurns = 12
	}
	key := sessionMemoryKey(tenantID, sessionID)
	// LRange 取出 [0, maxTurns-1] 的元素（最新到旧）
	arr, err := c.RDB.LRange(ctx, key, 0, maxTurns-1).Result()
	if err != nil {
		return nil, err
	}

	// Redis 里是新到旧；这里翻转成旧到新，便于直接喂给 LLM 消息历史。
	out := make([]SessionMessage, 0, len(arr))
	for i := len(arr) - 1; i >= 0; i-- {
		var m SessionMessage
		if err := json.Unmarshal([]byte(arr[i]), &m); err != nil {
			// 遇到解析错误时忽略该条记录，继续下一条
			continue
		}
		// 过滤掉不完整的消息
		if m.Role == "" || m.Content == "" {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}
