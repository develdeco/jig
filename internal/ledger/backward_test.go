package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// writeLedger puts raw bytes where the ledger under root lives.
func writeLedger(t *testing.T, root string, raw string) {
	t.Helper()
	path := home.LedgerPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// backward collects the refs ReadBackward visits, newest first, stopping
// after stop lines (0 for all).
func backward(t *testing.T, root string, maxBytes int64, stop int) []string {
	t.Helper()
	var refs []string
	err := ReadBackward(root, maxBytes, func(l journal.Line) bool {
		refs = append(refs, l.Ref)
		return stop == 0 || len(refs) < stop
	})
	if err != nil {
		t.Fatalf("ReadBackward: %v", err)
	}
	return refs
}

func TestReadBackwardVisitsNewestFirst(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := Append(root, journal.Line{Event: journal.EventTool, Ref: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(backward(t, root, 0, 0), ","); got != "4,3,2,1,0" {
		t.Errorf("refs = %s, want 4,3,2,1,0", got)
	}
	if got := strings.Join(backward(t, root, 0, 2), ","); got != "4,3" {
		t.Errorf("refs after stopping at 2 = %s, want 4,3", got)
	}
}

func TestReadBackwardAbsentOrEmptyLedger(t *testing.T) {
	root := t.TempDir()
	if got := backward(t, root, 0, 0); len(got) != 0 {
		t.Errorf("absent ledger visited %v", got)
	}
	writeLedger(t, root, "")
	if got := backward(t, root, 0, 0); len(got) != 0 {
		t.Errorf("empty ledger visited %v", got)
	}
}

// Lines longer than a chunk, and lines that straddle a chunk boundary, come
// back whole and in order.
func TestReadBackwardAcrossChunks(t *testing.T) {
	root := t.TempDir()
	var raw strings.Builder
	want := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		// Every fifth line is longer than a chunk; the rest are a few hundred
		// bytes, so boundaries fall everywhere.
		pad := 200 + i*7
		if i%5 == 0 {
			pad = backwardChunk + 1000 + i
		}
		fmt.Fprintf(&raw, `{"ts":"t","ticket":"","event":"tool","ref":"%d","summary":"%s"}`+"\n", i, strings.Repeat("x", pad))
		want = append([]string{fmt.Sprint(i)}, want...)
	}
	writeLedger(t, root, raw.String())
	if got := backward(t, root, 0, 0); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("refs = %v, want %v", got, want)
	}
}

func TestReadBackwardSkipsMalformedLinesAndKeepsAnUnterminatedLast(t *testing.T) {
	root := t.TempDir()
	writeLedger(t, root, `{"event":"tool","ref":"1"}`+"\n"+`not json`+"\n\n"+`{"event":"tool","ref":"2"}`+"\n"+`{"event":"tool","ref":"3"}`)
	if got := strings.Join(backward(t, root, 0, 0), ","); got != "3,2,1" {
		t.Errorf("refs = %s, want 3,2,1", got)
	}
	// A torn final line is skipped, as Read skips it.
	writeLedger(t, root, `{"event":"tool","ref":"1"}`+"\n"+`{"event":"tool","re`)
	if got := strings.Join(backward(t, root, 0, 0), ","); got != "1" {
		t.Errorf("refs with a torn tail = %s, want 1", got)
	}
}

// The byte bound stops the walk that far from the end and never hands out a
// line it cut in two.
func TestReadBackwardHonorsTheByteBound(t *testing.T) {
	root := t.TempDir()
	line := func(i int) string {
		return fmt.Sprintf(`{"ts":"t","ticket":"","event":"tool","ref":"%d"}`, i) + "\n"
	}
	var raw strings.Builder
	for i := 0; i < 10; i++ {
		raw.WriteString(line(i))
	}
	writeLedger(t, root, raw.String())
	one := int64(len(line(0)))
	if got := strings.Join(backward(t, root, one*3, 0), ","); got != "9,8,7" {
		t.Errorf("a bound of three lines visited %s, want 9,8,7", got)
	}
	// A bound that ends mid-line drops that line.
	if got := strings.Join(backward(t, root, one*3+10, 0), ","); got != "9,8,7" {
		t.Errorf("a bound ending mid-line visited %s, want 9,8,7", got)
	}
	if got := strings.Join(backward(t, root, one*100, 0), ","); got != "9,8,7,6,5,4,3,2,1,0" {
		t.Errorf("a bound past the start visited %s", got)
	}
}
