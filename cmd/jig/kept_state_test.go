package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/home"
)

// TestAKeptStateSaysWhenItWasKept: a pull request state the roster gives from
// what this machine kept carries the instant it was kept, and says nothing of
// the host not answering, since the host was never asked; a state never kept is
// unknown and undated.
func TestAKeptStateSaysWhenItWasKept(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	keptPRStates(t, r.home, map[string]string{digestPR2: "OPEN"})
	out, code := r.run(t, "next", "--json", "--roster")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "the host did not answer") {
		t.Errorf("the roster says the host did not answer, which was not asked:\n%s", out)
	}
	rows, _ := rosterRows(t, out)
	asOf := map[string]string{}
	state := map[string]string{}
	for _, row := range rows {
		d, _ := row["digest"].(map[string]any)
		prs, _ := d["prs"].([]any)
		for _, p := range prs {
			pr := p.(map[string]any)
			url := pr["url"].(string)
			state[url], _ = pr["state"].(string)
			asOf[url], _ = pr["as_of"].(string)
		}
	}
	at, err := time.Parse(time.RFC3339, asOf[digestPR2])
	if err != nil || state[digestPR2] != "open" || time.Since(at) > time.Minute {
		t.Errorf("a kept state reads %q as of %q, want open and the instant it was kept", state[digestPR2], asOf[digestPR2])
	}
	if state[digestPR6] != "unknown" || asOf[digestPR6] != "" {
		t.Errorf("a state never kept reads %q as of %q, want unknown and undated", state[digestPR6], asOf[digestPR6])
	}
}

// TestRosterReadsOnlyTheAncestryFacts: the roster reads and writes the ancestry
// facts and leaves the overlap facts, which are the listing's, alone, however
// large they have grown.
func TestRosterReadsOnlyTheAncestryFacts(t *testing.T) {
	w := newRosterWorld(t, 12, 6, false)
	overlaps := home.OverlapsPath(w.rig.home)
	if err := os.MkdirAll(filepath.Dir(overlaps), 0o755); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("not a fact\n"), 1<<20)
	if err := os.WriteFile(overlaps, big, 0o644); err != nil {
		t.Fatal(err)
	}
	w.read(t)
	got, err := os.ReadFile(overlaps)
	if err != nil || !bytes.Equal(got, big) {
		t.Errorf("the roster touched the overlap facts (%d bytes, %v)", len(got), err)
	}
	if info, err := os.Stat(home.AncestryPath(w.rig.home)); err != nil || info.Size() == 0 {
		t.Errorf("the roster kept no ancestry facts: %v", err)
	}
}
