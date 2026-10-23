package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// jline is one journal line as text: the raw bytes of a line written by some
// jig, which is what the readers here take apart. rest is more of the object
// ("session":"s1"), or empty.
func jline(ts, ticket, event, rest string) string {
	s := fmt.Sprintf(`{"ts":%q,"ticket":%q,"event":%q`, ts, ticket, event)
	if rest != "" {
		s += "," + rest
	}
	return s + "}"
}

// writeFile replaces path with lines, one per line, the way journal.Append
// replaces a journal: a temp file renamed over it.
func writeFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// rig is a jig home with journals for some tickets.
type rig struct {
	t    *testing.T
	home string
}

func newRig(t *testing.T) *rig { return &rig{t: t, home: t.TempDir()} }

func (r *rig) journalPath(ticket string) string {
	return filepath.Join(r.home, "store", "tickets", ticket, "journal.ndjson")
}

func (r *rig) writeJournal(ticket string, lines ...string) {
	r.t.Helper()
	writeFile(r.t, r.journalPath(ticket), lines...)
}

// scope reads the journals of tickets (in that order) under store "proj" and
// the rig's ledger.
func (r *rig) scope(allLedger bool, tickets ...string) Scope {
	return Scope{
		Journals: func() ([]Journal, error) {
			var js []Journal
			for _, id := range tickets {
				js = append(js, Journal{Store: "proj", Ticket: id, Path: r.journalPath(id)})
			}
			return js, nil
		},
		Ledger:    r.home,
		AllLedger: allLedger,
	}
}

func (r *rig) ledgerAppend(l journal.Line) {
	r.t.Helper()
	if err := ledger.Append(r.home, l); err != nil {
		r.t.Fatal(err)
	}
}

