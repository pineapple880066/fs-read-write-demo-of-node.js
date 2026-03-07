package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"agent_server_go/internal/mq/rabbitmq"
	"agent_server_go/internal/retrieval"
	"agent_server_go/internal/store/mysql"
	"agent_server_go/internal/vector/milvus"
)

// StartTaskConsumer 启动 MQ 消费者（当前是周5阶段的占位骨架）。
// 后续会在这里替换成真实的 ingest / chunk / embedding / reindex 流程。
func (s *Services) StartTaskConsumer(ctx context.Context) {
	// StartTaskConsumer 启动异步任务消费者。
	// 当前已实现 ingest 最小业务闭环：payload 解析 -> documents/chunks 落库 -> 状态更新。
	// 没有 MQ 或 DB 时直接跳过，避免本地最小环境启动失败
	if s.MQ == nil || s.Store == nil {
		return
	}

	err := s.MQ.ConsumeTasks(ctx, func(ctx context.Context, msg rabbitmq.TaskMessage) error {
		log.Printf("consume task: id=%s type=%s tenant=%s", msg.TaskID, msg.Type, msg.TenantID)

		// 处理前先把任务状态改成 running
		_ = s.Store.UpdateTaskStatus(ctx, msg.TaskID, "running", nil)

		// 按任务类型分发到不同处理函数（后续可扩展 chunk/embed/reindex 等）。
		if err := s.handleTaskMessage(ctx, msg); err != nil {
			errMsg := err.Error()
			_ = s.Store.UpdateTaskStatus(ctx, msg.TaskID, "failed", &errMsg)
			return err
		}

		// 业务处理成功后更新任务状态
		return s.Store.UpdateTaskStatus(ctx, msg.TaskID, "success", nil)
	})
	if err != nil {
		log.Printf("start consumer failed: %v", err)
	}
}

func (s *Services) handleTaskMessage(ctx context.Context, msg rabbitmq.TaskMessage) error {
	// handleTaskMessage 是 worker 的业务分发入口：根据 msg.Type 调具体处理函数。
	switch strings.ToLower(strings.TrimSpace(msg.Type)) {
	case "ingest":
		return s.handleIngestTask(ctx, msg)
	default:
		return fmt.Errorf("unsupported task type: %s", msg.Type)
	}
}

func (s *Services) handleIngestTask(ctx context.Context, msg rabbitmq.TaskMessage) error {
	// handleIngestTask 执行真实 ingest 最小流程：解析 payload -> 读内容 -> 切 chunk -> 落 documents/chunks。
	if s.Store == nil {
		return fmt.Errorf("store is nil")
	}

	var req IngestRequest
	if err := json.Unmarshal([]byte(msg.Payload), &req); err != nil {
		return fmt.Errorf("parse ingest payload failed: %w", err)
	}

	content, sourceURI, relPath, err := resolveIngestContent(req)
	if err != nil {
		return err
	}

	checksum := sha256Hex(req.TenantID + "\n" + sourceURI + "\n" + content)
	docID, err := s.Store.CreateDocument(ctx, mysql.DocumentRecord{
		TenantID:   req.TenantID,
		SourceType: req.SourceType,
		SourceURI:  sourceURI,
		Checksum:   checksum,
		Status:     "processing",
	})
	if err != nil {
		return fmt.Errorf("create document failed: %w", err)
	}

	chunkTexts := chunkTextForIngest(content, 1200, 150)
	chunks := make([]mysql.ChunkRecord, 0, len(chunkTexts))
	for _, t := range chunkTexts {
		chunks = append(chunks, mysql.ChunkRecord{
			Text:       t,
			TokenCount: approxTokenCount(t),
		})
	}

	if err := s.Store.ReplaceDocumentChunks(ctx, req.TenantID, docID, relPath, chunks); err != nil {
		_ = s.Store.UpdateDocumentStatus(ctx, docID, "failed")
		return fmt.Errorf("replace chunks failed: %w", err)
	}

	if s.Vector != nil && s.Vector.Enabled() {
		inserted, listErr := s.Store.ListChunksByDocument(ctx, req.TenantID, docID)
		if listErr != nil {
			log.Printf("list chunks for milvus failed: tenant=%s doc=%d err=%v", req.TenantID, docID, listErr)
		} else {
			rows := make([]milvus.EmbeddingRow, 0, len(inserted))
			for _, ch := range inserted {
				rows = append(rows, milvus.EmbeddingRow{
					ChunkID:   fmt.Sprintf("%d", ch.ID),
					TenantID:  ch.TenantID,
					RelPath:   ch.RelPath,
					Embedding: retrieval.HashEmbedText(ch.Text),
				})
			}
			if err := s.Vector.UpsertEmbeddings(ctx, rows); err != nil {
				log.Printf("milvus upsert failed: tenant=%s doc=%d err=%v", req.TenantID, docID, err)
			}
		}
	}
	if err := s.Store.UpdateDocumentStatus(ctx, docID, "ready"); err != nil {
		return fmt.Errorf("update document status failed: %w", err)
	}

	return nil
}

func resolveIngestContent(req IngestRequest) (content string, sourceURI string, relPath string, err error) {
	// resolveIngestContent 把 ingest 入参转成统一的“文本内容 + 源标识 + 相对路径”。
	sourceType := strings.ToLower(strings.TrimSpace(req.SourceType))
	switch sourceType {
	case "text", "inline_text":
		content = strings.TrimSpace(req.Text)
		if content == "" {
			return "", "", "", fmt.Errorf("text source requires non-empty text")
		}
		sourceURI = strings.TrimSpace(req.SourceURI)
		if sourceURI == "" {
			sourceURI = "inline:text"
		}
		relPath = "inline/input.txt"
		return content, sourceURI, relPath, nil

	case "file", "local_file":
		p := strings.TrimSpace(req.SourceURI)
		if p == "" {
			return "", "", "", fmt.Errorf("file source requires source_uri path")
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return "", "", "", fmt.Errorf("read file failed: %w", readErr)
		}
		content = string(b)
		sourceURI = p
		relPath = filepath.Base(p)
		if relPath == "" {
			relPath = "input.txt"
		}
		return content, sourceURI, relPath, nil
	default:
		return "", "", "", fmt.Errorf("unsupported source_type: %s (supported: text, file)", req.SourceType)
	}
}

func chunkTextForIngest(text string, chunkSize, overlap int) []string {
	// chunkTextForIngest 是简化版切分器：按 rune 长度切块，保留少量 overlap，避免边界信息丢失。
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = 1200
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 4
	}

	runes := []rune(text)
	if len(runes) <= chunkSize {
		return []string{text}
	}

	step := chunkSize - overlap
	out := make([]string, 0, (len(runes)/step)+1)
	for start := 0; start < len(runes); start += step {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		seg := strings.TrimSpace(string(runes[start:end]))
		if seg != "" {
			out = append(out, seg)
		}
		if end == len(runes) {
			break
		}
	}
	return out
}

func approxTokenCount(s string) int {
	// approxTokenCount 是粗略 token 估算（用于 chunks.token_count 展示/统计，不用于严格计费）。
	if s == "" {
		return 0
	}
	// 简化估算：UTF-8 rune 数 * 0.8（中英混合场景比 byte 长度更稳定）
	return int(float64(utf8.RuneCountInString(s))*0.8) + 1
}

func sha256Hex(s string) string {
	// sha256Hex 生成内容校验值，便于后续做幂等/去重。
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
