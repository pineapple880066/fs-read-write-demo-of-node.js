package service

// ChatRequest 对应 POST /v1/chat 的入参
type ChatRequest struct {
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	Mode      string `json:"mode"`
	RootDir   string `json:"root_dir,omitempty"`
}

// ChatResponse 对应 POST /v1/chat 的出参
type ChatResponse struct {
	Answer         string        `json:"answer"`
	EvidenceFiles  []string      `json:"evidence_files"`
	ChangedFiles   []string      `json:"changed_files,omitempty"`
	RetrievalDebug RetrievalMeta `json:"retrieval_debug"`
	TaskID         string        `json:"task_id,omitempty"`
}

// IngestRequest 对应 POST /v1/ingest 的入参
type IngestRequest struct {
	TenantID   string `json:"tenant_id"`
	SourceType string `json:"source_type"`
	SourceURI  string `json:"source_uri"`
	Text       string `json:"text,omitempty"`
}

// IngestResponse 返回异步任务 id 与初始状态
type IngestResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// SearchRequest 对应 POST /v1/search 的入参
type SearchRequest struct {
	TenantID string `json:"tenant_id"`
	Query    string `json:"query"`
	TopK     int    `json:"top_k"`
	RootDir  string `json:"root_dir,omitempty"`
}

// SearchHit 是检索结果中的单条命中（chunk 级别）
type SearchHit struct {
	ChunkID    string  `json:"chunk_id"`
	RelPath    string  `json:"rel_path"`
	Score      float64 `json:"score"`
	BM25Score  float64 `json:"bm25_score"`
	DenseScore float64 `json:"dense_score"`
}

// SearchResponse 对应 /search 的统一数据部分
type SearchResponse struct {
	Hits []SearchHit `json:"hits"`
}

// TaskResponse 对应 GET /v1/tasks/:id 的返回
type TaskResponse struct {
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	ErrorMessage string `json:"error_message,omitempty"`
	ResultRef    string `json:"result_ref,omitempty"`
}

// RetrievalMeta 用于 /chat 返回调试信息，便于观察检索行为
type RetrievalMeta struct {
	Query                     string   `json:"query"`
	Rewritten                 []string `json:"rewritten_queries"`
	CandidateHits             int      `json:"candidate_hits"`
	RetrievalStrategy         string   `json:"retrieval_strategy,omitempty"`
	TaskRoute                 string   `json:"task_route,omitempty"`
	TaskPlan                  []string `json:"task_plan,omitempty"`
	TargetFiles               []string `json:"target_files,omitempty"`
	RootDir                   string   `json:"root_dir,omitempty"`
	ModelContextTokens        int      `json:"model_context_tokens,omitempty"`
	ModelRequestBudgetSeconds int      `json:"model_request_budget_seconds,omitempty"`
	HistoryBeforeTokens       int      `json:"history_before_tokens,omitempty"`
	HistoryAfterTokens        int      `json:"history_after_tokens,omitempty"`
	HistoryBudgetTokens       int      `json:"history_budget_tokens,omitempty"`
	HistoryCompressed         bool     `json:"history_compressed,omitempty"`
	ModelError                string   `json:"model_error,omitempty"`
	EffectiveRootDir          string   `json:"effective_root_dir,omitempty"`
	VerificationCommands      []string `json:"verification_commands,omitempty"`
}
