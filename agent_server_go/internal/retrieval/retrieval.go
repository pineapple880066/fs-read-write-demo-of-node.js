package retrieval

import (
	"sort"
	"strings"
)

type Hit struct {
	// Hit 用于内部检索/融合排序场景，字段比 API SearchHit 更完整
	ChunkID       string  `json:"chunk_id"`
	RelPath       string  `json:"rel_path"`
	Score         float64 `json:"score"`
	BM25Score     float64 `json:"bm25_score"`
	DenseScore    float64 `json:"dense_score"`
	QueryCoverage float64 `json:"query_coverage"`
	PathBoost     float64 `json:"path_boost"`
}

func Normalize(v, maxV float64) float64 {
	// Normalize 将任意分数按 maxV 归一化到 [0,1]。
	// 把分数压缩到 [0,1]，便于不同来源分数融合
	if maxV <= 0 {
		return 0
	}
	n := v / maxV
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

// Final fusion score fixed by plan:
// final = 0.45*bm25_norm + 0.30*dense_norm + 0.15*query_coverage + 0.10*path_boost
func FuseScore(bm25Norm, denseNorm, queryCoverage, pathBoost float64) float64 {
	// FuseScore 按固定权重融合多路检索特征分数。
	// 融合公式权重与计划文档保持一致，便于对照验收
	return 0.45*bm25Norm + 0.30*denseNorm + 0.15*queryCoverage + 0.10*pathBoost
}

func SanitizeQueries(userTask string, rewritten []string) []string {
	// SanitizeQueries 合并/清洗 query，输出去重后的前 4 个检索词。
	// 合并用户原始 query 与改写 query，并做去重/裁剪
	merged := make([]string, 0, 6)
	merged = append(merged, strings.TrimSpace(userTask))
	for _, q := range rewritten {
		q = strings.TrimSpace(q)
		if q != "" {
			merged = append(merged, q)
		}
	}

	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, q := range merged {
		if q == "" {
			continue
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		out = append(out, q)
		// 控制 query 数量，避免检索成本失控
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func SortHits(hits []Hit) {
	// SortHits 按融合分（其次 BM25）对命中结果做稳定排序。
	// 先按融合分降序；分数相同再按 BM25 分排序，保证结果稳定
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].BM25Score > hits[j].BM25Score
		}
		return hits[i].Score > hits[j].Score
	})
}
