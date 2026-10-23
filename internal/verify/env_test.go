package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// TestRunBringsUpTheSlicesEnvClass: slice d names the env class rig, so the
// oracle runs with it up, the oracle line says which, and the frontier's
// env-up and env-down lines bracket the run.
func TestRunBringsUpTheSlicesEnvClass(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.commit("d", "d: rig smoke", map[string]string{"beta/smoke.txt": "x"})
	stateFile := filepath.Join(r.fx.Dir, "rig-state.txt")
	var upDuringOracle bool
	r.deps.Oracle = func(cmd, dir string, env []string, limit time.Duration) (string, error) {
		_, err := os.Stat(stateFile)
		upDuringOracle = err == nil
		return "", nil
	}

	res, err := Run(r.deps, r.request("d", sha))
	if err != nil || res.State != journal.VerifyPass || res.Env != "rig" {
		t.Fatalf("Run = %+v, %v, want a pass with rig", res, err)
	}
	if !upDuringOracle {
		t.Error("the rig env class was not up while the oracle ran")
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("the rig env class was left up: %v", err)
	}
	lines := r.lines()
	want := []string{"verify-pending", "env-up", "oracle:pass", "verified", "env-down"}
	if got := events(lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("journal events = %v, want %v", got, want)
	}
	if lines[2].Env != "rig" || lines[2].Slice != "d" {
		t.Errorf("oracle line = %+v, want Env rig", lines[2])
	}
	// The env lines are the shape the frontier gave them.
	for _, l := range []journal.Line{lines[1], lines[4]} {
		if l.Slice != "d" || l.Commit != "" || l.Attempt != 0 || l.Session != "" {
			t.Errorf("env line = %+v, want the slice and the event alone", l)
		}
	}
}

// TestRunParksTheSliceWhenItsEnvClassWillNotComeUp: the frontier's handling,
// kept: an env-unavailable line with the class's policy, the slice
// env-blocked, and no oracle run.
func TestRunParksTheSliceWhenItsEnvClassWillNotComeUp(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{EnvFail: true})
	sha := r.commit("d", "d: rig smoke", map[string]string{"beta/smoke.txt": "x"})

	res, err := Run(r.deps, r.request("d", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyError || !strings.Contains(res.Summary, `the env class "rig" is unavailable`) {
		t.Errorf("Result = %+v, want an error naming the env class", res)
	}
	if got, want := events(r.lines()), []string{"verify-pending", "env-unavailable:defer-ci", "verify-error:error"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events = %v, want %v", got, want)
	}
	if st := r.state("d"); st.State != "env-blocked" || st.Reason != "env-up-failed" || st.Attempts != 1 {
		t.Errorf("slice d state = %+v, want env-blocked (env-up-failed) after 1 attempt", st)
	}
	if n := len(r.oracle.seen()); n != 0 {
		t.Errorf("the oracle ran %d times, want never", n)
	}
}

// TestRunRecordsWhatTheScenariosWrote: the oracle run is given a directory in
// JIG_RECORD_DIR, a pass journals what was written there as the recorded
// line (between the oracle line and the verified one, as the frontier orders
// them), and each run has a directory of its own named <slice>-v<attempt>.
func TestRunRecordsWhatTheScenariosWrote(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.files = map[string]string{
		"login.svg":      "<svg xmlns='http://www.w3.org/2000/svg'><title>login</title></svg>",
		"login.svg.json": `{"scenario":"login","caption":"the login page"}`,
		"notes.txt":      "not a recording",
	}
	sha := r.fixClamp()
	id, err := r.st.ID()
	if err != nil {
		t.Fatal(err)
	}

	if res, err := Run(r.deps, r.request("a", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if got, want := events(r.lines()), []string{"verify-pending", "oracle:pass", "recorded:dropped: notes.txt", "verified"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("journal events = %v, want %v", got, want)
	}
	rec := r.lines()[2]
	if rec.Slice != "a" || rec.Commit != sha || rec.Attempt != 1 || rec.RecordRun != "a-v1" || rec.Outcome != "dropped: notes.txt" {
		t.Errorf("recorded line = %+v", rec)
	}
	if len(rec.Recordings) != 1 || rec.Recordings[0].File != "login.svg" || rec.Recordings[0].Scenario != "login" || rec.Recordings[0].Caption != "the login page" {
		t.Errorf("Recordings = %+v, want login.svg tagged login", rec.Recordings)
	}
	dir, err := home.RecordDir(r.fx.Home, id, r.fx.Ticket, sha, "a-v1")
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(r.oracle.seen()[0].env, recordDirEnv); filepath.Clean(got) != filepath.Clean(dir) {
		t.Errorf("%s = %q, want %q", recordDirEnv, got, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "login.svg")); err != nil {
		t.Errorf("the recording is not where the journal says: %v", err)
	}

	// A rerun records into a directory of its own and leaves the first alone.
	rq := r.request("a", sha)
	rq.Rerun = true
	if _, err := Run(r.deps, rq); err != nil {
		t.Fatal(err)
	}
	second := r.lines()[len(r.lines())-2]
	if second.Event != "recorded" || second.RecordRun != "a-v2" || second.Attempt != 2 {
		t.Errorf("rerun's recorded line = %+v, want run a-v2 of attempt 2", second)
	}
	if _, err := os.Stat(filepath.Join(dir, "login.svg")); err != nil {
		t.Errorf("the first run's recording is gone after the rerun: %v", err)
	}
}

// TestRunDiscardsAFailedRunsRecordings: only a green scenario is evidence.
func TestRunDiscardsAFailedRunsRecordings(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.files = map[string]string{"login.svg": "<svg xmlns='http://www.w3.org/2000/svg'/>"}
	r.oracle.err, r.oracle.out = errExit, "FAIL"
	sha := r.commit("b", "b", map[string]string{"x.txt": "1"})
	id, _ := r.st.ID()

	if res, err := Run(r.deps, r.request("b", sha)); err != nil || res.State != journal.VerifyFail {
		t.Fatalf("Run = %+v, %v, want a fail", res, err)
	}
	for _, l := range r.lines() {
		if l.Event == journal.EventRecorded {
			t.Errorf("a failed run journaled recordings: %+v", l)
		}
	}
	dir, _ := home.RecordDir(r.fx.Home, id, r.fx.Ticket, sha, "b-v1")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the failed run's recording directory is still there: %v", err)
	}
}
