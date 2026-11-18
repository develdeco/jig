// Package outcome defines the outcome vocabulary of a slice's result and the
// stall signature that tells a repeated non-success from progress.
package outcome

import (
	"strings"
)

// Outcome vocabulary (context "slice").
const (
	Green        = "green"
	CodeBug      = "code-bug"
	FlawedBrief  = "flawed-brief"
	OracleWrong  = "oracle-wrong"
	BlockedByEnv = "blocked-by-env"
	NeedsInput   = "needs-input"
	Failed       = "failed"
)

// Result is a slice attempt's result.
type Result struct {
	Outcome   string   `json:"outcome"`
	Summary   string   `json:"summary"`
	Commit    string   `json:"commit,omitempty"`
	Question  string   `json:"question,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
}

// Signature computes a stall-detection signature for a result: the same
// signature recurring means the session is patching without progress.
// gist = first 160 chars of summary, lowercased, with whitespace-tokens
// containing "/" or "\" (path segments) dropped, digits stripped, and
// whitespace collapsed and trimmed.
func Signature(context, outc, summary string) string {
	gist := summary
	if r := []rune(gist); len(r) > 160 {
		gist = string(r[:160])
	}
	gist = strings.ToLower(gist)

	tokens := strings.Fields(gist)
	kept := tokens[:0]
	for _, tok := range tokens {
		if strings.ContainsAny(tok, "/\\") {
			continue
		}
		kept = append(kept, tok)
	}
	gist = strings.Join(kept, " ")

	var b strings.Builder
	for _, ch := range gist {
		if ch >= '0' && ch <= '9' {
			continue
		}
		b.WriteRune(ch)
	}
	gist = strings.Join(strings.Fields(b.String()), " ")

	return context + "|" + outc + "|" + gist
}
