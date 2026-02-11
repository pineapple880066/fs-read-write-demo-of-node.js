package retrieval

import (
	"sort"
	"strings"
)

type Hit struct {
	ChunkID       string  `json:"chunk_id"`
	RelPath       string  `json:"rel_path"`
	Score         float64 `json:"score"`
	BM25Score     float64 `json:"bm25_score"`
	DenseScore    float64 `json:"dense_score"`
	QueryCoverage float64 `json:"query_coverage"`
	PathBoost     float64 `json:"path_boost"`
}

func Normalize(v, maxV float64) float64 {
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
	return 0.45*bm25Norm + 0.30*denseNorm + 0.15*queryCoverage + 0.10*pathBoost
}

func SanitizeQueries(userTask string, rewritten []string) []string {
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
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func SortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].BM25Score > hits[j].BM25Score
		}
		return hits[i].Score > hits[j].Score
	})
}
