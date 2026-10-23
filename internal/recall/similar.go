package recall

import (
	"strings"
	"unicode"
)

// Threshold is the Jaccard index at or above which two texts are taken to name
// the same work: a query against an open ticket's title or body (a duplicate),
// a slice's goal against a shipped ticket's title (covered work).
const Threshold = 0.5

// stopwords are the function words left out of a text's word set: they carry
// no topic, and in a title of five words one of them moves the index by a
// fifth.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "but": true, "by": true, "for": true, "from": true, "has": true,
	"have": true, "if": true, "in": true, "into": true, "is": true, "it": true,
	"its": true, "of": true, "on": true, "or": true, "so": true, "than": true,
	"that": true, "the": true, "then": true, "this": true, "to": true, "was": true,
	"when": true, "which": true, "will": true, "with": true,
}

// words is the set of words of s: lowercase runs of letters and digits, two
// or more characters long, without the stopwords. A word is never stemmed:
// "fetch" and "fetches" are two words.
func words(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) < 2 || stopwords[w] {
			continue
		}
		set[w] = true
	}
	return set
}

// jaccard is the size of the intersection of a and b over the size of their
// union; two empty sets share nothing, so their index is 0.
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}

// Similarity is the Jaccard index of the word sets of a and b, from 0 (no
// word in common, or one of them has none) to 1 (the same words).
func Similarity(a, b string) float64 {
	return jaccard(words(a), words(b))
}

// shares counts the words of query that text also has.
func shares(query map[string]bool, text string) int {
	if len(query) == 0 {
		return 0
	}
	n := 0
	for w := range words(text) {
		if query[w] {
			n++
		}
	}
	return n
}

// round2 rounds a score to two decimals, so output is stable and short.
func round2(x float64) float64 {
	return float64(int(x*100+0.5)) / 100
}
