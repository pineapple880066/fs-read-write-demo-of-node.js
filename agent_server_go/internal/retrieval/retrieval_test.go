package retrieval

import "testing"

func TestFuseScore(t *testing.T) {
	// TestFuseScore 校验融合公式实现正确。
	// 验证融合公式实现与预期数学表达式一致
	s := FuseScore(0.8, 0.5, 1.0, 0.2)
	want := 0.45*0.8 + 0.30*0.5 + 0.15*1.0 + 0.10*0.2
	if s != want {
		t.Fatalf("unexpected score: got=%f want=%f", s, want)
	}
}

func TestSanitizeQueries(t *testing.T) {
	// TestSanitizeQueries 校验 query 清洗规则（过滤/去重/裁剪）。
	// 验证：空串过滤、去重、最多保留 4 个 query
	q := SanitizeQueries("  hello  ", []string{"", "hello", "foo", "bar", "baz", "qux"})
	if len(q) != 4 {
		t.Fatalf("expected 4 queries, got %d", len(q))
	}
	if q[0] != "hello" {
		t.Fatalf("unexpected first query: %s", q[0])
	}
}

func TestHybridSearchLocalDocs(t *testing.T) {
	// TestHybridSearchLocalDocs 验证本地 Hybrid 检索至少能命中相关文本，并产出 BM25/dense 分数。
	docs := []HybridDoc{
		{ID: 1, RelPath: "a.txt", Text: "RabbitMQ task queue consumer ack and worker"},
		{ID: 2, RelPath: "b.txt", Text: "Vector retrieval and cosine similarity hybrid search"},
		{ID: 3, RelPath: "c.txt", Text: "plain unrelated content"},
	}
	hits := HybridSearchLocalDocs(docs, "rabbitmq worker task", nil, 2)
	if len(hits) == 0 {
		t.Fatalf("expected hits")
	}
	if hits[0].ID != 1 {
		t.Fatalf("expected doc 1 ranked first, got %d", hits[0].ID)
	}
	if hits[0].BM25Score <= 0 {
		t.Fatalf("expected bm25 score > 0")
	}
	// dense 分支为本地哈希向量近似，不要求很高，但应该有非负值
	if hits[0].DenseScore < 0 {
		t.Fatalf("expected dense score >= 0")
	}
}
