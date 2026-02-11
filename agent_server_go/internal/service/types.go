package service

type ChatRequest struct {
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	Mode      string `json:"mode"`
}

type ChatResponse struct {
	Answer         string        `json:"answer"`
	EvidenceFiles  []string      `json:"evidence_files"`
	RetrievalDebug RetrievalMeta `json:"retrieval_debug"`
	TaskID         string        `json:"task_id,omitempty"`
}

type IngestRequest struct {
	TenantID   string `json:"tenant_id"`
	SourceType string `json:"source_type"`
	SourceURI  string `json:"source_uri"`
	Text       string `json:"text,omitempty"`
}

type IngestResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

type SearchRequest struct {
	TenantID string `json:"tenant_id"`
	Query    string `json:"query"`
	TopK     int    `json:"top_k"`
}

type SearchHit struct {
	ChunkID    string  `json:"chunk_id"`
	RelPath    string  `json:"rel_path"`
	Score      float64 `json:"score"`
	BM25Score  float64 `json:"bm25_score"`
	DenseScore float64 `json:"dense_score"`
}

type SearchResponse struct {
	Hits []SearchHit `json:"hits"`
}

type TaskResponse struct {
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	ErrorMessage string `json:"error_message,omitempty"`
	ResultRef    string `json:"result_ref,omitempty"`
}

type RetrievalMeta struct {
	Query         string   `json:"query"`
	Rewritten     []string `json:"rewritten_queries"`
	CandidateHits int      `json:"candidate_hits"`
}
