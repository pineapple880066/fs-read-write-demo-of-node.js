package retrieval

import (
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	bm25K1       = 1.2
	bm25B        = 0.75
	bm25EPS      = 1e-6
	denseVecDims = 256 // 本地“向量检索”简化版维度；后续可替换为真实 embedding + Milvus
)

const DenseVectorDims = denseVecDims

var (
	tokenSplitRe = regexp.MustCompile(`[^a-z0-9_\p{Han}]+`)
	fullPathRe   = regexp.MustCompile(`[a-z0-9_./-]+\.[a-z0-9]+`)
	segmentRe    = regexp.MustCompile(`[a-z0-9_]{2,}`)
)

// HybridDoc 是检索输入的最小文档结构（通常由 DB chunks 映射得到）。
type HybridDoc struct {
	ID      int64
	RelPath string
	Text    string
}

// HybridDocHit 是 Hybrid 检索输出（含 text，便于后续构建上下文）。
type HybridDocHit struct {
	ID            int64
	RelPath       string
	Text          string
	Score         float64
	BM25Score     float64
	DenseScore    float64
	QueryCoverage float64
	PathBoost     float64
}

// 复用 TS 版停用词集合，保持 BM25 行为尽量一致。
var defaultStopWords = map[string]struct{}{
	"的": {}, "了": {}, "和": {}, "是": {}, "在": {}, "我": {}, "要": {}, "把": {},
	"to": {}, "the": {}, "a": {}, "an": {}, "for": {}, "and": {}, "or": {}, "is": {}, "are": {},
}

type indexedHybridDoc struct {
	HybridDoc
	tokens []string
	vec    []float64 // 本地哈希向量（近似 dense），后续可替换为真实 embedding 向量
}

type hybridIndex struct {
	docs   []indexedHybridDoc
	df     map[string]int
	avgLen float64
	N      int
}

type pathHints struct {
	fullPaths map[string]struct{}
	segments  map[string]struct{}
}

type mergedHybridStats struct {
	doc          indexedHybridDoc
	rawBM25Max   float64
	normBM25Max  float64
	rawDenseMax  float64
	normDenseMax float64
	queryHits    int
	rankScore    float64
}

