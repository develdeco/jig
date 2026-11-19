package e2e

// End-to-end test of the worktree advice (ADR 0039) for a harness that makes
// the ticket's worktree by its own method: jig advises an add until the session
// renames the harness's branch to the ticket's, and from then on `jig next`
// finds the worktree by its branch and `jig here` run in it binds the ticket.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// wtRow is one row of the worktrees `jig next --json` gives.
type wtRow struct {
	Repo    string `json:"repo"`
	Action  string `json:"action"`
	Branch  string `json:"branch"`
	Clone   string `json:"clone"`
	Dir     string `json:"dir"`
	Base    string `json:"base"`
	Command string `json:"command"`
	Note    string `json:"note"`
}

// wtNextOut is the part of `jig next <ticket> --json` these tests read.
type wtNextOut struct {
	State  string `json:"state"`
	Branch string `json:"branch"`
	Phase  *struct {
		Name  string `json:"name"`
		Ready bool   `json:"ready"`
		Code  string `json:"code"`
	} `json:"phase"`
	Worktrees []wtRow `json:"worktrees"`
}

// rowIn is the row of repo in rows.
func rowIn(t *testing.T, rows []wtRow, repo string) wtRow {
	t.Helper()
	for _, r := range rows {
		if r.Repo == repo {
			return r
		}
	}
	t.Fatalf("no worktree row for %s in %+v", repo, rows)
	return wtRow{}
}

// approveBriefOf records a person's approval of the brief of ticket id as it is
// on disk, so its phase is build.
func approveBriefOf(t *testing.T, st *store.Store, id string) {
	t.Helper()
	sum, err := st.BriefSHA256(id)
	if err != nil {
		t.Fatal(err)
	}
	a := store.Approval{By: "Test Person", At: time.Now().UTC().Format(time.RFC3339), BriefSHA256: sum, Provenance: "person"}
	if err := st.WriteApproval(id, a); err != nil {
		t.Fatal(err)
	}
}

// TestHarnessWorktreeRenamedIsFound: a harness makes the ticket's worktree by
// its own method, on a branch it names itself; jig advises an add until the
// session renames that branch to the ticket's (`git branch -m`, inside the
// worktree), and from then on `jig next` finds the worktree by its branch,
// present at its directory, and `jig here` run in it binds the ticket.
func TestHarnessWorktreeRenamedIsFound(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	approveBriefOf(t, st, fx.Ticket)

	n := nextJSON[wtNextOut](t, env, fx.RepoDir, fx.Ticket)
	if row := rowIn(t, n.Worktrees, "fixture-repo"); row.Action != "add" || row.Branch == "" {
		t.Fatalf("before any worktree: %+v, want an add that names the branch", row)
	}
	branch := n.Branch

	// The harness's worktree: a folder of its own choosing, a branch of its own.
	dir := filepath.Join(t.TempDir(), "harness-made")
	gitLog(t, fx.RepoDir, "worktree", "add", "-b", "harness/agent-1", dir)
	n = nextJSON[wtNextOut](t, env, fx.RepoDir, fx.Ticket)
	if row := rowIn(t, n.Worktrees, "fixture-repo"); row.Action != "add" {
		t.Fatalf("on the harness's own branch: %+v, want the add still advised", row)
	}

	// The tie: rename the branch inside the worktree.
	gitLog(t, dir, "branch", "-m", branch)
	n = nextJSON[wtNextOut](t, env, fx.RepoDir, fx.Ticket)
	row := rowIn(t, n.Worktrees, "fixture-repo")
	if row.Action != "present" || !gitx.SameDir(row.Dir, dir) || row.Command != "" {
		t.Fatalf("after the rename: %+v, want present at %s with no command", row, dir)
	}
	if text := runJig(t, env, fx.RepoDir, "next", fx.Ticket).Stdout; strings.Contains(text, "Add the worktree") || !strings.Contains(text, "fixture-repo,present,") {
		t.Errorf("jig next after the rename:\n%s", text)
	}

	if here := hereJSON(t, env, dir); here.Ticket.ID != fx.Ticket {
		t.Errorf("jig here in the renamed worktree binds %q, want %s", here.Ticket.ID, fx.Ticket)
	}
}
