package ledger

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// t0 is the instant the session tests count from; at(n) is n seconds after it.
var t0 = time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return t0.Add(time.Duration(seconds) * time.Second) }

// mustSessions folds the ledger under root or fails the test.
func mustSessions(t *testing.T, root string) map[string]Session {
	t.Helper()
	sessions, err := Sessions(root)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	return sessions
}

func mustAppend(t *testing.T, root string, l journal.Line) {
	t.Helper()
	if err := Append(root, l); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// TestMintID: an id is "s-" and twelve hex digits, and two never collide.
func TestMintID(t *testing.T) {
	t.Parallel()

	shape := regexp.MustCompile(`^s-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := MintID()
		if !shape.MatchString(id) {
			t.Fatalf("MintID() = %q, want s- and twelve hex digits", id)
		}
		if seen[id] {
			t.Fatalf("MintID() returned %q twice", id)
		}
		seen[id] = true
	}
}

// TestSessionLifecycleFolds walks one session through start, touch, end and a
// resume, and reads back what Sessions folds at each step.
func TestSessionLifecycleFolds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	if err := StartSession(root, at(0), Registration{
		ID: "s1", Harness: "claude", Agent: "lead", Cwd: "/work/app", Clone: "/work/app", Ticket: "JIG-1", Slice: "a", Model: "opus", Effort: "high",
	}); err != nil {
		t.Fatal(err)
	}
	want := Session{
		ID: "s1", Harness: "claude", Agents: []string{"lead"}, Cwd: "/work/app", Clone: "/work/app", Ticket: "JIG-1", Slice: "a",
		Model: "opus", Effort: "high", Started: Stamp(at(0)), LastSeen: Stamp(at(0)),
	}
	if got := mustSessions(t, root)["s1"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after start:\n got %+v\nwant %+v", got, want)
	}

	// A touch with only the id moves LastSeen and nothing else.
	if err := TouchSession(root, at(10), Registration{ID: "s1"}); err != nil {
		t.Fatal(err)
	}
	want.LastSeen = Stamp(at(10))
	if got := mustSessions(t, root)["s1"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("after a bare touch:\n got %+v\nwant %+v", got, want)
	}

	// A touch that names a ticket replaces the slice with its own, empty here;
	// one that names a slice alone replaces only the slice.
	if err := TouchSession(root, at(20), Registration{ID: "s1", Ticket: "JIG-2"}); err != nil {
		t.Fatal(err)
	}
	if got := mustSessions(t, root)["s1"]; got.Ticket != "JIG-2" || got.Slice != "" {
		t.Fatalf("after a touch naming JIG-2: ticket %q slice %q, want JIG-2 and no slice (the old slice belonged to JIG-1)", got.Ticket, got.Slice)
	}
	if err := TouchSession(root, at(30), Registration{ID: "s1", Slice: "b", Agent: "reviewer-7"}); err != nil {
		t.Fatal(err)
	}
	got := mustSessions(t, root)["s1"]
	if got.Ticket != "JIG-2" || got.Slice != "b" || got.LastSeen != Stamp(at(30)) {
		t.Fatalf("after a touch naming slice b: %+v, want ticket JIG-2, slice b, last seen at 30s", got)
	}
	if !reflect.DeepEqual(got.Agents, []string{"lead", "reviewer-7"}) {
		t.Fatalf("agents = %v, want lead then reviewer-7, each once", got.Agents)
	}

	if err := EndSession(root, at(40), "s1"); err != nil {
		t.Fatal(err)
	}
	got = mustSessions(t, root)["s1"]
	if got.Live() || got.Ended != Stamp(at(40)) || got.LastSeen != Stamp(at(40)) {
		t.Fatalf("after end: %+v, want ended and last seen at 40s", got)
	}

	// A touch after the end does not reopen it; a start does (a resume).
	if err := TouchSession(root, at(50), Registration{ID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if got = mustSessions(t, root)["s1"]; got.Live() || got.Ended != Stamp(at(40)) {
		t.Fatalf("after a touch following the end: %+v, want it still ended at 40s", got)
	}
	if err := StartSession(root, at(60), Registration{ID: "s1", Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	got = mustSessions(t, root)["s1"]
	if !got.Live() || got.Started != Stamp(at(0)) || got.Model != "sonnet" || got.Harness != "claude" {
		t.Fatalf("after a resume: %+v, want live, started at 0s, model replaced, harness kept", got)
	}
}

// TestSessionsFoldToolLines: tool lines give a session its last tool, its
// files and its agents, make a session of their own when no start was seen,
// and never change who the session is.
func TestSessionsFoldToolLines(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	if err := StartSession(root, at(0), Registration{ID: "s1", Cwd: "/work/app", Ticket: "JIG-1"}); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, root, journal.Line{TS: Stamp(at(1)), Event: journal.EventTool, Session: "s1", Tool: "Edit", Files: []string{"a.go", "b.go"}, Cwd: "/elsewhere", Ticket: "JIG-9"})
	mustAppend(t, root, journal.Line{TS: Stamp(at(2)), Event: journal.EventTool, Session: "s1", Agent: "sub-1", Tool: "Bash", Files: []string{"b.go", "c.go"}})
	mustAppend(t, root, journal.Line{TS: Stamp(at(3)), Event: journal.EventTool, Session: "s1"}) // names no tool

	// No start was seen for s2: its hooks were installed after it began.
	mustAppend(t, root, journal.Line{TS: Stamp(at(5)), Event: journal.EventTool, Session: "s2", Tool: "Bash"})

	sessions := mustSessions(t, root)
	s1 := sessions["s1"]
	if s1.LastTool != "Bash" || !reflect.DeepEqual(s1.Files, []string{"a.go", "b.go", "c.go"}) || !reflect.DeepEqual(s1.Agents, []string{"sub-1"}) {
		t.Errorf("s1 = %+v, want last tool Bash, files a b c once each, agent sub-1", s1)
	}
	if s1.Cwd != "/work/app" || s1.Ticket != "JIG-1" {
		t.Errorf("s1 cwd %q ticket %q: a tool line changed who the session is", s1.Cwd, s1.Ticket)
	}
	if s1.LastSeen != Stamp(at(3)) {
		t.Errorf("s1 last seen = %q, want the last tool line's stamp %q", s1.LastSeen, Stamp(at(3)))
	}
	s2, ok := sessions["s2"]
	if !ok || s2.Started != Stamp(at(5)) || s2.LastTool != "Bash" || !s2.Live() {
		t.Errorf("s2 = %+v (known: %v), want a live session started at its first tool line", s2, ok)
	}
}

// TestSessionsIgnoreWhatIsNotASessionLine: only session-start, session-touch,
// session-end and tool lines with a session id make sessions; other events
// that carry a session, lines without one, and lines that do not parse do not.
func TestSessionsIgnoreWhatIsNotASessionLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	if err := StartSession(root, at(0), Registration{ID: "s1"}); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, root, journal.Line{Event: journal.EventGitCommit, Session: "s-git"})
	mustAppend(t, root, journal.Line{Event: journal.EventVerifySpawned, Session: "s-verify", PID: 1})
	mustAppend(t, root, journal.Line{Event: journal.EventHook, Session: "s-hook"})
	mustAppend(t, root, journal.Line{Event: journal.EventSessionStart}) // no session id
	mustAppend(t, root, journal.Line{Event: journal.EventTool})         // no session id

	f, err := os.OpenFile(home.LedgerPath(root), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not json at all\n{\"event\":\"session-start\",\"session\":\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := StartSession(root, at(1), Registration{ID: "s2"}); err != nil {
		t.Fatal(err)
	}

	sessions := mustSessions(t, root)
	var ids []string
	for id := range sessions {
		ids = append(ids, id)
	}
	if len(sessions) != 2 || sessions["s1"].ID != "s1" || sessions["s2"].ID != "s2" {
		t.Fatalf("sessions = %v, want exactly s1 and s2", ids)
	}
}

// TestSessionsOrderByStampNotFileOrder: two processes stamp a line before
// they take the ledger lock, so the file can hold a later stamp before an
// earlier one; Started and LastSeen are the earliest and latest instants.
func TestSessionsOrderByStampNotFileOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Trimmed nanoseconds make stamps of different lengths: as text
	// "...:00.5Z" sorts before "...:00Z", as instants it is after.
	late, early := t0.Add(500*time.Millisecond), t0
	mustAppend(t, root, journal.Line{TS: Stamp(late), Event: journal.EventTool, Session: "s1"})
	mustAppend(t, root, journal.Line{TS: Stamp(early), Event: journal.EventSessionStart, Session: "s1"})

	s := mustSessions(t, root)["s1"]
	if s.Started != Stamp(early) || s.LastSeen != Stamp(late) {
		t.Fatalf("started %q last seen %q, want %q and %q", s.Started, s.LastSeen, Stamp(early), Stamp(late))
	}
}

// TestSessionsAbsentLedger: a jig home with no ledger has no sessions.
func TestSessionsAbsentLedger(t *testing.T) {
	t.Parallel()

	sessions, err := Sessions(t.TempDir())
	if err != nil || sessions == nil || len(sessions) != 0 {
		t.Fatalf("Sessions on an absent ledger = %v, %v, want an empty map and no error", sessions, err)
	}
}

// TestSessionLinesKeepTheCloneBesideTheLinesOwnKeys: the clone key is in the
// file next to the Line's keys, Read still reads the line as a journal.Line,
// and Sessions gives the clone back.
func TestSessionLinesKeepTheCloneBesideTheLinesOwnKeys(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	if err := StartSession(root, at(0), Registration{ID: "s1", Clone: "/work/app", Cwd: "/work/app/sub"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(home.LedgerPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"clone":"/work/app"`) || !strings.Contains(string(raw), `"event":"session-start"`) {
		t.Fatalf("ledger line = %s, want the clone key beside event", raw)
	}
	lines, err := Read(root)
	if err != nil || len(lines) != 1 || lines[0].Event != journal.EventSessionStart || lines[0].Session != "s1" || lines[0].Cwd != "/work/app/sub" {
		t.Fatalf("Read = %+v, %v, want the one session-start line as a journal.Line", lines, err)
	}
	if got := mustSessions(t, root)["s1"].Clone; got != "/work/app" {
		t.Fatalf("Session.Clone = %q, want /work/app", got)
	}
}

// TestSessionLineNeedsAnID: a session line for no session is refused and
// writes nothing.
func TestSessionLineNeedsAnID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	for name, err := range map[string]error{
		"start": StartSession(root, at(0), Registration{}),
		"touch": TouchSession(root, at(0), Registration{Harness: "claude"}),
		"end":   EndSession(root, at(0), ""),
	} {
		if err == nil {
			t.Errorf("%s with no session id succeeded, want a refusal", name)
		}
	}
	if _, err := os.Stat(home.LedgerPath(root)); err == nil {
		t.Errorf("the ledger exists after refused session lines")
	}
}

