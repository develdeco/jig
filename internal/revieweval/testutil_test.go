package revieweval

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// corpusRoot is the repo-root testdata/revieweval corpus directory.
func corpusRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(fixture.RepoRoot(t), "testdata", "revieweval")
}

// loadCorpus loads the whole real corpus, failing the test on any load
// error.
func loadCorpus(t *testing.T) []Case {
	t.Helper()
	cases, err := LoadCorpus(corpusRoot(t))
	if err != nil {
		t.Fatalf("revieweval: load corpus: %v", err)
	}
	return cases
}

// caseByName returns the named case from cases, failing the test when it
// is not present.
func caseByName(t *testing.T, cases []Case, name string) Case {
	t.Helper()
	for _, c := range cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("revieweval: no case %q in corpus", name)
	return Case{}
}

// scriptedBackend plays back a scripted result.json for each dispatch,
// keyed by Dispatch.Ticket (RunCase sets Ticket to the case name): it
// reads "<dir>/<ticket>.json" and copies it verbatim to d.ResultJSON. It
// never touches the dispatch's worktree, matching a real reviewer that
// makes no edits.
type scriptedBackend struct {
	dir string
}

func (b scriptedBackend) Run(d session.Dispatch) error {
	data, err := os.ReadFile(filepath.Join(b.dir, d.Ticket+".json"))
	if err != nil {
		return fmt.Errorf("revieweval: scriptedBackend: no scripted result for %s: %w", d.Ticket, err)
	}
	return os.WriteFile(d.ResultJSON, data, 0o644)
}

// invalidResultBackend writes a result.json that ParseReviewResult must
// reject (no "verdict" field at all), so callers can exercise RunCase's
// invalid-result path without a real backend.
type invalidResultBackend struct{}

func (invalidResultBackend) Run(d session.Dispatch) error {
	return os.WriteFile(d.ResultJSON, []byte(`{"outcome":"failed","summary":"not a review result"}`), 0o644)
}

// parsedResult parses a literal result.json body into a
// verifydeliver.ReviewResult via the real strict parser, failing the test
// if it is rejected.
func parsedResult(t *testing.T, body string) verifydeliver.ReviewResult {
	t.Helper()
	result, err := verifydeliver.ParseReviewResult([]byte(body))
	if err != nil {
		t.Fatalf("revieweval: parsedResult: %v", err)
	}
	return result
}
