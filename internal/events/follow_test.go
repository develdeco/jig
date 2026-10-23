package events

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// followWait is how long a test waits for a follower to say something.
const followWait = 20 * time.Second

// follower is a Follow running in a goroutine, its envelopes on a channel.
type follower struct {
	t      *testing.T
	out    chan Envelope
	cancel context.CancelFunc
	done   chan error
}

func startFollow(t *testing.T, scope Scope, o FollowOptions) *follower {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f := &follower{t: t, out: make(chan Envelope, 1024), cancel: cancel, done: make(chan error, 1)}
	if o.Interval == 0 {
		o.Interval = 5 * time.Millisecond
	}
	go func() {
		f.done <- Follow(ctx, scope, o, func(e Envelope) error {
			f.out <- e
			return nil
		})
	}()
	t.Cleanup(cancel)
	return f
}

// next is the next envelope, as src:event, failing the test if none comes.
func (f *follower) next() Envelope {
	f.t.Helper()
	select {
	case e := <-f.out:
		return e
	case <-time.After(followWait):
		f.t.Fatalf("no envelope within %s", followWait)
	}
	return Envelope{}
}

// none fails the test if an envelope arrives within a short while.
func (f *follower) none() {
	f.t.Helper()
	select {
	case e := <-f.out:
		f.t.Fatalf("unexpected envelope %s:%s", e.Src, e.Event)
	case <-time.After(100 * time.Millisecond):
	}
}

// stop ends the follow and returns what Follow returned.
func (f *follower) stop() error {
	f.t.Helper()
	f.cancel()
	select {
	case err := <-f.done:
		return err
	case <-time.After(followWait):
		f.t.Fatal("Follow did not return after its context ended")
	}
	return nil
}

// TestFollowEmitsTheStreamThenWhatIsAdded: the lines there already come
// first, sorted; then each line that a journal or the ledger gains, as it
// does.
func TestFollowEmitsTheStreamThenWhatIsAdded(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := jline("2026-10-23T09:00:01Z", "T-1", "verify-pending", "")
	b := jline("2026-10-23T09:00:05Z", "T-1", "oracle", "")
	r.writeJournal("T-1", b, a)
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:00Z", Ticket: "T-1", Event: "session-start", Session: "s1"})

	f := startFollow(t, r.scope(false, "T-1"), FollowOptions{})
	for _, want := range []string{"ledger:session-start", "journal:verify-pending", "journal:oracle"} {
		if e := f.next(); e.Src+":"+e.Event != want {
			t.Fatalf("history: got %s:%s, want %s", e.Src, e.Event, want)
		}
	}
	f.none()

	c := jline("2026-10-23T09:00:09Z", "T-1", "verified", "")
	r.writeJournal("T-1", a, b, c)
	if e := f.next(); e.Event != "verified" || e.Src != SrcJournal || e.Store != "proj" {
		t.Fatalf("after an append: %+v, want the journal's verified line", e)
	}
	r.ledgerAppend(journal.Line{TS: "2026-10-23T09:00:10Z", Event: "tool", Session: "s1", Tool: "Bash"})
	if e := f.next(); e.Event != "tool" || e.Src != SrcLedger {
		t.Fatalf("after a ledger append: %+v, want the tool line", e)
	}
	f.none()
	if err := f.stop(); err != nil {
		t.Fatalf("Follow = %v after its context ended, want nil", err)
	}
}

// TestFollowSinceLeavesOutTheHistoryBefore: with a since, the history is
// cut at it and lines added later are not.
func TestFollowSinceLeavesOutTheHistoryBefore(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := jline("2026-10-23T09:00:01Z", "T-1", "old", "")
	b := jline("2026-10-23T09:00:20Z", "T-1", "new", "")
	r.writeJournal("T-1", a, b)

	f := startFollow(t, r.scope(false, "T-1"), FollowOptions{Since: mustParse(t, "2026-10-23T09:00:10Z")})
	if e := f.next(); e.Event != "new" {
		t.Fatalf("first envelope = %s, want new", e.Event)
	}
	f.none()
	c := jline("2026-10-23T09:00:30Z", "T-1", "newer", "")
	r.writeJournal("T-1", a, b, c)
	if e := f.next(); e.Event != "newer" {
		t.Fatalf("next envelope = %s, want newer", e.Event)
	}
	f.stop()
}

