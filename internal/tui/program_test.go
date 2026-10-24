package tui

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestHoldKeepsWhatIsWrittenUntilRelease: what is written to a held file
// reaches the original only on release, all of it and in order, and what is
// written after the release goes straight through.
func TestHoldKeepsWhatIsWrittenUntilRelease(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	got := make(chan string, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				got <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	f := w
	release := hold(&f)
	if f == w {
		t.Fatal("hold left the file as it was")
	}
	fmt.Fprint(f, "under\nthe screens\n")
	select {
	case s := <-got:
		t.Fatalf("the original got %q before the release", s)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	read := func(want string) {
		t.Helper()
		var all string
		deadline := time.After(5 * time.Second)
		for all != want {
			select {
			case s := <-got:
				all += s
			case <-deadline:
				t.Fatalf("the original got %q, want %q", all, want)
			}
		}
	}
	read("under\nthe screens\n")
	fmt.Fprint(f, "after\n")
	read("after\n")
}

// TestPartialMark: the start of the marker at the end of a read is held back
// until the next read says whether it is the marker.
func TestPartialMark(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		b    string
		want int
	}{
		{"text", 0},
		{"text" + string(holdMark[:1]), 1},
		{"text" + string(holdMark[:5]), 5},
		{"", 0},
	} {
		if got := partialMark([]byte(c.b)); got != c.want {
			t.Errorf("partialMark(%q) = %d, want %d", c.b, got, c.want)
		}
	}
}
