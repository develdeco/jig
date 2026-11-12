package verifydeliver

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/session"
)

// holdGitFiles opens every file under a git directory's objects of fx, the way
// a git process reading them has them open: on Windows an open file cannot be
// deleted or renamed through any name of it, so a file another fixture shares
// with fx cannot be either.
func holdGitFiles(t *testing.T, fx *fixture.Fixture) int {
	t.Helper()
	var held []*os.File
	t.Cleanup(func() {
		for _, f := range held {
			f.Close()
		}
	})
	err := filepath.WalkDir(fx.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.Contains(filepath.ToSlash(p), "/objects/") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		held = append(held, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(held)
}

// TestAGateRunsWhileAnotherFixtureHoldsItsGitFiles: a gate in one fixture, and
// the removal of its directories when the test ends, work while another
// fixture has every object file open. Fixtures made from one template link
// their loose objects, so each of those files has a name in both; Windows
// refuses to delete or rename an open file only through the name it was
// opened by, and this test keeps that so.
func TestAGateRunsWhileAnotherFixtureHoldsItsGitFiles(t *testing.T) {
	t.Parallel()

	other := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	if n := holdGitFiles(t, other); n == 0 {
		t.Fatal("the other fixture has no object files to hold")
	}

	t.Run("a gate beside it", func(t *testing.T) {
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, fx, "rung-a")
		d := newDeps(t, fx)
		backend := stubBackend{run: func(sd session.Dispatch) error {
			req := readReviewRequest(t, sd.SliceJSON)
			return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, ReviewResult{ReviewedPaths: req.MustReview, Summary: "clean"}), 0o644)
		}}
		if _, err := Gate(d, newReviewerGateSource(backend), GateOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Gate: %v", err)
		}
	})
}