// TestFollowSeesALateLine: a merge that brings in a line older than the ones
// emitted gets it emitted when it arrives, not held for its place.
func TestFollowSeesALateLine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := jline("2026-10-23T09:00:05Z", "T-1", "a", "")
	r.writeJournal("T-1", a)
	f := startFollow(t, r.scope(false, "T-1"), FollowOptions{})
	if e := f.next(); e.Event != "a" {
		t.Fatalf("first envelope = %s, want a", e.Event)
	}
	late := jline("2026-10-23T09:00:01Z", "T-1", "late", "")
	r.writeJournal("T-1", late, a)
	if e := f.next(); e.Event != "late" {
		t.Fatalf("next envelope = %s, want the late line", e.Event)
	}
	f.none()
	f.stop()
}

// TestFollowEndsWithItsContext: a context that has ended already still gets
// the stream as it is, then Follow returns nil.
func TestFollowEndsWithItsContext(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:01Z", "T-1", "a", ""))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got []string
	err := Follow(ctx, r.scope(false, "T-1"), FollowOptions{Interval: time.Hour}, func(e Envelope) error {
		got = append(got, e.Event)
		return nil
	})
	if err != nil || !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("Follow = %v with %v, want nil with the stream", err, got)
	}
}

// TestFollowStopsWhenEmitFails: a reader that went away (a closed pipe) ends
// the follow with its error.
func TestFollowStopsWhenEmitFails(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:01Z", "T-1", "a", ""), jline("2026-10-23T09:00:02Z", "T-1", "b", ""))
	gone := errors.New("pipe closed")
	calls := 0
	err := Follow(context.Background(), r.scope(false, "T-1"), FollowOptions{Interval: time.Millisecond}, func(Envelope) error {
		calls++
		return gone
	})
	if !errors.Is(err, gone) || calls != 1 {
		t.Fatalf("Follow = %v after %d calls, want the emit error after the first", err, calls)
	}
}

// TestFollowRidesOutAFailedPoll: a poll that fails once (a journal replaced at
// the moment it was opened) costs nothing; the next one delivers.
func TestFollowRidesOutAFailedPoll(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:01Z", "T-1", "a", ""))
	var mu sync.Mutex
	calls := 0
	scope := Scope{Journals: func() ([]Journal, error) {
		mu.Lock()
		defer mu.Unlock()
		if calls++; calls <= 3 {
			return nil, errors.New("sharing violation")
		}
		return []Journal{{Store: "proj", Ticket: "T-1", Path: r.journalPath("T-1")}}, nil
	}}
	f := startFollow(t, scope, FollowOptions{})
	if e := f.next(); e.Event != "a" {
		t.Fatalf("first envelope = %s, want a, after three failed polls", e.Event)
	}
	if err := f.stop(); err != nil {
		t.Fatalf("Follow = %v, want nil", err)
	}
}

// TestFollowGivesUpWhenEveryPollFails: a file that stays unreadable ends the
// follow with its error rather than a silent loop.
func TestFollowGivesUpWhenEveryPollFails(t *testing.T) {
	t.Parallel()
	scope := Scope{Journals: func() ([]Journal, error) { return nil, errors.New("no such store") }}
	err := Follow(context.Background(), scope, FollowOptions{Interval: time.Millisecond}, func(Envelope) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "no such store") {
		t.Fatalf("Follow = %v, want the listing error after %d failed polls", err, maxPollFailures)
	}
}

// TestFollowDeliversEveryAppendOnceAndBlocksNone: a writer replaces the
// journal forty times, as journal.Append does, while a follower polls as
// fast as it can. Every replacement goes through - on Windows a rename over a
// file the follower held open would not - and every line comes out once, in
// order.
func TestFollowDeliversEveryAppendOnceAndBlocksNone(t *testing.T) {
	t.Parallel()
	const n = 40
	r := newRig(t)
	path := r.journalPath("T-1")
	f := startFollow(t, r.scope(false, "T-1"), FollowOptions{Interval: time.Millisecond})

	var lines []string
	for i := 0; i < n; i++ {
		lines = append(lines, jline(fmt.Sprintf("2026-10-23T09:%02d:%02dZ", i/60, i%60), "T-1", fmt.Sprintf("e%03d", i), ""))
		data := []byte(strings.Join(lines, "\n") + "\n")
		if err := store.AtomicWrite(path, data); err != nil {
			t.Fatalf("replace %d: %v", i, err)
		}
	}
	for i := 0; i < n; i++ {
		if e := f.next(); e.Event != fmt.Sprintf("e%03d", i) {
			t.Fatalf("envelope %d = %s, want e%03d", i, e.Event, i)
		}
	}
	f.none()
	f.stop()
}
