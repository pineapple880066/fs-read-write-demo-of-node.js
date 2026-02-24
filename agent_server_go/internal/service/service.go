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
	// 业务层聚合依赖：数据库、缓存、消息队列、模型网关等
	Store     *mysql.Store
	Cache     *redis.Client
	MQ        *rabbitmq.Client
	Model     *modelgateway.Client
	RateRPS   int
	RateBurst int
}

func New(store *mysql.Store, cache *redis.Client, mq *rabbitmq.Client, model *modelgateway.Client, rateRPS int, rateBurst int) *Services {
	// service 层本身不做复杂初始化，只负责依赖组装
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
	// 1) 基本参数校验
	if req.TenantID == "" || req.SessionID == "" || req.UserID == "" || strings.TrimSpace(req.Message) == "" {
		return ChatResponse{}, errors.New("tenant_id/session_id/user_id/message are required")
	}

	// 2) 生成 query 变体（当前是启发式示例，后续可换成 LLM query rewrite）
	rewritten := retrieval.SanitizeQueries(req.Message, []string{ // 合并原始 query + 补充 query，并做去重裁剪
		"source code architecture",
		"retrieval pipeline",
		strings.ToLower(req.Mode),
	})

	// 3) 调用搜索接口拿候选上下文（这里复用 Search 逻辑，避免重复代码）
	searchResp, err := s.Search(ctx, SearchRequest{ // 复用搜索能力，为 chat 提供证据片段
		TenantID: req.TenantID,
		Query:    req.Message,
		TopK:     8,
	})
	if err != nil {
		return ChatResponse{}, err
	}

	// 4) 提取证据文件路径（供模型提示词和前端展示）
	evidence := make([]string, 0, len(searchResp.Hits))
	for _, h := range searchResp.Hits {
		evidence = append(evidence, h.RelPath)
	}

	// 5) 调模型生成回答；模型不可用时使用兜底文案
	answer := ""
	if s.Model != nil {
		// 当前 prompt 是最小版本，后续应拆到 prompt builder
		prompt := fmt.Sprintf("Task: %s\nEvidence: %v\nReturn concise Chinese answer.", req.Message, evidence)
		content, modelErr := s.Model.Chat(ctx, []modelgateway.ChatMessage{{Role: "user", Content: prompt}}, 0.2) // 调 LLM 生成回答
		if modelErr == nil {
			answer = content
		}
	}
	if answer == "" {
		answer = "当前为后端骨架实现：已完成检索、路由、任务队列接口。模型网关可用时会返回真实回答。"
	}

	// 6) 记录检索日志（失败不影响主流程）
	if s.Store != nil {
		_ = s.Store.InsertRetrievalLog(ctx, mysql.RetrievalLog{ // 写入检索日志，便于后续分析
			TenantID:   req.TenantID,
			SessionID:  req.SessionID,
			Query:      req.Message,
			TopK:       len(searchResp.Hits),
			HitIDsJSON: mysql.MustJSON(searchResp.Hits),
			LatencyMS:  0,
		})
	}

	// 7) 返回 chat 结果，同时附上检索调试信息
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
	// ingest 用于异步导入文档/数据源，当前先打通任务创建与入队骨架
	if req.TenantID == "" || req.SourceType == "" {
		return IngestResponse{}, errors.New("tenant_id and source_type are required")
	}

	// 使用时间戳生成任务 id（简单可用，后续可替换成 UUID）
	taskID := fmt.Sprintf("task_%d", time.Now().UnixNano())
	msg := rabbitmq.TaskMessage{ // 组装异步任务消息体（后续发到 MQ）
		TaskID:     taskID,
		TenantID:   req.TenantID,
		Type:       "ingest",
		Payload:    mysql.MustJSON(req), // 把 ingest 请求序列化为 JSON，放入任务 payload
		RetryCount: 0,
		CreatedAt:  time.Now().UTC(),
	}

	if s.Store != nil {
		// 先落库任务状态为 pending，便于 /tasks 查询
		err := s.Store.CreateTask(ctx, mysql.TaskRecord{ // 先创建任务记录，状态 pending
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
		// 写入消息队列，交给 worker 异步处理
		if err := s.MQ.PublishTask(msg); err != nil { // 将任务消息发布到 RabbitMQ
			return IngestResponse{}, err
		}
	}

	// 即使未配置 MQ，也先返回 pending，保证 API 契约稳定
	return IngestResponse{TaskID: taskID, Status: "pending"}, nil
}

func (s *Services) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	// 1) 参数校验与默认值处理
	if req.TenantID == "" || strings.TrimSpace(req.Query) == "" {
		return SearchResponse{}, errors.New("tenant_id and query are required")
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}

	// 2) 检索缓存（按 tenant + topK + query）
	cacheKey := fmt.Sprintf("search:%s:%d:%s", req.TenantID, req.TopK, strings.TrimSpace(req.Query))
	if s.Cache != nil {
		var cached SearchResponse
		if ok, err := s.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && ok { // 命中缓存则直接返回
			return cached, nil
		}
	}

	// Placeholder retrieval scores. Replace with real BM25/dense pipeline in week-6.
	// 3) 当前返回占位命中，用于先打通 API 契约和前后端/调用方调试
	hits := make([]SearchHit, 0, req.TopK)
	for i := 0; i < req.TopK; i++ {
		// 用递减分数模拟“越靠前越相关”的检索结果
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
		// 使用 retrieval 包中的融合公式，保证与计划一致
		finalScore := retrieval.FuseScore(retrieval.Normalize(bm25, 1.0), retrieval.Normalize(dense, 1.0), queryCoverage, pathBoost) // 按计划公式做融合打分

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
		// 写缓存失败不影响主流程
		_ = s.Cache.SetJSON(ctx, cacheKey, resp, 2*time.Minute) // 写搜索缓存，TTL=2分钟
	}

	return resp, nil
}

func (s *Services) GetTask(ctx context.Context, taskID string) (TaskResponse, error) {
	// tasks 接口用于查询异步任务状态
	if taskID == "" {
		return TaskResponse{}, errors.New("task id is required")
	}

	if s.Store == nil {
		// 未配置数据库时返回占位状态，保证接口可调试
		return TaskResponse{TaskID: taskID, Status: "pending", Progress: 10}, nil
	}

	r, err := s.Store.GetTask(ctx, taskID) // 从 tasks 表读取任务状态
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskResponse{}, errors.New("task not found")
		}
		return TaskResponse{}, err
	}

	// 将文本状态映射成前端可直接展示的粗粒度进度
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

	// ResultRef 当前预留，后续可用于返回导入结果地址/文件路径等
	return TaskResponse{
		TaskID:       r.TaskID,
		Status:       r.Status,
		Progress:     progress,
		ErrorMessage: msg,
	}, nil
}

func unique(in []string) []string {
	// 保序去重：保留首次出现顺序，便于 evidence 展示
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
