package tui

import (
	"fmt"
	"io"
	"os"
	"testing"
)

// TestHoldKeepsWhatIsWrittenUntilRelease: what is written to a held file
// reaches the original only on release, all of it and in order.
func TestHoldKeepsWhatIsWrittenUntilRelease(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	f := w
	release := hold(&f)
	if f == w {
		t.Fatal("hold left the file as it was")
	}
	fmt.Fprint(f, "under\nthe screens\n")
	release()
	if f != w {
		t.Fatal("release did not give the file back")
	}
	_ = w.Close()
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "under\nthe screens\n" {
		t.Errorf("the original got %q (%v), want what was written while held", got, err)
	}
}
