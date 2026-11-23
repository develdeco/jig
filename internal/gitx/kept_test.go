package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func anyFact(string, string) bool { return true }

// TestKeptFactsAskedAtTheSameTimeAreAskedOnce: a fact kept is not asked again,
// and one asked at the same time by two readers is put once.
func TestKeptFactsAskedAtTheSameTimeAreAskedOnce(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "facts.txt")
	if err := os.WriteFile(file, []byte("a b\t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &Kept{path: file, valid: anyFact, maxBytes: 1 << 20}
	asked := func() (string, error) { return "", fmt.Errorf("asked") }
	if got, err := k.Answer("a b", asked); err != nil || got != "1" {
		t.Errorf("a kept fact = %q, %v", got, err)
	}

	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	slow := func() (string, error) {
		calls.Add(1)
		close(started)
		<-release
		return "yes", nil
	}
	first := make(chan string, 1)
	go func() {
		got, _ := k.Answer("g h", slow)
		first <- got
	}()
	<-started
	second := make(chan string, 1)
	go func() {
		got, _ := k.Answer("g h", func() (string, error) { calls.Add(1); return "again", nil })
		second <- got
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if a, b := <-first, <-second; a != "yes" || b != "yes" || calls.Load() != 1 {
		t.Errorf("two readers got %q and %q with %d asks, want one ask and yes for both", a, b, calls.Load())
	}
}

// TestATornFactLineIsDropped: a line that does not parse whole is no fact: one
// cut short anywhere (after its tab included), one a cut line was joined to by
// the next append, one with an answer that is no answer, and a line of another
// kind of fact. Only the whole lines are read.
func TestATornFactLineIsDropped(t *testing.T) {
	t.Parallel()
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	whole := "ancestor " + a + " " + b + "\t1\n"
	file := filepath.Join(t.TempDir(), "ancestry.txt")
	content := whole +
		"ancestor " + b + " " + c + "\t" + // cut after its tab, joined by the next append:
		"ancestor " + c + " " + a + "\t0\n" +
		"ancestor " + a + " " + c + "\t2\n" + // not an answer
		"diff " + a + " " + b + " ACDMRTUXB\t[]\n" + // a fact of the other file
		"ancestor " + c + " " + b + "\t0\n" +
		"ancestor " + b + " " + a + "\t" // a last line cut after its tab
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	k := keptFileAt(file, validAncestry, maxAncestryBytes)
	asked := func() (string, error) { return "asked", nil }
	for key, want := range map[string]string{
		"ancestor " + a + " " + b: "1",
		"ancestor " + c + " " + b: "0",
		"ancestor " + b + " " + c: "asked",
		"ancestor " + c + " " + a: "asked",
		"ancestor " + a + " " + c: "asked",
		"ancestor " + b + " " + a: "asked",
		"diff " + a + " " + b:     "asked",
	} {
		if got, err := k.Answer(key, asked); err != nil || got != want {
			t.Errorf("%q = %q, %v, want %q", key, got, err, want)
		}
	}
}

// TestKeptFactsAreBoundedByBytes: a file past its bound is cut to its newest
// facts, from a whole line, however long it was kept in use, and what stays is
// still read.
func TestKeptFactsAreBoundedByBytes(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "ancestry.txt")
	var sb strings.Builder
	n := 0
	for sb.Len() < 3*maxAncestryBytes {
		fmt.Fprintf(&sb, "ancestor %040x %040x\t1\n", n, n+1)
		n++
	}
	if err := os.WriteFile(file, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	k := keptFileAt(file, validAncestry, maxAncestryBytes)
	got, err := k.Answer(fmt.Sprintf("ancestor %040x %040x", n-1, n), func() (string, error) { return "asked", nil })
	if err != nil || got != "1" {
		t.Errorf("the newest fact = %q, %v, want it kept", got, err)
	}
	if got, _ := k.Answer(fmt.Sprintf("ancestor %040x %040x", 0, 1), func() (string, error) { return "asked", nil }); got != "asked" {
		t.Errorf("the oldest fact = %q, want it cut", got)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxAncestryBytes/2+100 {
		t.Errorf("the file is %d bytes after the cut, want about half of %d", info.Size(), maxAncestryBytes)
	}
	data, _ := os.ReadFile(file)
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if !strings.HasPrefix(line, "ancestor ") || !strings.HasSuffix(line, "\t1") {
			t.Fatalf("the cut file holds a line that is not whole: %q", line)
		}
	}
}
