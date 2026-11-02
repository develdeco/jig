package e2e

// End-to-end tests of the worktree advice (ADR 0039): jig makes no worktree and
// has no verb for one, it advises. `jig next <ticket>` gives, for each repo the
// ticket builds in, the git command that adds the ticket's worktree beside the
// clone, and later the command that removes it; `jig next <chart>` carries the
// advice for each ticket of the run; `jig publish --pr` says the removal the
// moment the last pull request is recorded. Each test runs the commands jig
// prints, as the session would, and asks again.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
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

// wtPath is a path the way jig writes it in a row: forward slashes.
func wtPath(p string) string { return filepath.ToSlash(filepath.Clean(p)) }

// wtQuoted is wtPath wrapped in double quotes when it has whitespace, as jig
// writes it in a command.
func wtQuoted(p string) string {
	p = wtPath(p)
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}

// runAdvised runs the git command of a row the way the session would: split
// into words as a shell does, and run through git.
func runAdvised(t *testing.T, command string) {
	t.Helper()
	argv, err := splitCommand(command)
	if err != nil || len(argv) < 2 || argv[0] != "git" {
		t.Fatalf("the advised command %q is not a git command: %v", command, err)
	}
	if out, err := gitx.Run(t.TempDir(), argv[1:]...); err != nil {
		t.Fatalf("the advised command %q failed: %v\n%s", command, err, out)
	}
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

// cleanRound writes a clean report for round 1 of ticket id, the word the phase
// machinery takes for "the gate has a clean round".
func cleanRound(t *testing.T, st *store.Store, id string) {
	t.Helper()
	dir := filepath.Join(st.TicketDir(id), "gate", "round-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(dir, "report.yaml"), []byte("verdict: clean\n"))
}

// TestNextAdvisesAWorktreePerRepo: a ticket stacked on a parent builds nowhere
// until the parent's gate has a clean round, so jig advises no worktree; then
// `jig next` gives, for each of the two repos it builds in, the add beside that
// repo's clone, the parent's branch being the base in the repo they share and
// the target in the other. Running the advised command makes the worktree at the
// parent's head on the ticket's branch, and the row becomes present.
func TestNextAdvisesAWorktreePerRepo(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{SecondRepo: true})
	h := newHarness(t, fx, env, "s-wt")
	st, parentHead := stackOnAParent(t, fx, h)
	approveBriefOf(t, st, fx.Ticket)
	h.Push(parentBranch)
	if err := journal.Append(st, "JIG-2", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}

	// The parent has no clean round: the child's build is held, and a held build
	// has no worktree yet.
	n := nextJSON[wtNextOut](t, env, fx.RepoDir, fx.Ticket)
	if n.Phase == nil || n.Phase.Name != "build" || n.Phase.Code != "PHASE_BLOCKED" || len(n.Worktrees) != 0 {
		t.Fatalf("before the parent has a clean round: phase %+v, worktrees %+v, want a held build and no worktree", n.Phase, n.Worktrees)
	}
	if text := runJig(t, env, fx.RepoDir, "next", fx.Ticket).Stdout; strings.Contains(text, "worktrees[") || strings.Contains(text, "Add the worktree") {
		t.Errorf("jig next advises a worktree for a held build:\n%s", text)
	}

	cleanRound(t, st, "JIG-2")
	n = nextJSON[wtNextOut](t, env, fx.RepoDir, fx.Ticket)
	if n.Phase == nil || !n.Phase.Ready || len(n.Worktrees) != 2 {
		t.Fatalf("after the clean round: phase %+v, worktrees %+v, want a build that is ready and two rows", n.Phase, n.Worktrees)
	}
	shared, own := rowIn(t, n.Worktrees, "fixture-repo"), rowIn(t, n.Worktrees, "second-repo")
	if n.Worktrees[0].Repo != "fixture-repo" || n.Worktrees[1].Repo != "second-repo" {
		t.Errorf("rows are in the order %s, %s, want the project's", n.Worktrees[0].Repo, n.Worktrees[1].Repo)
	}
	sharedDir, ownDir := wtPath(fx.RepoDir)+".worktrees/"+fx.Ticket, wtPath(fx.SecondRepoDir)+".worktrees/"+fx.Ticket
	wantShared := wtRow{
		Repo: "fixture-repo", Action: "add", Branch: n.Branch, Clone: wtPath(fx.RepoDir), Dir: sharedDir, Base: parentBranch,
		Command: "git -C " + wtQuoted(fx.RepoDir) + " worktree add " + wtQuoted(sharedDir) + " -b " + n.Branch + " " + parentBranch,
	}
	wantOwn := wtRow{
		Repo: "second-repo", Action: "add", Branch: n.Branch, Clone: wtPath(fx.SecondRepoDir), Dir: ownDir, Base: "origin/main",
		Command: "git -C " + wtQuoted(fx.SecondRepoDir) + " worktree add " + wtQuoted(ownDir) + " -b " + n.Branch + " origin/main",
	}
	if shared != wantShared {
		t.Errorf("fixture-repo = %+v, want %+v", shared, wantShared)
	}
	if own != wantOwn {
		t.Errorf("second-repo = %+v, want %+v", own, wantOwn)
	}
	if sharedDir == ownDir {
		t.Errorf("both repos share the folder %s", sharedDir)
	}

	text := runJig(t, env, fx.RepoDir, "next", fx.Ticket).Stdout
	for _, want := range []string{
		"worktrees[2]{repo,action,dir,base,command}:",
		"Add the worktree of JIG-1 in fixture-repo: `" + shared.Command + "`",
		"Add the worktree of JIG-1 in second-repo: `" + own.Command + "`",
		"git switch -c " + n.Branch,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("jig next JIG-1 lacks %q:\n%s", want, text)
		}
	}

	// The session runs the command jig printed: the worktree is the parent's
	// head, on the ticket's branch.
	runAdvised(t, shared.Command)
	if head := gitLog(t, sharedDir, "rev-parse", "HEAD"); head != parentHead {
		t.Errorf("the new worktree is at %s, want the parent's head %s", head, parentHead)
	}
	if branch := gitLog(t, sharedDir, "rev-parse", "--abbrev-ref", "HEAD"); branch != n.Branch {
		t.Errorf("the new worktree is on %q, want %q", branch, n.Branch)
	}
	n = nextJSON[wtNextOut](t, env, fx.RepoDir, fx.Ticket)
	now := rowIn(t, n.Worktrees, "fixture-repo")
	if now.Action != "present" || !gitx.SameDir(now.Dir, sharedDir) || now.Command != "" || now.Base != "" {
		t.Errorf("fixture-repo after the add = %+v, want present at %s with no command", now, sharedDir)
	}
	if still := rowIn(t, n.Worktrees, "second-repo"); still.Action != "add" {
		t.Errorf("second-repo = %+v, want its add still advised", still)
	}
	after := runJig(t, env, fx.RepoDir, "next", fx.Ticket).Stdout
	if strings.Contains(after, "Add the worktree of JIG-1 in fixture-repo") || !strings.Contains(after, "Add the worktree of JIG-1 in second-repo") {
		t.Errorf("jig next after the first add:\n%s", after)
	}

	// The fixture's second clone never fetched, as a clone people use has: it has
	// no origin/main to start from until it does.
	gitLog(t, fx.SecondRepoDir, "fetch", "-q", "origin")
	runAdvised(t, own.Command)
	if head, want := gitLog(t, ownDir, "rev-parse", "HEAD"), gitLog(t, fx.SecondRepoDir, "rev-parse", "origin/main"); head != want {
		t.Errorf("the second repo's worktree is at %s, want origin/main at %s", head, want)
	}
	n = nextJSON[wtNextOut](t, env, fx.RepoDir, fx.Ticket)
	for _, repo := range []string{"fixture-repo", "second-repo"} {
		if got := rowIn(t, n.Worktrees, repo); got.Action != "present" {
			t.Errorf("%s after both adds = %+v, want present", repo, got)
		}
	}
	if text := runJig(t, env, fx.RepoDir, "next", fx.Ticket).Stdout; strings.Contains(text, "Add the worktree") {
		t.Errorf("jig next still advises an add once both worktrees are there:\n%s", text)
	}
}
