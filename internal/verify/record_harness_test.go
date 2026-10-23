package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/outcome"
)

// The recording contract (ADR 0029, step 3), as `jig verify` keeps it: the
// oracle run is handed a directory under the jig home in JIG_RECORD_DIR, the
// recordings it leaves there are verified when the run passes and journaled
// with the commit it ran at and the run that wrote them. These tests are the
// frontier's own, kept against the copy.

const (
	loginSVG   = "<svg xmlns='http://www.w3.org/2000/svg'><title>login</title></svg>"
	payPNG     = "\x89PNG pretend bytes of a screenshot"
	payTagJSON = `{"scenario":"checkout","flow":"purchase","step":2,"caption":"pays for the cart"}`
)

// oracleRun is one call the oracle stub received.
type oracleRun struct {
	env       []string
	recordDir string // JIG_RECORD_DIR in env, "" when it is not set
}

// recordingOracle is an oracle stub that writes files into the directory its
// env names, like a scenario that records, and then passes or fails.
type recordingOracle struct {
	files map[string]string // name -> content, written when a directory is given
	dirs  []string          // subdirectories made beside them
	fail  bool
	// act, when set, runs first with the record directory, as a harness that
	// replaces the directory or its parents would.
	act func(recordDir string) error

	mu   sync.Mutex
	runs []oracleRun
}

func (o *recordingOracle) run(env []string) error {
	rd := envValue(env, recordDirEnv)
	o.mu.Lock()
	o.runs = append(o.runs, oracleRun{env: env, recordDir: rd})
	o.mu.Unlock()
	if rd != "" {
		if o.act != nil {
			if err := o.act(rd); err != nil {
				return err
			}
		}
		for name, content := range o.files {
			if err := os.WriteFile(filepath.Join(rd, name), []byte(content), 0o644); err != nil {
				return err
			}
		}
		for _, name := range o.dirs {
			if err := os.Mkdir(filepath.Join(rd, name), 0o755); err != nil {
				return err
			}
		}
	}
	if o.fail {
		return errors.New("exit status 1")
	}
	return nil
}

func (o *recordingOracle) seen() []oracleRun {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]oracleRun(nil), o.runs...)
}

// recordRun is one verification's recording, driven the way Run drives it
// (start, the oracle, finish) with the journal captured.
type recordRun struct {
	t       *testing.T
	rec     recorder
	oracle  *recordingOracle
	sha     string
	home    string
	storeID string

	mu    sync.Mutex
	lines []journal.Line
}

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func newRecordRun(t *testing.T, o *recordingOracle) *recordRun {
	t.Helper()
	r := &recordRun{t: t, oracle: o, sha: testSHA, home: t.TempDir(), storeID: "0011223344556677"}
	r.rec = recorder{home: r.home, storeID: r.storeID, ticket: "JIG_1", journal: func(l journal.Line) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.lines = append(r.lines, l)
		return nil
	}}
	return r
}

// atGreen records the oracle run of attempt 1, and atGreenAttempt that of
// attempt n: the oracle runs with the directory start made, and finish settles
// it. The result is green unless the oracle failed.
func (r *recordRun) atGreen() outcome.Result { return r.atGreenAttempt(1) }

func (r *recordRun) atGreenAttempt(n int) outcome.Result {
	r.t.Helper()
	target := r.rec.start("a", n, r.sha)
	err := r.oracle.run(target.env())
	if ferr := r.rec.finish("a", n, r.sha, target, err == nil); ferr != nil {
		r.t.Fatalf("finish: %v", ferr)
	}
	if err != nil {
		return outcome.Result{Outcome: outcome.CodeBug}
	}
	return outcome.Result{Outcome: outcome.Green}
}

// top is the evidence directory of the jig home.
func (r *recordRun) top() string { return filepath.Join(r.home, "evidence") }

// recordDir is where the named run's recordings belong.
func (r *recordRun) recordDir(run string) string {
	r.t.Helper()
	dir, err := home.RecordDir(r.home, r.storeID, "JIG_1", r.sha, run)
	if err != nil {
		r.t.Fatal(err)
	}
	return dir
}

// firstRun is the name of attempt 1's oracle run of slice a.
const firstRun = "a-v1"

func (r *recordRun) journaled() []journal.Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]journal.Line(nil), r.lines...)
}

// recordedLines returns the journal's "recorded" lines.
func recordedLines(lines []journal.Line) []journal.Line {
	var out []journal.Line
	for _, l := range lines {
		if l.Event == journal.EventRecorded {
			out = append(out, l)
		}
	}
	return out
}

func sum(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// asJSON is s as the inside of a JSON string: each backslash written as two,
// the way a Windows path appears in a JSON key or in a decoder's message.
func asJSON(s string) string { return strings.ReplaceAll(s, `\`, `\`) }

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s: Lstat err = %v, want it gone", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s: %v, want it left in place", path, err)
	}
}
