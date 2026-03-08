package service

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent_server_go/internal/cache/redis"
	"agent_server_go/internal/modelgateway"
	"agent_server_go/internal/mq/rabbitmq"
	"agent_server_go/internal/retrieval"
	"agent_server_go/internal/store/mysql"
	"agent_server_go/internal/vector/milvus"
)

// searchCodebaseToolArgs 是 function-calling 中 search_codebase 的参数结构。
type searchCodebaseToolArgs struct {
	Query         string   `json:"query"`
	QueryVariants []string `json:"query_variants,omitempty"`
	TopK          int      `json:"top_k,omitempty"`
	RootDir       string   `json:"root_dir,omitempty"`
}

type readFileToolArgs struct {
	Path     string `json:"path"`
	MaxChars int    `json:"max_chars,omitempty"`
}

type readFileRangeToolArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	MaxChars  int    `json:"max_chars,omitempty"`
}

type writeFileToolArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type runCommandToolArgs struct {
	Command string `json:"command"`
	Workdir string `json:"workdir,omitempty"`
}

type Services struct {
	// 业务层聚合依赖：数据库、缓存、消息队列、模型网关等
	Store             *mysql.Store
	Cache             *redis.Client
	MQ                *rabbitmq.Client
	Model             *modelgateway.Client
	Vector            *milvus.Client
	TSBridge          *retrieval.TSBridge
	RateRPS           int
	RateBurst         int
	MemoryMaxMessages int64
	MemoryTTL         time.Duration
	// 上下文窗口压缩参数：按模型最大上下文动态计算历史预算。
	ContextBudgetRatio  float64
	MinHistoryTokens    int
	SummaryTargetTokens int
}

func New(store *mysql.Store, cache *redis.Client, mq *rabbitmq.Client, model *modelgateway.Client, rateRPS int, rateBurst int) *Services {
	// New 创建 service 层对象（聚合 数据库、缓存、MQ、模型等依赖）。
	// service 层本身不做复杂初始化，只负责依赖组装
	return &Services{
		Store:               store,
		Cache:               cache,
		MQ:                  mq,
		Model:               model,
		RateRPS:             rateRPS,
		RateBurst:           rateBurst,
		MemoryMaxMessages:   12,
		MemoryTTL:           30 * time.Minute,
		ContextBudgetRatio:  0.78,
		MinHistoryTokens:    320,
		SummaryTargetTokens: 320,
	}
}

func (s *Services) SetTSBridge(b *retrieval.TSBridge) {
	// SetTSBridge 注入可选的 TS 检索桥接器；未注入时保留 Go 占位检索逻辑。
	s.TSBridge = b
}

func (s *Services) SetVectorClient(v *milvus.Client) {
	// SetVectorClient 注入 Milvus 向量库客户端；未注入时 dense 检索分支自动跳过。
	s.Vector = v
}

// SetMemoryOptions 配置会话短期记忆策略。
func (s *Services) SetMemoryOptions(maxMessages int, ttlSeconds int) {
	if maxMessages > 0 {
		s.MemoryMaxMessages = int64(maxMessages)
	}
	if ttlSeconds > 0 {
		s.MemoryTTL = time.Duration(ttlSeconds) * time.Second
	}
}

