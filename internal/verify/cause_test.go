package verify

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestOracleFailLineNamesItsCause: a run the wedge guard killed and a run jig's
// own stop ended each journal a fail line that names why, and a run that failed
// with test output journals none.
func TestOracleFailLineNamesItsCause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		err   error
		cause string
	}{
		{"timeout", timedOut("go test ./..."), journal.CauseTimeout},
		{"stopped", envrun.Stopped("go test ./..."), journal.CauseStopped},
		{"test failure", os.ErrInvalid, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			r.oracle.out, r.oracle.err = "--- FAIL: TestX\n", c.err
			sha := r.commit("b", "b: one", map[string]string{"x.txt": "1"})
			// A stopped run ends with the stop error; the others with a fail.
			_, _ = Run(r.deps, r.request("b", sha))
			var fails []journal.Line
			for _, l := range r.lines() {
				if l.Event == journal.EventOracle && l.Outcome == "fail" {
					fails = append(fails, l)
				}
			}
			if len(fails) != 1 {
				t.Fatalf("fail lines = %+v, want one", fails)
			}
			if fails[0].Cause != c.cause {
				t.Errorf("Cause = %q, want %q", fails[0].Cause, c.cause)
			}
		})
	}
}

// TestAStoppedRunLeavesNoRecordingDirectory: a verification whose oracle was
// ended by jig's stop removes the directory it made for the run's recordings
// and records nothing; a finished failure does the same, and a finished pass
// keeps what the scenarios wrote.
func TestAStoppedRunLeavesNoRecordingDirectory(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		err      error
		wantKept bool
	}{
		{"stopped", envrun.Stopped("go test ./..."), false},
		{"failed", os.ErrInvalid, false},
		{"passed", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			r.oracle.err = c.err
			r.oracle.files = map[string]string{"login.svg": loginSVG}
			sha := r.commit("b", "b: one", map[string]string{"x.txt": "1"})
			_, _ = Run(r.deps, r.request("b", sha))
			var runs []string
			_ = filepath.WalkDir(filepath.Join(r.deps.JigHome, "evidence"), func(p string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					runs = append(runs, p)
				}
				return nil
			})
			var dirs []string
			_ = filepath.WalkDir(filepath.Join(r.deps.JigHome, "evidence"), func(p string, d fs.DirEntry, err error) error {
				if err == nil && d.IsDir() && strings.Contains(filepath.Base(p), "-v") {
					dirs = append(dirs, p)
				}
				return nil
			})
			if c.wantKept && len(runs) == 0 {
				t.Errorf("a passing run kept no recording")
			}
			if !c.wantKept && (len(runs) != 0 || len(dirs) != 0) {
				t.Errorf("recording leftovers %v %v, want none", runs, dirs)
			}
		})
	}
}
