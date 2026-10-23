package slicectx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// testBrief is a brief with three sections; the slice tests cut slices from
// the first two.
const testBrief = "# Ticket\n\nintro\n\n## Slice A - clamp\n\nFix Clamp.\n\n## Slice B - callers\n\nAdd a caller.\n\nWith detail.\n\n## Seams\n\n- the verb\n"

// world is one ticket in a store with a clone, laid out in temp dirs.
type world struct {
	t     *testing.T
	st    *store.Store
	clone string
}

// newWorld makes a store (not a git repo) holding ticket JIG-1 with testBrief
// and slices a and b (b blocked by a), and a git clone with one commit.
func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, st: &store.Store{Root: t.TempDir()}, clone: t.TempDir()}
	dir := w.st.TicketDir("JIG-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(testBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := map[string]string{}
	for _, sec := range store.BriefSections([]byte(testBrief)) {
		hash[sec.Heading] = sec.Hash
	}
	err := w.st.AppendSlices("JIG-1", []store.Slice{
		{ID: "a", Workspace: "alpha", Goal: "Fix Clamp.", Oracle: "test", FromBrief: []string{hash["Slice A - clamp"]}},
		{ID: "b", Workspace: "alpha", Goal: "Add a caller.", Oracle: "test", Env: "rig", BlockedBy: []string{"a"},
			FromBrief: []string{hash["Slice B - callers"], "deadbeef"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	w.git("init", "-b", "main")
	w.write(".claude/jig.yaml", "workspaces:\n  - id: alpha\n    path: alpha\noracles:\n  test: \"go test ./{path}/...\"\n")
	w.commit("initial", "", ".claude/jig.yaml")
	return w
}

func (w *world) git(args ...string) string {
	w.t.Helper()
	out, err := gitx.Run(w.clone, args...)
	if err != nil {
		w.t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func (w *world) write(rel, content string) {
	w.t.Helper()
	full := filepath.Join(w.clone, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// commit commits the files (already written) with subject and, when slice is
// not empty, the Jig-Slice trailer for it; it returns the sha.
func (w *world) commit(subject, slice string, files ...string) string {
	w.t.Helper()
	w.git(append([]string{"add", "--"}, files...)...)
	args := []string{"commit", "-m", subject}
	if slice != "" {
		args = append(args, "-m", "Jig-Slice: "+slice)
	}
	w.git(args...)
	return w.git("rev-parse", "HEAD")
}

// line appends a journal line for JIG-1.
func (w *world) line(l journal.Line) {
	w.t.Helper()
	if err := journal.Append(w.st, "JIG-1", l); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) input(slice string) Input {
	return Input{Store: w.st, Ticket: "JIG-1", Slice: slice, Repo: "repo", RepoDir: w.clone}
}

// removeSlices deletes the slices.yaml of JIG-1, so a test can lay down a
// different one.
func removeSlices(w *world) error {
	return os.Remove(filepath.Join(w.st.TicketDir("JIG-1"), "slices.yaml"))
}
