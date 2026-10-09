package verifydeliver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/outcome"
)

// TestRefusalReasonIsOneBoundedLine: a backend error can carry a whole stderr
// tail; the record keeps one line of it.
func TestRefusalReasonIsOneBoundedLine(t *testing.T) {
	t.Parallel()
	got := refusalReason(fmt.Errorf("the pick session failed:\n  exit 1\r\n\t%s", strings.Repeat("x", 1000)))
	if strings.ContainsAny(got, "\r\n\t") || strings.Contains(got, "  ") {
		t.Errorf("reason %q keeps line breaks or runs of whitespace", got)
	}
	if !strings.HasPrefix(got, "the pick session failed: exit 1 xxx") {
		t.Errorf("reason = %q", got[:40])
	}
	if want := refusalReasonCap + len("..."); len([]rune(got)) != want {
		t.Errorf("reason is %d runes, want it capped at %d", len([]rune(got)), want)
	}
	short := refusalReason(fmt.Errorf("short"))
	if short != "short" {
		t.Errorf("a short reason = %q, want it unchanged", short)
	}
}

// TestBackendFallback: it is the "outcome" field that says a result file is
// the backend's, not any wording, and not the file's other fields.
func TestBackendFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		data        string
		ok          bool
		wantOutcome string
		wantSummary string
	}{
		{"a slice result", `{"outcome":"failed","summary":"no result block; denied tool calls: Write x"}`, true, "failed", "no result block; denied tool calls: Write x"},
		{"a slice result with the tail the backends keep", `{"outcome":"failed","summary":"no result block","raw_tail":"..."}`, true, "failed", "no result block"},
		{"an outcome and nothing else", `{"outcome":"blocked"}`, true, "blocked", ""},
		{"an outcome that is not a string", `{"outcome":7,"summary":["x"]}`, true, "", ""},
		{"a pick result that also has an outcome", `{"flows":[],"summary":"s","outcome":"green"}`, true, "green", "s"},
		{"a pick result", `{"flows":[],"summary":"the outcome is fine"}`, false, "", ""},
		{"a pick result whose caption says outcome", `{"flows":[{"title":"t","items":[{"id":"r1","caption":"outcome"}]}],"summary":"s"}`, false, "", ""},
		{"a case variant is not the field", `{"Outcome":"failed"}`, false, "", ""},
		{"an outcome nested below the top", `{"flows":[],"summary":"s","x":{"outcome":"green"}}`, false, "", ""},
		{"not an object", `["outcome"]`, false, "", ""},
		{"not json", `outcome: failed`, false, "", ""},
		{"empty", ``, false, "", ""},
	}
	for _, c := range cases {
		outcome, summary, ok := backendFallback([]byte(c.data))
		if ok != c.ok || outcome != c.wantOutcome || summary != c.wantSummary {
			t.Errorf("%s: backendFallback(%s) = %q, %q, %v; want %q, %q, %v", c.name, c.data, outcome, summary, ok, c.wantOutcome, c.wantSummary, c.ok)
		}
	}
}

// TestBackendFallbackRecognizesWhatTheBackendsWrite: what headless and herdr
// write when a session wrote no result is the marshaled outcome.Result of its
// final message, whatever that message was, and it has the "outcome" field
// backendFallback looks for. If that shape changes, this fails before a pick
// stops recognizing it.
func TestBackendFallbackRecognizesWhatTheBackendsWrite(t *testing.T) {
	t.Parallel()
	finals := map[string]string{
		"no result block":        "I could not write the result file.",
		"no message at all":      "",
		"a green block":          "All done.\n```json\n{\"outcome\":\"green\",\"summary\":\"done\"}\n```",
		"a block that is a pick": "```json\n{\"flows\":[],\"summary\":\"s\"}\n```",
		"two blocks":             "```json\n{}\n```\n```json\n{}\n```",
	}
	for name, final := range finals {
		data, err := json.Marshal(outcome.ParseText("slice", final))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok := backendFallback(data); !ok {
			t.Errorf("%s: the backend's fallback %s is not recognized", name, data)
		}
	}
}
