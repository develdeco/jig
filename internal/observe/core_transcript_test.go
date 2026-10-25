package observe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// claudePayload is a Claude Code hook payload of the session s1 in the rig's
// clone, with the transcript path the harness reports.
func claudePayload(t *testing.T, r *rig, hookEvent, transcript string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"session_id":      "s1",
		"transcript_path": transcript,
		"cwd":             r.fx.RepoDir,
		"hook_event_name": hookEvent,
		"source":          "startup",
		"model":           "claude-x",
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ledgerText is the ledger file under the rig's jig home, as written.
func (r *rig) ledgerText() string {
	r.t.Helper()
	data, err := os.ReadFile(home.LedgerPath(r.fx.Home))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(data)
}

// TestClaudeSessionStartCarriesTheTranscriptPathIntoTheLedger: the path in
// Claude Code's payload (transcript_path) is on the event the adapter makes
// and on the session-start line the core writes, and the ledger's fold gives
// it to the session, so a reader of the ledger can find what the session was
// asked.
func TestClaudeSessionStartCarriesTheTranscriptPathIntoTheLedger(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	transcript := filepath.Join(r.fx.Dir, "transcripts", "s1.jsonl")
	adapter, ok := AdapterFor("claude")
	if !ok {
		t.Fatal("no claude adapter")
	}
	ev, err := adapter.Parse(claudePayload(t, r, "SessionStart", transcript))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	r.handle(ev)

	got, err := ledger.Sessions(r.fx.Home)
	if err != nil {
		t.Fatal(err)
	}
	if s := got["s1"]; s.Transcript != transcript || s.Ticket != r.fx.Ticket || s.Harness != "claude" {
		t.Fatalf("session s1 = %+v, want the transcript %s, the ticket %s and the harness claude", s, transcript, r.fx.Ticket)
	}
	starts := r.ledgerOf(journal.EventSessionStart)
	if len(starts) != 1 {
		t.Fatalf("session-start lines = %+v, want one", starts)
	}
	wantKey, _ := json.Marshal(transcript)
	if text := r.ledgerText(); !strings.Contains(text, `"transcript":`+string(wantKey)) {
		t.Errorf("the ledger holds no transcript key with the path %s:\n%s", transcript, text)
	}
}

// TestASessionWhoseHarnessReportsNoTranscriptRecordsNone: the session id is
// what every harness gives and the path only some do, so a start with no path
// writes no transcript key and the session has none.
func TestASessionWhoseHarnessReportsNoTranscriptRecordsNone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))

	got, err := ledger.Sessions(r.fx.Home)
	if err != nil || got["s1"].Ticket != r.fx.Ticket || got["s1"].Transcript != "" {
		t.Fatalf("Sessions = %+v, %v, want s1 on %s with no transcript", got["s1"], err, r.fx.Ticket)
	}
	if text := r.ledgerText(); strings.Contains(text, `"transcript"`) {
		t.Errorf("a harness that reported no path left a transcript key in the ledger:\n%s", text)
	}
}

// TestTheFirstTouchOfASessionJigDidNotSeeStartCarriesItsTranscript: hooks
// installed after a session began, or a session that began outside a project,
// have no start line with the path; the touch that first puts the session on
// a ticket records it, from the event's own transcript_path.
func TestTheFirstTouchOfASessionJigDidNotSeeStartCarriesItsTranscript(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	const transcript = "/t/s1.jsonl"
	r.handle(r.ev(KindTool, func(e *Event) {
		e.Tool, e.Command, e.Transcript = "Bash", "jig claim JIG-1 --slice a", transcript
	}))

	touches := r.ledgerOf(journal.EventSessionTouch)
	if len(touches) != 1 {
		t.Fatalf("session-touch lines = %+v, want one", touches)
	}
	got, err := ledger.Sessions(r.fx.Home)
	if err != nil || got["s1"].Transcript != transcript || got["s1"].Ticket != r.fx.Ticket {
		t.Fatalf("Sessions = %+v, %v, want s1 on %s with the transcript %s", got["s1"], err, r.fx.Ticket, transcript)
	}
}

// TestASubagentsTranscriptIsNotTheSessions: a hook fired by a subagent may
// name a transcript of the subagent's own; the session's record keeps the
// path its own agent reported.
func TestASubagentsTranscriptIsNotTheSessions(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindTool, func(e *Event) {
		e.Tool, e.Command, e.Agent, e.Transcript = "Bash", "go test", "a-7", "/t/agent-a-7.jsonl"
	}))
	if got, err := ledger.Sessions(r.fx.Home); err != nil || got["s1"].Transcript != "" {
		t.Fatalf("Sessions = %+v, %v, want no transcript from a subagent's event", got["s1"], err)
	}

	r.handle(r.ev(KindSessionStart, func(e *Event) { e.Transcript = "/t/s1.jsonl" }))
	if got, err := ledger.Sessions(r.fx.Home); err != nil || got["s1"].Transcript != "/t/s1.jsonl" {
		t.Fatalf("Sessions = %+v, %v, want the transcript the session's own start reported", got["s1"], err)
	}
}
