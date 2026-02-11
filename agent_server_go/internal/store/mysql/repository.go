package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type TaskRecord struct {
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

type RetrievalLog struct {
	TenantID   string
	SessionID  string
	Query      string
	TopK       int
	HitIDsJSON string
	LatencyMS  int64
}

func (s *Store) CreateTask(ctx context.Context, task TaskRecord) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO tasks(id, tenant_id, type, status, payload_json, error_message, retry_count, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,NOW(),NOW())
	`, task.TaskID, task.TenantID, task.Type, task.Status, task.PayloadJSON, task.ErrorMessage, task.RetryCount)
	return err
}

func (s *Store) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	var out TaskRecord
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, tenant_id, type, status, payload_json, error_message, retry_count, created_at, updated_at
		FROM tasks WHERE id = ?
	`, taskID).Scan(&out.TaskID, &out.TenantID, &out.Type, &out.Status, &out.PayloadJSON, &out.ErrorMessage, &out.RetryCount, &out.CreatedAt, &out.UpdatedAt)
	return out, err
}

func (s *Store) UpdateTaskStatus(ctx context.Context, taskID string, status string, errMsg *string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, error_message = ?, updated_at = NOW()
		WHERE id = ?
	`, status, errMsg, taskID)
	return err
}

func (s *Store) InsertRetrievalLog(ctx context.Context, log RetrievalLog) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO retrieval_logs(tenant_id, session_id, query, top_k, hit_ids_json, latency_ms, created_at)
		VALUES(?,?,?,?,?,?,NOW())
	`, log.TenantID, log.SessionID, log.Query, log.TopK, log.HitIDsJSON, log.LatencyMS)
	return err
}

func MustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