// SetContextCompressionOptions 配置上下文窗口压缩策略。
func (s *Services) SetContextCompressionOptions(budgetRatio float64, minHistoryTokens int, summaryTargetTokens int) {
	if budgetRatio > 0.2 && budgetRatio <= 0.95 {
		s.ContextBudgetRatio = budgetRatio
	}
	if minHistoryTokens > 0 {
		s.MinHistoryTokens = minHistoryTokens
	}
	if summaryTargetTokens > 0 {
		s.SummaryTargetTokens = summaryTargetTokens
	}
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
	// searchTenantChunks 在 MySQL chunks 表上执行“BM25 + Milvus dense”混合检索。
	// 当 Milvus 未配置或不可用时，会自动退回纯 BM25。
	if s.Store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(query) == "" {
		return SearchResponse{}, "", false
	}
	all, err := s.Store.ListChunksByTenant(ctx, tenantID, 800)
	if err != nil || len(all) == 0 {
		return SearchResponse{}, "", false
	}

	docs := make([]retrieval.HybridDoc, 0, len(all))
	chunkByID := make(map[string]mysql.ChunkRecord, len(all))
	for _, ch := range all {
		docs = append(docs, retrieval.HybridDoc{
			ID:      ch.ID,
			RelPath: ch.RelPath,
			Text:    ch.Text,
		})
		chunkByID[strconv.FormatInt(ch.ID, 10)] = ch
	}

	recallK := topK * 5
	if recallK < 40 {
		recallK = 40
	}
	if recallK > len(docs) {
		recallK = len(docs)
	}

	bm25Hits := retrieval.BM25SearchLocalDocs(docs, query, queryVariants, recallK)
	type merged struct {
		chunk         mysql.ChunkRecord
		rawBM25       float64
		rawDense      float64
		queryCoverage float64
		pathBoost     float64
	}
	mergedByID := make(map[string]*merged, len(bm25Hits))
	maxBM25 := 0.0
	maxDense := 0.0

	for _, h := range bm25Hits {
		key := strconv.FormatInt(h.ID, 10)
		chunk, ok := chunkByID[key]
		if !ok {
			continue
		}
		mergedByID[key] = &merged{
			chunk:         chunk,
			rawBM25:       h.BM25Score,
			rawDense:      0,
			queryCoverage: h.QueryCoverage,
			pathBoost:     h.PathBoost,
		}
		if h.BM25Score > maxBM25 {
			maxBM25 = h.BM25Score
		}
	}

	if s.Vector != nil && s.Vector.Enabled() {
		for _, q := range retrieval.SanitizeQueries(query, queryVariants) {
			vec := retrieval.HashEmbedText(q)
			denseHits, denseErr := s.Vector.Search(ctx, tenantID, vec, recallK)
			if denseErr != nil {
				log.Printf("milvus search failed: tenant=%s err=%v", tenantID, denseErr)
				break
			}
			for _, h := range denseHits {
				chunk, ok := chunkByID[h.ChunkID]
				if !ok {
					continue
				}
				item := mergedByID[h.ChunkID]
				if item == nil {
					item = &merged{chunk: chunk}
					mergedByID[h.ChunkID] = item
				}
				if h.Score > item.rawDense {
					item.rawDense = h.Score
				}
				if h.Score > maxDense {
					maxDense = h.Score
				}
			}
		}
	}

	if len(mergedByID) == 0 {
		return SearchResponse{}, "", false
	}

	type hybridHit struct {
		SearchHit
		text string
	}
	hybridHits := make([]hybridHit, 0, len(mergedByID))
	for id, item := range mergedByID {
		score := retrieval.FuseScore(
			retrieval.Normalize(item.rawBM25, maxBM25),
			retrieval.Normalize(item.rawDense, maxDense),
			item.queryCoverage,
			item.pathBoost,
		)
		hybridHits = append(hybridHits, hybridHit{
			SearchHit: SearchHit{
				ChunkID:    id,
				RelPath:    item.chunk.RelPath,
				Score:      score,
				BM25Score:  item.rawBM25,
				DenseScore: item.rawDense,
			},
			text: item.chunk.Text,
		})
	}

	sort.SliceStable(hybridHits, func(i, j int) bool {
		if hybridHits[i].Score == hybridHits[j].Score {
			if hybridHits[i].BM25Score == hybridHits[j].BM25Score {
				return hybridHits[i].DenseScore > hybridHits[j].DenseScore
			}
			return hybridHits[i].BM25Score > hybridHits[j].BM25Score
		}
		return hybridHits[i].Score > hybridHits[j].Score
	})
	if len(hybridHits) > topK {
		hybridHits = hybridHits[:topK]
	}

	hits := make([]SearchHit, 0, len(hybridHits))
	contextHits := make([]retrieval.HybridDocHit, 0, len(hybridHits))
	for _, c := range hybridHits {
		hits = append(hits, SearchHit{
			ChunkID:    c.ChunkID,
			RelPath:    c.RelPath,
			Score:      c.Score,
			BM25Score:  c.BM25Score,
			DenseScore: c.DenseScore,
		})
		id64, _ := strconv.ParseInt(c.ChunkID, 10, 64)
		contextHits = append(contextHits, retrieval.HybridDocHit{
			ID:         id64,
			RelPath:    c.RelPath,
			Text:       c.text,
			Score:      c.Score,
			BM25Score:  c.BM25Score,
			DenseScore: c.DenseScore,
		})
	}
	contextText := retrieval.BuildContextFromHybridHits(contextHits, 8000)
	return SearchResponse{Hits: hits}, contextText, true
}

func (s *Services) buildRAGMaterials(ctx context.Context, tenantID, rootDir, query string, queryVariants []string, topK int) (SearchResponse, string, string) {
	// buildRAGMaterials 聚合检索材料：
	// 1) 先查租户已导入 chunks（MySQL + Milvus）
	// 2) 传了 root_dir 时，尝试 Go 本地 AST + chunk 检索
	// 3) 再走 TS bridge 扫本地目录
	// 4) 再走默认 TS target root
	// 5) 最后回退占位命中
	// 返回值：
	// 1) SearchResponse：给 /search API 或 chat 调试信息用（结构化 hits）
	// 2) string       ：给 /chat prompt / tool 直接使用的打包 context 文本
	// 3) string       ：检索策略标记（tenant_hybrid/local_ast/ts_bridge_root/ts_bridge_default/placeholder）
	if topK <= 0 {
		// 调用方没传 topK 时给一个默认值，避免 TS/占位检索收到 0
		topK = 8
	}

	// queryVariants 去重清洗：避免把原 query 重复传给 TS retrieve.ts。
	tsVariants := make([]string, 0, len(queryVariants))
	seen := map[string]struct{}{strings.TrimSpace(query): {}}
	for _, q := range queryVariants {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		tsVariants = append(tsVariants, q)
	}

	rootDir = strings.TrimSpace(rootDir)
	// 优先使用租户已导入的数据（MySQL chunks）。这是 API SaaS 主流程的真实检索来源。
	if resp, ctxText, ok := s.searchTenantChunks(ctx, tenantID, query, queryVariants, topK); ok {
		return resp, ctxText, "tenant_hybrid"
	}
	if rootDir != "" {
		// 本地目录检索优先尝试 Go AST + chunk 混合检索，结构类问题通常比纯文本更稳。
		if resp, ctxText, ok := s.searchLocalWorkspaceWithAST(rootDir, query, queryVariants, topK); ok {
			return resp, ctxText, "local_ast"
		}
	}
	if rootDir != "" && s.TSBridge != nil && s.TSBridge.Enabled() {
		// tenant 中没有已索引数据时，再按 root_dir 扫本地代码。
		if rag, err := s.TSBridge.BuildRAGWithRoot(ctx, rootDir, query, tsVariants, topK); err == nil {
			return SearchResponse{Hits: mapTSHitsToSearchHits(rag.Hits, topK)}, rag.Context, "ts_bridge_root"
		}
		// root_dir 扫描失败不直接报错，继续降级到默认根目录或占位结果。
	}
	if s.TSBridge != nil && s.TSBridge.Enabled() {
		// 真正调用 TS RAG（Node 子进程 -> agent/dist/retrieve.js）。
		// 成功：拿到 hits + context，转换后直接返回。
		if rag, err := s.TSBridge.BuildRAGWithRoot(ctx, "", query, tsVariants, topK); err == nil {
			return SearchResponse{Hits: mapTSHitsToSearchHits(rag.Hits, topK)}, rag.Context, "ts_bridge_default"
		}
		// 失败时静默降级到占位结果；这样不会因为本地 Node/TS 目录问题影响 API 可用性
	}

	// 未命中租户数据、TS bridge 未配置或调用失败时，回退到占位命中（context 返回空字符串）
	return SearchResponse{Hits: buildPlaceholderSearchHits(topK)}, "", "placeholder"
}

