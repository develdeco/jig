package verify

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// repoName is the fixture project's one repo, the name start.<repo>.sha is
// kept under.
const repoName = "fixture-repo"

// wantNoOracleRun asserts the verification stopped before the oracle, with a
// verify-error that says why.
func wantNoOracleRun(t *testing.T, r *rig, res Result, err error, reason string) {
	t.Helper()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyError || !strings.Contains(res.Summary, reason) {
		t.Errorf("Result = %+v, want an error that says %q", res, reason)
	}
	if got, want := events(r.lines()), []string{"verify-error:error"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events = %v, want %v", got, want)
	}
	if last := r.lines()[len(r.lines())-1]; !strings.Contains(last.Summary, reason) || last.Commit != res.SHA {
		t.Errorf("verify-error line = %+v, want the reason and the commit", last)
	}
	if n := len(r.oracle.seen()); n != 0 {
		t.Errorf("the oracle ran %d times, want never", n)
	}
	if res.Code != ExitUnrun {
		t.Errorf("Code = %d, want %d: jig could not start the command", res.Code, ExitUnrun)
	}
	if st := r.state("a"); st.State != "queued" {
		t.Errorf("slice a state = %+v, want it left as it was", st)
	}
}

// TestRunHoldsTheCommitToTheStartSha: the commit must be new work on the
// ticket's branch, a descendant of the start sha the ticket recorded, and the
// frontier's verifyGreen is what says so. A commit that is the start sha, one
// that does not descend from it, and a start sha this clone does not have are
// still run and recorded, and cover nothing.
func TestRunHoldsTheCommitToTheStartSha(t *testing.T) {
	t.Parallel()
	t.Run("a descendant is covered", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		r.deps.Repo = repoName
		base, _ := gitx.RevParse(r.fx.RepoDir, "HEAD")
		if err := r.st.WriteStartSHA(r.fx.Ticket, repoName, base); err != nil {
			t.Fatal(err)
		}
		sha := r.fixClamp()
		if res, err := Run(r.deps, r.request("a", sha)); err != nil || res.State != journal.VerifyPass || !res.Covered {
			t.Fatalf("Run = %+v, %v, want a covering pass", res, err)
		}
	})
	for name, setup := range map[string]func(r *rig, sha string) string{
		"the start sha itself is no new work": func(r *rig, sha string) string {
			if err := r.st.WriteStartSHA(r.fx.Ticket, repoName, sha); err != nil {
				t.Fatal(err)
			}
			return sha
		},
		"a commit that does not descend from it": func(r *rig, sha string) string {
			later := r.commit("", "later", map[string]string{"later.txt": "x"})
			if err := r.st.WriteStartSHA(r.fx.Ticket, repoName, later); err != nil {
				t.Fatal(err)
			}
			return later
		},
		"a start sha this clone does not have": func(r *rig, sha string) string {
			if err := r.st.WriteStartSHA(r.fx.Ticket, repoName, "0123456789abcdef0123456789abcdef01234567"); err != nil {
				t.Fatal(err)
			}
			return sha
		},
	} {
		setup := setup
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			r.deps.Repo = repoName
			sha := setup(r, r.fixClamp())
			res, err := Run(r.deps, r.request("a", sha))
			if err != nil || res.Covered || res.Uncovered != journal.UncoveredNotDescend || !res.Ran {
				t.Fatalf("Run = %+v, %v, want a run that covers nothing: %s", res, err, journal.UncoveredNotDescend)
			}
			if got, want := events(r.lines()), []string{"oracle:pass"}; !reflect.DeepEqual(got, want) {
				t.Errorf("journal events = %v, want %v", got, want)
			}
		})
	}
}

// TestRunFindsTheStartShaFromTheTargetBranch: with no start sha recorded the
// commit is held to the merge base with origin's target branch in the clone,
// and with neither known it is held to nothing.
func TestRunFindsTheStartShaFromTheTargetBranch(t *testing.T) {
	t.Parallel()
	t.Run("a commit past origin's target passes", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		r.deps.Repo, r.deps.Target = repoName, "main"
		if _, err := gitx.Run(r.fx.RepoDir, "fetch", "origin"); err != nil {
			t.Fatal(err)
		}
		sha := r.fixClamp()
		if res, err := Run(r.deps, r.request("a", sha)); err != nil || res.State != journal.VerifyPass {
			t.Fatalf("Run = %+v, %v, want a pass", res, err)
		}
	})
	t.Run("origin's target tip is no new work", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		r.deps.Repo, r.deps.Target = repoName, "main"
		if _, err := gitx.Run(r.fx.RepoDir, "fetch", "origin"); err != nil {
			t.Fatal(err)
		}
		tip, _ := gitx.RevParse(r.fx.RepoDir, "HEAD")
		if res, err := Run(r.deps, r.request("a", tip)); err != nil || res.Covered || res.Uncovered != journal.UncoveredNotDescend {
			t.Fatalf("Run = %+v, %v, want a run that covers nothing: no new work", res, err)
		}
	})
	t.Run("no origin branch fetched, no recorded sha: no descent is checked", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		r.deps.Repo, r.deps.Target = repoName, "main"
		sha := r.fixClamp()
		if res, err := Run(r.deps, r.request("a", sha)); err != nil || res.State != journal.VerifyPass {
			t.Fatalf("Run = %+v, %v, want a pass", res, err)
		}
	})
}

// TestRunNeedsAnSh: the oracle and the env class's commands run through sh,
// so on a machine with none the oracle cannot be reached: the run ends in a
// verify-error that says why, and the commit is not failed for it.
func TestRunNeedsAnSh(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.deps.Sh = func() (string, error) {
		return "", errors.New("jig runs oracles and env class commands with sh, and found none")
	}
	sha := r.fixClamp()
	res, err := Run(r.deps, r.request("a", sha))
	wantNoOracleRun(t, r, res, err, "with sh, and found none")
}

// TestRunUsesTheManifestOfTheCommit: the oracle is the one the manifest at the
// verified commit resolves, and a timeout the manifest declares bounds
// nothing: the harness owns time.
func TestRunUsesTheManifestOfTheCommit(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	manifestPath := filepath.Join(r.fx.RepoDir, ".claude", "jig.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	declared := `workspaces:
  - id: alpha
    path: alpha
  - id: beta
    path: beta
oracles:
  test: "echo verified {path}"
timeout: 45m
`
	sha := r.commit("a", "declare a timeout", map[string]string{".claude/jig.yaml": declared})
	_ = data

	res, err := Run(r.deps, r.request("a", sha))
	if err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v, want a pass", res, err)
	}
	calls := r.oracle.seen()
	if len(calls) != 1 {
		t.Fatalf("the oracle ran %d times, want 1", len(calls))
	}
	if calls[0].limit != 0 {
		t.Errorf("limit = %s, want none: the manifest's 45m no longer bounds a run", calls[0].limit)
	}
	if calls[0].cmd != "echo verified alpha" {
		t.Errorf("command = %q, want the oracle the commit's manifest declares", calls[0].cmd)
	}
	if line := r.lines()[0]; line.Command != "echo verified alpha" {
		t.Errorf("oracle line command = %q", line.Command)
	}
}
