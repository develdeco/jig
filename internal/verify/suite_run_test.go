package verify

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// suiteManifest declares the suite oracle beside the test oracle.
const suiteManifest = `workspaces:
  - id: alpha
    path: alpha
  - id: beta
    path: beta
oracles:
  test: "echo test {path}"
  suite: "echo the-whole-suite {path}"
`

// issueRig is a rig whose ticket is an issue, with manifest committed when it
// is not empty.
func issueRig(t *testing.T, manifest string) (*rig, string) {
	t.Helper()
	r := newRig(t, fixture.Opts{})
	if err := r.st.SetTicketKind(r.fx.Ticket, store.KindIssue, ""); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"x.txt": "x"}
	if manifest != "" {
		files[".claude/jig.yaml"] = manifest
	}
	return r, r.commit("", "declare", files)
}

// TestSuiteRunRunsTheDeclaredSuiteForAnIssue: an issue's final run is the
// repo's suite oracle resolved against the root workspace, with no slices, and
// records one final-oracle line without slices at the commit.
func TestSuiteRunRunsTheDeclaredSuiteForAnIssue(t *testing.T) {
	t.Parallel()
	r, sha := issueRig(t, suiteManifest)

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	calls := r.oracle.seen()
	if len(calls) != 1 || calls[0].cmd != "echo the-whole-suite ." {
		t.Fatalf("the oracle ran %+v, want the suite once", calls)
	}
	if res.State != journal.VerifyPass || !res.Suite || len(res.Slices) != 0 || !res.Covered {
		t.Errorf("Result = %+v, want a covering suite pass with no slices", res)
	}
	line, ok := journal.FinalRunAt(r.lines(), "", sha)
	if !ok || line.Outcome != "pass" || len(line.Slices) != 0 || line.Command != "echo the-whole-suite ." {
		t.Errorf("FinalRunAt = %+v, %v, want the pass without slices", line, ok)
	}
}

// TestSuiteRunWithoutADeclarationIsUnrun: a repo with no suite has nothing to
// run; the run says so, writes nothing and runs nothing.
func TestSuiteRunWithoutADeclarationIsUnrun(t *testing.T) {
	t.Parallel()
	r, _ := issueRig(t, "")

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	if res.State != journal.VerifyError || res.Code != ExitUnrun || !strings.Contains(res.Summary, "no suite declared in") {
		t.Errorf("Result = %+v, want an unrun naming the missing suite", res.Result)
	}
	if n := len(r.oracle.seen()); n != 0 {
		t.Errorf("the oracle ran %d times, want 0", n)
	}
	if got := events(r.lines()); len(got) != 0 {
		t.Errorf("journal = %v, want nothing", got)
	}
}

// TestSuiteRunIsNotReadForATask: a task's final run never resolves the suite
// name, so a declared suite changes nothing for it.
func TestSuiteRunIsNotReadForATask(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.commit("a", "declare", map[string]string{".claude/jig.yaml": suiteManifest})

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	if res.Suite || len(res.Slices) == 0 || strings.Contains(res.Command, "the-whole-suite") {
		t.Errorf("Result = %+v, want the slice union and no suite", res)
	}
}