// TestRecentOrdersByInstant: newest first, ties by id, and a stamp with
// trimmed nanoseconds compares as the instant it is.
func TestRecentOrdersByInstant(t *testing.T) {
	t.Parallel()

	in := map[string]Session{
		"b":   {ID: "b", LastSeen: Stamp(t0)},
		"a":   {ID: "a", LastSeen: Stamp(t0)},
		"new": {ID: "new", LastSeen: Stamp(t0.Add(500 * time.Millisecond))},
		"old": {ID: "old", LastSeen: Stamp(t0.Add(-time.Hour))},
	}
	var got []string
	for _, s := range Recent(in) {
		got = append(got, s.ID)
	}
	if want := []string{"new", "a", "b", "old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Recent = %v, want %v", got, want)
	}
}

// TestGraphLine: Graph writes one graph event for a ticket and refuses a line
// that names none.
func TestGraphLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	if err := Graph(root, journal.Line{Slice: "a", Session: "s1"}); err == nil {
		t.Fatal("Graph with no ticket succeeded, want a refusal")
	}
	if err := Graph(root, journal.Line{
		TS: Stamp(at(0)), Event: "ignored", Ticket: "JIG-1", Slice: "a", Session: "s1", Cwd: "/work/app", Files: []string{"x.go"}, Summary: "starting points",
	}); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(root)
	if err != nil || len(lines) != 1 {
		t.Fatalf("Read = %+v, %v, want the one graph line", lines, err)
	}
	l := lines[0]
	if l.Event != journal.EventGraph || l.Ticket != "JIG-1" || l.Slice != "a" || l.Session != "s1" || l.Cwd != "/work/app" || !reflect.DeepEqual(l.Files, []string{"x.go"}) || l.Summary != "starting points" || l.TS != Stamp(at(0)) {
		t.Fatalf("graph line = %+v", l)
	}
	if got := mustSessions(t, root); len(got) != 0 {
		t.Fatalf("a graph line made sessions: %v", got)
	}
}

func TestSessionLinesCarryTheCommitAndTheFoldIgnoresIt(t *testing.T) {
	root := t.TempDir()
	if err := StartSession(root, time.Time{}, Registration{ID: "s1", Ticket: "JIG-1", Commit: "abc123"}); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(root)
	if err != nil || len(lines) != 1 || lines[0].Commit != "abc123" {
		t.Fatalf("Read = %+v, %v, want the line to carry the commit", lines, err)
	}
	got, err := Sessions(root)
	if err != nil || got["s1"].Ticket != "JIG-1" {
		t.Errorf("Sessions = %+v, %v", got, err)
	}
}
