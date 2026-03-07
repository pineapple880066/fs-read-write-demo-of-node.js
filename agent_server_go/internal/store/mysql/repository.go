package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type TaskRecord struct {
	// 对应 tasks 表
	TaskID       string
	TenantID     string
	Type         string
	Status       string
	PayloadJSON  string
	ErrorMessage sql.NullString
	RetryCount   int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type DocumentRecord struct {
	// 对应 documents 表
	ID         int64
	TenantID   string
	SourceType string
	SourceURI  string
	Checksum   string
	Status     string
	CreatedAt  time.Time
}

type ChunkRecord struct {
	// 对应 chunks 表（检索时会读出 text）
	ID         int64
	TenantID   string
	DocumentID int64
	RelPath    string
	ChunkIndex int
	Text       string
	TokenCount int
	CreatedAt  time.Time
}

type RetrievalLog struct {
	// 对应 retrieval_logs 表
	TenantID   string
	SessionID  string
	Query      string
	TopK       int
	HitIDsJSON string
	LatencyMS  int64
}

func (s *Store) CreateDocument(ctx context.Context, doc DocumentRecord) (int64, error) {
	// CreateDocument 写入一条文档记录，返回自增 document_id。
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO documents(tenant_id, source_type, source_uri, checksum, status, created_at)
		VALUES(?,?,?,?,?,NOW())
	`, doc.TenantID, doc.SourceType, doc.SourceURI, doc.Checksum, doc.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateDocumentStatus(ctx context.Context, documentID int64, status string) error {
	// UpdateDocumentStatus 更新 documents.status（如 processing/ready/failed）。
	_, err := s.DB.ExecContext(ctx, `UPDATE documents SET status = ? WHERE id = ?`, status, documentID)
	return err
}

func (s *Store) ReplaceDocumentChunks(ctx context.Context, tenantID string, documentID int64, relPath string, chunks []ChunkRecord) error {
	// ReplaceDocumentChunks 以“先删后插”的方式重建某文档的分块（简化版，便于重跑 ingest）。
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM chunks WHERE tenant_id = ? AND document_id = ?`, tenantID, documentID); err != nil {
		return err
	}

	for i, c := range chunks {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO chunks(tenant_id, document_id, rel_path, chunk_index, text, token_count, created_at)
			VALUES(?,?,?,?,?,?,NOW())
		`, tenantID, documentID, relPath, i, c.Text, c.TokenCount); err != nil {
			return err
		}
	}

	err = tx.Commit()
	return err
}

func (s *Store) ListChunksByTenant(ctx context.Context, tenantID string, limit int) ([]ChunkRecord, error) {
	// ListChunksByTenant 返回某租户最近分块（给简化版检索使用）。
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, tenant_id, document_id, rel_path, chunk_index, text, token_count, created_at
		FROM chunks
		WHERE tenant_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ChunkRecord, 0, limit)
	for rows.Next() {
		var c ChunkRecord
		if err := rows.Scan(&c.ID, &c.TenantID, &c.DocumentID, &c.RelPath, &c.ChunkIndex, &c.Text, &c.TokenCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListChunksByDocument(ctx context.Context, tenantID string, documentID int64) ([]ChunkRecord, error) {
	// ListChunksByDocument 返回某文档下的全部 chunks，供 ingest 后写入向量库使用。
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, tenant_id, document_id, rel_path, chunk_index, text, token_count, created_at
		FROM chunks
		WHERE tenant_id = ? AND document_id = ?
		ORDER BY chunk_index ASC, id ASC
	`, tenantID, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ChunkRecord, 0, 16)
	for rows.Next() {
		var c ChunkRecord
		if err := rows.Scan(&c.ID, &c.TenantID, &c.DocumentID, &c.RelPath, &c.ChunkIndex, &c.Text, &c.TokenCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateTask(ctx context.Context, task TaskRecord) error {
	// CreateTask 在 tasks 表插入一条新任务记录。
	// 创建任务记录（ingest 等异步流程的起点）
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO tasks(id, tenant_id, type, status, payload_json, error_message, retry_count, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,NOW(),NOW())
	`, task.TaskID, task.TenantID, task.Type, task.Status, task.PayloadJSON, task.ErrorMessage, task.RetryCount)
	return err
}

func (s *Store) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	// GetTask 按 task_id 查询任务当前状态。
	// 按任务 id 查询状态，供 /tasks/:id 接口使用
	var out TaskRecord
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, tenant_id, type, status, payload_json, error_message, retry_count, created_at, updated_at
		FROM tasks WHERE id = ?
	`, taskID).Scan(&out.TaskID, &out.TenantID, &out.Type, &out.Status, &out.PayloadJSON, &out.ErrorMessage, &out.RetryCount, &out.CreatedAt, &out.UpdatedAt)
	return out, err
}

func (s *Store) UpdateTaskStatus(ctx context.Context, taskID string, status string, errMsg *string) error {
	// UpdateTaskStatus 更新任务状态与错误信息（worker 消费时常用）。
	// worker 消费消息后更新任务状态（running/success/failed）
	_, err := s.DB.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, error_message = ?, updated_at = NOW()
		WHERE id = ?
	`, status, errMsg, taskID)
	return err
}

func (s *Store) InsertRetrievalLog(ctx context.Context, log RetrievalLog) error {
	// InsertRetrievalLog 记录一次检索行为（查询词、命中、耗时等）。
	// 记录检索行为，后续可用于分析命中率、延迟等指标
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO retrieval_logs(tenant_id, session_id, query, top_k, hit_ids_json, latency_ms, created_at)
		VALUES(?,?,?,?,?,?,NOW())
	`, log.TenantID, log.SessionID, log.Query, log.TopK, log.HitIDsJSON, log.LatencyMS)
	return err
}

func MustJSON(v any) string {
	// MustJSON 将任意结构序列化为 JSON 字符串（当前忽略序列化错误）。
	// 这里用于“尽量不让日志/任务序列化失败打断主流程”的场景。
	// 若要更严格，可改成返回 (string, error)。
	b, _ := json.Marshal(v)
	return string(b)
}
