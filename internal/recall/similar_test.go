package recall

import "testing"

func TestSimilarity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		{"the same words", "Add retry to the fetch client", "add retry fetch client", 1},
		{"case and punctuation do not count", "Fix: Clamp (upper bound)!", "fix clamp upper bound", 1},
		{"half the union", "retry fetch", "retry backoff", 1.0 / 3},
		{"nothing in common", "settings page", "fetch client", 0},
		{"stopwords and one-letter words are not words", "a to the of x", "a to the of x", 0},
		{"a word is not stemmed", "fetch", "fetches", 0},
		{"digits are words", "v2 migration", "migration v2", 1},
		{"one side without words", "", "retry", 0},
	}
	for _, c := range cases {
		if got := Similarity(c.a, c.b); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("%s: Similarity(%q, %q) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestSimilarityIsSymmetric(t *testing.T) {
	t.Parallel()
	a, b := "give the percentage caller a range check", "range check for the percentage caller"
	if x, y := Similarity(a, b), Similarity(b, a); x != y || x < Threshold {
		t.Errorf("Similarity both ways = %v and %v, want one value of at least %v", x, y, Threshold)
	}
}
