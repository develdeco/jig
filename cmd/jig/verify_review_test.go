package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestVerifyDirOutsideTheWorktreeCoversNothing: a command run in a directory
// that is not the worktree (or inside it) is no run of the worktree's HEAD, so
// it is recorded as another directory and covers nothing: no verified line and
// no change to the slice.
func TestVerifyDirOutsideTheWorktreeCoversNothing(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.setOracle("a", "echo ran in $(pwd)")
	r.fixClamp()
	before, err := r.st.ReadSliceState(r.fx.Ticket, "a")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()

	out, code := r.run("verify", "--dir", elsewhere)
	if code != 0 || !strings.Contains(out, "covers: no") || !strings.Contains(out, "uncovered: another directory") {
		t.Fatalf("verify --dir outside the worktree: exit %d, want a pass that covers nothing: another directory\n%s", code, out)
	}
	for _, l := range r.journal() {
		if l.Event == journal.EventVerified {
			t.Errorf("a run in another directory wrote %+v", l)
		}
		if l.Event == journal.EventOracle && (l.Uncovered != journal.UncoveredOtherDir || filepath.Clean(l.Dir) != filepath.Clean(elsewhere)) {
			t.Errorf("oracle line = %+v, want the directory it ran in and the reason", l)
		}
	}
	if after, err := r.st.ReadSliceState(r.fx.Ticket, "a"); err != nil || after != before {
		t.Errorf("slice a state = %+v, %v, want %+v", after, err, before)
	}

	// A directory inside the worktree still covers.
	if err := os.MkdirAll(filepath.Join(r.fx.RepoDir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code = r.run("verify", "--dir", filepath.Join(r.fx.RepoDir, "alpha"))
	if code != 0 || !strings.Contains(out, "covers: yes") {
		t.Errorf("verify --dir inside the worktree: exit %d, want it to cover\n%s", code, out)
	}
}

// TestVerifyDirThatDoesNotExistIsJigsOwnFailure: a command directory that is
// not there is jig failing before the command starts: 125, no line, and the
// slice is left as it was.
func TestVerifyDirThatDoesNotExistIsJigsOwnFailure(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	if out, code := r.run("verify"); code != 0 {
		t.Fatalf("setup verify: exit %d\n%s", code, out)
	}
	before, _ := r.st.ReadSliceState(r.fx.Ticket, "a")
	lines := len(r.journal())

	out, code := r.run("verify", "--dir", filepath.Join(t.TempDir(), "missing"))
	if code != 125 || strings.Contains(out, "result:") {
		t.Errorf("verify --dir <missing>: exit %d, want 125 and no footer\n%s", code, out)
	}
	if got := len(r.journal()); got != lines {
		t.Errorf("the journal grew by %d lines, want none", got-lines)
	}
	if after, _ := r.st.ReadSliceState(r.fx.Ticket, "a"); after != before {
		t.Errorf("slice a state = %+v, want %+v", after, before)
	}
}

// TestVerifyExitsWith125WhenItCannotReachTheCommand: a slice that names an env
// class the manifest does not declare never reaches its command: exit 125, an
// error and no footer.
func TestVerifyExitsWith125WhenItCannotReachTheCommand(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	path := filepath.Join(r.st.TicketDir(r.fx.Ticket), "slices.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(data), "    env: rig\n", "    env: nonesuch\n", 1)
	if body == string(data) {
		t.Fatalf("test setup: %s names no env class:\n%s", path, body)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r.commit("d", "d's work", map[string]string{"beta/d.txt": "1"})

	out, code := r.run("verify", "--slice", "d")
	if code != 125 || strings.Contains(out, "result:") || !strings.Contains(out, "nonesuch") {
		t.Errorf("verify of a slice with an undeclared env class: exit %d, want 125, the reason and no footer\n%s", code, out)
	}
}