// HybridSearchLocalDocs 在本地内存中对 docs 执行 BM25 + 向量混合检索（简化实现）。
// 设计目标：
// 1) BM25 行为尽量贴近 TS 版本
// 2) 提供一个“可跑通”的向量分支用于 Hybrid 融合
// 3) 后续可替换 dense 分支为真实 embedding + Milvus
func HybridSearchLocalDocs(docs []HybridDoc, query string, queryVariants []string, topK int) []HybridDocHit {
	if len(docs) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 8
	}

	idx := buildHybridIndex(docs)
	queries := collectQueriesLocal(query, queryVariants)
	if len(queries) == 0 {
		return nil
	}
	hints := buildPathHintsLocal(queries)
	merged := make(map[int64]*mergedHybridStats, len(idx.docs))

	// recallK 取 topK 的若干倍，既能保留候选，又避免全量排序开销过大。
	recallK := topK * 5
	if recallK < 40 {
		recallK = 40
	}
	if recallK > len(idx.docs) {
		recallK = len(idx.docs)
	}

	for _, q := range queries {
		bm25Scores, maxBM25 := scoreBM25All(idx, q)
		denseScores, maxDense := scoreDenseAll(idx, q)

		type qcand struct {
			id    int64
			score float64
		}
		tmp := make([]qcand, 0, len(idx.docs))
		for _, d := range idx.docs {
			bm25Norm := Normalize(bm25Scores[d.ID], maxBM25)
			denseNorm := Normalize(denseScores[d.ID], maxDense)
			// 每个 query 内的候选排名使用“临时混合分”，仅用于 rankScore，不作为最终输出分。
			interim := 0.6*bm25Norm + 0.4*denseNorm
			if interim <= 0 {
				continue
			}
			tmp = append(tmp, qcand{id: d.ID, score: interim})
		}
		sort.SliceStable(tmp, func(i, j int) bool { return tmp[i].score > tmp[j].score })
		if len(tmp) > recallK {
			tmp = tmp[:recallK]
		}

		// 记录本 query 中进入候选列表的 doc 集合，用于 queryCoverage 统计。
		hitSet := make(map[int64]struct{}, len(tmp))
		for _, c := range tmp {
			hitSet[c.id] = struct{}{}
		}

		// 先把候选按 rank 信息合并
		for rank, c := range tmp {
			doc := idx.getDocByID(c.id)
			if doc == nil {
				continue
			}
			prev := merged[c.id]
			if prev == nil {
				prev = &mergedHybridStats{doc: *doc}
				merged[c.id] = prev
			}
			bm25Raw := bm25Scores[c.id]
			denseRaw := denseScores[c.id]
			prev.rawBM25Max = math.Max(prev.rawBM25Max, bm25Raw)
			prev.rawDenseMax = math.Max(prev.rawDenseMax, denseRaw)
			prev.normBM25Max = math.Max(prev.normBM25Max, Normalize(bm25Raw, maxBM25))
			prev.normDenseMax = math.Max(prev.normDenseMax, Normalize(denseRaw, maxDense))
			prev.rankScore += 1.0 / float64(rank+1)
		}

		// queryCoverage：一个 doc 在多少个 query 中“进入候选列表”
		for id := range hitSet {
			if prev := merged[id]; prev != nil {
				prev.queryHits++
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	totalQueries := float64(len(queries))
	out := make([]HybridDocHit, 0, len(merged))
	for _, m := range merged {
		pathBoost := calcPathBoostLocal(m.doc.RelPath, hints)
		queryCoverage := float64(m.queryHits) / totalQueries
		final := FuseScore(m.normBM25Max, m.normDenseMax, queryCoverage, pathBoost)
		out = append(out, HybridDocHit{
			ID:            m.doc.ID,
			RelPath:       m.doc.RelPath,
			Text:          m.doc.Text,
			Score:         round4(final),
			BM25Score:     round4(m.rawBM25Max),
			DenseScore:    round4(m.rawDenseMax),
			QueryCoverage: round4(queryCoverage),
			PathBoost:     round4(pathBoost),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].BM25Score == out[j].BM25Score {
				return out[i].DenseScore > out[j].DenseScore
			}
			return out[i].BM25Score > out[j].BM25Score
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

// BM25SearchLocalDocs 只保留 BM25 词法召回分支，供“BM25 + 外部 Milvus dense”融合使用。
func BM25SearchLocalDocs(docs []HybridDoc, query string, queryVariants []string, topK int) []HybridDocHit {
	if len(docs) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 8
	}

	idx := buildHybridIndex(docs)
	queries := collectQueriesLocal(query, queryVariants)
	if len(queries) == 0 {
		return nil
	}
	hints := buildPathHintsLocal(queries)
	merged := make(map[int64]*mergedHybridStats, len(idx.docs))

	recallK := topK * 5
	if recallK < 40 {
		recallK = 40
	}
	if recallK > len(idx.docs) {
		recallK = len(idx.docs)
	}

	for _, q := range queries {
		bm25Scores, maxBM25 := scoreBM25All(idx, q)

		type qcand struct {
			id    int64
			score float64
		}
		tmp := make([]qcand, 0, len(idx.docs))
		for _, d := range idx.docs {
			bm25Norm := Normalize(bm25Scores[d.ID], maxBM25)
			if bm25Norm <= 0 {
				continue
			}
			tmp = append(tmp, qcand{id: d.ID, score: bm25Norm})
		}
		sort.SliceStable(tmp, func(i, j int) bool { return tmp[i].score > tmp[j].score })
		if len(tmp) > recallK {
			tmp = tmp[:recallK]
		}

		hitSet := make(map[int64]struct{}, len(tmp))
		for _, c := range tmp {
			hitSet[c.id] = struct{}{}
		}

		for _, c := range tmp {
			doc := idx.getDocByID(c.id)
			if doc == nil {
				continue
			}
			prev := merged[c.id]
			if prev == nil {
				prev = &mergedHybridStats{doc: *doc}
				merged[c.id] = prev
			}
			bm25Raw := bm25Scores[c.id]
			prev.rawBM25Max = math.Max(prev.rawBM25Max, bm25Raw)
			prev.normBM25Max = math.Max(prev.normBM25Max, Normalize(bm25Raw, maxBM25))
		}
		for id := range hitSet {
			if prev := merged[id]; prev != nil {
				prev.queryHits++
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}

	totalQueries := float64(len(queries))
	out := make([]HybridDocHit, 0, len(merged))
	for _, m := range merged {
		pathBoost := calcPathBoostLocal(m.doc.RelPath, hints)
		queryCoverage := float64(m.queryHits) / totalQueries
		score := 0.75*m.normBM25Max + 0.15*queryCoverage + 0.10*pathBoost
		out = append(out, HybridDocHit{
			ID:            m.doc.ID,
			RelPath:       m.doc.RelPath,
			Text:          m.doc.Text,
			Score:         round4(score),
			BM25Score:     round4(m.rawBM25Max),
			DenseScore:    0,
			QueryCoverage: round4(queryCoverage),
			PathBoost:     round4(pathBoost),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].BM25Score > out[j].BM25Score
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

// BuildContextFromHybridHits 把命中列表打包成 prompt 可用上下文。
func BuildContextFromHybridHits(hits []HybridDocHit, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 4000
	}
	var b strings.Builder
	used := 0
	for _, h := range hits {
		header := "--- CHUNK: " + h.RelPath + "#" + strconvI64(h.ID) + " (score=" + formatScore(h.Score) + ") ---\n"
		if used+len(header) >= maxChars {
			break
		}
		text := h.Text
		remain := maxChars - used - len(header)
		if remain <= 0 {
			break
		}
		if len(text) > remain {
			text = text[:remain]
		}
		b.WriteString(header)
		b.WriteString(text)
		b.WriteString("\n\n")
		used += len(header) + len(text) + 2
	}
	return b.String()
}

func buildHybridIndex(docs []HybridDoc) hybridIndex {
	idx := hybridIndex{
		docs: make([]indexedHybridDoc, 0, len(docs)),
		df:   make(map[string]int),
	}
	totalLen := 0
	for _, d := range docs {
		tokens := tokenizeLocal(d.Text)
		v := vectorizeLocal(d.Text, tokens)
		dd := indexedHybridDoc{HybridDoc: d, tokens: tokens, vec: v}
		idx.docs = append(idx.docs, dd)
		totalLen += len(tokens)
		uniq := make(map[string]struct{}, len(tokens))
		for _, t := range tokens {
			uniq[t] = struct{}{}
		}
		for t := range uniq {
			idx.df[t]++
		}
	}
	idx.N = len(idx.docs)
	if idx.N > 0 {
		idx.avgLen = float64(totalLen) / float64(idx.N)
	}
	return idx
}

func (idx hybridIndex) getDocByID(id int64) *indexedHybridDoc {
	for i := range idx.docs {
		if idx.docs[i].ID == id {
			return &idx.docs[i]
		}
	}
	return nil
}

func tokenizeLocal(text string) []string {
	parts := tokenSplitRe.Split(strings.ToLower(text), -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || len([]rune(p)) <= 1 {
			continue
		}
		if _, ok := defaultStopWords[p]; ok {
			continue
		}
		out = append(out, p)
	}
	return out
}

func scoreBM25All(idx hybridIndex, query string) (map[int64]float64, float64) {
	qTokens := tokenizeLocal(query)
	scores := make(map[int64]float64, len(idx.docs))
	maxScore := 0.0
	for _, d := range idx.docs {
		tf := make(map[string]int, len(d.tokens))
		for _, t := range d.tokens {
			tf[t]++
		}
		score := 0.0
		for _, t := range qTokens {
			f := tf[t]
			if f == 0 {
				continue
			}
			df := idx.df[t]
			idf := math.Log(1 + (float64(idx.N-df)+0.5)/(float64(df)+0.5))
			denom := float64(f) + bm25K1*(1-bm25B+bm25B*(float64(len(d.tokens))/(idx.avgLen+bm25EPS)))
			score += idf * ((float64(f) * (bm25K1 + 1)) / (denom + bm25EPS))
		}
		scores[d.ID] = score
		if score > maxScore {
			maxScore = score
		}
	}
	return scores, maxScore
}

func scoreDenseAll(idx hybridIndex, query string) (map[int64]float64, float64) {
	qTokens := tokenizeLocal(query)
	qv := vectorizeLocal(query, qTokens)
	scores := make(map[int64]float64, len(idx.docs))
	maxScore := 0.0
	for _, d := range idx.docs {
		s := dot(qv, d.vec)
		// 归一化后理论上在 [0,1]（非负权重），这里再做一次防御式钳制
		if s < 0 {
			s = 0
		}
		if s > 1 {
			s = 1
		}
		scores[d.ID] = s
		if s > maxScore {
			maxScore = s
		}
	}
	return scores, maxScore
}

func vectorizeLocal(text string, tokens []string) []float64 {
	// vectorizeLocal 构造本地“哈希向量”（近似 dense）：
	// - token 特征（权重较高）
	// - 字符 bigram 特征（提升中文/无空格文本模糊匹配）
	vec := make([]float64, denseVecDims)
	for _, t := range tokens {
		addHashedFeature(vec, "tok:"+t, 1.0)
	}
	normalized := normalizeForBigram(text)
	runes := []rune(normalized)
	for i := 0; i+1 < len(runes); i++ {
		if !isIndexRune(runes[i]) || !isIndexRune(runes[i+1]) {
			continue
		}
		addHashedFeature(vec, "bg:"+string(runes[i:i+2]), 0.35)
	}
	l2Normalize(vec)
	return vec
}

// HashEmbedText 返回与本地 dense 检索同源的确定性哈希向量。
// 当前先把这组向量写入 Milvus，后续可平滑替换为真实 embedding provider。
func HashEmbedText(text string) []float32 {
	tokens := tokenizeLocal(text)
	vec64 := vectorizeLocal(text, tokens)
	out := make([]float32, len(vec64))
	for i, v := range vec64 {
		out[i] = float32(v)
	}
	return out
}

func normalizeForBigram(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func isIndexRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || (r >= 0x4e00 && r <= 0x9fff)
}

func addHashedFeature(vec []float64, key string, w float64) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := int(h.Sum32() % uint32(len(vec)))
	vec[idx] += w
}

func dot(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func l2Normalize(v []float64) {
	sum := 0.0
	for _, x := range v {
		sum += x * x
	}
	if sum <= 0 {
		return
	}
	n := math.Sqrt(sum)
	for i := range v {
		v[i] /= n
	}
}

func collectQueriesLocal(query string, queryVariants []string) []string {
	arr := make([]string, 0, 1+len(queryVariants))
	arr = append(arr, query)
	arr = append(arr, queryVariants...)
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	for _, q := range arr {
		q = strings.TrimSpace(q)
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

func buildPathHintsLocal(queries []string) pathHints {
	joined := strings.ToLower(strings.Join(queries, " "))
	full := fullPathRe.FindAllString(joined, -1)
	segs := segmentRe.FindAllString(joined, -1)
	h := pathHints{
		fullPaths: make(map[string]struct{}, len(full)),
		segments:  make(map[string]struct{}, len(segs)),
	}
	for _, x := range full {
		h.fullPaths[x] = struct{}{}
	}
	for _, x := range segs {
		h.segments[x] = struct{}{}
	}
	return h
}

func calcPathBoostLocal(relPath string, hints pathHints) float64 {
	p := normalizePathLocal(relPath)
	for full := range hints.fullPaths {
		if full != "" && strings.Contains(p, full) {
			return 1
		}
	}
	hit := 0
	for seg := range hints.segments {
		if len(seg) < 3 {
			continue
		}
		if strings.Contains(p, seg) {
			hit++
		}
	}
	if hit == 0 {
		return 0
	}
	if hit >= 3 {
		return 1
	}
	return float64(hit) / 3.0
}

func normalizePathLocal(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '/' || r == '-' {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func round4(n float64) float64 { return math.Round(n*10000) / 10000 }

func strconvI64(v int64) string { return strconvFormatInt(v) }

func formatScore(v float64) string { return strconvFormatFloat(v) }

// 下面这两个薄封装是为了避免在上面很多地方重复写 strconv 格式串。
func strconvFormatInt(v int64) string     { return strconv.FormatInt(v, 10) }
func strconvFormatFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
