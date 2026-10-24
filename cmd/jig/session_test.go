package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// sessionClock is a clock that reads start the first time and a second more
// each time after, so lines written one after another are stamped in order.
func sessionClock(start time.Time) func() time.Time {
	next := start
	return func() time.Time {
		now := next
		next = next.Add(time.Second)
		return now
	}
}

var sessionTestStart = time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)

// sessionEnv is a test env on a fresh jig home with the stepping clock, and
// that home.
func sessionEnv(t *testing.T) (env, string) {
	t.Helper()
	root := t.TempDir()
	return testEnv(root).withClock(sessionClock(sessionTestStart)), root
}

// mustRunSession runs the CLI with e, fails the test unless it exits with
// want, and returns the output.
func mustRunSession(t *testing.T, e env, want int, args ...string) string {
	t.Helper()
	out, code := runMain(t, e, "", args...)
	if code != want {
		t.Fatalf("jig %s: exit %d, want %d:\n%s", strings.Join(args, " "), code, want, out)
	}
	return out
}

// sessionsIn folds the ledger under root.
func sessionsIn(t *testing.T, root string) map[string]ledger.Session {
	t.Helper()
	sessions, err := ledger.Sessions(root)
	if err != nil {
		t.Fatalf("ledger.Sessions: %v", err)
	}
	return sessions
}

