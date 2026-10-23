package spend

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// asst is one transcript line of an assistant message, in the shape Claude
// Code writes it.
func asst(session, id, model string, in, out, cacheRead, cacheWrite, write1h int64) string {
	return fmt.Sprintf(`{"type":"assistant","sessionId":%q,"requestId":"req_%s","message":{"id":%q,"model":%q,"role":"assistant","usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		session, id, id, model, in, out, cacheRead, cacheWrite, cacheWrite-write1h, write1h)
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// TestFromTranscriptCountsEachMessageOnce: Claude Code writes a transcript
// line for each content block of a message, every one with the usage of the
// message, so the lines of a message are the spend of one message; a message
// whose usage grew while it streamed counts at its final figures.
func TestFromTranscriptCountsEachMessageOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s-1.jsonl")
	writeLines(t, path,
		asst("s-1", "m1", "claude-sonnet-4-5-20250929", 100, 50, 1000, 200, 50),
		`{"type":"user","sessionId":"s-1","message":{"role":"user","content":"not usage"}}`,
		asst("s-1", "m1", "claude-sonnet-4-5-20250929", 100, 50, 1000, 200, 50), // the second block of the same message
		asst("s-1", "m1", "claude-sonnet-4-5-20250929", 100, 50, 1000, 200, 50),
		asst("s-1", "m2", "claude-sonnet-4-5-20250929", 10, 1, 0, 0, 0),
		asst("s-1", "m2", "claude-sonnet-4-5-20250929", 10, 40, 0, 0, 0), // m2 streamed: output grew
	)
	got, err := FromTranscript(path, "s-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages != 2 || got.Input != 110 || got.Output != 90 || got.CacheRead != 1000 || got.CacheWrite != 200 {
		t.Errorf("totals = %+v, want 2 messages, input 110, output 90, cache read 1000, cache write 200", got)
	}
	if got.Tokens() != 110+90+1000+200 {
		t.Errorf("Tokens() = %d, want the four counts added", got.Tokens())
	}
}

// TestFromTranscriptLeavesOutAnotherSessionLines: the transcript of a resumed
// session begins with the lines of the session it came from, under the id of
// that session; they are the spend of that session, which its own spend line
// has.
func TestFromTranscriptLeavesOutAnotherSessionLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s-2.jsonl")
	writeLines(t, path,
		asst("s-1", "old", "claude-sonnet-4-5", 999999, 999999, 0, 0, 0),
		asst("s-2", "new", "claude-sonnet-4-5", 5, 6, 0, 0, 0),
		`{"type":"assistant","message":{"id":"nosession","model":"claude-sonnet-4-5","usage":{"input_tokens":1,"output_tokens":1}}}`,
	)
	got, err := FromTranscript(path, "s-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Input != 6 || got.Output != 7 {
		t.Errorf("totals = %+v, want only the lines of s-2: 5+1 input and 6+1 output (a line with no session id is counted)", got)
	}
	all, err := FromTranscript(path, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Input != 999999+5+1 {
		t.Errorf("with no session named every line counts: input = %d", all.Input)
	}
}

// TestFromTranscriptReadsSubagents: the usage of a subagent is the usage of
// the session, and lives in a transcript of its own beside the main one.
func TestFromTranscriptReadsSubagents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "s-1.jsonl")
	writeLines(t, path, asst("s-1", "main", "claude-sonnet-4-5", 10, 10, 0, 0, 0))
	writeLines(t, filepath.Join(dir, "s-1", "subagents", "agent-a.jsonl"), asst("s-1", "sub1", "claude-haiku-4-5", 1, 2, 0, 0, 0))
	writeLines(t, filepath.Join(dir, "s-1", "subagents", "agent-b.jsonl"), asst("s-1", "sub2", "claude-haiku-4-5", 3, 4, 0, 0, 0))
	writeLines(t, filepath.Join(dir, "s-1", "workflows", "w.jsonl"), asst("s-1", "wf", "claude-haiku-4-5", 1000, 1000, 0, 0, 0))
	got, err := FromTranscript(path, "s-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages != 3 || got.Input != 14 || got.Output != 16 {
		t.Errorf("totals = %+v, want the main transcript and both subagents (3 messages, 14 in, 16 out), nothing from workflows/", got)
	}
	if want := []string{"claude-haiku-4-5", "claude-sonnet-4-5"}; !slices.Equal(got.Models, want) {
		t.Errorf("models = %q, want %q", got.Models, want)
	}
}

// TestFromTranscriptPrices: dollars at list price, with the one hour cache
// priced apart; a costUSD of the line itself wins; a model with no price
// counts its tokens and none of its dollars, and says so.
func TestFromTranscriptPrices(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s-1.jsonl")
	writeLines(t, path,
		// sonnet 4.5: 3/15, cache write 3.75 (5m) and 6 (1h), cache read 0.30.
		asst("s-1", "a", "claude-sonnet-4-5-20250929", 100, 50, 1000, 200, 50),
		// opus 4.5: 5/25, cache write 6.25, read 0.5; no 1h part.
		asst("s-1", "b", "claude-opus-4-5-20251101", 1000, 2000, 4000, 500, 0),
		// no price known.
		asst("s-1", "c", "claude-fable-5", 7, 3, 0, 0, 0),
		`{"type":"assistant","sessionId":"s-1","costUSD":0.5,"message":{"id":"d","model":"claude-3-5-haiku-20241022","usage":{"input_tokens":20,"output_tokens":30}}}`,
	)
	got, err := FromTranscript(path, "s-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := (100*3+50*15+1000*0.30+150*3.75+50*6)/1e6 + (1000*5+2000*25+4000*0.5+500*6.25)/1e6 + 0.5
	if !near(got.USD, want) {
		t.Errorf("USD = %v, want %v (two priced messages and one with its own cost)", got.USD, want)
	}
	if want := []string{"claude-fable-5"}; !slices.Equal(got.Unpriced, want) {
		t.Errorf("unpriced = %q, want %q", got.Unpriced, want)
	}
	if got.Input != 100+1000+7+20 {
		t.Errorf("the tokens of the unpriced model are counted all the same: input = %d", got.Input)
	}

	// A price the jig home adds makes the model priced.
	prices := Prices{"claude-fable-5": {Input: 10, Output: 50, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 1}}
	priced, err := FromTranscript(path, "s-1", prices)
	if err != nil {
		t.Fatal(err)
	}
	if len(priced.Unpriced) != 0 || !near(priced.USD, want+(7*10+3*50)/1e6) {
		t.Errorf("with fable priced: USD = %v, unpriced = %q, want %v and none", priced.USD, priced.Unpriced, want+(7*10+3*50)/1e6)
	}
}

// TestFromTranscriptSkipsWhatIsNotUsage: lines that are not JSON, hold no
// usage, or are zero-usage messages written by Claude Code itself add nothing,
// and a very long line or a last line with no newline is no trouble.
func TestFromTranscriptSkipsWhatIsNotUsage(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s-1.jsonl")
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("x", 3<<20) + ` usage"}}`
	body := strings.Join([]string{
		`not json at all "usage"`,
		`{"type":"assistant","message":"a string, not a message","usage":1}`,
		`{"type":"assistant","message":{"id":"n","model":"claude-sonnet-4-5","usage":null}}`,
		asst("s-1", "syn", "<synthetic>", 0, 0, 0, 0, 0),
		huge,
		asst("s-1", "ok", "claude-sonnet-4-5", 1, 2, 3, 4, 0),
	}, "\n") // no newline after the last line
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FromTranscript(path, "s-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages != 1 || got.Input != 1 || got.Output != 2 || got.CacheRead != 3 || got.CacheWrite != 4 {
		t.Errorf("totals = %+v, want the one real message", got)
	}
	if want := []string{"claude-sonnet-4-5"}; !slices.Equal(got.Models, want) {
		t.Errorf("models = %q, want %q (not <synthetic>)", got.Models, want)
	}
}

// TestFromTranscriptAbsentIsNoSpend: a harness with no transcript, or one not
// written yet, has spent nothing that jig can see; it is not an error.
func TestFromTranscriptAbsentIsNoSpend(t *testing.T) {
	t.Parallel()
	for name, path := range map[string]string{"no path": "", "no file": filepath.Join(t.TempDir(), "gone.jsonl")} {
		got, err := FromTranscript(path, "s-1", nil)
		if err != nil || got.Messages != 0 || got.Tokens() != 0 || got.USD != 0 {
			t.Errorf("%s: totals = %+v, err = %v, want zero and no error", name, got, err)
		}
	}
	if _, err := FromTranscript(t.TempDir(), "s-1", nil); err == nil {
		t.Errorf("a directory where the transcript should be read as no error, want one")
	}
}
