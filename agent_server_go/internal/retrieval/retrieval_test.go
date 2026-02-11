package retrieval

import "testing"

func TestFuseScore(t *testing.T) {
	s := FuseScore(0.8, 0.5, 1.0, 0.2)
	want := 0.45*0.8 + 0.30*0.5 + 0.15*1.0 + 0.10*0.2
	if s != want {
		t.Fatalf("unexpected score: got=%f want=%f", s, want)
	}
}

func TestSanitizeQueries(t *testing.T) {
	q := SanitizeQueries("  hello  ", []string{"", "hello", "foo", "bar", "baz", "qux"})
	if len(q) != 4 {
		t.Fatalf("expected 4 queries, got %d", len(q))
	}
	if q[0] != "hello" {
		t.Fatalf("unexpected first query: %s", q[0])
	}
}
