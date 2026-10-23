package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// eventsRig is a fixture with a second ticket and lines in both journals and
// the ledger, and an env that runs in the clone.
type eventsRig struct {
	t    *testing.T
	fx   *fixture.Fixture
	st   *store.Store
	name string // the project's name, as the stream's store tag
	e    env
}

// eventsAt stamps the n-th second of a morning, far from any real clock.
func eventsAt(n int) string { return fmt.Sprintf("2026-10-23T09:00:%02dZ", n) }

func newEventsRig(t *testing.T) *eventsRig {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r := &eventsRig{t: t, fx: fx, st: st, name: cfg.Name, e: testEnv(fx.Home).inDir(fx.RepoDir).withClock(fixedClock(mustTime(t, eventsAt(30))))}

	// JIG-1 and JIG-2, interleaved with each other and with the ledger.
	r.journal("JIG-1", eventsAt(1), "verify-pending", "a")
	r.ledger(journal.Line{TS: eventsAt(2), Ticket: "JIG-1", Event: journal.EventVerifySpawned, Session: "s-1", PID: 11})
	r.journal("JIG-2", eventsAt(3), "claim", "x")
	r.journal("JIG-1", eventsAt(4), "oracle", "a")
	r.ledger(journal.Line{TS: eventsAt(5), Event: journal.EventTool, Session: "s-1", Tool: "Bash"})
	r.ledger(journal.Line{TS: eventsAt(6), Event: journal.EventTool, Session: "s-other", Tool: "Bash"})
	r.ledger(journal.Line{TS: eventsAt(7), Ticket: "JIG-2", Event: journal.EventSessionStart, Session: "s-2"})
	r.journal("JIG-1", eventsAt(8), "verified", "a")
	return r
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func (r *eventsRig) journal(ticket, ts, event, slice string) {
	r.t.Helper()
	if err := journal.Append(r.st, ticket, journal.Line{TS: ts, Event: event, Slice: slice}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *eventsRig) ledger(l journal.Line) {
	r.t.Helper()
	if err := ledger.Append(r.fx.Home, l); err != nil {
		r.t.Fatal(err)
	}
}

// decodeStream parses the output of jig events as the stream it is: every
// line a JSON object that reads as an Envelope.
func decodeStream(t *testing.T, out string) []events.Envelope {
	t.Helper()
	var envs []events.Envelope
	for _, raw := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if raw == "" {
			continue
		}
		var e events.Envelope
		if err := json.Unmarshal([]byte(raw), &e); err != nil || !strings.HasPrefix(raw, "{") {
			t.Fatalf("output line is not a JSON envelope: %q (%v)\nwhole output:\n%s", raw, err, out)
		}
		envs = append(envs, e)
	}
	return envs
}

// shape is src:ticket:event for each envelope, for comparing a stream.
func shape(envs []events.Envelope) []string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Src+":"+e.Ticket+":"+e.Event)
	}
	return out
}

func (r *eventsRig) run(want int, args ...string) []events.Envelope {
	r.t.Helper()
	out, code := runMain(r.t, r.e, "", args...)
	if code != want {
		r.t.Fatalf("jig %s: exit %d, want %d:\n%s", strings.Join(args, " "), code, want, out)
	}
	return decodeStream(r.t, out)
}

