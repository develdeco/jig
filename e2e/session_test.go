package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// sessionListIDs runs `jig session list` (with extra flags) as a process of
// its own and returns the ids it lists, in the order it lists them.
func sessionListIDs(t *testing.T, env jigEnv, cwd string, flags ...string) []string {
	t.Helper()
	res := runJig(t, env, cwd, append([]string{"session", "list"}, flags...)...)
	if res.Code != 0 {
		t.Fatalf("jig session list %v: exit %d\n%s%s", flags, res.Code, res.Stdout, res.Stderr)
	}
	var ids []string
	for _, l := range strings.Split(res.Stdout, "\n") {
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "  Run ") {
			ids = append(ids, strings.SplitN(strings.TrimSpace(l), ",", 2)[0])
		}
	}
	return ids
}

// sessionSameDir reports whether a and b name one directory, whatever the
// spelling of their paths (short names, symlinks, case).
func sessionSameDir(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// sessionLines counts the ledger's lines of event.
func sessionLines(t *testing.T, env jigEnv, event string) int {
	t.Helper()
	n := 0
	for _, l := range readLedger(t, env) {
		if l.Event == event {
			n++
		}
	}
	return n
}

// TestSessionVisibleAcrossProcesses: a session registered by one jig process
// is listed by another, sessions registered by several processes at once all
// land, and ending one takes it off the default list and leaves it on --all.
func TestSessionVisibleAcrossProcesses(t *testing.T) {
	t.Parallel()
	env := jigEnv{home: t.TempDir(), userHome: t.TempDir()}.without("JIG_SESSION")
	cwd := t.TempDir()

	start := runJig(t, env, cwd, "session", "start", "--session", "s1", "--harness", "claude", "--model", "opus")
	if start.Code != 0 || !strings.Contains(start.Stdout, "  id: s1\n") {
		t.Fatalf("session start s1: exit %d\n%s%s", start.Code, start.Stdout, start.Stderr)
	}

	// Another process sees it.
	listed := runJig(t, env, t.TempDir(), "session", "list")
	if listed.Code != 0 || !strings.Contains(listed.Stdout, "s1,claude,,,") {
		t.Fatalf("session list from another process: exit %d, want a row for s1\n%s%s", listed.Code, listed.Stdout, listed.Stderr)
	}

	// Four more start at the same moment, each in a process of its own; the
	// ledger lock lets every one in and loses none.
	var procs []*jigProc
	var want []string
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("c%d", i)
		want = append(want, id)
		procs = append(procs, startJig(t, env, cwd, "session", "start", "--session", id))
	}
	for i, p := range procs {
		if res := p.Stop(); res.Code != 0 {
			t.Fatalf("session start c%d: exit %d\n%s%s", i+1, res.Code, res.Stdout, res.Stderr)
		}
	}
	live := sessionListIDs(t, env, cwd)
	if len(live) != 5 {
		t.Fatalf("live sessions = %v, want s1 and c1..c4", live)
	}
	for _, id := range append(want, "s1") {
		found := false
		for _, have := range live {
			found = found || have == id
		}
		if !found {
			t.Errorf("session %s is not listed; the list has %v", id, live)
		}
	}

	// A touch is visible too: it is the latest line of its session.
	if res := runJig(t, env, cwd, "session", "touch", "--session", "c1"); res.Code != 0 {
		t.Fatalf("session touch c1: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	if got := sessionListIDs(t, env, cwd); got[0] != "c1" {
		t.Errorf("after touching c1 the list starts with %s, want the most recently seen session, c1", got[0])
	}

	// End s1: gone from the list, still there with --all, and ended.
	if res := runJig(t, env, cwd, "session", "end", "--session", "s1"); res.Code != 0 {
		t.Fatalf("session end s1: exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	for _, id := range sessionListIDs(t, env, cwd) {
		if id == "s1" {
			t.Errorf("s1 is still on the default list after it ended")
		}
	}
	all := runJig(t, env, cwd, "session", "list", "--all", "--json")
	if all.Code != 0 {
		t.Fatalf("session list --all --json: exit %d\n%s%s", all.Code, all.Stdout, all.Stderr)
	}
	var sessions []ledger.Session
	if err := json.Unmarshal([]byte(all.Stdout), &sessions); err != nil {
		t.Fatalf("session list --all --json is not JSON: %v\n%s", err, all.Stdout)
	}
	if len(sessions) != 5 {
		t.Fatalf("--all lists %d sessions, want 5", len(sessions))
	}
	for _, s := range sessions {
		switch {
		case s.ID == "s1" && (s.Live() || s.Harness != "claude" || s.Model != "opus"):
			t.Errorf("s1 on --all = %+v, want it ended, with its harness and model", s)
		case s.ID != "s1" && !s.Live():
			t.Errorf("%s on --all = %+v, want it live", s.ID, s)
		}
	}

	// Ending it again is not an error and writes nothing; touching it is.
	before := len(readLedger(t, env))
	if res := runJig(t, env, cwd, "session", "end", "--session", "s1"); res.Code != 0 {
		t.Errorf("a second session end: exit %d, want 0\n%s", res.Code, res.Stdout)
	}
	if res := runJig(t, env, cwd, "session", "touch", "--session", "s1"); res.Code != 1 || !strings.Contains(res.Stdout, "code: SESSION_ENDED") {
		t.Errorf("session touch of an ended session: exit %d, want 1 and SESSION_ENDED\n%s", res.Code, res.Stdout)
	}
	if after := len(readLedger(t, env)); after != before {
		t.Errorf("those two wrote %d ledger lines, want none", after-before)
	}

	// What the ledger holds is the lines the verbs name.
	if n := sessionLines(t, env, journal.EventSessionStart); n != 5 {
		t.Errorf("%d session-start lines, want 5", n)
	}
	if n := sessionLines(t, env, journal.EventSessionEnd); n != 1 {
		t.Errorf("%d session-end lines, want 1", n)
	}
	if n := sessionLines(t, env, journal.EventSessionTouch); n != 1 {
		t.Errorf("%d session-touch lines, want 1", n)
	}
}

// TestHereMintsSession: `jig here` in a project clone with no session given
// mints one, prints it and records it, so a process of its own lists it; a
// session that was given is left as it is, and a directory outside a project
// mints nothing.
//
// It needs the `jig here` verb, which D3 adds; against a binary without it
// the test skips rather than fail on a verb this ticket does not own.
func TestHereMintsSession(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SESSION")

	first := runJig(t, env, fx.RepoDir, "here")
	if first.Code == 2 && strings.Contains(first.Stdout, `unknown command "here"`) {
		t.Skip("jig here is not in this binary yet (D3 adds it); its session section is covered by the cmd/jig tests")
	}
	if first.Code != 0 {
		t.Fatalf("jig here: exit %d\n%s%s", first.Code, first.Stdout, first.Stderr)
	}
	mint := func(out string) string {
		m := regexp.MustCompile(`(?m)^session:\n  id: (s-[0-9a-f]{12})\n  how: minted$`).FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("jig here printed no minted session block:\n%s", out)
		}
		return m[1]
	}
	id := mint(first.Stdout)

	// A process of its own lists it, and the ledger line says where it began.
	if got := sessionListIDs(t, env, t.TempDir()); len(got) != 1 || got[0] != id {
		t.Fatalf("session list = %v, want only %s", got, id)
	}
	var line journal.Line
	for _, l := range readLedger(t, env) {
		if l.Event == journal.EventSessionStart && l.Session == id {
			line = l
		}
	}
	if line.Session == "" || !sessionSameDir(line.Cwd, fx.RepoDir) {
		t.Errorf("session-start line for %s = %+v, want one with cwd %s", id, line, fx.RepoDir)
	}

	// Each call without a session mints its own.
	if second := mint(runJig(t, env, fx.RepoDir, "here").Stdout); second == id {
		t.Errorf("a second jig here minted %s again", id)
	}
	if n := sessionLines(t, env, journal.EventSessionStart); n != 2 {
		t.Errorf("%d session-start lines after two calls, want 2", n)
	}

	// A session that is given is not replaced, and nothing is written for it.
	given := runJig(t, env.with("JIG_SESSION=given-1"), fx.RepoDir, "here")
	if given.Code != 0 || strings.Contains(given.Stdout, "how: minted") {
		t.Errorf("jig here with JIG_SESSION set: exit %d, output minted a session:\n%s", given.Code, given.Stdout)
	}
	if n := sessionLines(t, env, journal.EventSessionStart); n != 2 {
		t.Errorf("%d session-start lines after a call with a session given, want 2", n)
	}

	// Outside a project: silent, and no session.
	outside := runJig(t, env, t.TempDir(), "here")
	if outside.Code != 0 || strings.TrimSpace(outside.Stdout) != "" {
		t.Errorf("jig here outside a project: exit %d, stdout %q, want 0 and nothing", outside.Code, outside.Stdout)
	}
	if n := sessionLines(t, env, journal.EventSessionStart); n != 2 {
		t.Errorf("%d session-start lines after a call outside a project, want 2", n)
	}
}
