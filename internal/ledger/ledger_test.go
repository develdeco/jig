package ledger

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// TestMain turns this test binary into an appender when JIG_LEDGER_CHILD
// names a role (see runAppender); the tests hand it the variables it needs
// through the child's own environment, never their own.
func TestMain(m *testing.M) {
	if tag := os.Getenv("JIG_LEDGER_CHILD"); tag != "" {
		os.Exit(runAppender(tag))
	}
	os.Exit(m.Run())
}

// childLines is how many lines each appender writes.
const childLines = 200

// runAppender waits for the start file, then appends childLines lines tagged
// with its name and their sequence number, as fast as it can.
func runAppender(tag string) int {
	root := os.Getenv("JIG_LEDGER_ROOT")
	start := os.Getenv("JIG_LEDGER_START")
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
		l := journal.Line{Event: journal.EventTool, Session: tag, Ref: strconv.Itoa(i)}
		if err := Append(root, l); err != nil {
			os.Stderr.WriteString("append: " + err.Error() + "\n")
			return 1
		}
	}
	return 0
}

// TestAppendConcurrentProcesses: two processes appending to one ledger at the
// same time lose nothing and tear nothing - 400 parseable lines, each
// process's own in the order it wrote them.
func TestAppendConcurrentProcesses(t *testing.T) {
	root := t.TempDir()
	start := filepath.Join(t.TempDir(), "start")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var cmds []*exec.Cmd
	for _, tag := range []string{"alpha", "beta"} {
		cmd := exec.Command(self)
		cmd.Env = append(os.Environ(), "JIG_LEDGER_CHILD="+tag, "JIG_LEDGER_ROOT="+root, "JIG_LEDGER_START="+start)
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

	raw, err := os.ReadFile(home.LedgerPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n"); n != 2*childLines {
		t.Fatalf("ledger has %d newline-terminated lines, want %d", n, 2*childLines)
	}
	lines, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2*childLines {
		t.Fatalf("Read returned %d parseable lines, want %d (a line was torn or lost)", len(lines), 2*childLines)
	}
	next := map[string]int{}
	for _, l := range lines {
		if l.Event != journal.EventTool || l.TS == "" {
			t.Fatalf("line = %+v, want a tool line with a timestamp", l)
		}
		if want := strconv.Itoa(next[l.Session]); l.Ref != want {
			t.Fatalf("%s's line %s came where its line %s belongs", l.Session, l.Ref, want)
		}
		next[l.Session]++
	}
	if next["alpha"] != childLines || next["beta"] != childLines {
		t.Fatalf("lines per appender = %v, want %d each", next, childLines)
	}
}

// TestAppendInProcessGoroutines: the lock is not only across processes.
func TestAppendInProcessGoroutines(t *testing.T) {
	root := t.TempDir()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if err := Append(root, journal.Line{Event: journal.EventHook, Session: "s"}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	lines, err := Read(root)
	if err != nil || len(lines) != 100 {
		t.Fatalf("Read = %d lines, %v; want 100", len(lines), err)
	}
}

// TestAppendStampsNanosecondTime: a line with no ts gets now, with fractional
// seconds, and a ts the caller gave is kept.
func TestAppendStampsNanosecondTime(t *testing.T) {
	root := t.TempDir()
	before := time.Now().Add(-time.Second)
	fractional := false
	for i := 0; i < 5 && !fractional; i++ {
		if err := Append(root, journal.Line{Event: journal.EventSessionStart}); err != nil {
			t.Fatal(err)
		}
		lines, err := Read(root)
		if err != nil {
			t.Fatal(err)
		}
		ts, err := time.Parse(time.RFC3339, lines[len(lines)-1].TS)
		if err != nil {
			t.Fatalf("ts %q does not parse as RFC3339: %v", lines[len(lines)-1].TS, err)
		}
		if ts.Before(before) || ts.After(time.Now().Add(time.Second)) {
			t.Fatalf("ts %s is not now", ts)
		}
		fractional = strings.Contains(lines[len(lines)-1].TS, ".")
	}
	if !fractional {
		t.Error("five stamped lines and none has fractional seconds")
	}

	if err := Append(root, journal.Line{Event: journal.EventSessionEnd, TS: "2026-01-02T03:04:05Z"}); err != nil {
		t.Fatal(err)
	}
	lines, _ := Read(root)
	if got := lines[len(lines)-1].TS; got != "2026-01-02T03:04:05Z" {
		t.Errorf("ts = %q, want the caller's kept", got)
	}
}

// TestReadSkipsMalformedLines: one bad line (a torn write, a hand edit) hides
// nothing else, and an absent ledger reads as no lines.
func TestReadSkipsMalformedLines(t *testing.T) {
	root := t.TempDir()
	if lines, err := Read(root); err != nil || len(lines) != 0 {
		t.Fatalf("Read of an absent ledger = %v, %v; want none", lines, err)
	}
	path := home.LedgerPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		`{"ts":"2026-01-01T00:00:00Z","ticket":"","event":"hook","session":"one"}`,
		`{"ts":"2026-01-01T00:00:01Z","tick`,
		``,
		`not json at all`,
		`[1,2,3]`,
		`{"ts":"2026-01-01T00:00:02Z","ticket":"","event":"hook","session":"two"}`,
		`{"ts":"2026-01-01T00:00:03Z","ticket":"","event":"hook","session":"three"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lines {
		got = append(got, l.Session)
	}
	if strings.Join(got, ",") != "one,two,three" {
		t.Errorf("sessions read = %v, want one,two,three", got)
	}
}

// TestAppendAfterATornLine: a writer that died mid-line leaves a fragment, and
// the next line starts on a line of its own instead of fusing into it.
func TestAppendAfterATornLine(t *testing.T) {
	root := t.TempDir()
	path := home.LedgerPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"ts":"2026-01-01T00:00:00Z","ticket":"","event":"to`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, journal.Line{Event: journal.EventHook, Session: "after"}); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(root)
	if err != nil || len(lines) != 1 || lines[0].Session != "after" {
		t.Fatalf("Read = %+v, %v; want the one good line after the torn one", lines, err)
	}
}

// TestReadFromFollowsByOffset: a follower reads what is new each time, never
// twice and never a half-written line.
func TestReadFromFollowsByOffset(t *testing.T) {
	root := t.TempDir()
	lines, off, err := ReadFrom(root, 0)
	if err != nil || len(lines) != 0 || off != 0 {
		t.Fatalf("ReadFrom of an absent ledger = %v, %d, %v; want none at 0", lines, off, err)
	}

	for _, s := range []string{"a", "b"} {
		if err := Append(root, journal.Line{Event: journal.EventHook, Session: s}); err != nil {
			t.Fatal(err)
		}
	}
	lines, off, err = ReadFrom(root, off)
	if err != nil || len(lines) != 2 || lines[0].Session != "a" || lines[1].Session != "b" {
		t.Fatalf("first ReadFrom = %+v, %d, %v; want a and b", lines, off, err)
	}
	if again, off2, err := ReadFrom(root, off); err != nil || len(again) != 0 || off2 != off {
		t.Fatalf("ReadFrom at the end = %v, %d, %v; want none at %d", again, off2, err, off)
	}

	// A line mid-write: its first half is on disk.
	path := home.LedgerPath(root)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	whole := `{"ts":"2026-01-01T00:00:00Z","ticket":"","event":"hook","session":"c"}` + "\n"
	if _, err := f.WriteString(whole[:30]); err != nil {
		t.Fatal(err)
	}
	if part, off2, err := ReadFrom(root, off); err != nil || len(part) != 0 || off2 != off {
		t.Fatalf("ReadFrom with half a line = %v, %d, %v; want none, offset unchanged at %d", part, off2, err, off)
	}
	if _, err := f.WriteString(whole[30:]); err != nil {
		t.Fatal(err)
	}
	lines, off, err = ReadFrom(root, off)
	if err != nil || len(lines) != 1 || lines[0].Session != "c" {
		t.Fatalf("ReadFrom with the line finished = %+v, %d, %v; want c", lines, off, err)
	}

	// The file replaced by a shorter one: start over.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(whole), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, off2, err := ReadFrom(root, off+1000)
	if err != nil || len(lines) != 1 || off2 != int64(len(whole)) {
		t.Fatalf("ReadFrom past the end = %+v, %d, %v; want a restart from 0 reading the %d bytes", lines, off2, err, len(whole))
	}
}

// TestAppendAndReadALongLine: a line far past any scanner's default token
// size goes in whole and comes back whole, through Read and ReadFrom.
func TestAppendAndReadALongLine(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("y", 300_000)
	if err := Append(root, journal.Line{Event: journal.EventTool, Summary: big}); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, journal.Line{Event: journal.EventHook, Summary: "after"}); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(root)
	if err != nil || len(lines) != 2 || lines[0].Summary != big || lines[1].Summary != "after" {
		t.Fatalf("Read = %d lines, %v; want both whole", len(lines), err)
	}
	lines, _, err = ReadFrom(root, 0)
	if err != nil || len(lines) != 2 || lines[0].Summary != big {
		t.Fatalf("ReadFrom = %d lines, %v; want both whole", len(lines), err)
	}
}
