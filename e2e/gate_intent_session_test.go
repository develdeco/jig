package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/ledger"
)

// ticketSessionWords is what the ticket's own session says in its transcript,
// so that an excerpt holding it was made from that session and no other.
const ticketSessionWords = "TICKET-SESSION-WORDS-7d41e2"

// writeTicketTranscript writes a minimal Claude Code transcript at path, where
// the caller says: the user turn says text, and the assistant turn mentions
// every file in mentioned.
func writeTicketTranscript(t *testing.T, path, cwd, text string, mentioned ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	cwdJSON, _ := json.Marshal(cwd)
	userJSON, _ := json.Marshal(text)
	assistantJSON, _ := json.Marshal("I edited " + strings.Join(mentioned, ", ") + ".")
	lines := []string{
		`{"type":"user","cwd":` + string(cwdJSON) + `,"timestamp":"2026-01-01T00:10:00.000Z","message":{"role":"user","content":` + string(userJSON) + `}}`,
		`{"type":"assistant","cwd":` + string(cwdJSON) + `,"timestamp":"2026-01-01T00:20:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":` + string(assistantJSON) + `}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGateIntentFromTheTicketsSession: a brief-less ticket is gated with two
// sessions in the machine ledger. One built the ticket, and its transcript,
// which sits where no search of the local transcripts would look, states the
// intent. The other is unrelated to the ticket, and its transcript, which that
// search would pick, mentions the very files the round changed. The excerpt
// the reviewer is pointed at is the ticket's session's: the ledger knows which
// session holds the ticket, so jig does not guess by file overlap.
//
// A ticket whose sessions recorded no transcript is the other case, and
// TestGatePointsTheReviewerAtTheIntentExcerpt is its test: the builder there
// ran no hook, the ledger has no transcript for it, and the search finds the
// excerpt as before.
func TestGateIntentFromTheTicketsSession(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "inferred-intent"})
	ticket := fx.Ticket
	if err := fixture.RemoveBrief(fx.StoreDir, ticket); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}

	// The unrelated session works in the same clone, on the default branch, so
	// no ticket is its own. Its transcript is in the place Claude Code keeps
	// them and mentions the files of the round.
	mentioned := []string{"alpha/alpha.go", "alpha/percent.go", "alpha/percent_test.go", "beta/beta.go", "beta/version.go"}
	unrelatedTranscript := filepath.Join(env.userHome, ".claude", "projects", "proj1", "session-unrelated.jsonl")
	writeSyntheticClaudeSession(t, env.userHome, "session-unrelated", fx.RepoDir, mentioned, time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC))
	unrelated := newHarness(t, fx, env, "session-unrelated")
	unrelated.Transcript = unrelatedTranscript
	unrelated.Hook("SessionStart", map[string]any{"source": "startup", "model": "claude-test"})
	unrelated.Hook("PostToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": filepath.Join(fx.RepoDir, "alpha", "alpha.go")}})

	// The builder builds the ticket on its branch, and its session starts there
	// with a transcript outside every .claude/projects directory.
	built := buildTicketOn(t, fx, env, true)
	builder := built.builder
	writeTicketTranscript(t, builder.Transcript, fx.RepoDir, "Please clamp percentages and make the greeting casual. "+ticketSessionWords, mentioned...)
	builder.Hook("SessionStart", map[string]any{"source": "startup", "model": "claude-test"})

	// Both sessions are in the ledger with the transcript their hook reported;
	// only the builder's is on the ticket.
	sessions, err := ledger.Sessions(env.home)
	if err != nil {
		t.Fatal(err)
	}
	if s := sessions[builder.Session]; s.Transcript != builder.Transcript || s.Ticket != ticket {
		t.Fatalf("the builder's session = %+v, want its transcript %s on %s", s, builder.Transcript, ticket)
	}
	if s := sessions["session-unrelated"]; s.Transcript != unrelatedTranscript || s.Ticket == ticket {
		t.Fatalf("the unrelated session = %+v, want its transcript %s and not %s", s, unrelatedTranscript, ticket)
	}

	gate := runJig(t, env, fx.StoreDir, "gate", ticket)
	if gate.Code != 0 {
		t.Fatalf("gate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	wantExcerpt := filepath.Join(home.IntentExcerptDir(env.home), ticket, builder.Session+".md")
	if !strings.Contains(gate.Stdout, "intent: none") || !strings.Contains(gate.Stdout, wantExcerpt) {
		t.Fatalf("gate stdout missing the intent row (none) and the excerpt %s:\n%s", wantExcerpt, gate.Stdout)
	}

	// The reviewer reads the request, which points at the ticket's session's
	// excerpt: what the author said, and nothing of the unrelated session.
	ctx := runJig(t, env, fx.StoreDir, "review", "context", ticket, "--json")
	if ctx.Code != 0 {
		t.Fatalf("review context exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	var request struct {
		IntentExcerpt string `json:"intent_excerpt"`
	}
	if err := json.Unmarshal([]byte(ctx.Stdout), &request); err != nil {
		t.Fatalf("parse review context --json: %v\n%s", err, ctx.Stdout)
	}
	if request.IntentExcerpt != wantExcerpt {
		t.Fatalf("intent_excerpt = %q, want %q (the builder's session)", request.IntentExcerpt, wantExcerpt)
	}
	excerpt, err := os.ReadFile(wantExcerpt)
	if err != nil {
		t.Fatalf("read the excerpt: %v", err)
	}
	if !strings.Contains(string(excerpt), ticketSessionWords) || strings.Contains(string(excerpt), intentExcerptMarker) {
		t.Fatalf("the excerpt should hold the ticket session's words %q and not the unrelated session's marker %q:\n%s", ticketSessionWords, intentExcerptMarker, excerpt)
	}
	if _, err := os.Stat(filepath.Join(home.IntentExcerptDir(env.home), ticket, "session-unrelated.md")); err == nil {
		t.Error("the unrelated session was excerpted too")
	}
}
