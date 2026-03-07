package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// TSRAGHit 对应 TypeScript retrieve.ts 返回的命中结构（含 chunk 文本）。
// Go 的 /search API 不直接返回 text，但 /chat 组 prompt 会用到。
type TSRAGHit struct {
	ID            int     `json:"id"`
	RelPath       string  `json:"relPath"`
	Text          string  `json:"text"`
	Score         float64 `json:"score"`
	BM25Score     float64 `json:"bm25Score"`
	QueryCoverage float64 `json:"queryCoverage"`
	PathBoost     float64 `json:"pathBoost"`
}

// TSRAGResult 是桥接脚本返回的最小结果：命中 + 已打包上下文。
type TSRAGResult struct {
	Hits      []TSRAGHit `json:"hits"`
	Context   string     `json:"context"`
	FileCount int        `json:"file_count"`
}

// TSBridgeConfig 描述“Go 调用 TS RAG”的运行配置。
type TSBridgeConfig struct {
	// NodeBin 是 node 可执行程序名/路径，默认可用 "node"。
	NodeBin string
	// ScriptPath 指向 agent_server_go/scripts/ts_rag_bridge.mjs。
	ScriptPath string
	// AgentDistDir 指向用户已有的 TS 编译产物目录（通常 ../agent/dist）。
	AgentDistDir string
	// TargetRootDir 是要检索的代码目录（当前 /search 无 path 入参，因此用全局配置）。
	TargetRootDir string
}

// TSBridge 封装 Node 子进程调用，复用用户既有 TypeScript 检索实现。
type TSBridge struct {
	cfg TSBridgeConfig
}

func NewTSBridge(cfg TSBridgeConfig) *TSBridge {
	// NewTSBridge 仅保存配置；真正可用性在 Call 时判断（便于服务降级回退）。
	return &TSBridge{cfg: cfg}
}

func (b *TSBridge) Enabled() bool {
	// Enabled 用于快速判断桥接配置是否齐全；未齐全时业务层可回退占位检索。
	if b == nil {
		return false
	}
	return strings.TrimSpace(b.cfg.NodeBin) != "" &&
		strings.TrimSpace(b.cfg.ScriptPath) != "" &&
		strings.TrimSpace(b.cfg.AgentDistDir) != "" &&
		strings.TrimSpace(b.cfg.TargetRootDir) != ""
}

// DefaultRootDir 返回桥接层配置的默认扫描根目录（通常是容器内的 /workspace）。
func (b *TSBridge) DefaultRootDir() string {
	if b == nil {
		return ""
	}
	return strings.TrimSpace(b.cfg.TargetRootDir)
}

// BuildRAG 调用 Node 桥接脚本，直接复用 TS buildRagData（检索 + 上下文拼接）。
func (b *TSBridge) BuildRAG(ctx context.Context, query string, queryVariants []string, topK int) (TSRAGResult, error) {
	return b.BuildRAGWithRoot(ctx, b.cfg.TargetRootDir, query, queryVariants, topK)
}

// BuildRAGWithRoot 与 BuildRAG 相同，但允许调用方按请求动态覆盖 targetRootDir。
func (b *TSBridge) BuildRAGWithRoot(ctx context.Context, targetRootDir string, query string, queryVariants []string, topK int) (TSRAGResult, error) {
	if !b.Enabled() {
		return TSRAGResult{}, fmt.Errorf("ts rag bridge not configured")
	}
	if strings.TrimSpace(query) == "" {
		return TSRAGResult{}, fmt.Errorf("empty query")
	}
	targetRootDir = strings.TrimSpace(targetRootDir)
	if targetRootDir == "" {
		targetRootDir = b.cfg.TargetRootDir
	}
	if targetRootDir == "" {
		return TSRAGResult{}, fmt.Errorf("empty target root dir")
	}
	if topK <= 0 {
		topK = 8
	}

	in := map[string]any{
		"agentDistDir":  b.cfg.AgentDistDir,
		"targetRootDir": targetRootDir,
		"query":         query,
		"queryVariants": queryVariants,
		"topK":          topK,
	}
	body, err := json.Marshal(in)
	if err != nil {
		return TSRAGResult{}, err
	}

	// 使用 CommandContext：HTTP 请求取消/超时时会连带终止子进程，避免僵尸任务。
	cmd := exec.CommandContext(ctx, b.cfg.NodeBin, b.cfg.ScriptPath)
	cmd.Stdin = bytes.NewReader(body)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return TSRAGResult{}, fmt.Errorf("ts rag bridge failed: %s", msg)
	}

	var out TSRAGResult
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return TSRAGResult{}, fmt.Errorf("parse ts rag output failed: %w", err)
	}
	return out, nil
}