func buildChatTools(mode string) []modelgateway.ToolDefinition {
	// buildChatTools 定义模型可调用的函数。
	// chat 模式只开放检索；edit 模式额外开放读写文件工具。
	tools := []modelgateway.ToolDefinition{
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
						"root_dir": map[string]any{
							"type":        "string",
							"description": "Optional local project root directory for scanning code files.",
						},
					},
					"required": []string{"query"},
				},
			},
		},
	}

	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "edit" || mode == "code" || mode == "coding" {
		tools = append(tools,
			modelgateway.ToolDefinition{
				Type: "function",
				Function: modelgateway.ToolFunction{
					Name:        "read_file",
					Description: "Read one source file from the workspace before making edits.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": "Relative or absolute path to the file inside workspace root.",
							},
							"max_chars": map[string]any{
								"type":        "integer",
								"description": "Optional max characters to return.",
							},
						},
						"required": []string{"path"},
					},
				},
			},
			modelgateway.ToolDefinition{
				Type: "function",
				Function: modelgateway.ToolFunction{
					Name:        "read_file_range",
					Description: "Read a specific line range from one source file. Use this for large files or when the controller requires full-file inspection before edits.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": "Relative or absolute path to the file inside workspace root.",
							},
							"start_line": map[string]any{
								"type":        "integer",
								"description": "1-based inclusive start line.",
							},
							"end_line": map[string]any{
								"type":        "integer",
								"description": "1-based inclusive end line.",
							},
							"max_chars": map[string]any{
								"type":        "integer",
								"description": "Optional max characters to return.",
							},
						},
						"required": []string{"path", "start_line", "end_line"},
					},
				},
			},
			modelgateway.ToolDefinition{
				Type: "function",
				Function: modelgateway.ToolFunction{
					Name:        "write_file",
					Description: "Write updated content to one existing source file inside workspace root.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": "Relative or absolute path to the file inside workspace root.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Full replacement file content.",
							},
						},
						"required": []string{"path", "content"},
					},
				},
			},
			modelgateway.ToolDefinition{
				Type: "function",
				Function: modelgateway.ToolFunction{
					Name:        "run_command",
					Description: "Run one safe syntax/build/test verification command inside the workspace after edits. Supported families: go test/build/vet, node --check, npx tsc --noEmit, npm test/build, python -m py_compile, pytest.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"command": map[string]any{
								"type":        "string",
								"description": "Exact command to run, without shell chaining.",
							},
							"workdir": map[string]any{
								"type":        "string",
								"description": "Optional relative or absolute directory inside workspace root.",
							},
						},
						"required": []string{"command"},
					},
				},
			},
		)
	}
	return tools
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

	rootDir := strings.TrimSpace(args.RootDir)
	if rootDir == "" {
		rootDir = strings.TrimSpace(req.RootDir)
	}
	searchResp, ragContext, _ := s.buildRAGMaterials(ctx, req.TenantID, rootDir, query, mergedVariants, topK)
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

func (s *Services) resolveWorkspaceRoot(rootDir string) string {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir != "" {
		if _, err := os.Stat(rootDir); err == nil {
			return rootDir
		}
	}
	if s.TSBridge != nil {
		if fallback := strings.TrimSpace(s.TSBridge.DefaultRootDir()); fallback != "" {
			if _, err := os.Stat(fallback); err == nil {
				return fallback
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func safeWorkspacePath(rootDir, inputPath string) (string, string, error) {
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", "", err
	}
	target := strings.TrimSpace(inputPath)
	if target == "" {
		return "", "", fmt.Errorf("empty path")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(rootAbs, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes workspace root")
	}
	return targetAbs, filepath.ToSlash(rel), nil
}

func (s *Services) runReadFileTool(req ChatRequest, tc modelgateway.ToolCall) (string, string, bool) {
	args := readFileToolArgs{}
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "error": "invalid_tool_arguments", "detail": err.Error()})
			return string(b), "", false
		}
	}
	root := s.resolveWorkspaceRoot(req.RootDir)
	absPath, relPath, err := safeWorkspacePath(root, args.Path)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return string(b), "", false
	}
	body, err := os.ReadFile(absPath)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error(), "path": relPath})
		return string(b), "", false
	}
	fullText := string(body)
	text := fullText
	maxChars := args.MaxChars
	if maxChars <= 0 {
		maxChars = 24000
	}
	truncated := false
	if len(text) > maxChars {
		text = text[:maxChars]
		truncated = true
	}
	b, _ := json.Marshal(map[string]any{
		"ok":          true,
		"path":        relPath,
		"truncated":   truncated,
		"total_chars": len(fullText),
		"content":     text,
	})
	return string(b), relPath, !truncated
}