// summary is an envelope as src:event, for comparing orders.
func summary(envs []Envelope) []string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Src+":"+e.Event)
	}
	return out
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// TestEnvelopeEncodesFlat: src and store sit beside the line's own keys, a
// ledger line has no store, and an envelope read back as a journal.Line is
// the line.
func TestEnvelopeEncodesFlat(t *testing.T) {
	t.Parallel()
	exit := 2
	line := journal.Line{TS: "2026-10-23T09:00:00.123456789Z", Ticket: "JIG-1", Slice: "a", Event: journal.EventOracle, Outcome: "fail", Exit: &exit, Session: "s1"}

	data, err := json.Marshal(Envelope{Src: SrcJournal, Store: "proj", Line: line})
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]any
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"src": "journal", "store": "proj", "ts": line.TS, "ticket": "JIG-1", "slice": "a", "event": "oracle", "outcome": "fail", "session": "s1", "exit": float64(2)} {
		if keys[k] != want {
			t.Errorf("key %q = %v, want %v\n%s", k, keys[k], want, data)
		}
	}
	if _, nested := keys["Line"]; nested {
		t.Errorf("the line is nested under Line, want it flat:\n%s", data)
	}
	var back journal.Line
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, line) {
		t.Errorf("read back as a journal.Line = %+v, %v, want %+v", back, err, line)
	}

	data, err = json.Marshal(Envelope{Src: SrcLedger, Line: line})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"store"`) || !strings.Contains(string(data), `"src":"ledger"`) {
		t.Errorf("a ledger envelope = %s, want src ledger and no store", data)
	}
}

// TestEnvelopeAt: both stamp precisions parse; none or a bad one does not.
func TestEnvelopeAt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		ts   string
		want bool
	}{
		{"2026-10-23T09:00:00Z", true},
		{"2026-10-23T09:00:00.5Z", true},
		{"2026-10-23T09:00:00.123456789Z", true},
		{"2026-10-23T09:00:00+02:00", true},
		{"", false},
		{"yesterday", false},
	} {
		if _, ok := (Envelope{Line: journal.Line{TS: c.ts}}).At(); ok != c.want {
			t.Errorf("At(%q) ok = %v, want %v", c.ts, ok, c.want)
		}
	}
}

// TestReadOrdersByInstantNotByText: stamps of whole seconds and of nanoseconds
// mix, and "09:00:01Z" sorts after "09:00:00.5Z" by time though not by text
// ("Z" is greater than "."). A merge may put lines in any order in the file.
func TestReadOrdersByInstantNotByText(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1",
		jline("2026-10-23T09:00:02Z", "T-1", "c", ""),
		jline("2026-10-23T09:00:00.5Z", "T-1", "a", ""),
		jline("2026-10-23T09:00:01Z", "T-1", "b", ""),
		jline("2026-10-23T09:00:01.000000001Z", "T-1", "b2", ""),
	)
	envs, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range envs {
		got = append(got, e.Event)
	}
	if want := []string{"a", "b", "b2", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for _, e := range envs {
		if e.Src != SrcJournal || e.Store != "proj" {
			t.Errorf("envelope %+v: want src journal and store proj", e)
		}
	}
}

// TestReadBreaksTiesJournalFirstThenFileOrder: lines of one instant come out
// journals before the ledger, journals in the order the scope lists them, each
// in its own file order.
func TestReadBreaksTiesJournalFirstThenFileOrder(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	const at = "2026-10-23T09:00:00Z"
	r.writeJournal("T-1", jline(at, "T-1", "first", ""), jline(at, "T-1", "second", ""))
	r.writeJournal("T-2", jline(at, "T-2", "other", ""))
	r.ledgerAppend(journal.Line{TS: at, Ticket: "T-1", Event: "ledger-one"})
	r.ledgerAppend(journal.Line{TS: at, Ticket: "T-1", Event: "ledger-two"})

	envs, err := Read(r.scope(false, "T-1", "T-2"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"journal:first", "journal:second", "journal:other", "ledger:ledger-one", "ledger:ledger-two"}
	if got := summary(envs); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	// The scope's order is the tie-break, not the ticket's name.
	envs, err = Read(r.scope(false, "T-2", "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"journal:other", "journal:first", "journal:second", "ledger:ledger-one", "ledger:ledger-two"}
	if got := summary(envs); !reflect.DeepEqual(got, want) {
		t.Fatalf("order with T-2 first = %v, want %v", got, want)
	}
}

// TestReadInterleavesJournalsAndLedger: one stream, by time.
func TestReadInterleavesJournalsAndLedger(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1",
		jline("2026-10-23T09:00:01Z", "T-1", "verify-pending", ""),
		jline("2026-10-23T09:00:05Z", "T-1", "oracle", ""),
	)
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:00Z", Ticket: "T-1", Event: "session-start", Session: "s1"})
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:02Z", Ticket: "T-1", Event: "verify-spawned", Session: "s1", PID: 7})
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:09Z", Event: "tool", Session: "s1", Tool: "Bash"})

	envs, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ledger:session-start", "journal:verify-pending", "ledger:verify-spawned", "journal:oracle", "ledger:tool"}
	if got := summary(envs); !reflect.DeepEqual(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	if envs[2].PID != 7 || envs[4].Tool != "Bash" {
		t.Errorf("ledger lines lost their fields: %+v, %+v", envs[2], envs[4])
	}
}

// TestReadSinceLeavesOutWhatIsOlder: a line at since stays, one before goes,
// and a line with no readable stamp is older than any since.
func TestReadSinceLeavesOutWhatIsOlder(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1",
		jline("2026-10-23T09:00:00Z", "T-1", "old", ""),
		jline("2026-10-23T09:00:10Z", "T-1", "edge", ""),
		jline("2026-10-23T09:00:20Z", "T-1", "new", ""),
		`{"ticket":"T-1","event":"unstamped"}`,
	)
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:05Z", Ticket: "T-1", Event: "ledger-old"})
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:15Z", Ticket: "T-1", Event: "ledger-new"})

	all, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(all); got[0] != "journal:unstamped" || len(got) != 6 {
		t.Fatalf("without since = %v, want all six lines, the unstamped one first", got)
	}

	envs, err := Read(r.scope(false, "T-1"), mustParse(t, "2026-10-23T09:00:10Z"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"journal:edge", "ledger:ledger-new", "journal:new"}
	if got := summary(envs); !reflect.DeepEqual(got, want) {
		t.Fatalf("since 09:00:10 = %v, want %v", got, want)
	}
}

// TestReadSkipsWhatIsNotALine: blank lines, text, a torn line and a JSON
// value that is not an object are skipped, and the lines around them stay.
func TestReadSkipsWhatIsNotALine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1",
		jline("2026-10-23T09:00:00Z", "T-1", "a", ""),
		"",
		"not json at all",
		"[1,2,3]",
		`{"ts":"2026-10-23T09:00:01Z","ticket":"T-1","eve`,
		jline("2026-10-23T09:00:02Z", "T-1", "b", ""),
	)
	envs, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summary(envs), []string{"journal:a", "journal:b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}

// TestReadWithNothingThere: a journal not made yet and an absent ledger are
// an empty stream, not an error; a scope with neither is empty too.
func TestReadWithNothingThere(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	envs, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil || len(envs) != 0 {
		t.Fatalf("Read = %v, %v, want an empty stream", envs, err)
	}
	envs, err = Read(Scope{}, time.Time{})
	if err != nil || len(envs) != 0 {
		t.Fatalf("Read of the empty scope = %v, %v, want an empty stream", envs, err)
	}
}

// TestReadFailsWhenAJournalCannotBeRead: a short stream is not passed off as
// a whole one. The journal is a directory, which no OS lets a program read.
func TestReadFailsWhenAJournalCannotBeRead(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "a", ""))
	if err := os.MkdirAll(r.journalPath("T-2"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(r.scope(false, "T-1", "T-2"), time.Time{}); err == nil || !strings.Contains(err.Error(), "journal.ndjson") {
		t.Fatalf("Read error = %v, want one naming the journal it could not read", err)
	}

	// A Reader gives back what it could read, with the error, and tries the
	// failed file again.
	rd := NewReader(r.scope(false, "T-1", "T-2"), time.Time{})
	envs, err := rd.Poll()
	if err == nil || len(envs) != 1 || envs[0].Event != "a" {
		t.Fatalf("Poll = %v, %v, want the line of T-1 and the error of T-2", summary(envs), err)
	}
	if _, err := rd.Poll(); err == nil {
		t.Errorf("a second Poll did not try T-2 again")
	}
}

// TestLedgerLinesInScope: with no AllLedger a ledger line is in the stream
// when it names a ticket of the scope, or names none and its session was last
// on one; AllLedger takes every line.
func TestLedgerLinesInScope(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "claim", ""))
	at := func(n int) string { return fmt.Sprintf("2026-10-23T09:01:%02dZ", n) }
	// In: names the ticket.
	r.ledgerAppend(journal.Line{TS: at(0), Event: "session-start", Session: "s1", Ticket: "T-1"})
	// In: s1 is on T-1.
	r.ledgerAppend(journal.Line{TS: at(1), Event: "tool", Session: "s1", Tool: "Edit"})
	// Out: s2 is on nothing.
	r.ledgerAppend(journal.Line{TS: at(2), Event: "tool", Session: "s2", Tool: "Edit"})
	// Out: another ticket, and its session's tool line.
	r.ledgerAppend(journal.Line{TS: at(3), Event: "session-start", Session: "s3", Ticket: "X-9"})
	r.ledgerAppend(journal.Line{TS: at(4), Event: "tool", Session: "s3", Tool: "Bash"})
	// Out: no ticket and no session.
	r.ledgerAppend(journal.Line{TS: at(5), Event: "hook"})
	// In: s2 moves to T-1, and its tool line after.
	r.ledgerAppend(journal.Line{TS: at(6), Event: "session-touch", Session: "s2", Ticket: "T-1"})
	r.ledgerAppend(journal.Line{TS: at(7), Event: "tool", Session: "s2", Tool: "Bash"})
	// Out: s1 moves away, and its tool line after.
	r.ledgerAppend(journal.Line{TS: at(8), Event: "session-touch", Session: "s1", Ticket: "X-9"})
	r.ledgerAppend(journal.Line{TS: at(9), Event: "tool", Session: "s1", Tool: "Read"})

	envs, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range envs {
		if e.Src == SrcLedger {
			got = append(got, e.Event+"/"+e.Session+"/"+e.Tool)
		}
	}
	want := []string{"session-start/s1/", "tool/s1/Edit", "session-touch/s2/", "tool/s2/Bash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger lines in scope = %v, want %v", got, want)
	}

	// The bindings come from the whole ledger, lines before since included.
	envs, err = Read(r.scope(false, "T-1"), mustParse(t, at(7)))
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(envs); !reflect.DeepEqual(got, []string{"ledger:tool"}) || envs[0].Session != "s2" {
		t.Fatalf("since %s = %v, want only s2's tool line", at(7), got)
	}

	all, err := Read(r.scope(true, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(all); n != 11 { // the journal line and the ten ledger lines
		t.Fatalf("AllLedger gave %d lines, want 11", n)
	}
}

// TestLedgerNeedsNoJournals: AllLedger with no journals is the ledger alone,
// and without it a scope with no tickets has no ledger lines.
func TestLedgerNeedsNoJournals(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:00Z", Event: "hook", Session: "s1"})
	envs, err := Read(Scope{Ledger: r.home, AllLedger: true}, time.Time{})
	if err != nil || len(envs) != 1 || envs[0].Src != SrcLedger {
		t.Fatalf("AllLedger alone = %v, %v, want the one ledger line", summary(envs), err)
	}
	envs, err = Read(Scope{Ledger: r.home}, time.Time{})
	if err != nil || len(envs) != 0 {
		t.Fatalf("a scope with no tickets = %v, %v, want no ledger lines", summary(envs), err)
	}
}

// TestPollReturnsOnlyWhatIsNew: a journal replaced whole, with its old lines
// reordered and new ones between them (what a union merge does), gives back
// only the new lines; two identical lines are two lines.
func TestPollReturnsOnlyWhatIsNew(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := jline("2026-10-23T09:00:01Z", "T-1", "a", "")
	b := jline("2026-10-23T09:00:02Z", "T-1", "b", "")
	c := jline("2026-10-23T09:00:03Z", "T-1", "c", "")
	r.writeJournal("T-1", a, b)
	rd := NewReader(r.scope(false, "T-1"), time.Time{})

	envs, err := rd.Poll()
	if err != nil || !reflect.DeepEqual(summary(envs), []string{"journal:a", "journal:b"}) {
		t.Fatalf("first Poll = %v, %v", summary(envs), err)
	}
	if envs, err = rd.Poll(); err != nil || len(envs) != 0 {
		t.Fatalf("Poll of an unchanged journal = %v, %v, want nothing", summary(envs), err)
	}

	// Append: the new line only.
	r.writeJournal("T-1", a, b, c)
	if envs, err = rd.Poll(); err != nil || !reflect.DeepEqual(summary(envs), []string{"journal:c"}) {
		t.Fatalf("Poll after an append = %v, %v, want c", summary(envs), err)
	}

	// A merge: another machine's line lands first, ours are reordered.
	theirs := jline("2026-10-23T09:00:00.5Z", "T-1", "theirs", `"session":"remote"`)
	r.writeJournal("T-1", c, theirs, b, a)
	envs, err = rd.Poll()
	if err != nil || !reflect.DeepEqual(summary(envs), []string{"journal:theirs"}) || envs[0].Session != "remote" {
		t.Fatalf("Poll after a merge = %v, %v, want only theirs, late", summary(envs), err)
	}

	// The same bytes twice are two lines of the journal, and each is returned.
	r.writeJournal("T-1", c, theirs, b, a, a)
	if envs, err = rd.Poll(); err != nil || !reflect.DeepEqual(summary(envs), []string{"journal:a"}) {
		t.Fatalf("Poll after a repeated line = %v, %v, want the second a", summary(envs), err)
	}
	r.writeJournal("T-1", c, theirs, b, a, a)
	if envs, err = rd.Poll(); err != nil || len(envs) != 0 {
		t.Fatalf("Poll of the same file = %v, %v, want nothing", summary(envs), err)
	}
}

// TestPollTakesALineOnceItIsWhole: a final line cut off mid-write is passed
// over and returned by the poll that finds it whole.
func TestPollTakesALineOnceItIsWhole(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := jline("2026-10-23T09:00:01Z", "T-1", "a", "")
	b := jline("2026-10-23T09:00:02Z", "T-1", "b", "")
	path := r.journalPath("T-1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(a+"\n"+b[:20]), 0o644); err != nil {
		t.Fatal(err)
	}
	rd := NewReader(r.scope(false, "T-1"), time.Time{})
	envs, err := rd.Poll()
	if err != nil || !reflect.DeepEqual(summary(envs), []string{"journal:a"}) {
		t.Fatalf("Poll of a torn journal = %v, %v, want a", summary(envs), err)
	}
	if err := os.WriteFile(path, []byte(a+"\n"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if envs, err = rd.Poll(); err != nil || !reflect.DeepEqual(summary(envs), []string{"journal:b"}) {
		t.Fatalf("Poll after the line was finished = %v, %v, want b", summary(envs), err)
	}
}

// TestPollFollowsTheLedgerByOffset: only the lines after the last poll, and a
// line still being written waits for its newline.
func TestPollFollowsTheLedgerByOffset(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	rd := NewReader(Scope{Ledger: r.home, AllLedger: true}, time.Time{})
	if envs, err := rd.Poll(); err != nil || len(envs) != 0 {
		t.Fatalf("Poll with no ledger = %v, %v", summary(envs), err)
	}
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:00Z", Event: "one"})
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:01Z", Event: "two"})
	envs, err := rd.Poll()
	if err != nil || !reflect.DeepEqual(summary(envs), []string{"ledger:one", "ledger:two"}) {
		t.Fatalf("Poll = %v, %v", summary(envs), err)
	}

	f, err := os.OpenFile(filepath.Join(r.home, "ledger", "ledger.ndjson"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"ts":"2026-10-23T09:00:02Z","event":"th`); err != nil {
		t.Fatal(err)
	}
	if envs, err = rd.Poll(); err != nil || len(envs) != 0 {
		t.Fatalf("Poll with half a line = %v, %v, want nothing", summary(envs), err)
	}
	if _, err := f.WriteString("ree\"}\n"); err != nil {
		t.Fatal(err)
	}
	if envs, err = rd.Poll(); err != nil || !reflect.DeepEqual(summary(envs), []string{"ledger:three"}) {
		t.Fatalf("Poll with the line done = %v, %v, want three", summary(envs), err)
	}
}

// TestPollSeesANewJournal: Journals is asked at every poll.
func TestPollSeesANewJournal(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tickets := []string{"T-1"}
	scope := Scope{Journals: func() ([]Journal, error) {
		var js []Journal
		for _, id := range tickets {
			js = append(js, Journal{Store: "proj", Ticket: id, Path: r.journalPath(id)})
		}
		return js, nil
	}}
	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "a", ""))
	rd := NewReader(scope, time.Time{})
	if envs, err := rd.Poll(); err != nil || len(envs) != 1 {
		t.Fatalf("Poll = %v, %v", summary(envs), err)
	}
	tickets = append(tickets, "T-2")
	r.writeJournal("T-2", jline("2026-10-23T09:00:01Z", "T-2", "b", ""))
	envs, err := rd.Poll()
	if err != nil || len(envs) != 1 || envs[0].Ticket != "T-2" {
		t.Fatalf("Poll after T-2 was made = %v, %v, want its line", summary(envs), err)
	}
}

// TestPollKeepsTheLedgerWhenItCannotListJournals: which ledger lines are in
// scope depends on the journals, so a failed listing reads no ledger and
// loses none of it.
func TestPollKeepsTheLedgerWhenItCannotListJournals(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:00Z", Ticket: "T-1", Event: "session-start", Session: "s1"})
	broken := true
	scope := Scope{
		Journals: func() ([]Journal, error) {
			if broken {
				return nil, fmt.Errorf("the store is being replaced")
			}
			return []Journal{{Store: "proj", Ticket: "T-1", Path: r.journalPath("T-1")}}, nil
		},
		Ledger: r.home,
	}
	rd := NewReader(scope, time.Time{})
	if envs, err := rd.Poll(); err == nil || len(envs) != 0 {
		t.Fatalf("Poll = %v, %v, want the listing error and nothing", summary(envs), err)
	}
	broken = false
	envs, err := rd.Poll()
	if err != nil || !reflect.DeepEqual(summary(envs), []string{"ledger:session-start"}) {
		t.Fatalf("Poll after the listing came back = %v, %v, want the ledger line it kept", summary(envs), err)
	}
}

// TestPollHoldsNoFileAfterwards: after a Poll the journal can be replaced and
// the ledger removed at once, which Windows refuses for a file someone holds
// open. A follower that held a handle would stop every journal append.
func TestPollHoldsNoFileAfterwards(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "a", ""))
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:01Z", Ticket: "T-1", Event: "session-start", Session: "s1"})
	rd := NewReader(r.scope(false, "T-1"), time.Time{})
	if envs, err := rd.Poll(); err != nil || len(envs) != 2 {
		t.Fatalf("Poll = %v, %v", summary(envs), err)
	}

	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "a", ""), jline("2026-10-23T09:00:02Z", "T-1", "b", ""))
	if err := os.Remove(filepath.Join(r.home, "ledger", "ledger.ndjson")); err != nil {
		t.Fatalf("the ledger cannot be removed after a Poll: %v", err)
	}
	envs, err := rd.Poll()
	if err != nil || !reflect.DeepEqual(summary(envs), []string{"journal:b"}) {
		t.Fatalf("Poll after the journal was replaced = %v, %v, want b", summary(envs), err)
	}
}
