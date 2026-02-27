package service

import (
	"context"
	"database/sql"
	"encoding/json"
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

// searchCodebaseToolArgs 是 function-calling 中 search_codebase 的参数结构。
type searchCodebaseToolArgs struct {
	Query         string   `json:"query"`
	QueryVariants []string `json:"query_variants,omitempty"`
	TopK          int      `json:"top_k,omitempty"`
}

type Services struct {
	// 业务层聚合依赖：数据库、缓存、消息队列、模型网关等
	Store     *mysql.Store
	Cache     *redis.Client
	MQ        *rabbitmq.Client
	Model     *modelgateway.Client
	TSBridge  *retrieval.TSBridge
	RateRPS   int
	RateBurst int
}

func New(store *mysql.Store, cache *redis.Client, mq *rabbitmq.Client, model *modelgateway.Client, rateRPS int, rateBurst int) *Services {
	// New 创建 service 层对象（聚合 数据库、缓存、MQ、模型等依赖）。
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

func (s *Services) SetTSBridge(b *retrieval.TSBridge) {
	// SetTSBridge 注入可选的 TS 检索桥接器；未注入时保留 Go 占位检索逻辑。
	s.TSBridge = b
}

func mapTSHitsToSearchHits(hits []retrieval.TSRAGHit, topK int) []SearchHit {
	// mapTSHitsToSearchHits 将 TS retrieve.ts 的命中结构映射到 /search API 契约。
	// TS 命中结构里字段名是 relPath / bm25Score；这里转换成 Go API 的 rel_path / bm25_score。
	if topK <= 0 {
		topK = len(hits)
	}
	if len(hits) > topK {
		// 只保留前 topK 个命中（TS 侧可能返回更多）
		hits = hits[:topK]
	}

	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHit{
			ChunkID:    fmt.Sprintf("%d", h.ID),
			RelPath:    h.RelPath,
			Score:      h.Score,
			BM25Score:  h.BM25Score,
			DenseScore: 0, // TS 这条链当前是 BM25 + 规则融合，dense 暂未接入
		})
	}
	return out
}

func buildPlaceholderSearchHits(topK int) []SearchHit {
	// buildPlaceholderSearchHits 保留原先占位检索结果，用作 TS bridge 不可用时的降级输出。
	// 这不是“真实检索”，只是为了保证 API 契约始终可用。
	hits := make([]SearchHit, 0, topK)
	for i := 0; i < topK; i++ {
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
			// append 相当于 JS 的 hits.push({...})（不是对象赋值）
			ChunkID:    fmt.Sprintf("chunk_%d", i+1),
			RelPath:    fmt.Sprintf("src/module_%d.go", i+1),
			Score:      finalScore,
			BM25Score:  bm25,
			DenseScore: dense,
		})
	}
	return hits
}

func (s *Services) searchTenantChunks(ctx context.Context, tenantID, query string, queryVariants []string, topK int) (SearchResponse, string, bool) {
	// searchTenantChunks 在 MySQL chunks 表上做本地 Hybrid 检索（BM25 + 向量近似），用于跑通 SaaS 闭环。
	// BM25 部分按 TS 版公式移植；向量部分当前为本地哈希向量近似，后续可替换真实 embedding + Milvus。
	if s.Store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(query) == "" {
		return SearchResponse{}, "", false
	}
	all, err := s.Store.ListChunksByTenant(ctx, tenantID, 800)
	if err != nil || len(all) == 0 {
		return SearchResponse{}, "", false
	}

	docs := make([]retrieval.HybridDoc, 0, len(all))
	for _, ch := range all {
		docs = append(docs, retrieval.HybridDoc{
			ID:      ch.ID,
			RelPath: ch.RelPath,
			Text:    ch.Text,
		})
	}

	hh := retrieval.HybridSearchLocalDocs(docs, query, queryVariants, topK)
	if len(hh) == 0 {
		return SearchResponse{}, "", false
	}

	hits := make([]SearchHit, 0, len(hh))
	for _, c := range hh {
		hits = append(hits, SearchHit{
			ChunkID:    fmt.Sprintf("%d", c.ID),
			RelPath:    c.RelPath,
			Score:      c.Score,
			BM25Score:  c.BM25Score,
			DenseScore: c.DenseScore,
		})
	}
	contextText := retrieval.BuildContextFromHybridHits(hh, 8000)
	return SearchResponse{Hits: hits}, contextText, true
}