func readFileRange(absPath string, startLine, endLine, maxChars int) (content string, totalLines int, actual lineRange, err error) {
	if startLine <= 0 {
		startLine = 1
	}
	if endLine < startLine {
		endLine = startLine
	}
	if maxChars <= 0 {
		maxChars = 24000
	}

	body, err := os.ReadFile(absPath)
	if err != nil {
		return "", 0, lineRange{}, err
	}
	lines := strings.Split(string(body), "\n")
	totalLines = len(lines)
	if totalLines == 0 {
		totalLines = 1
	}
	if startLine > totalLines {
		startLine = totalLines
	}
	if endLine > totalLines {
		endLine = totalLines
	}

	actual = lineRange{Start: startLine, End: endLine}
	var b strings.Builder
	for i := startLine; i <= endLine; i++ {
		line := fmt.Sprintf("%d: %s\n", i, lines[i-1])
		if b.Len()+len(line) > maxChars {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n"), totalLines, actual, nil
}

func (s *Services) runReadFileRangeTool(req ChatRequest, tc modelgateway.ToolCall) (string, string, lineRange) {
	args := readFileRangeToolArgs{}
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "error": "invalid_tool_arguments", "detail": err.Error()})
			return string(b), "", lineRange{}
		}
	}
	root := s.resolveWorkspaceRoot(req.RootDir)
	absPath, relPath, err := safeWorkspacePath(root, args.Path)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return string(b), "", lineRange{}
	}

	content, totalLines, actual, err := readFileRange(absPath, args.StartLine, args.EndLine, args.MaxChars)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error(), "path": relPath})
		return string(b), "", lineRange{}
	}
	b, _ := json.Marshal(map[string]any{
		"ok":          true,
		"path":        relPath,
		"start_line":  actual.Start,
		"end_line":    actual.End,
		"total_lines": totalLines,
		"content":     content,
	})
	return string(b), relPath, actual
}

func (s *Services) runWriteFileTool(req ChatRequest, tc modelgateway.ToolCall) (string, string) {
	args := writeFileToolArgs{}
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "error": "invalid_tool_arguments", "detail": err.Error()})
			return string(b), ""
		}
	}
	root := s.resolveWorkspaceRoot(req.RootDir)
	absPath, relPath, err := safeWorkspacePath(root, args.Path)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return string(b), ""
	}
	if _, statErr := os.Stat(absPath); statErr != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": "target file does not exist", "path": relPath})
		return string(b), ""
	}
	if len(args.Content) > 600000 {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": "content too large", "path": relPath})
		return string(b), ""
	}
	if writeErr := os.WriteFile(absPath, []byte(args.Content), 0644); writeErr != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": writeErr.Error(), "path": relPath})
		return string(b), ""
	}
	b, _ := json.Marshal(map[string]any{
		"ok":    true,
		"path":  relPath,
		"bytes": len(args.Content),
	})
	return string(b), relPath
}

func (s *Services) resolveToolWorkdir(rootDir, workdir string) (string, string, error) {
	root := s.resolveWorkspaceRoot(rootDir)
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return root, ".", nil
	}
	absPath, relPath, err := safeWorkspacePath(root, workdir)
	if err != nil {
		return "", "", err
	}
	stat, err := os.Stat(absPath)
	if err != nil {
		return "", "", err
	}
	if !stat.IsDir() {
		return "", "", fmt.Errorf("workdir must be a directory")
	}
	return absPath, relPath, nil
}

func hasShellMeta(command string) bool {
	return strings.ContainsAny(command, "&;|><`$\n\r")
}

func isAllowedVerificationCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "go":
		if len(args) < 2 {
			return false
		}
		switch args[1] {
		case "test", "build", "vet":
			return true
		default:
			return false
		}
	case "node":
		return len(args) >= 3 && args[1] == "--check"
	case "npm":
		if len(args) == 2 && args[1] == "test" {
			return true
		}
		return len(args) >= 3 && args[1] == "run" && (args[2] == "test" || args[2] == "build" || args[2] == "lint")
	case "npx":
		return len(args) >= 2 && args[1] == "tsc"
	case "tsc":
		return true
	case "python", "python3":
		return len(args) >= 4 && args[1] == "-m" && args[2] == "py_compile"
	case "pytest":
		return true
	default:
		return false
	}
}

func trimCommandOutput(out string, maxChars int) string {
	out = strings.TrimSpace(out)
	if maxChars <= 0 {
		maxChars = 12000
	}
	if len(out) <= maxChars {
		return out
	}
	return out[:maxChars] + "\n...[truncated]"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func inferVerificationCommand(root string, changedFiles []string) string {
	if len(changedFiles) == 0 {
		return ""
	}
	hasGo := false
	hasTS := false
	hasJS := false
	hasPy := false
	firstJS := ""
	pyFiles := make([]string, 0, len(changedFiles))
	for _, file := range changedFiles {
		ext := strings.ToLower(filepath.Ext(file))
		switch ext {
		case ".go":
			hasGo = true
		case ".ts", ".tsx":
			hasTS = true
		case ".js", ".jsx", ".mjs", ".cjs":
			hasJS = true
			if firstJS == "" {
				firstJS = file
			}
		case ".py":
			hasPy = true
			pyFiles = append(pyFiles, file)
		}
	}

	switch {
	case hasGo && fileExists(filepath.Join(root, "go.mod")):
		return "go test ./..."
	case hasTS && fileExists(filepath.Join(root, "tsconfig.json")):
		return "npx tsc --noEmit"
	case hasJS && firstJS != "":
		return "node --check " + firstJS
	case hasPy && len(pyFiles) > 0:
		return "python3 -m py_compile " + strings.Join(pyFiles, " ")
	default:
		return ""
	}
}

func (s *Services) executeVerificationCommand(ctx context.Context, req ChatRequest, command, workdir string) (string, string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": "empty command"})
		return string(b), "", false
	}
	if hasShellMeta(command) {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": "shell metacharacters are not allowed"})
		return string(b), "", false
	}

	args := strings.Fields(command)
	if !isAllowedVerificationCommand(args) {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": "command not allowed", "command": command})
		return string(b), "", false
	}

	dirAbs, dirRel, err := s.resolveToolWorkdir(req.RootDir, workdir)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error(), "command": command})
		return string(b), "", false
	}

	execCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, args[0], args[1:]...)
	cmd.Dir = dirAbs
	output, runErr := cmd.CombinedOutput()
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	outText := trimCommandOutput(string(output), 12000)
	resp := map[string]any{
		"ok":        runErr == nil,
		"command":   strings.Join(args, " "),
		"workdir":   dirRel,
		"exit_code": exitCode,
		"output":    outText,
	}
	if runErr != nil {
		resp["error"] = runErr.Error()
	}
	b, _ := json.Marshal(resp)
	return string(b), strings.Join(args, " "), true
}