// TestSessionStartRecordsWhatItIsGiven: one session-start line carrying the
// flags, the working directory resolved against the env's, and the stamp of
// the env's clock.
func TestSessionStartRecordsWhatItIsGiven(t *testing.T) {
	t.Parallel()
	e, root := sessionEnv(t)
	work := t.TempDir()
	e = e.inDir(work)

	out := mustRunSession(t, e, 0, "session", "start", "--session", "s1", "--harness", "claude", "--agent", "lead",
		"--model", "opus", "--effort", "high", "--cwd", "sub")
	for _, want := range []string{"session:\n", "  id: s1\n", "  harness: claude\n", "  agent: lead\n", "  model: opus\n", "  effort: high\n", "  started: ", "help[1]:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	got := sessionsIn(t, root)["s1"]
	want := ledger.Session{
		ID: "s1", Harness: "claude", Agents: []string{"lead"}, Cwd: filepath.Join(work, "sub"), Model: "opus", Effort: "high",
		Started: ledger.Stamp(sessionTestStart), LastSeen: ledger.Stamp(sessionTestStart),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session = %+v, want %+v", got, want)
	}

	// With no --cwd it is the env's working directory.
	mustRunSession(t, e, 0, "session", "start", "--session", "s2")
	if got := sessionsIn(t, root)["s2"].Cwd; got != work {
		t.Errorf("s2 cwd = %q, want the env's working directory %q", got, work)
	}
}

// TestSessionStartPicksItsID: the flag, else $JIG_SESSION, else a minted id.
func TestSessionStartPicksItsID(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	clock := sessionClock(sessionTestStart)
	plain := testEnv(root).withClock(clock)
	pinned := envFrom(map[string]string{"JIG_HOME": root, "JIG_SESSION": "from-env"}).withClock(clock)

	mustRunSession(t, pinned, 0, "session", "start")
	mustRunSession(t, pinned, 0, "session", "start", "--session", "from-flag")
	out := mustRunSession(t, plain, 0, "session", "start")

	minted := regexp.MustCompile(`(?m)^  id: (s-[0-9a-f]{12})$`).FindStringSubmatch(out)
	if minted == nil {
		t.Fatalf("a start with no id did not print a minted one:\n%s", out)
	}
	sessions := sessionsIn(t, root)
	for _, id := range []string{"from-env", "from-flag", minted[1]} {
		if _, ok := sessions[id]; !ok {
			t.Errorf("session %q is not in the ledger; have %v", id, sessions)
		}
	}
	if len(sessions) != 3 {
		t.Errorf("%d sessions, want 3 (the flag beats JIG_SESSION, which beats minting)", len(sessions))
	}
}

// TestSessionTouchAndEnd: touch moves last_seen, end closes the session and
// is idempotent, and a closed session is not touched.
func TestSessionTouchAndEnd(t *testing.T) {
	t.Parallel()
	e, root := sessionEnv(t)

	mustRunSession(t, e, 0, "session", "start", "--session", "s1") // 09:00:00
	out := mustRunSession(t, e, 0, "session", "touch", "--session", "s1")
	touched := ledger.Stamp(sessionTestStart.Add(time.Second))
	if !strings.Contains(out, "  last_seen: "+axi.Quote(touched)+"\n") {
		t.Errorf("touch did not print its stamp %s:\n%s", touched, out)
	}
	if got := sessionsIn(t, root)["s1"]; got.LastSeen != touched || !got.Live() {
		t.Fatalf("after touch: %+v, want live and last seen %s", got, touched)
	}

	out = mustRunSession(t, e, 0, "session", "end", "--session", "s1")
	endedAt := ledger.Stamp(sessionTestStart.Add(2 * time.Second))
	if !strings.Contains(out, endedAt) {
		t.Errorf("end did not print its stamp %s:\n%s", endedAt, out)
	}
	if got := sessionsIn(t, root)["s1"]; got.Live() || got.Ended != endedAt {
		t.Fatalf("after end: %+v, want ended at %s", got, endedAt)
	}

	lines := func() int {
		l, err := ledger.Read(root)
		if err != nil {
			t.Fatal(err)
		}
		return len(l)
	}
	before := lines()
	out = mustRunSession(t, e, 0, "session", "end", "--session", "s1")
	if !strings.Contains(out, endedAt) || lines() != before {
		t.Errorf("a second end printed %q and left %d ledger lines (had %d), want it to repeat the first end's stamp and write nothing", out, lines(), before)
	}

	out = mustRunSession(t, e, 1, "session", "touch", "--session", "s1")
	if !strings.Contains(out, "code: SESSION_ENDED") || !strings.Contains(out, "jig session start --session s1") || lines() != before {
		t.Errorf("touching an ended session: %q (ledger lines %d, had %d), want SESSION_ENDED, a resume hint and no line", out, lines(), before)
	}

	// Starting it again reopens it.
	mustRunSession(t, e, 0, "session", "start", "--session", "s1")
	if got := sessionsIn(t, root)["s1"]; !got.Live() {
		t.Errorf("after a second start: %+v, want it live again", got)
	}
}

// TestSessionVerbsRefuse: touch and end need a session that exists, take their
// id from --session or $JIG_SESSION, and take no positional argument.
func TestSessionVerbsRefuse(t *testing.T) {
	t.Parallel()
	e, root := sessionEnv(t)
	mustRunSession(t, e, 0, "session", "start", "--session", "s1")
	before, _ := ledger.Read(root)

	for _, verb := range []string{"touch", "end"} {
		out := mustRunSession(t, e, 2, "session", verb)
		if !strings.Contains(out, "code: VALIDATION_ERROR") || !strings.Contains(out, "JIG_SESSION") {
			t.Errorf("session %s with no id: %q, want VALIDATION_ERROR naming --session and JIG_SESSION", verb, out)
		}
		out = mustRunSession(t, e, 1, "session", verb, "--session", "nope")
		if !strings.Contains(out, "code: SESSION_NOT_FOUND") || !strings.Contains(out, "no session nope") {
			t.Errorf("session %s of an unknown id: %q, want SESSION_NOT_FOUND", verb, out)
		}
		out = mustRunSession(t, e, 2, "session", verb, "s1")
		if !strings.Contains(out, "code: VALIDATION_ERROR") || !strings.Contains(out, `unexpected argument "s1"`) {
			t.Errorf("session %s with a positional id: %q, want VALIDATION_ERROR", verb, out)
		}
	}
	out := mustRunSession(t, e, 2, "session", "list", "extra")
	if !strings.Contains(out, `unexpected argument "extra"`) {
		t.Errorf("session list extra: %q, want a refusal", out)
	}
	if after, _ := ledger.Read(root); len(after) != len(before) {
		t.Errorf("refused verbs wrote %d ledger lines", len(after)-len(before))
	}

	// $JIG_SESSION names the session when the flag does not.
	pinned := envFrom(map[string]string{"JIG_HOME": root, "JIG_SESSION": "s1"}).withClock(sessionClock(sessionTestStart.Add(time.Hour)))
	mustRunSession(t, pinned, 0, "session", "touch")
	mustRunSession(t, pinned, 0, "session", "end")
	if got := sessionsIn(t, root)["s1"]; got.Live() {
		t.Errorf("session end with JIG_SESSION=s1 left s1 live: %+v", got)
	}
}

// TestSessionList: live sessions by default, every one with --all, the most
// recently seen first, as a table or as JSON.
func TestSessionList(t *testing.T) {
	t.Parallel()
	e, _ := sessionEnv(t)

	empty := mustRunSession(t, e, 0, "session", "list")
	if !strings.HasPrefix(empty, "sessions[0]{id,harness,ticket,slice,last_seen,ended,cwd}:\n") || !strings.Contains(empty, "jig session start") {
		t.Errorf("an empty list printed:\n%s", empty)
	}
	if got := mustRunSession(t, e, 0, "session", "list", "--json"); strings.TrimSpace(got) != "[]" {
		t.Errorf("an empty --json list printed %q, want []", got)
	}

	mustRunSession(t, e, 0, "session", "start", "--session", "first", "--harness", "claude") // :00
	mustRunSession(t, e, 0, "session", "start", "--session", "second")                       // :01
	mustRunSession(t, e, 0, "session", "start", "--session", "third")                        // :02
	mustRunSession(t, e, 0, "session", "touch", "--session", "first")                        // :03
	mustRunSession(t, e, 0, "session", "end", "--session", "second")                         // :04

	rows := func(out string) []string {
		var ids []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "  Run ") {
				ids = append(ids, strings.SplitN(strings.TrimSpace(l), ",", 2)[0])
			}
		}
		return ids
	}
	live := mustRunSession(t, e, 0, "session", "list")
	if got := rows(live); strings.Join(got, " ") != "first third" {
		t.Errorf("live sessions = %v, want first (touched at :03) then third; output:\n%s", got, live)
	}
	if !strings.Contains(live, "sessions[2]{") || !strings.Contains(live, "first,claude,,,") {
		t.Errorf("table lacks the first session's row:\n%s", live)
	}
	all := mustRunSession(t, e, 0, "session", "list", "--all")
	if got := rows(all); strings.Join(got, " ") != "second first third" {
		t.Errorf("all sessions = %v, want second (seen last, when it ended) first third; output:\n%s", got, all)
	}
	if !strings.Contains(all, ledger.Stamp(sessionTestStart.Add(4*time.Second))) {
		t.Errorf("--all does not show second's end stamp:\n%s", all)
	}

	var decoded []ledger.Session
	if err := json.Unmarshal([]byte(mustRunSession(t, e, 0, "session", "list", "--all", "--json")), &decoded); err != nil {
		t.Fatalf("--json is not JSON: %v", err)
	}
	if len(decoded) != 3 || decoded[0].ID != "second" || decoded[0].Live() || decoded[1].Harness != "claude" || decoded[2].ID != "third" {
		t.Errorf("--json sessions = %+v", decoded)
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(mustRunSession(t, e, 0, "session", "list", "--json")), &raw); err != nil || len(raw) != 2 {
		t.Fatalf("--json (live) = %v, %v, want two sessions", raw, err)
	}
	if _, ended := raw[0]["ended"]; ended {
		t.Errorf("a live session's JSON has an ended key: %v", raw[0])
	}
}