func (s *Services) buildRAGMaterials(ctx context.Context, tenantID, query string, queryVariants []string, topK int) (SearchResponse, string) {
	// buildRAGMaterials 聚合检索材料：优先查租户已导入 chunks；未命中时再用 TS retrieve.ts；最后回退占位命中。
	// 返回值：
	// 1) SearchResponse：给 /search API 或 chat 调试信息用（结构化 hits）
	// 2) string       ：给 /chat prompt / tool 直接使用的打包 context 文本
	if topK <= 0 {
		// 调用方没传 topK 时给一个默认值，避免 TS/占位检索收到 0
		topK = 8
	}
	// 优先使用租户已导入的数据（MySQL chunks）。这是 API SaaS 主流程的真实检索来源。
	if resp, ctxText, ok := s.searchTenantChunks(ctx, tenantID, query, queryVariants, topK); ok {
		return resp, ctxText
	}
	if s.TSBridge != nil && s.TSBridge.Enabled() {
		// 传给 TS retrieve.ts 时，queryVariants 不需要重复包含原 query。
		// 原因：TS 的 collectQueries() 会把 query 和 queryVariants 合并，如果这里不去重会重复计算。
		tsVariants := make([]string, 0, len(queryVariants))
		// seen 是“去重集合”：
		// key = 一个 query 字符串
		// value 用空 struct{} 表示“出现过”，因为它占内存最小。
		seen := map[string]struct{}{strings.TrimSpace(query): struct{}{}}
		for _, q := range queryVariants {
			// 逐个清洗 query 变体（去首尾空格）
			q = strings.TrimSpace(q)
			if q == "" {
				// 空字符串无意义，跳过
				continue
			}
			if _, ok := seen[q]; ok {
				// 去重
				// 已经出现过（包括原 query）就不再重复加入
				continue
			}
			seen[q] = struct{}{}
			// tsVariants 才是最终传给 TS retrieve.ts 的“额外 query 列表”
			tsVariants = append(tsVariants, q)
		}

		// 真正调用 TS RAG（Node 子进程 -> agent/dist/retrieve.js）。
		// 成功：拿到 hits + context，转换后直接返回。
		if rag, err := s.TSBridge.BuildRAG(ctx, query, tsVariants, topK); err == nil {
			return SearchResponse{Hits: mapTSHitsToSearchHits(rag.Hits, topK)}, rag.Context
		}
		// 失败时静默降级到占位结果；这样不会因为本地 Node/TS 目录问题影响 API 可用性
	}

	// 未命中租户数据、TS bridge 未配置或调用失败时，回退到占位命中（context 返回空字符串）
	return SearchResponse{Hits: buildPlaceholderSearchHits(topK)}, ""
}

func buildChatTools() []modelgateway.ToolDefinition {
	// buildChatTools 定义模型可调用的函数（当前只暴露本地代码检索工具）。
	// 这里返回的是“给模型看的工具说明书”（schema），不是工具执行逻辑本身。
	return []modelgateway.ToolDefinition{
		{
			Type: "function",
			Function: modelgateway.ToolFunction{
				Name:        "search_codebase",
				Description: "Search the local codebase and return ranked hits with packed context for answering code/project questions.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search query used for code retrieval.",
						},
						"query_variants": map[string]any{
							"type":        "array",
							"description": "Optional additional search queries.",
							"items": map[string]any{
								"type": "string",
							},
						},
						"top_k": map[string]any{
							"type":        "integer",
							"description": "Max number of hits to return (1-12).",
						},
					},
					"required": []string{"query"},
				},
			},
		},
	}
}