func (s *Services) runCommandTool(ctx context.Context, req ChatRequest, tc modelgateway.ToolCall) (string, string, bool) {
	args := runCommandToolArgs{}
	if strings.TrimSpace(tc.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			b, _ := json.Marshal(map[string]any{"ok": false, "error": "invalid_tool_arguments", "detail": err.Error()})
			return string(b), "", false
		}
	}
	return s.executeVerificationCommand(ctx, req, args.Command, args.Workdir)
}

func (s *Services) loadSessionMemory(ctx context.Context, tenantID, sessionID string) []modelgateway.ChatMessage {
	// loadSessionMemory 从 Redis 读取最近会话消息，按旧到新转成模型消息格式。
	if s.Cache == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	items, err := s.Cache.LoadSessionMessages(ctx, tenantID, sessionID, s.MemoryMaxMessages)
	if err != nil || len(items) == 0 {
		return nil
	}
	out := make([]modelgateway.ChatMessage, 0, len(items))
	for _, it := range items {
		role := strings.TrimSpace(strings.ToLower(it.Role))
		if role != "user" && role != "assistant" && role != "system" {
			continue
		}
		content := strings.TrimSpace(it.Content)
		if content == "" {
			continue
		}
		out = append(out, modelgateway.ChatMessage{
			Role:    role,
			Content: content,
		})
	}
	return out
}

