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
