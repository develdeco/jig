package e2e

// End-to-end tests of `jig events`: the ticket journals and the machine
// ledger as one ndjson stream, ordered by time, each line tagged with the
// file it came from. The harness stands for the agent session: it commits in
// the operator clone and has jig verify the commit in a detached worker, which
// is what the git hook and the PostToolUse hook do once `jig observe` is in.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
)

// parseEnvelopes reads the stdout of jig events as the stream it is: every
// line a JSON object.
func parseEnvelopes(t *testing.T, stdout string) []events.Envelope {
	t.Helper()
	var envs []events.Envelope
	for _, raw := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if raw == "" {
			continue
		}
		var e events.Envelope
		if !strings.HasPrefix(raw, "{") || json.Unmarshal([]byte(raw), &e) != nil {
			t.Fatalf("not an envelope: %q\nwhole output:\n%s", raw, stdout)
		}
		envs = append(envs, e)
	}
	return envs
}

// eventsOf is the events of envs, in order, of one source ("" for both).
func eventsOf(envs []events.Envelope, src string) []string {
	var out []string
	for _, e := range envs {
		if src == "" || e.Src == src {
			out = append(out, e.Event)
		}
	}
	return out
}

// TestEventsReadsHarnessRun: a session registers, commits a slice and has it
// verified by a detached worker, and its tools run; jig events then reads the
// journal and the ledger of that run as one stream, in time order, the
// journal lines tagged with the project and the ledger lines about the
// ticket (those that name it, and those of a session that is on it) with
// none. A ticket argument narrows it, and --all adds the lines no ticket owns.
func TestEventsReadsHarnessRun(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-events")
	branch := "jig/" + fx.Ticket
	h.Branch(branch)

	if res := h.Jig("session", "start", "--harness", "claude"); res.Code != 0 {
		t.Fatalf("jig session start exit = %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	sha := h.CommitScenario("a", 1)
	if res := h.Jig("verify", fx.Ticket, sha, "--slice", "a"); res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
		t.Fatalf("jig verify exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventVerified && l.Slice == "a" && l.Commit == sha
	})
	h.WaitVerifiers()
	// What the PostToolUse hook leaves once `jig observe` is in: a tool line
	// with the session and no ticket.
	if err := ledger.Append(env.home, journal.Line{Event: journal.EventTool, Session: h.Session, Tool: "Bash"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	res := runJig(t, env, fx.RepoDir, "events", fx.Ticket)
	if res.Code != 0 || res.Stderr != "" {
		t.Fatalf("jig events %s: exit %d\nstdout:\n%s\nstderr:\n%s", fx.Ticket, res.Code, res.Stdout, res.Stderr)
	}
	envs := parseEnvelopes(t, res.Stdout)

	if got, want := eventsOf(envs, events.SrcJournal), []string{"verify-pending", "oracle", "verified"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events = %v, want %v", got, want)
	}
	// verify-spawned names the ticket and binds the session to it, so the
	// tool line that follows is about the ticket too.
	if got, want := eventsOf(envs, events.SrcLedger), []string{"verify-spawned", "tool"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ledger events = %v, want %v", got, want)
	}
	var prev time.Time
	for _, e := range envs {
		at, ok := e.At()
		if !ok || at.Before(prev) {
			t.Fatalf("stream is not in time order at %s (%s):\n%s", e.TS, e.Event, res.Stdout)
		}
		prev = at
		switch e.Src {
		case events.SrcJournal:
			if e.Store != cfg.Name || e.Ticket != fx.Ticket {
				t.Errorf("journal envelope %+v: want store %q and ticket %s", e, cfg.Name, fx.Ticket)
			}
		case events.SrcLedger:
			if e.Store != "" || e.Session != h.Session {
				t.Errorf("ledger envelope %+v: want no store and session %s", e, h.Session)
			}
		}
	}
	for _, e := range envs {
		if e.Event == journal.EventOracle && (e.Outcome != "pass" || e.Commit != sha || e.Slice != "a" || e.Source != "jig") {
			t.Errorf("oracle envelope = %+v, want a pass of slice a at %s by jig", e, sha)
		}
	}

	// No ticket: the store's whole stream, which is this ticket's here.
	whole := runJig(t, env, fx.RepoDir, "events")
	if whole.Code != 0 || whole.Stdout != res.Stdout {
		t.Errorf("jig events = exit %d\n%s\nwant the stream of %s:\n%s", whole.Code, whole.Stdout, fx.Ticket, res.Stdout)
	}
	// --all: the session-start line names no ticket and owns none; the
	// whole ledger has it.
	all := runJig(t, env, t.TempDir(), "events", "--all")
	if all.Code != 0 {
		t.Fatalf("jig events --all: exit %d\n%s%s", all.Code, all.Stdout, all.Stderr)
	}
	allEnvs := parseEnvelopes(t, all.Stdout)
	if got, want := eventsOf(allEnvs, events.SrcLedger), []string{"session-start", "verify-spawned", "tool"}; !reflect.DeepEqual(got, want) {
		t.Errorf("--all ledger events = %v, want %v", got, want)
	}
	if got, want := eventsOf(allEnvs, events.SrcJournal), []string{"verify-pending", "oracle", "verified"}; !reflect.DeepEqual(got, want) {
		t.Errorf("--all journal events = %v, want %v", got, want)
	}

	// --since cuts the stream: nothing is newer than now.
	if later := runJig(t, env, fx.RepoDir, "events", fx.Ticket, "--since", "now"); later.Code != 0 || later.Stdout != "" {
		t.Errorf("jig events --since now: exit %d, stdout %q, want an empty stream", later.Code, later.Stdout)
	}
}

// TestEventsFollowSeesHookVerification: jig events --follow, running before the
// commit, prints the verification as it is recorded: the worker's
// verify-pending line first, then its oracle line, then verified - each as
// the detached worker writes it - and the ledger line of the spawn between.
func TestEventsFollowSeesHookVerification(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-follow")
	branch := "jig/" + fx.Ticket
	h.Branch(branch)

	follow := startJig(t, env, fx.RepoDir, "events", "--follow", fx.Ticket)

	sha := h.CommitScenario("a", 1)
	if res := h.Jig("verify", fx.Ticket, sha, "--slice", "a"); res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
		t.Fatalf("jig verify exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}

	// Read the stream until the verification is over, with the worker's go
	// test in a fresh worktree to wait out.
	var seen []events.Envelope
	deadline := time.Now().Add(verifyWait)
	for !hasEvent(seen, journal.EventVerified) {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("no verified line in the stream within %s; got %v", verifyWait, eventsOf(seen, ""))
		}
		line, ok := follow.Line(left)
		if !ok {
			t.Fatalf("the stream ended or stalled; got %v\nstderr:\n%s", eventsOf(seen, ""), follow.Stop().Stderr)
		}
		seen = append(seen, parseEnvelopes(t, line)...)
	}
	h.WaitVerifiers()

	if got, want := eventsOf(seen, events.SrcJournal), []string{"verify-pending", "oracle", "verified"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events followed = %v, want %v", got, want)
	}
	pending, oracle := indexOfEvent(seen, journal.EventVerifyPending), indexOfEvent(seen, journal.EventOracle)
	if pending < 0 || oracle < 0 || pending >= oracle {
		t.Fatalf("verify-pending at %d and oracle at %d in %v, want pending first", pending, oracle, eventsOf(seen, ""))
	}
	if o := seen[oracle]; o.Outcome != "pass" || o.Commit != sha || o.Slice != "a" || o.Src != events.SrcJournal {
		t.Errorf("oracle envelope = %+v, want a journal pass of slice a at %s", o, sha)
	}
	if !hasEvent(seen, journal.EventVerifySpawned) {
		t.Errorf("the ledger's verify-spawned line did not reach the stream: %v", eventsOf(seen, ""))
	}

	// Stopping ends it: it was interrupted, not failed by what it read.
	res := follow.Stop()
	if res.Stderr != "" {
		t.Errorf("jig events --follow wrote to stderr:\n%s", res.Stderr)
	}
}

func hasEvent(envs []events.Envelope, event string) bool { return indexOfEvent(envs, event) >= 0 }

func indexOfEvent(envs []events.Envelope, event string) int {
	for i, e := range envs {
		if e.Event == event {
			return i
		}
	}
	return -1
}
