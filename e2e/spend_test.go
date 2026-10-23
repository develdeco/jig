package e2e

// End-to-end tests of cost as records: the transcript of a harness session is
// summed into spend lines of the ticket journal at Stop and SessionEnd, a
// budget is a record of the ticket, and a ticket near or over it is reported
// in `jig status` and in what the Stop and prompt hooks tell, never stopped.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
)

// copyTranscript copies the fixture transcript (14500 tokens of session
// s-spend: 1600 input, 1000 output, 11000 cache read, 900 cache write) to a
// file of the test's own, named like the session as Claude Code names it.
func copyTranscript(t *testing.T, session string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), session+".jsonl")
	writeFileOrFatal(t, path, readFileOrFatal(t, filepath.Join("testdata", "transcripts", "budget.jsonl")))
	return path
}

// addUsage appends one assistant message of input tokens to a transcript.
func addUsage(t *testing.T, path, session, id string, input int64) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"assistant","sessionId":%q,"message":{"id":%q,"role":"assistant","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":%d,"output_tokens":0}}}`+"\n", session, id, input)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// spendLinesOf are the spend lines of a session in the ticket journal.
func spendLinesOf(t *testing.T, fx *fixture.Fixture, session string) []journal.Line {
	t.Helper()
	var out []journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventSpend && l.Session == session {
			out = append(out, l)
		}
	}
	return out
}

// unattendedMarker makes the project of the fixture default to unattended,
// with extra lines of jig.yaml after it.
func unattendedMarker(t *testing.T, fx *fixture.Fixture, extra string) {
	t.Helper()
	cfg, err := project.LoadStoreConfig(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(fx.RepoDir, project.MarkerFile), []byte("jig: \"1\"\nproject: "+cfg.Name+"\nunattended: true\n"+extra))
}

// TestBudgetOverInStatusAndStop: a ticket with a budget, a session whose
// transcript grows past it. Stop writes the cumulative spend line each time
// the total moved; the person is told at 85% and when over, the model at its
// next prompt, and `jig status` shows where the ticket stands. The project
// is unattended, and the spend notes still stop nothing.
func TestBudgetOverInStatusAndStop(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	T := fx.Ticket
	h := newHarness(t, fx, env, "s-spend")
	h.Branch("jig/" + T)
	h.Transcript = copyTranscript(t, "s-spend")
	unattendedMarker(t, fx, "")

	if r := runJig(t, env, fx.RepoDir, "budget", T, "--tokens", "20000", "--usd", "5"); r.Code != 0 {
		t.Fatalf("jig budget: exit %d\n%s", r.Code, r.Stdout)
	}
	if got := string(readFileOrFatal(t, filepath.Join(fx.StoreDir, "tickets", T, "budget.yaml"))); got != "tokens: 20000\nusd: 5\n" {
		t.Errorf("budget.yaml = %q", got)
	}
	if r := h.Hook("SessionStart", map[string]any{"source": "startup"}); r.Code != 0 {
		t.Fatalf("SessionStart: exit %d\n%s", r.Code, r.Stderr)
	}

	// 14500 of 20000 tokens: the spend is recorded and nobody is told.
	if r := h.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 || r.Stdout != "" {
		t.Fatalf("Stop at 72%% of the budget: exit %d, %q, want silence", r.Code, r.Stdout)
	}
	lines := spendLinesOf(t, fx, "s-spend")
	if len(lines) != 1 || lines[0].Harness != "claude" || lines[0].Provenance != "jig" || lines[0].Model != "claude-sonnet-4-5-20250929" ||
		lines[0].Spend == nil || lines[0].Spend.Input != 1600 || lines[0].Spend.Output != 1000 || lines[0].Spend.CacheRead != 11000 || lines[0].Spend.CacheWrite != 900 {
		t.Fatalf("spend lines = %+v, want one: 1600 input, 1000 output, 11000 cache read, 900 cache write (msg_1 is on two lines of the transcript and counts once)", lines)
	}
	if u := lines[0].Spend.USD; u < 0.0264 || u > 0.0266 {
		t.Errorf("spend usd = %v, want 0.026475 (sonnet 4.5 at list price)", u)
	}
	if r := h.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Stdout != "" || len(spendLinesOf(t, fx, "s-spend")) != 1 {
		t.Errorf("a second Stop over the same transcript wrote a line or spoke: %q", r.Stdout)
	}
	st := runJig(t, env, fx.RepoDir, "status", T)
	for _, want := range []string{"spend:\n  sessions: 1\n  tokens: 14500\n", "  usd: 0.03\n", "  budget_tokens: 20000\n  budget_usd: 5.00\n  budget: ok\n"} {
		if !strings.Contains(st.Stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, st.Stdout)
		}
	}

	// 17000: near. The person is told, and nothing refuses.
	addUsage(t, h.Transcript, "s-spend", "msg_3", 2500)
	near := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if !strings.Contains(near.SystemMessage, T+" has used 85% of its spend budget: 17k of 20k tokens, $0.03 of $5.00 (1 session).") || near.Decision != "" {
		t.Errorf("Stop at 85%%: %+v, want the person told and no refusal", near)
	}

	// 25000: over. The project is unattended, and spend is advice: no block.
	addUsage(t, h.Transcript, "s-spend", "msg_4", 8000)
	over := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if !strings.Contains(over.SystemMessage, T+" is over its spend budget: 25k of 20k tokens, $0.06 of $5.00 (1 session).") ||
		!strings.Contains(over.SystemMessage, "Jig only reports spend; it stops nothing.") || over.Decision != "" || over.Reason != "" {
		t.Errorf("Stop over the budget: %+v, want the person told and no refusal, though the project is unattended", over)
	}
	lines = spendLinesOf(t, fx, "s-spend")
	if len(lines) != 3 || lines[1].Spend.Input != 1600+2500 || lines[2].Spend.Input != 1600+2500+8000 {
		t.Fatalf("spend lines = %+v, want three cumulative ones", lines)
	}
	if st = runJig(t, env, fx.RepoDir, "status", T); !strings.Contains(st.Stdout, "  tokens: 25000\n") || !strings.Contains(st.Stdout, "  budget: over\n") {
		t.Errorf("status over the budget:\n%s", st.Stdout)
	}

	// The model hears of it at its next prompt, once; a Stop after that has
	// nothing left to say.
	prompt := decodeHook(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "carry on"}))
	if !strings.Contains(prompt.Specific.Context, T+" is over its spend budget: 25k of 20k tokens") {
		t.Errorf("prompt context = %q, want the over-budget note", prompt.Specific.Context)
	}
	if r := h.Hook("UserPromptSubmit", map[string]any{"prompt": "and again"}); r.Stdout != "" {
		t.Errorf("a second prompt = %q, want silence: the session was told", r.Stdout)
	}
	if r := h.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 || r.Stdout != "" {
		t.Errorf("Stop after the model heard = exit %d, %q, want silence", r.Code, r.Stdout)
	}

	// The session end writes what the last turn spent after the last Stop.
	addUsage(t, h.Transcript, "s-spend", "msg_5", 100)
	if r := h.Hook("SessionEnd", map[string]any{"reason": "other"}); r.Code != 0 || r.Stdout != "" {
		t.Errorf("SessionEnd = exit %d, %q, want silence", r.Code, r.Stdout)
	}
	if lines = spendLinesOf(t, fx, "s-spend"); len(lines) != 4 || lines[3].Spend.Input != 1600+2500+8000+100 {
		t.Fatalf("spend lines = %+v, want the session end to write a fourth, at 12200 input", lines)
	}

	// Another session on the ticket adds to it: the ticket spend is the latest
	// line of each, and the earlier lines of s-spend are only history.
	h2 := newHarness(t, fx, env, "s-other")
	h2.Transcript = filepath.Join(t.TempDir(), "s-other.jsonl")
	addUsage(t, h2.Transcript, "s-other", "msg_o1", 1000)
	if r := h2.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("Stop of the second session: exit %d\n%s", r.Code, r.Stderr)
	}
	st = runJig(t, env, fx.RepoDir, "status", T)
	if !strings.Contains(st.Stdout, "spend:\n  sessions: 2\n  tokens: 26100\n  input: 13200\n") || !strings.Contains(st.Stdout, "  budget: over\n") {
		t.Errorf("status with two sessions:\n%s", st.Stdout)
	}
	if shown := runJig(t, env, fx.RepoDir, "budget", T); !strings.Contains(shown.Stdout, "tokens: 20000\n") || !strings.Contains(shown.Stdout, "sessions: 2\n") {
		t.Errorf("jig budget shows:\n%s", shown.Stdout)
	}
}

// TestBudgetDefaultsFromTheProjectMarker: a project whose jig.yaml carries a
// budget gives it to a ticket at the ticket first recorded spend, so status
// and the Stop context read one budget record without anybody running `jig
// budget`.
func TestBudgetDefaultsFromTheProjectMarker(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	T := fx.Ticket
	h := newHarness(t, fx, env, "s-spend")
	h.Branch("jig/" + T)
	h.Transcript = copyTranscript(t, "s-spend")
	unattendedMarker(t, fx, "budget:\n  tokens: 10000\n")

	if shown := runJig(t, env, fx.RepoDir, "budget", T); shown.Code != 0 || !strings.Contains(shown.Stdout, "tokens: 10000\n") || !strings.Contains(shown.Stdout, "project default (not yet recorded on the ticket)") {
		t.Fatalf("jig budget before any spend:\n%s", shown.Stdout)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", T, "budget.yaml")); !os.IsNotExist(err) {
		t.Fatalf("budget.yaml exists before any spend: %v", err)
	}

	over := decodeHook(t, h.Hook("Stop", map[string]any{"stop_hook_active": false}))
	if !strings.Contains(over.SystemMessage, T+" is over its spend budget: 14.5k of 10k tokens (1 session).") || over.Decision != "" {
		t.Errorf("Stop with 14500 tokens under a default of 10000: %+v", over)
	}
	if got := string(readFileOrFatal(t, filepath.Join(fx.StoreDir, "tickets", T, "budget.yaml"))); got != "tokens: 10000\n" {
		t.Errorf("budget.yaml = %q, want the project default recorded on the ticket", got)
	}
	if st := runJig(t, env, fx.RepoDir, "status", T); !strings.Contains(st.Stdout, "  budget_tokens: 10000\n  budget: over\n") {
		t.Errorf("status:\n%s", st.Stdout)
	}
}