// TestEventsStreamsTheStoreAndTheLedgerInTimeOrder: with no ticket, every
// ticket's journal and the ledger lines about the store's tickets, one JSON
// object per line and nothing else, oldest first, each tagged with its source;
// a session that is on none of the tickets is left out.
func TestEventsStreamsTheStoreAndTheLedgerInTimeOrder(t *testing.T) {
	t.Parallel()
	r := newEventsRig(t)

	out, code := runMain(t, r.e, "", "events")
	if code != 0 {
		t.Fatalf("jig events: exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "help[") {
		t.Errorf("a stream carries no help block:\n%s", out)
	}
	envs := decodeStream(t, out)
	want := []string{
		"journal:JIG-1:verify-pending",
		"ledger:JIG-1:verify-spawned",
		"journal:JIG-2:claim",
		"journal:JIG-1:oracle",
		"ledger::tool", // s-1 is on JIG-1, since its verify-spawned line
		"ledger:JIG-2:session-start",
		"journal:JIG-1:verified",
	}
	if got := shape(envs); !reflect.DeepEqual(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	for _, e := range envs {
		switch e.Src {
		case events.SrcJournal:
			if e.Store != r.name || r.name == "" {
				t.Errorf("journal envelope %+v: store = %q, want the project %q", e, e.Store, r.name)
			}
		case events.SrcLedger:
			if e.Store != "" {
				t.Errorf("ledger envelope %+v has a store", e)
			}
		}
	}
	if envs[1].PID != 11 || envs[0].Slice != "a" {
		t.Errorf("lines lost their fields: %+v, %+v", envs[0], envs[1])
	}
	// The wire form is flat: src and store beside the line's own keys.
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, `"src":"journal"`) || !strings.Contains(first, `"store":"`+r.name+`"`) || !strings.Contains(first, `"event":"verify-pending"`) {
		t.Errorf("first line = %s, want a flat envelope", first)
	}
}

// TestEventsNarrowsToATicket: one ticket's journal and the ledger lines about
// it, whether they name it or only the session that is on it.
func TestEventsNarrowsToATicket(t *testing.T) {
	t.Parallel()
	r := newEventsRig(t)

	want := []string{"journal:JIG-1:verify-pending", "ledger:JIG-1:verify-spawned", "journal:JIG-1:oracle", "ledger::tool", "journal:JIG-1:verified"}
	if got := shape(r.run(0, "events", "JIG-1")); !reflect.DeepEqual(got, want) {
		t.Fatalf("events JIG-1 = %v, want %v", got, want)
	}
	// The ticket anywhere among the flags.
	if got := shape(r.run(0, "events", "--since", "now", "JIG-1")); len(got) != 0 {
		t.Errorf("events --since now JIG-1 = %v, want nothing yet", got)
	}
	want = []string{"journal:JIG-2:claim", "ledger:JIG-2:session-start"}
	if got := shape(r.run(0, "events", "JIG-2")); !reflect.DeepEqual(got, want) {
		t.Fatalf("events JIG-2 = %v, want %v", got, want)
	}
}

// TestEventsRefusesWhatItCannotRead: a ticket the store lacks, two tickets, a
// bad --since, and --all with a ticket or a store are VALIDATION_ERRORs, and
// they print no stream.
func TestEventsRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	r := newEventsRig(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"events", "JIG-99"}, "JIG-99"},
		{[]string{"events", "JIG-1", "JIG-2"}, "at most one ticket"},
		{[]string{"events", "--since", "yesterday"}, "not a time"},
		{[]string{"events", "--since", "-5m"}, "not a time"},
		{[]string{"events", "--all", "JIG-1"}, "--all"},
		{[]string{"events", "--all", "--store", r.fx.StoreDir}, "--all"},
		{[]string{"events", "--all", "--project", r.name}, "--all"},
		{[]string{"events", "--nonexistent-flag-xyz"}, "--nonexistent-flag-xyz"},
		{[]string{"events", "--project", "no-such-project"}, "no-such-project"},
	} {
		out, code := runMain(t, r.e, "", c.args...)
		if code != 2 || !strings.Contains(out, "VALIDATION_ERROR") || !strings.Contains(out, c.want) {
			t.Errorf("jig %s: exit %d, want 2 and a VALIDATION_ERROR naming %q:\n%s", strings.Join(c.args, " "), code, c.want, out)
		}
		if strings.Contains(out, `"src"`) {
			t.Errorf("jig %s printed events along with its refusal:\n%s", strings.Join(c.args, " "), out)
		}
	}
}

