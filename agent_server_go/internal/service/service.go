package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent_server_go/internal/cache/redis"
	"agent_server_go/internal/modelgateway"
	"agent_server_go/internal/mq/rabbitmq"
	"agent_server_go/internal/retrieval"
	"agent_server_go/internal/store/mysql"
)

type Services struct {
	Store     *mysql.Store
	Cache     *redis.Client
	MQ        *rabbitmq.Client
	Model     *modelgateway.Client
	RateRPS   int
	RateBurst int
}

func New(store *mysql.Store, cache *redis.Client, mq *rabbitmq.Client, model *modelgateway.Client, rateRPS int, rateBurst int) *Services {
	return &Services{
		Store:     store,
		Cache:     cache,
		MQ:        mq,
		Model:     model,
		RateRPS:   rateRPS,
		RateBurst: rateBurst,
	}
}

func (s *Services) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if req.TenantID == "" || req.SessionID == "" || req.UserID == "" || strings.TrimSpace(req.Message) == "" {
		return ChatResponse{}, errors.New("tenant_id/session_id/user_id/message are required")
	}

	rewritten := retrieval.SanitizeQueries(req.Message, []string{
		"source code architecture",
		"retrieval pipeline",
		strings.ToLower(req.Mode),
	})

	searchResp, err := s.Search(ctx, SearchRequest{
		TenantID: req.TenantID,
		Query:    req.Message,
		TopK:     8,
	})
	if err != nil {
		return ChatResponse{}, err
	}

	evidence := make([]string, 0, len(searchResp.Hits))
	for _, h := range searchResp.Hits {
		evidence = append(evidence, h.RelPath)
	}

	answer := ""
	if s.Model != nil {
		prompt := fmt.Sprintf("Task: %s\nEvidence: %v\nReturn concise Chinese answer.", req.Message, evidence)
		content, modelErr := s.Model.Chat(ctx, []modelgateway.ChatMessage{{Role: "user", Content: prompt}}, 0.2)
		if modelErr == nil {
			answer = content
		}
	}
	if answer == "" {
		answer = "当前为后端骨架实现：已完成检索、路由、任务队列接口。模型网关可用时会返回真实回答。"
	}

	if s.Store != nil {
		_ = s.Store.InsertRetrievalLog(ctx, mysql.RetrievalLog{
			TenantID:   req.TenantID,
			SessionID:  req.SessionID,
			Query:      req.Message,
			TopK:       len(searchResp.Hits),
			HitIDsJSON: mysql.MustJSON(searchResp.Hits),
			LatencyMS:  0,
		})
	}

	return ChatResponse{
		Answer:        answer,
		EvidenceFiles: unique(evidence),
		RetrievalDebug: RetrievalMeta{
			Query:         req.Message,
			Rewritten:     rewritten,
			CandidateHits: len(searchResp.Hits),
		},
	}, nil
}

func (s *Services) Ingest(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	if req.TenantID == "" || req.SourceType == "" {
		return IngestResponse{}, errors.New("tenant_id and source_type are required")
	}

	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())
	msg := rabbitmq.TaskMessage{
		TaskID:     taskID,
		TenantID:   req.TenantID,
		Type:       "ingest",
		Payload:    mysql.MustJSON(req),
		RetryCount: 0,
		CreatedAt:  time.Now().UTC(),
	}

	if s.Store != nil {
		err := s.Store.CreateTask(ctx, mysql.TaskRecord{
			TaskID:      taskID,
			TenantID:    req.TenantID,
			Type:        "ingest",
			Status:      "pending",
			PayloadJSON: msg.Payload,
		})
		if err != nil {
			return IngestResponse{}, err
		}
	}

	if s.MQ != nil {
		if err := s.MQ.PublishTask(msg); err != nil {
			return IngestResponse{}, err
		}
	}

	return IngestResponse{TaskID: taskID, Status: "pending"}, nil
}

func (s *Services) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if req.TenantID == "" || strings.TrimSpace(req.Query) == "" {
		return SearchResponse{}, errors.New("tenant_id and query are required")
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}

	cacheKey := fmt.Sprintf("search:%s:%d:%s", req.TenantID, req.TopK, strings.TrimSpace(req.Query))
	if s.Cache != nil {
		var cached SearchResponse
		if ok, err := s.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && ok {
			return cached, nil
		}
	}

	// Placeholder retrieval scores. Replace with real BM25/dense pipeline in week-6.
	hits := make([]SearchHit, 0, req.TopK)
	for i := 0; i < req.TopK; i++ {
		bm25 := 1.0 - float64(i)*0.08
		dense := 0.9 - float64(i)*0.07
		if bm25 < 0 {
			bm25 = 0
		}
		if dense < 0 {
			dense = 0
		}
		queryCoverage := 1.0
		pathBoost := 0.2
		finalScore := retrieval.FuseScore(retrieval.Normalize(bm25, 1.0), retrieval.Normalize(dense, 1.0), queryCoverage, pathBoost)

		hits = append(hits, SearchHit{
			ChunkID:    fmt.Sprintf("chunk_%d", i+1),
			RelPath:    fmt.Sprintf("src/module_%d.go", i+1),
			Score:      finalScore,
			BM25Score:  bm25,
			DenseScore: dense,
		})
	}

	resp := SearchResponse{Hits: hits}
	if s.Cache != nil {
		_ = s.Cache.SetJSON(ctx, cacheKey, resp, 2*time.Minute)
	}

	return resp, nil
}

func (s *Services) GetTask(ctx context.Context, taskID string) (TaskResponse, error) {
	if taskID == "" {
		return TaskResponse{}, errors.New("task id is required")
	}

	if s.Store == nil {
		return TaskResponse{TaskID: taskID, Status: "pending", Progress: 10}, nil
	}

	r, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskResponse{}, errors.New("task not found")
		}
		return TaskResponse{}, err
	}

	progress := 10
	switch r.Status {
	case "pending":
		progress = 10
	case "running":
		progress = 50
	case "success":
		progress = 100
	case "failed":
		progress = 100
	}

	msg := ""
	if r.ErrorMessage.Valid {
		msg = r.ErrorMessage.String
	}

	return TaskResponse{
		TaskID:       r.TaskID,
		Status:       r.Status,
		Progress:     progress,
		ErrorMessage: msg,
	}, nil
}

func unique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
