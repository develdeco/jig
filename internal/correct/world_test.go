package correct

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// testBrief has three sections; slices a and b are cut from the first two.
const testBrief = "# Ticket\n\nintro\n\n## Slice A - clamp\n\nFix Clamp.\n\n## Slice B - callers\n\nAdd a caller.\n\n## Seams\n\n- the verb\n"

// testManifest declares two workspaces.
const testManifest = "workspaces:\n  - id: alpha\n    path: alpha\n  - id: beta\n    path: ./beta/\noracles:\n  test: \"go test ./{path}/...\"\n"

// world is ticket JIG-1 in a store (not a git repo) with a clone.
type world struct {
	t     *testing.T
	st    *store.Store
	clone string
	// start is when the session of the tests began; a line made after it
	// belongs to the session.
	start time.Time
	// hash is the section hash by heading.
	hash map[string]string
	n    int
}

// newWorld makes the store with the brief and slices a (alpha) and b (beta),
// and a clone with the manifest committed.
func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, st: &store.Store{Root: t.TempDir()}, clone: t.TempDir(), hash: map[string]string{}}
	dir := w.st.TicketDir("JIG-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w.writeBrief(testBrief)
	for _, sec := range store.BriefSections([]byte(testBrief)) {
		w.hash[sec.Heading] = sec.Hash
	}
	err := w.st.AppendSlices("JIG-1", []store.Slice{
		{ID: "a", Workspace: "alpha", Goal: "Fix Clamp.", Oracle: "test", FromBrief: []string{w.hash["Slice A - clamp"]}},
		{ID: "b", Workspace: "beta", Goal: "Add a caller.", Oracle: "test", FromBrief: []string{w.hash["Slice B - callers"]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.git("init", "-b", "main")
	w.write(".claude/jig.yaml", testManifest)
	w.commit("initial", ".claude/jig.yaml")
	w.start = time.Now().UTC().Add(-time.Minute)
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

// commit commits the files (already written) and returns the sha.
func (w *world) commit(subject string, files ...string) string {
	w.t.Helper()
	w.git(append([]string{"add", "--"}, files...)...)
	w.git("commit", "-m", subject)
	return w.git("rev-parse", "HEAD")
}

// commitFiles writes each file with some content and commits them.
func (w *world) commitFiles(subject string, files ...string) string {
	w.t.Helper()
	for _, f := range files {
		w.n++
		w.write(f, fmt.Sprintf("content %s %d", subject, w.n))
	}
	return w.commit(subject, files...)
}

func (w *world) writeBrief(body string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.st.TicketDir("JIG-1"), "brief.md"), []byte(body), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// line appends a journal line for JIG-1, stamped a second after the last.
func (w *world) line(l journal.Line) {
	w.t.Helper()
	w.n++
	if l.TS == "" {
		l.TS = w.start.Add(time.Duration(w.n) * time.Second).Format(time.RFC3339Nano)
	}
	if err := journal.Append(w.st, "JIG-1", l); err != nil {
		w.t.Fatal(err)
	}
}

// fail appends a failed oracle line of slice with the signature sig.
func (w *world) fail(slice, sig string, attempt int) {
	w.t.Helper()
	w.line(journal.Line{Slice: slice, Event: journal.EventOracle, Outcome: "fail", Signature: sig, Attempt: attempt,
		Commit: "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00", Summary: "FAIL " + sig, Source: "jig"})
}

// journalLines reads the ticket journal.
func (w *world) journalLines() []journal.Line {
	w.t.Helper()
	lines, err := journal.Read(w.st, "JIG-1")
	if err != nil {
		w.t.Fatal(err)
	}
	return lines
}

// corrections lists the correction lines of the journal.
func (w *world) corrections() []journal.Line {
	var out []journal.Line
	for _, l := range w.journalLines() {
		if l.Event == journal.EventCorrection {
			out = append(out, l)
		}
	}
	return out
}

// input is what a hook of session s-1 gives Check, which is run in the clone.
func (w *world) input() Input {
	return Input{
		Store:   w.st,
		Ticket:  "JIG-1",
		Session: "s-1",
		Harness: "claude",
		Cwd:     w.clone,
		Now:     time.Now(),
		Since:   w.start,
		Journal: func() ([]journal.Line, error) { return journal.Read(w.st, "JIG-1") },
	}
}

// sliceState reads the state of slice id.
func (w *world) sliceState(id string) store.SliceState {
	w.t.Helper()
	ss, err := w.st.ReadSliceState("JIG-1", id)
	if err != nil {
		w.t.Fatal(err)
	}
	return ss
}

// refs lists the Refs of notes.
func refs(notes []Note) []string {
	out := make([]string, len(notes))
	for i, n := range notes {
		out[i] = n.Ref
	}
	return out
}
