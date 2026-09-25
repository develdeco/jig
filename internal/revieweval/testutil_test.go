package revieweval

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/session"
)

// writeFile writes content to path (creating its directory as needed),
// failing the test on any error: the small-corpus-dir builder every loader
// test uses to build a tiny case under t.TempDir().
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("revieweval: mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("revieweval: write %s: %v", path, err)
	}
}

// validGoldYAML is one round's minimal, valid gold.yaml: a single gold
// finding a-loader test can mutate one field of at a time.
const validGoldYAML = `exhaustive: false
findings:
  - id: f1
    file: a.go
    lines: [10, 10]
    action: fix
    description: a seeded problem
`

// newMinimalCase builds a one-round case directory under t.TempDir():
// brief.md and round-1/{patch.diff,gold.yaml}, gold.yaml given verbatim so
// a loader test can hand it a mutated body. Returns the case directory.
func newMinimalCase(t *testing.T, name, gold string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(dir, "brief.md"), "# brief\n")
	writeFile(t, filepath.Join(dir, "round-1", "patch.diff"), "diff --git a/a.go b/a.go\n")
	writeFile(t, filepath.Join(dir, "round-1", "gold.yaml"), gold)
	return dir
}

// caseNameForRunID reverses runID for the scripted backends' benefit: a
// live dispatch carries only the opaque run id (Dispatch.Ticket is
// RoundInput.Ticket, never the case name), but a fixture set is still laid
// out on disk by the case's real name (testdata/results/<set>/<case>/...),
// which the corpus agent's fixtures own and this package never renames.
// Rather than thread a side-channel name mapping through every backend,
// this recomputes runID over each entry already under dir and returns the
// one that matches id - the same thing a person auditing a run id back to
// a case would do, and the only approach that needs no change to how a
// fixture set is laid out.
func caseNameForRunID(dir, id string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("revieweval: caseNameForRunID: read %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() && runID(e.Name()) == id {
			return e.Name(), nil
		}
	}
	return "", fmt.Errorf("revieweval: caseNameForRunID: no case under %s maps to run id %s", dir, id)
}

// scriptedReviewerBackend plays back a scripted result.json for each
// dispatch, keyed by the case name behind Dispatch.Ticket's own opaque run
// id (caseNameForRunID) and Dispatch.Attempt (the round): it reads
// "<dir>/<case>/round-<attempt>.json" and copies it verbatim to
// d.ResultJSON. A missing fixture is an error, never a silent empty
// result. It never touches the dispatch's worktree, matching a real
// reviewer that makes no edits.
type scriptedReviewerBackend struct {
	dir string
}

func (b scriptedReviewerBackend) Run(d session.Dispatch) error {
	caseName, err := caseNameForRunID(b.dir, d.Ticket)
	if err != nil {
		return err
	}
	path := filepath.Join(b.dir, caseName, fmt.Sprintf("round-%d.json", d.Attempt))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("revieweval: scriptedReviewerBackend: no scripted result at %s: %w", path, err)
	}
	return os.WriteFile(d.ResultJSON, data, 0o644)
}

// errorBackend always fails a dispatch: an infrastructure failure, not an
// invalid result.
type errorBackend struct{ err error }

func (b errorBackend) Run(d session.Dispatch) error { return b.err }

// scriptedJudge returns a fixed verdict list for every Confirm call: a
// test-local stand-in that pins the exact verdict a matcher test wants for
// each candidate, in the order the round built them.
type scriptedJudge struct {
	verdicts []Verdict
	err      error
}

func (j scriptedJudge) Confirm(q JudgeQuery) ([]Verdict, error) {
	if j.err != nil {
		return nil, j.err
	}
	return j.verdicts, nil
}