// TestEventsSince: a timestamp, a duration back from the clock, and now.
func TestEventsSince(t *testing.T) {
	t.Parallel()
	r := newEventsRig(t) // the clock reads 09:00:30

	for _, c := range []struct {
		since string
		want  []string
	}{
		{eventsAt(5), []string{"ledger::tool", "ledger:JIG-2:session-start", "journal:JIG-1:verified"}},
		{"2026-10-23T09:00:08.0000001Z", nil},
		{"25s", []string{"ledger::tool", "ledger:JIG-2:session-start", "journal:JIG-1:verified"}},
		{"0s", nil},
		{"now", nil},
		{"1h", nil}, // everything: filled below
	} {
		got := shape(r.run(0, "events", "--since", c.since))
		if c.since == "1h" {
			if len(got) != 7 {
				t.Errorf("--since 1h = %v, want the whole stream", got)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("--since %s = %v, want %v", c.since, got, c.want)
		}
	}
}

// TestEventsAllSpansEveryProjectAndTheWholeLedger: the stores the machine map
// names, each journal tagged with its project, and every ledger line, ticket
// or not. A mapping whose store is gone is passed over.
func TestEventsAllSpansEveryProjectAndTheWholeLedger(t *testing.T) {
	t.Parallel()
	r := newEventsRig(t)

	other := filepath.Join(t.TempDir(), "other-tickets")
	if err := os.MkdirAll(filepath.Join(other, "tickets", "OTH-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "project.yaml"), []byte("name: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherSt, err := store.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(otherSt, "OTH-1", journal.Line{TS: eventsAt(3), Event: "claim"}); err != nil {
		t.Fatal(err)
	}
	if err := project.UpdateMachine(r.fx.Home, func(m map[string]project.MachineProject) error {
		m["other"] = project.MachineProject{Store: other}
		m["gone"] = project.MachineProject{Store: filepath.Join(t.TempDir(), "never-cloned")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// From a directory in no project, as a front end started anywhere would.
	r.e = r.e.inDir(t.TempDir())
	envs := r.run(0, "events", "--all")
	if len(envs) != 9 { // 4 journal lines of this store, 1 of the other, and the 4 ledger lines
		t.Fatalf("events --all = %v, want every journal line and every ledger line", shape(envs))
	}
	stores := map[string]int{}
	ledgerLines := 0
	for _, e := range envs {
		if e.Src == events.SrcLedger {
			ledgerLines++
			continue
		}
		stores[e.Store]++
	}
	if want := map[string]int{r.name: 4, "other": 1}; !reflect.DeepEqual(stores, want) {
		t.Errorf("journal lines by store = %v, want %v", stores, want)
	}
	if ledgerLines != 4 {
		t.Errorf("%d ledger lines, want all 4, the other session's tool line too", ledgerLines)
	}
	// Still ordered: OTH-1's claim at :03 sits between JIG-1's tool lines.
	for i := 1; i < len(envs); i++ {
		a, _ := envs[i-1].At()
		b, _ := envs[i].At()
		if b.Before(a) {
			t.Fatalf("stream is out of order at %d: %s then %s", i, envs[i-1].TS, envs[i].TS)
		}
	}
}

// TestEventsWithoutAStoreSaysSo: outside every project, with nothing to name
// one, is the same refusal every store verb gives.
func TestEventsWithoutAStoreSaysSo(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir()).inDir(t.TempDir())
	out, code := runMain(t, e, "", "events")
	if code != 2 || !strings.Contains(out, "no jig store found") {
		t.Fatalf("jig events outside a project: exit %d:\n%s", code, out)
	}
}

// syncBuffer is a writer a test reads while a command still writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestEventsFollowPrintsWhatIsWrittenUntilInterrupted: the stream so far, then
// each line a journal or the ledger gains, and an exit 0 when the context
// ends.
func TestEventsFollowPrintsWhatIsWrittenUntilInterrupted(t *testing.T) {
	t.Parallel()
	r := newEventsRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := r.e.withContext(ctx)

	var out syncBuffer
	done := make(chan int, 1)
	go func() { done <- run(e, []string{"events", "JIG-1", "--follow"}, &out, strings.NewReader("")) }()

	waitLines := func(n int) []events.Envelope {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if envs := decodeStream(t, out.String()); len(envs) >= n {
				return envs
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("no %d lines within 30s; output so far:\n%s", n, out.String())
		return nil
	}
	if got := shape(waitLines(5)); len(got) != 5 || got[0] != "journal:JIG-1:verify-pending" {
		t.Fatalf("history = %v", got)
	}

	r.journal("JIG-1", eventsAt(20), "gate-open", "")
	r.ledger(journal.Line{TS: eventsAt(21), Event: journal.EventTool, Session: "s-1", Tool: "Edit"})
	r.ledger(journal.Line{TS: eventsAt(22), Event: journal.EventTool, Session: "s-other", Tool: "Edit"}) // not about JIG-1
	got := shape(waitLines(7))
	if want := []string{"journal:JIG-1:gate-open", "ledger::tool"}; !reflect.DeepEqual(got[5:], want) {
		t.Fatalf("followed lines = %v, want %v", got[5:], want)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("jig events --follow exit %d after the interrupt, want 0", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("jig events --follow did not stop when its context ended")
	}
	if n := len(decodeStream(t, out.String())); n != 7 {
		t.Errorf("%d lines in all, want 7: the session that is on no ticket of the stream printed one", n)
	}
}

// failWriter is a stdout that has gone away.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// TestEventsEndsWhenItsReaderGoesAway: a write that fails ends the run with
// status 1 and no second attempt to say so on the same stdout.
func TestEventsEndsWhenItsReaderGoesAway(t *testing.T) {
	t.Parallel()
	r := newEventsRig(t)
	code := run(r.e, []string{"events", "--follow"}, failWriter{}, strings.NewReader(""))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
}

// TestEventsHelpListsItsFlags: -h prints the registered flags, none hidden
// away and none missing.
func TestEventsHelpListsItsFlags(t *testing.T) {
	t.Parallel()
	out, code := runMain(t, testEnv(t.TempDir()), "", "events", "-h")
	if code != 0 {
		t.Fatalf("jig events -h: exit %d:\n%s", code, out)
	}
	for _, flag := range []string{"follow", "since", "all", "store", "project"} {
		if !strings.Contains(out, flag) {
			t.Errorf("jig events -h does not list --%s:\n%s", flag, out)
		}
	}
}

// TestParseEventsSince: the forms --since takes.
func TestParseEventsSince(t *testing.T) {
	t.Parallel()
	now := mustTime(t, "2026-10-23T12:00:00Z")
	for _, c := range []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"", time.Time{}, false},
		{"now", now, false},
		{"30m", now.Add(-30 * time.Minute), false},
		{"1h30m", now.Add(-90 * time.Minute), false},
		{"0", now, false},
		{"2026-10-23T09:00:00Z", mustTime(t, "2026-10-23T09:00:00Z"), false},
		{"2026-10-23T09:00:00.123456789+02:00", mustTime(t, "2026-10-23T07:00:00.123456789Z"), false},
		{"-30m", time.Time{}, true},
		{"2026-10-23", time.Time{}, true},
		{"later", time.Time{}, true},
	} {
		got, err := parseEventsSince(c.in, now)
		if (err != nil) != c.wantErr || !got.Equal(c.want) {
			t.Errorf("parseEventsSince(%q) = %v, %v, want %v (error %v)", c.in, got, err, c.want, c.wantErr)
		}
	}
}
