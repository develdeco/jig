// Package outcome defines the typed outcome contract session results are
// parsed against, plus stall detection over repeated non-success results.
package outcome

import (
	"encoding/json"
	"fmt"
	"regexp"
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

// Contexts maps a parsing context to the outcomes valid within it.
var Contexts = map[string][]string{
	"slice": {Green, CodeBug, FlawedBrief, OracleWrong, BlockedByEnv, NeedsInput, Failed},
}

// Success reports which outcomes count as a successful result.
var Success = map[string]bool{Green: true}

// Result is a parsed session result.
type Result struct {
	Outcome   string   `json:"outcome"`
	Summary   string   `json:"summary"`
	Commit    string   `json:"commit,omitempty"`
	Question  string   `json:"question,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
	RawTail   string   `json:"-"`
}

// fencedJSON matches a single fenced ```json block, non-greedy up to the
// first closing fence.
var fencedJSON = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")

func validOutcome(context, outc string) bool {
	for _, v := range Contexts[context] {
		if v == outc {
			return true
		}
	}
	return false
}

// last1200 returns the last 1200 runes of s, or s itself when it is no
// longer than that.
func last1200(s string) string {
	if r := []rune(s); len(r) > 1200 {
		return string(r[len(r)-1200:])
	}
	return s
}

// ParseJSON parses a result.json body. Malformed JSON, or an outcome not
// valid for context, produces a Failed result explaining why; the parser
// never guesses. RawTail is always the last 1200 characters of data (same
// rule as ParseText), so a malformed or off-vocabulary result can still be
// diagnosed from the state/journal it produced.
func ParseJSON(context string, data []byte) Result {
	tail := last1200(string(data))
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return Result{Outcome: Failed, Summary: fmt.Sprintf("result was not valid JSON: %v", err), RawTail: tail}
	}
	if !validOutcome(context, res.Outcome) {
		return Result{Outcome: Failed, Summary: fmt.Sprintf("outcome %q is not valid for context %q", res.Outcome, context), RawTail: tail}
	}
	res.RawTail = tail
	return res
}

// ParseText extracts a session result from free-form text. Exactly one
// fenced ```json block is required: zero blocks or more than one both
// produce a Failed result with a distinct summary (a jig deviation from
// the predecessor's last-block-wins rule). RawTail is always the last 1200
// characters of text.
func ParseText(context string, text string) Result {
	tail := last1200(text)

	blocks := fencedJSON.FindAllStringSubmatch(text, -1)
	switch len(blocks) {
	case 0:
		return Result{Outcome: Failed, Summary: "no result block", RawTail: tail}
	default:
		if len(blocks) > 1 {
			return Result{Outcome: Failed, Summary: "multiple result blocks", RawTail: tail}
		}
		res := ParseJSON(context, []byte(blocks[0][1]))
		res.RawTail = tail
		return res
	}
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

// StallCounter counts repeated signatures across a run. Successes never
// count; a signature repeating at least twice is a stall.
type StallCounter struct {
	counts map[string]int
}

// Observe records one result's signature (ignored when success is true) and
// reports how many times that signature has now been seen and whether that
// count has reached the stall threshold (2).
func (c *StallCounter) Observe(sig string, success bool) (repeats int, stalled bool) {
	if success {
		return 0, false
	}
	if c.counts == nil {
		c.counts = make(map[string]int)
	}
	c.counts[sig]++
	repeats = c.counts[sig]
	stalled = repeats >= 2
	return repeats, stalled
}