// TestSessionVerbsReadTheirFlagsInHelp: -h prints each verb's flags.
func TestSessionVerbsReadTheirFlagsInHelp(t *testing.T) {
	t.Parallel()
	e, _ := sessionEnv(t)
	for verb, flags := range map[string][]string{
		"start": {"--session", "--harness", "--agent", "--model", "--effort", "--cwd"},
		"touch": {"--session"},
		"end":   {"--session"},
		"list":  {"--all", "--json"},
	} {
		out := mustRunSession(t, e, 0, "session", verb, "-h")
		if !strings.Contains(out, "flags{session "+verb+"}") {
			t.Errorf("session %s -h:\n%s", verb, out)
		}
		for _, f := range flags {
			if !strings.Contains(out, f+",") {
				t.Errorf("session %s -h lacks %s:\n%s", verb, f, out)
			}
		}
	}
}

// TestSessionLinesAreLedgerEvents: the verbs write the event names the rest
// of the digester reads, and nothing else.
func TestSessionLinesAreLedgerEvents(t *testing.T) {
	t.Parallel()
	e, root := sessionEnv(t)
	mustRunSession(t, e, 0, "session", "start", "--session", "s1")
	mustRunSession(t, e, 0, "session", "touch", "--session", "s1")
	mustRunSession(t, e, 0, "session", "end", "--session", "s1")

	lines, err := ledger.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, l := range lines {
		if l.Session != "s1" {
			t.Errorf("line %+v is not about s1", l)
		}
		events = append(events, l.Event)
	}
	if want := []string{journal.EventSessionStart, journal.EventSessionTouch, journal.EventSessionEnd}; strings.Join(events, " ") != strings.Join(want, " ") {
		t.Errorf("events = %v, want %v", events, want)
	}
}

// TestSessionVerbArgTakesTheHarnessSession: a verb with no --session acts on
// $JIG_SESSION, else on the id the harness set for its agent's commands, the
// way locate reads them.
func TestSessionVerbArgTakesTheHarnessSession(t *testing.T) {
	t.Parallel()
	both := envFrom(map[string]string{"JIG_SESSION": "s-env", "CLAUDE_CODE_SESSION_ID": "s-claude"})
	harness := envFrom(map[string]string{"CLAUDE_CODE_SESSION_ID": " s-claude "})
	cases := []struct {
		name string
		e    env
		flag string
		want string
	}{
		{"flag first", both, "s-flag", "s-flag"},
		{"JIG_SESSION over the harness", both, "", "s-env"},
		{"the harness alone", harness, "", "s-claude"},
		{"none", envFrom(nil), "", ""},
	}
	for _, c := range cases {
		if got := sessionVerbArg(c.e, c.flag); got != c.want {
			t.Errorf("%s: sessionVerbArg = %q, want %q", c.name, got, c.want)
		}
	}
}