func (s *Services) runSearchCodebaseTool(ctx context.Context, req ChatRequest, rewritten []string, tc modelgateway.ToolCall) (toolContent string, evidence []string, hitCount int) {
	// runSearchCodebaseTool 执行 search_codebase，并返回给模型的 JSON 字符串结果。
	// 注意：toolContent 是字符串，因为 function-calling 协议里 tool 消息 content 一般是文本（这里放 JSON 文本）。
	args := searchCodebaseToolArgs{}
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		// 模型会把函数参数放在一个 JSON 字符串里，这里需要先 parse。
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			b, _ := json.Marshal(map[string]any{
				"ok":     false,
				"error":  "invalid_tool_arguments",
				"detail": err.Error(),
			})
			return string(b), nil, 0
		}
	}

	query := strings.TrimSpace(args.Query)
	if query == "" {
		// 模型没给 query 时，用用户原始问题兜底
		query = strings.TrimSpace(req.Message)
	}
	topK := args.TopK
	if topK <= 0 {
		topK = 8
	}
	if topK > 12 {
		// 给工具调用加上限，防止模型一次性请求过多命中导致上下文爆炸
		topK = 12
	}

	// 合并 route 产生的改写 query 与模型在 tool 参数里给出的 query_variants
	mergedVariants := make([]string, 0, len(rewritten)+len(args.QueryVariants))
	mergedVariants = append(mergedVariants, rewritten...)
	mergedVariants = append(mergedVariants, args.QueryVariants...)

	searchResp, ragContext := s.buildRAGMaterials(ctx, req.TenantID, query, mergedVariants, topK)
	files := make([]string, 0, len(searchResp.Hits))
	compactHits := make([]map[string]any, 0, len(searchResp.Hits))
	for _, h := range searchResp.Hits {
		// tool 返回给模型时不需要整个 chunk 正文（太长），只回关键字段 + 单独 context 文本
		files = append(files, h.RelPath)
		compactHits = append(compactHits, map[string]any{
			"chunk_id":    h.ChunkID,
			"rel_path":    h.RelPath,
			"score":       h.Score,
			"bm25_score":  h.BM25Score,
			"dense_score": h.DenseScore,
		})
	}
	files = unique(files)

	b, _ := json.Marshal(map[string]any{
		"ok":      true,
		"query":   query,
		"top_k":   topK,
		"hits":    compactHits,
		"files":   files,
		"context": ragContext,
	})
	// 返回：
	// 1) toolContent: 发回模型的 JSON 字符串
	// 2) files:       给 API 最终 response 的 evidence_files 用
	// 3) hitCount:    调试统计（retrieval_debug.candidate_hits）
	return string(b), files, len(searchResp.Hits)
}

func (s *Services) chatWithFunctionCalling(ctx context.Context, req ChatRequest, rewritten []string) (answer string, evidence []string, hitCount int, err error) {
	// chatWithFunctionCalling 使用 OpenAI-compatible tools/tool_calls 完成“先检索再回答”的真实函数调用流程。
	// 流程是：
	// 1) 把 tools schema 发给模型
	// 2) 模型返回 tool_calls（例如 search_codebase）
	// 3) Go 执行工具
	// 4) 把工具结果作为 role=tool 消息回给模型
	// 5) 模型基于工具结果给最终答案
	if s.Model == nil {
		return "", nil, 0, fmt.Errorf("model client is nil")
	}

	tools := buildChatTools()
	messages := []modelgateway.ChatMessage{
		{
			Role: "system",
			Content: "You are a coding agent for local repositories. " +
				"Use the search_codebase tool when the user asks about project/code details. " +
				"After receiving tool results, answer in Chinese and cite relevant files.",
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("mode=%s\nuser_task=%s", strings.TrimSpace(req.Mode), strings.TrimSpace(req.Message)),
		},
	}

	for round := 0; round < 3; round++ {
		// 每一轮都让模型决定：继续调工具，还是直接给答案
		out, callErr := s.Model.ChatCompletion(ctx, messages, 0.2, tools, "auto")
		if callErr != nil {
			return "", evidence, hitCount, callErr
		}

		if len(out.ToolCalls) == 0 {
			// 没有 tool_calls 说明模型已经给出最终答案（或至少不再请求工具）
			return strings.TrimSpace(out.Content), unique(evidence), hitCount, nil
		}

		// 把模型发出的 tool_calls 作为 assistant 消息回放回消息历史，符合 function-calling 协议。
		messages = append(messages, modelgateway.ChatMessage{
			Role:      "assistant",
			Content:   out.Content,
			ToolCalls: out.ToolCalls,
		})

		for _, tc := range out.ToolCalls {
			toolContent := ""
			switch tc.Function.Name {
			case "search_codebase":
				var toolEvidence []string
				var toolHits int
				toolContent, toolEvidence, toolHits = s.runSearchCodebaseTool(ctx, req, rewritten, tc)
				evidence = append(evidence, toolEvidence...)
				if toolHits > hitCount {
					hitCount = toolHits
				}
			default:
				b, _ := json.Marshal(map[string]any{
					"ok":    false,
					"error": "unknown_tool",
					"name":  tc.Function.Name,
				})
				toolContent = string(b)
			}

			messages = append(messages, modelgateway.ChatMessage{
				// role=tool 是 function-calling 协议要求：表示“工具执行结果”
				Role:       "tool",
				Name:       tc.Function.Name,
				ToolCallID: tc.ID,
				Content:    toolContent,
			})
		}
	}

	// 兜底保护：防止模型无限循环调工具
	return "", unique(evidence), hitCount, fmt.Errorf("tool_call_round_limit_exceeded")
}

