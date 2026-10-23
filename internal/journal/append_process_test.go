package journal

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// TestMain turns this test binary into an appender when JIG_JOURNAL_CHILD
// names a role (see runAppender); the tests hand it the variables it needs
// through the child's own environment, never their own.
func TestMain(m *testing.M) {
	if tag := os.Getenv("JIG_JOURNAL_CHILD"); tag != "" {
		os.Exit(runAppender(tag))
	}
	os.Exit(m.Run())
}

// childLines is how many lines each appender writes.
const childLines = 200

// runAppender waits for the start file, then appends childLines lines tagged
// with its name and their sequence number, as fast as it can.
func runAppender(tag string) int {
	st, err := store.Open(os.Getenv("JIG_JOURNAL_ROOT"))
	if err != nil {
		os.Stderr.WriteString("open: " + err.Error() + "\n")
		return 1
	}
	start := os.Getenv("JIG_JOURNAL_START")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(start); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return 1
		}
		time.Sleep(5 * time.Millisecond)
	}
	for i := 0; i < childLines; i++ {
		l := Line{Event: EventNote, Session: tag, Ref: strconv.Itoa(i)}
		if err := Append(st, "JIG-1", l); err != nil {
			os.Stderr.WriteString("append: " + err.Error() + "\n")
			return 1
		}
	}
	return 0
}

// TestAppendConcurrentProcesses: two processes appending to one journal at the
// same time lose nothing and tear nothing - 400 parseable lines, each
// process's own in the order it wrote them. The goroutine test beside it
// proves the lock inside a process; this one proves it across two.
func TestAppendConcurrentProcesses(t *testing.T) {
	st := newTestStore(t)
	start := filepath.Join(t.TempDir(), "start")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var cmds []*exec.Cmd
	for _, tag := range []string{"alpha", "beta"} {
		cmd := exec.Command(self)
		cmd.Env = append(os.Environ(), "JIG_JOURNAL_CHILD="+tag, "JIG_JOURNAL_ROOT="+st.Root, "JIG_JOURNAL_START="+start)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %s: %v", tag, err)
		}
		cmds = append(cmds, cmd)
	}
	if err := os.WriteFile(start, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("appender failed: %v", err)
		}
	}

	raw, err := os.ReadFile(journalPath(st, "JIG-1"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n"); n != 2*childLines {
		t.Fatalf("journal has %d newline-terminated lines, want %d", n, 2*childLines)
	}
	lines, err := Read(st, "JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2*childLines {
		t.Fatalf("Read returned %d parseable lines, want %d (a line was torn or lost)", len(lines), 2*childLines)
	}
	next := map[string]int{}
	for _, l := range lines {
		if l.Event != EventNote || l.TS == "" || l.Ticket != "JIG-1" {
			t.Fatalf("line = %+v, want a note line of JIG-1 with a timestamp", l)
		}
		if want := strconv.Itoa(next[l.Session]); l.Ref != want {
			t.Fatalf("%s line %s came where its line %s belongs", l.Session, l.Ref, want)
		}
		next[l.Session]++
	}
	if next["alpha"] != childLines || next["beta"] != childLines {
		t.Fatalf("lines per appender = %v, want %d each", next, childLines)
	}
}

// TestAppendRefusesWithoutTheLock: a journal whose lock another process holds
// past the timeout is not written to, and the refusal says so with a code.
// Before, Append went on unlocked after thirty seconds and could lose the
// line of the writer that held the lock.
func TestAppendRefusesWithoutTheLock(t *testing.T) {
	st := newTestStore(t)
	if err := Append(st, "JIG-1", Line{Event: EventNote, Ref: "first"}); err != nil {
		t.Fatal(err)
	}
	release, held, err := store.TryLock(journalPath(st, "JIG-1"), 0)
	if err != nil || !held {
		t.Fatalf("TryLock = held %v, err %v", held, err)
	}

	err = appendWithin(st, "JIG-1", Line{Event: EventNote, Ref: "second"}, 200*time.Millisecond)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "JOURNAL_LOCKED" {
		t.Fatalf("Append under a held lock = %v, want JOURNAL_LOCKED", err)
	}
	release()
	lines, err := Read(st, "JIG-1")
	if err != nil || len(lines) != 1 || lines[0].Ref != "first" {
		t.Fatalf("Read = %+v, %v; want only the first line (the refused append wrote nothing)", lines, err)
	}
	if err := Append(st, "JIG-1", Line{Event: EventNote, Ref: "third"}); err != nil {
		t.Fatalf("Append after the lock was released: %v", err)
	}
}

// TestReadLeavesOutATornFinalLine: a line a writer is still writing, or died
// in the middle of, hides nothing before it.
func TestReadLeavesOutATornFinalLine(t *testing.T) {
	st := newTestStore(t)
	if err := Append(st, "JIG-1", Line{Event: EventNote, Ref: "one"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(journalPath(st, "JIG-1"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ts":"2026-01-01T00:00:00Z","ticket":"JIG-1","event":"no`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	lines, err := Read(st, "JIG-1")
	if err != nil || len(lines) != 1 || lines[0].Ref != "one" {
		t.Fatalf("Read with a torn final line = %+v, %v; want the one whole line", lines, err)
	}
}

// TestAppendAfterATornLine: a writer that died mid-line costs that line and
// not the next - the next append starts on a line of its own, and the torn
// fragment, now a bad line in the middle, hides nothing.
func TestAppendAfterATornLine(t *testing.T) {
	st := newTestStore(t)
	if err := Append(st, "JIG-1", Line{Event: EventNote, Ref: "one"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(journalPath(st, "JIG-1"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ts":"2026-01-01T00:00:00Z","ticket":"JIG-1","ev`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Append(st, "JIG-1", Line{Event: EventNote, Ref: "two"}); err != nil {
		t.Fatal(err)
	}

	lines, err := Read(st, "JIG-1")
	if err != nil || len(lines) != 2 || lines[0].Ref != "one" || lines[1].Ref != "two" {
		t.Fatalf("Read = %+v, %v; want lines one and two", lines, err)
	}
	raw, _ := os.ReadFile(journalPath(st, "JIG-1"))
	if !bytes.HasSuffix(raw, []byte("}\n")) {
		t.Errorf("journal ends %q, want a newline-terminated line", raw[max(0, len(raw)-20):])
	}
}

// TestAppendIsInPlace: the journal is appended to, not rewritten - the file
// keeps its identity (a reader that holds it open, and a follower that reads
// by offset, never see it replaced) and its earlier bytes are untouched.
func TestAppendIsInPlace(t *testing.T) {
	st := newTestStore(t)
	if err := Append(st, "JIG-1", Line{Event: EventNote, Ref: "one"}); err != nil {
		t.Fatal(err)
	}
	path := journalPath(st, "JIG-1")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if err := Append(st, "JIG-1", Line{Event: EventNote, Ref: "two"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("Append replaced the journal file instead of appending to it")
	}
	if all, _ := os.ReadFile(path); !bytes.HasPrefix(all, first) || len(all) <= len(first) {
		t.Errorf("the second append did not extend the first's bytes:\n%s", all)
	}
}
