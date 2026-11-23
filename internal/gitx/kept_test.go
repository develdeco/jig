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

// TestKeptFactsIgnoreWhatIsNotAnAnswer: a line that is not whole is skipped, a
// fact kept is not asked again, one asked at the same time by two readers is
// asked once, and a file past its bound is cut to its newest half.
func TestKeptFactsIgnoreWhatIsNotAnAnswer(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "facts.txt")
	if err := os.WriteFile(file, []byte("a b\t1\nnot a line\n\tno key\nc d\t0\ne f"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := &Kept{path: file}
	asked := func() (string, error) { return "", fmt.Errorf("asked") }
	if got, err := k.Answer("a b", asked); err != nil || got != "1" {
		t.Errorf("a kept fact = %q, %v", got, err)
	}
	if got, err := k.Answer("c d", asked); err != nil || got != "0" {
		t.Errorf("a kept fact of no = %q, %v", got, err)
	}
	if _, err := k.Answer("e f", asked); err == nil {
		t.Errorf("a line cut short was kept as an answer")
	}
	if _, err := k.Answer("not a line", asked); err == nil {
		t.Errorf("a line with no answer was kept")
	}

	// Two readers asking one question at once put it once.
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

	var sb strings.Builder
	for i := 0; i < maxKeptFacts+10; i++ {
		fmt.Fprintf(&sb, "k%d\t1\n", i)
	}
	if err := os.WriteFile(file, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	k = &Kept{path: file}
	k.mu.Lock()
	k.loadLocked()
	k.mu.Unlock()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "\n"); n != maxKeptFacts/2 {
		t.Errorf("the file was cut to %d facts, want %d", n, maxKeptFacts/2)
	}
	if !strings.Contains(string(data), fmt.Sprintf("k%d\t", maxKeptFacts+9)) {
		t.Errorf("the newest fact was cut")
	}
}