func (s *Services) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// Chat 执行“问答/总结”主流程：参数校验 -> 检索 -> 调模型 -> 记录日志 -> 返回结果。
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

	// 3) 获取检索材料：优先用 TS retrieve.ts（命中 + context），失败时回退占位结果
	searchResp, ragContext := s.buildRAGMaterials(ctx, req.TenantID, req.Message, rewritten, 8)
	// 这里的 searchResp/ragContext 是“预取”结果：
	// - 给普通 fallback prompt 直接用
	// - function-calling 成功时也能作为初始 evidence 参考/兜底

	// 4) 提取证据文件路径（供模型提示词和前端展示）
	evidence := make([]string, 0, len(searchResp.Hits))
	for _, h := range searchResp.Hits {
		evidence = append(evidence, h.RelPath)
	}

	// 5) 调模型生成回答；模型不可用时使用兜底文案
	answer := ""
	candidateHits := len(searchResp.Hits)
	if s.Model != nil {
		// 优先走真正的 OpenAI-compatible function-calling（工具内部调用 TS RAG）。
		if fcAnswer, fcEvidence, fcHits, fcErr := s.chatWithFunctionCalling(ctx, req, rewritten); fcErr == nil && strings.TrimSpace(fcAnswer) != "" {
			// function-calling 成功：用工具执行后返回的答案和 evidence 覆盖预取结果
			answer = fcAnswer
			if len(fcEvidence) > 0 {
				evidence = unique(fcEvidence)
			}
			if fcHits > 0 {
				candidateHits = fcHits
			}
		} else {
			// provider 不支持 tools 或 function-calling 失败时，回退到普通 prompt + 检索上下文模式。
			prompt := fmt.Sprintf(
				"Task: %s\nEvidence files: %v\nRetrieved context:\n%s\nReturn concise Chinese answer in Chinese. If context is insufficient, say what is missing.",
				req.Message,
				evidence,
				strings.TrimSpace(ragContext),
			)
			content, modelErr := s.Model.Chat(ctx, []modelgateway.ChatMessage{{Role: "user", Content: prompt}}, 0.2) // 调 LLM 生成回答
			if modelErr == nil {
				answer = content
			}
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
			CandidateHits: candidateHits,
		},
	}, nil
}

func (s *Services) Ingest(ctx context.Context, req IngestRequest) (IngestResponse, error) {
	// Ingest 创建异步导入任务：落库 pending + 发 MQ 消息（若已配置）。
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
			errMsg := err.Error()
			if s.Store != nil {
				_ = s.Store.UpdateTaskStatus(ctx, taskID, "failed", &errMsg)
			}
			return IngestResponse{}, err
		}
	} else if s.Store != nil {
		// 开发环境兜底：没配 MQ 时也异步起 goroutine 走同一套任务处理逻辑（方便本地快速验证业务闭环）。
		go func(m rabbitmq.TaskMessage) {
			bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = s.Store.UpdateTaskStatus(bg, m.TaskID, "running", nil)
			if err := s.handleTaskMessage(bg, m); err != nil {
				e := err.Error()
				_ = s.Store.UpdateTaskStatus(bg, m.TaskID, "failed", &e)
				return
			}
			_ = s.Store.UpdateTaskStatus(bg, m.TaskID, "success", nil)
		}(msg)
	}

	// 即使未配置 MQ，也先返回 pending，保证 API 契约稳定
	return IngestResponse{TaskID: taskID, Status: "pending"}, nil
}

func (s *Services) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	// Search 执行检索流程：校验 -> 读缓存 -> 计算/生成结果 -> 写缓存 -> 返回。
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

	// 3) 实际检索：优先走 TS retrieve.ts（本地扫目录 + BM25 融合），失败时回退占位命中
	resp, _ := s.buildRAGMaterials(ctx, req.TenantID, req.Query, nil, req.TopK)
	if s.Cache != nil {
		// 写缓存失败不影响主流程
		_ = s.Cache.SetJSON(ctx, cacheKey, resp, 2*time.Minute) // 写搜索缓存，TTL=2分钟
	}

	return resp, nil
}

func (s *Services) GetTask(ctx context.Context, taskID string) (TaskResponse, error) {
	// GetTask 查询异步任务状态，并映射为接口层可展示的进度信息。
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
	// unique 对字符串切片做保序去重（保留首次出现顺序）。
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