func (s *Services) appendSessionMemory(ctx context.Context, tenantID, sessionID, role, content string) {
	// appendSessionMemory 把 user/assistant 消息写入 Redis 会话记忆（失败不影响主流程）。
	if s.Cache == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	role = strings.TrimSpace(strings.ToLower(role))
	if role != "user" && role != "assistant" && role != "system" {
		return
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	_ = s.Cache.AppendSessionMessage(ctx, tenantID, sessionID, redis.SessionMessage{
		Role:    role,
		Content: content,
		At:      time.Now().Unix(),
	}, s.MemoryMaxMessages, s.MemoryTTL)
}

type historyCompressionStats struct {
	ModelContextTokens int
	HistoryBefore      int
	HistoryAfter       int
	HistoryBudget      int
	Compressed         bool
}

func estimateTextTokens(s string) int {
	// estimateTextTokens 是近似估算：ASCII 约 4 字符/1 token，非 ASCII 按 1 字符/1 token 估算。
	// 这里只用于上下文预算，不追求 tokenizer 级别精确。
	if strings.TrimSpace(s) == "" {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for _, r := range s {
		if r <= 127 {
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII + 1
}

func estimateMessageTokens(m modelgateway.ChatMessage) int {
	// 每条消息额外加协议开销，避免预算过于乐观。
	return 6 + estimateTextTokens(m.Role) + estimateTextTokens(m.Name) + estimateTextTokens(m.Content)
}

func estimateMessagesTokens(messages []modelgateway.ChatMessage) int {
	total := 0
	for _, m := range messages {
		total += estimateMessageTokens(m)
	}
	return total
}

func trimByApproxTokens(s string, maxTokens int) string {
	if maxTokens <= 0 || strings.TrimSpace(s) == "" {
		return ""
	}
	if estimateTextTokens(s) <= maxTokens {
		return strings.TrimSpace(s)
	}
	// 反向按 rune 截断，直到落到目标 token 以下。
	runes := []rune(s)
	lo, hi := 0, len(runes)
	best := ""
	for lo <= hi {
		mid := (lo + hi) / 2
		cur := strings.TrimSpace(string(runes[:mid]))
		if estimateTextTokens(cur) <= maxTokens {
			best = cur
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

func serializeHistoryForSummary(history []modelgateway.ChatMessage, maxBytes int) string {
	if len(history) == 0 {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = 12000
	}
	var b strings.Builder
	for i, m := range history {
		role := strings.TrimSpace(strings.ToLower(m.Role))
		if role == "" {
			role = "unknown"
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		line := fmt.Sprintf("%d) %s: %s\n", i+1, role, content)
		// 控制输入摘要模型的大小，避免二次请求又超窗。
		if b.Len()+len(line) > maxBytes {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

func (s *Services) summarizeHistoryForContext(ctx context.Context, mode string, history []modelgateway.ChatMessage, targetTokens int) string {
	// summarizeHistoryForContext 用 LLM 压缩旧对话，只保留后续回答必须记住的信息。
	if s.Model == nil || len(history) == 0 {
		return ""
	}
	if targetTokens <= 0 {
		targetTokens = s.SummaryTargetTokens
	}
	historyText := serializeHistoryForSummary(history, 12000)
	if strings.TrimSpace(historyText) == "" {
		return ""
	}

	systemPrompt := "You compress conversation memory for a coding assistant. Keep only high-value facts."
	userPrompt := fmt.Sprintf(
		"请把下面历史对话压缩成后续回答可用的上下文记忆。\n"+
			"要求：\n"+
			"1) 保留：用户目标、关键约束、已做决定、未完成事项、重要术语映射。\n"+
			"2) 删除：寒暄、重复内容、低价值细节。\n"+
			"3) 输出中文，使用 4-8 条要点。\n"+
			"4) 目标长度不超过约 %d tokens。\n"+
			"5) 当前模式：%s。\n\n历史对话：\n%s",
		targetTokens,
		strings.TrimSpace(mode),
		historyText,
	)

	summaryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := s.Model.Chat(summaryCtx, []modelgateway.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, 0.1)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func reverseMessages(in []modelgateway.ChatMessage) []modelgateway.ChatMessage {
	out := make([]modelgateway.ChatMessage, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, in[i])
	}
	return out
}

func (s *Services) compressHistoryByContextWindow(ctx context.Context, req ChatRequest, history []modelgateway.ChatMessage, ragContext string) ([]modelgateway.ChatMessage, historyCompressionStats) {
	// compressHistoryByContextWindow 按模型上下文上限动态裁剪历史：
	// 1) 估算本轮总预算
	// 2) 超预算时优先保留最近消息
	// 3) 被裁掉的旧消息交给 LLM 摘要，再以 system memory 形式回灌
	stats := historyCompressionStats{}
	if len(history) == 0 {
		return history, stats
	}

	maxCtx := 8192
	if s.Model != nil {
		maxCtx = s.Model.GetMaxContextTokens(ctx)
	}
	if maxCtx <= 0 {
		maxCtx = 8192
	}
	stats.ModelContextTokens = maxCtx

	// 估算本轮中“非历史”的 token 开销（system prompt + 当前 query + 预取 RAG 上下文 + 输出预留）。
	systemOverhead := estimateTextTokens("You are a coding agent for local repositories.")
	queryOverhead := estimateTextTokens(req.Message) + 40
	ragOverhead := estimateTextTokens(ragContext)
	if ragOverhead > maxCtx/3 {
		// RAG context 仅作为预算参考，避免极端情况下历史预算被压成 0。
		ragOverhead = maxCtx / 3
	}
	reserveForAnswer := maxCtx / 6
	if reserveForAnswer < 512 {
		reserveForAnswer = 512
	}

	budgetRatio := s.ContextBudgetRatio
	if budgetRatio <= 0 {
		budgetRatio = 0.78
	}
	totalPromptBudget := int(float64(maxCtx) * budgetRatio)
	historyBudget := totalPromptBudget - systemOverhead - queryOverhead - ragOverhead - reserveForAnswer
	if historyBudget < s.MinHistoryTokens {
		historyBudget = s.MinHistoryTokens
	}
	if historyBudget < 128 {
		historyBudget = 128
	}
	stats.HistoryBudget = historyBudget

	beforeTokens := estimateMessagesTokens(history)
	stats.HistoryBefore = beforeTokens
	if beforeTokens <= historyBudget {
		stats.HistoryAfter = beforeTokens
		return history, stats
	}

	// 第一阶段：只保留最近消息（短期上下文优先）。
	recentBudget := int(float64(historyBudget) * 0.65)
	if recentBudget < 128 {
		recentBudget = historyBudget
	}
	recent := make([]modelgateway.ChatMessage, 0, len(history))
	used := 0
	for i := len(history) - 1; i >= 0; i-- {
		cost := estimateMessageTokens(history[i])
		if used+cost > recentBudget && len(recent) >= 2 {
			break
		}
		recent = append(recent, history[i])
		used += cost
	}
	recent = reverseMessages(recent)
	if len(recent) > len(history) {
		recent = history
	}
	cutIdx := len(history) - len(recent)
	if cutIdx < 0 {
		cutIdx = 0
	}
	older := history[:cutIdx]

	// 第二阶段：把 older 摘要后作为一条 system memory 回灌。
	if len(older) > 0 {
		target := s.SummaryTargetTokens
		if target <= 0 {
			target = 320
		}
		summary := s.summarizeHistoryForContext(ctx, req.Mode, older, target)
		if summary != "" {
			memory := "以下是自动压缩的会话历史摘要，请在回答时遵守：\n" + summary
			combined := make([]modelgateway.ChatMessage, 0, len(recent)+1)
			combined = append(combined, modelgateway.ChatMessage{Role: "system", Content: memory})
			combined = append(combined, recent...)
			combinedTokens := estimateMessagesTokens(combined)
			if combinedTokens > historyBudget {
				allowedSummary := historyBudget - estimateMessagesTokens(recent) - 8
				memory = trimByApproxTokens(memory, allowedSummary)
				if strings.TrimSpace(memory) != "" {
					combined = append([]modelgateway.ChatMessage{{Role: "system", Content: memory}}, recent...)
				} else {
					combined = recent
				}
			}
			after := estimateMessagesTokens(combined)
			stats.HistoryAfter = after
			stats.Compressed = after < beforeTokens
			return combined, stats
		}
	}

	// 摘要失败时只用 recent 兜底。
	after := estimateMessagesTokens(recent)
	stats.HistoryAfter = after
	stats.Compressed = after < beforeTokens
	return recent, stats
}

func (s *Services) chatWithFunctionCalling(ctx context.Context, req ChatRequest, rewritten []string, history []modelgateway.ChatMessage, plan taskPlan) (answer string, evidence []string, changedFiles []string, verificationCommands []string, hitCount int, err error) {
	// chatWithFunctionCalling 使用 OpenAI-compatible tools/tool_calls 完成“先检索再回答”的真实函数调用流程。
	// 流程是：
	// 1) 把 tools schema 发给模型
	// 2) 模型返回 tool_calls（例如 search_codebase）
	// 3) Go 执行工具
	// 4) 把工具结果作为 role=tool 消息回给模型
	// 5) 模型基于工具结果给最终答案
	if s.Model == nil {
		return "", nil, nil, nil, 0, fmt.Errorf("model client is nil")
	}

	effectiveMode := inferEffectiveMode(req)
	tools := buildChatTools(effectiveMode)
	systemPrompt := "You are a coding agent for local repositories. Use the search_codebase tool when the user asks about project/code details. After receiving tool results, answer in Chinese and cite relevant files."
	mode := effectiveMode
	if mode == "edit" {
		systemPrompt = "You are a coding agent for local repositories. In edit mode, inspect files first, make minimal correct edits using read_file, read_file_range and write_file, then run at least one verification command with run_command before giving the final answer. Only write files inside the workspace root. Prefer syntax/build/test verification that matches changed files. After verification, answer in Chinese and summarize exactly which files changed and what verification was executed."
	}
	messages := []modelgateway.ChatMessage{
		{
			Role:    "system",
			Content: systemPrompt,
		},
	}
	if controllerText := planToPrompt(plan); controllerText != "" {
		messages = append(messages, modelgateway.ChatMessage{
			Role:    "system",
			Content: controllerText,
		})
	}
	messages = append(messages, history...)
	messages = append(messages, modelgateway.ChatMessage{
		Role:    "user",
		Content: fmt.Sprintf("mode=%s\nuser_task=%s", mode, strings.TrimSpace(req.Message)),
	})

	roundLimit := 3
	if mode == "edit" {
		roundLimit = 6
	}
	ranVerification := false
	inspectionState := newInspectionState(plan)

	for round := 0; round < roundLimit; round++ {
		// 每一轮都让模型决定：继续调工具，还是直接给答案
		out, callErr := s.Model.ChatCompletion(ctx, messages, 0.2, tools, "auto")
		if callErr != nil {
			return "", evidence, changedFiles, verificationCommands, hitCount, callErr
		}

		if len(out.ToolCalls) == 0 {
			if mode == "edit" && len(changedFiles) > 0 && !ranVerification {
				root := s.resolveWorkspaceRoot(req.RootDir)
				if autoCmd := inferVerificationCommand(root, unique(changedFiles)); autoCmd != "" {
					autoContent, usedCmd, executed := s.executeVerificationCommand(ctx, req, autoCmd, "")
					if executed && usedCmd != "" {
						ranVerification = true
						verificationCommands = append(verificationCommands, usedCmd)
						messages = append(messages, modelgateway.ChatMessage{
							Role:    "system",
							Content: "Backend auto verification was executed because you edited files but did not verify them yourself. Use the verification result below before giving the final answer.",
						})
						messages = append(messages, modelgateway.ChatMessage{
							Role:    "user",
							Content: "自动校验结果如下，请基于结果给出最终中文答复：\n" + autoContent,
						})
						continue
					}
				}
			}
			// 没有 tool_calls 说明模型已经给出最终答案（或至少不再请求工具）
			return strings.TrimSpace(out.Content), unique(evidence), unique(changedFiles), unique(verificationCommands), hitCount, nil
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
			case "read_file":
				var relPath string
				var fullyRead bool
				toolContent, relPath, fullyRead = s.runReadFileTool(req, tc)
				if relPath != "" && fullyRead {
					markFileFullyRead(inspectionState, relPath)
				}
			case "read_file_range":
				var relPath string
				var actual lineRange
				toolContent, relPath, actual = s.runReadFileRangeTool(req, tc)
				if relPath != "" && actual.Start > 0 {
					markFileRangeRead(inspectionState, relPath, actual)
				}
			case "write_file":
				args := writeFileToolArgs{}
				if strings.TrimSpace(tc.Function.Arguments) != "" && json.Unmarshal([]byte(tc.Function.Arguments), &args) == nil {
					root := s.resolveWorkspaceRoot(req.RootDir)
					if _, relPath, err := safeWorkspacePath(root, args.Path); err == nil {
						if required := plan.MandatoryRanges[relPath]; len(required) > 0 && !hasReadCoverage(inspectionState, relPath, required) {
							missing := missingLineRanges(inspectionState, relPath, required)
							b, _ := json.Marshal(map[string]any{
								"ok":             false,
								"error":          "must_read_target_file_before_write",
								"path":           relPath,
								"missing_ranges": missing,
							})
							toolContent = string(b)
							break
						}
					}
				}
				var changed string
				toolContent, changed = s.runWriteFileTool(req, tc)
				if changed != "" {
					changedFiles = append(changedFiles, changed)
				}
			case "run_command":
				var usedCmd string
				var executed bool
				toolContent, usedCmd, executed = s.runCommandTool(ctx, req, tc)
				if executed && usedCmd != "" {
					ranVerification = true
					verificationCommands = append(verificationCommands, usedCmd)
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
	return "", unique(evidence), unique(changedFiles), unique(verificationCommands), hitCount, fmt.Errorf("tool_call_round_limit_exceeded")
}

func (s *Services) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	// Chat 执行“问答/总结”主流程：参数校验 -> 检索 -> 调模型 -> 记录日志 -> 返回结果。
	// 1) 基本参数校验
	if req.TenantID == "" || req.SessionID == "" || req.UserID == "" || strings.TrimSpace(req.Message) == "" {
		return ChatResponse{}, errors.New("tenant_id/session_id/user_id/message are required")
	}

	effectiveMode := inferEffectiveMode(req)

	// 2) 生成 query 变体（当前是启发式示例，后续可换成 LLM query rewrite）
	rewritten := retrieval.SanitizeQueries(req.Message, []string{ // 合并原始 query + 补充 query，并做去重裁剪
		"source code architecture",
		"retrieval pipeline",
		strings.ToLower(effectiveMode),
	})
	// 读取最近会话历史（短期记忆），用于提升连续对话质量。
	history := s.loadSessionMemory(ctx, req.TenantID, req.SessionID)

	// 3) 获取检索材料：优先用 TS retrieve.ts（命中 + context），失败时回退占位结果
	searchResp, ragContext, retrievalStrategy := s.buildRAGMaterials(ctx, req.TenantID, req.RootDir, req.Message, rewritten, 8)
	// 这里的 searchResp/ragContext 是“预取”结果：
	// - 给普通 fallback prompt 直接用
	// - function-calling 成功时也能作为初始 evidence 参考/兜底
	optimizedHistory, memoryStats := s.compressHistoryByContextWindow(ctx, req, history, ragContext)

	// 4) 提取证据文件路径（供模型提示词和前端展示）
	evidence := make([]string, 0, len(searchResp.Hits))
	for _, h := range searchResp.Hits {
		evidence = append(evidence, h.RelPath)
	}

	// 5) 调模型生成回答；模型不可用时使用兜底文案
	answer := ""
	candidateHits := len(searchResp.Hits)
	modelErrors := make([]string, 0, 2)
	changedFiles := make([]string, 0, 2)
	verificationCommands := make([]string, 0, 2)
	effectiveRootDir := s.resolveWorkspaceRoot(req.RootDir)
	plan := buildTaskPlan(req, effectiveRootDir, evidence)
	if s.Model != nil {
		// 优先走真正的 OpenAI-compatible function-calling（工具内部调用 TS RAG）。
		if fcAnswer, fcEvidence, fcChangedFiles, fcVerificationCommands, fcHits, fcErr := s.chatWithFunctionCalling(ctx, req, rewritten, optimizedHistory, plan); fcErr == nil && strings.TrimSpace(fcAnswer) != "" {
			// function-calling 成功：用工具执行后返回的答案和 evidence 覆盖预取结果
			answer = fcAnswer
			if len(fcEvidence) > 0 {
				evidence = unique(fcEvidence)
			}
			if len(fcChangedFiles) > 0 {
				changedFiles = unique(fcChangedFiles)
			}
			if len(fcVerificationCommands) > 0 {
				verificationCommands = unique(fcVerificationCommands)
			}
			if fcHits > 0 {
				candidateHits = fcHits
			}
		} else {
			if fcErr != nil {
				modelErrors = append(modelErrors, "function-calling: "+fcErr.Error())
				log.Printf("chat function-calling failed: session=%s tenant=%s err=%v", req.SessionID, req.TenantID, fcErr)
			}
			// provider 不支持 tools 或 function-calling 失败时，回退到普通 prompt + 检索上下文模式。
			prompt := fmt.Sprintf(
				"Task route: %s\nTask plan: %v\nTarget files: %v\nTask: %s\nEvidence files: %v\nRetrieved context:\n%s\nReturn concise Chinese answer in Chinese. If context is insufficient, say what is missing.",
				plan.Route,
				plan.Steps,
				plan.TargetFiles,
				req.Message,
				evidence,
				strings.TrimSpace(ragContext),
			)
			fallbackMessages := make([]modelgateway.ChatMessage, 0, len(history)+2)
			fallbackMessages = append(fallbackMessages, modelgateway.ChatMessage{
				Role:    "system",
				Content: "You are a coding assistant. Answer in Chinese and cite relevant files from the provided context.",
			})
			fallbackMessages = append(fallbackMessages, optimizedHistory...)
			fallbackMessages = append(fallbackMessages, modelgateway.ChatMessage{Role: "user", Content: prompt})
			content, modelErr := s.Model.Chat(ctx, fallbackMessages, 0.2) // 调 LLM 生成回答
			if modelErr == nil {
				answer = content
			} else {
				modelErrors = append(modelErrors, "fallback-chat: "+modelErr.Error())
				log.Printf("chat fallback failed: session=%s tenant=%s err=%v", req.SessionID, req.TenantID, modelErr)
			}
		}
	}
	if answer == "" {
		if len(modelErrors) > 0 {
			answer = "模型调用失败。请展开 retrieval debug 查看 model_error，或检查 docker logs agent_server_app。"
		} else {
			answer = "当前为后端骨架实现：已完成检索、路由、任务队列接口。模型网关可用时会返回真实回答。"
		}
	}
	// 把本轮 user/assistant 写入会话短期记忆（失败不影响主流程）。
	s.appendSessionMemory(ctx, req.TenantID, req.SessionID, "user", req.Message)
	s.appendSessionMemory(ctx, req.TenantID, req.SessionID, "assistant", answer)

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
		ChangedFiles:  unique(changedFiles),
		RetrievalDebug: RetrievalMeta{
			Query:                req.Message,
			Rewritten:            rewritten,
			CandidateHits:        candidateHits,
			RetrievalStrategy:    retrievalStrategy,
			TaskRoute:            plan.Route,
			TaskPlan:             plan.Steps,
			TargetFiles:          plan.TargetFiles,
			RootDir:              strings.TrimSpace(req.RootDir),
			ModelContextTokens:   memoryStats.ModelContextTokens,
			HistoryBeforeTokens:  memoryStats.HistoryBefore,
			HistoryAfterTokens:   memoryStats.HistoryAfter,
			HistoryBudgetTokens:  memoryStats.HistoryBudget,
			HistoryCompressed:    memoryStats.Compressed,
			ModelError:           strings.Join(modelErrors, " | "),
			EffectiveRootDir:     effectiveRootDir,
			VerificationCommands: verificationCommands,
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
	if strings.TrimSpace(req.Query) == "" {
		return SearchResponse{}, errors.New("query is required")
	}
	if strings.TrimSpace(req.TenantID) == "" && strings.TrimSpace(req.RootDir) == "" {
		return SearchResponse{}, errors.New("tenant_id or root_dir is required")
	}
	if req.TopK <= 0 {
		req.TopK = 8
	}

	// 2) 检索缓存（按 tenant + topK + query）
	cacheKey := fmt.Sprintf("search:%s:%d:%s:%s", strings.TrimSpace(req.TenantID), req.TopK, strings.TrimSpace(req.Query), hashShort(strings.TrimSpace(req.RootDir)))
	if s.Cache != nil {
		var cached SearchResponse
		if ok, err := s.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && ok { // 命中缓存则直接返回
			return cached, nil
		}
	}

	// 3) 实际检索：优先走 TS retrieve.ts（本地扫目录 + BM25 融合），失败时回退占位命中
	resp, _, _ := s.buildRAGMaterials(ctx, req.TenantID, req.RootDir, req.Query, nil, req.TopK)
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

func hashShort(s string) string {
	// hashShort 生成短哈希，用于把长路径压缩进缓存 key。
	if s == "" {
		return "-"
	}
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:6])
}
