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
	"reflect"
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

// TestWorktreeAdviceEndsWithThePublish: a ticket built and reviewed clean is
// advised no add while a work tree holds its branch (the clone's own: present);
// once the clone switches away the add is the existing-branch form, and running
// it makes a linked worktree that is present. Recording the pull request
// finishes the publish, and its result says the removal; `jig next` says it
// after, until the command is run, and then nothing.
func TestWorktreeAdviceEndsWithThePublish(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, true)
	env, _ := githubClone(t, b)
	b.env = env
	b.builder.env = env
	approveBriefOf(t, b.st, b.fx.Ticket)
	b.openRound(t)
	b.reviewedClean(t, 1)
	clone := b.builder.Clone

	// The builder's clone is on the ticket's branch: that work tree is the
	// ticket's worktree, and jig advises no add that git would refuse.
	n := nextJSON[wtNextOut](t, env, clone, b.fx.Ticket)
	if len(n.Worktrees) != 1 {
		t.Fatalf("jig next: worktrees %+v, want one row", n.Worktrees)
	}
	if row := n.Worktrees[0]; row.Repo != "fixture-repo" || row.Action != "present" || !gitx.SameDir(row.Dir, clone) || row.Command != "" {
		t.Fatalf("with the clone on the ticket's branch: %+v, want present at the clone", row)
	}
	if text := runJig(t, env, clone, "next", b.fx.Ticket).Stdout; strings.Contains(text, "Add the worktree") || !strings.Contains(text, "fixture-repo,present,") {
		t.Errorf("jig next with the clone on the branch:\n%s", text)
	}

	// The clone goes back to main; the branch stays. The advice is to add a
	// worktree on the branch that exists, with no -b and no base.
	b.builder.Branch("main")
	n = nextJSON[wtNextOut](t, env, clone, b.fx.Ticket)
	add := rowIn(t, n.Worktrees, "fixture-repo")
	dir := wtPath(clone) + ".worktrees/" + b.fx.Ticket
	wantAdd := "git -C " + wtQuoted(clone) + " worktree add " + wtQuoted(dir) + " " + b.branch
	if add.Action != "add" || add.Command != wantAdd || add.Base != "" || add.Dir != dir {
		t.Fatalf("with the clone on main: %+v, want the existing-branch add %q", add, wantAdd)
	}
	runAdvised(t, add.Command)
	n = nextJSON[wtNextOut](t, env, clone, b.fx.Ticket)
	if row := rowIn(t, n.Worktrees, "fixture-repo"); row.Action != "present" || !gitx.SameDir(row.Dir, dir) || gitx.SameDir(row.Dir, clone) {
		t.Fatalf("after the add: %+v, want present at the linked worktree %s", row, dir)
	}

	// The builder pushes the branch and the pull request is recorded: the
	// record finishes the publish and says how to remove the worktree.
	b.builder.Push(b.branch)
	if body := runJig(t, env, clone, "publish", b.fx.Ticket, "--body"); body.Code != 0 {
		t.Fatalf("jig publish --body exit = %d\nstdout:\n%s\nstderr:\n%s", body.Code, body.Stdout, body.Stderr)
	}
	const prURL = "https://github.com/acme/fixture-repo/pull/7"
	rec := runJig(t, env.with("GH_STUB_HEAD_REF="+b.branch, "GH_STUB_HEAD_OID="+b.sha), clone, "publish", b.fx.Ticket, "--pr", prURL)
	if rec.Code != 0 || !strings.Contains(rec.Stdout, "verified: yes") {
		t.Fatalf("jig publish --pr exit = %d, want 0 and a verified pull request\nstdout:\n%s\nstderr:\n%s", rec.Code, rec.Stdout, rec.Stderr)
	}
	removeCmd := "git -C " + wtQuoted(clone) + " worktree remove " + wtQuoted(dir)
	removeLine := "Remove the worktree of JIG-1 in fixture-repo (git refuses one with changes): `" + removeCmd + "`"
	statusLine := "Run `jig status JIG-1` to confirm the ticket is fully green"
	for _, want := range []string{"publish_pr:", "worktrees[1]{repo,action,dir,base,command}:", "fixture-repo,remove,", removeLine} {
		if !strings.Contains(rec.Stdout, want) {
			t.Errorf("jig publish --pr lacks %q:\n%s", want, rec.Stdout)
		}
	}
	if table, prBlock, helpBlock := strings.Index(rec.Stdout, "worktrees[1]"), strings.Index(rec.Stdout, "publish_pr:"), strings.Index(rec.Stdout, "help["); !(prBlock < table && table < helpBlock) {
		t.Errorf("jig publish --pr must print the pull request, then the worktrees, then the help:\n%s", rec.Stdout)
	}
	if rm, st := strings.Index(rec.Stdout, removeLine), strings.Index(rec.Stdout, statusLine); rm < 0 || st < 0 || rm > st {
		t.Errorf("the remove line must come ahead of the status line:\n%s", rec.Stdout)
	}

	// jig next says the same remove while the worktree is there.
	n = nextJSON[wtNextOut](t, env, clone, b.fx.Ticket)
	row := rowIn(t, n.Worktrees, "fixture-repo")
	if n.State != "done" || len(n.Worktrees) != 1 || row.Action != "remove" || row.Command != removeCmd || !gitx.SameDir(row.Dir, dir) {
		t.Fatalf("jig next after the publish: state %q, worktrees %+v, want done with the remove %q", n.State, n.Worktrees, removeCmd)
	}
	if text := runJig(t, env, clone, "next", b.fx.Ticket).Stdout; !strings.Contains(text, removeLine) {
		t.Errorf("jig next after the publish lacks %q:\n%s", removeLine, text)
	}

	// The session runs it: the folder is gone and jig has nothing more to say.
	runAdvised(t, row.Command)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the worktree is still at %s after the remove: %v", dir, err)
	}
	n = nextJSON[wtNextOut](t, env, clone, b.fx.Ticket)
	if n.State != "done" || len(n.Worktrees) != 0 {
		t.Errorf("jig next after the remove: state %q, worktrees %+v, want done with none", n.State, n.Worktrees)
	}
	if text := runJig(t, env, clone, "next", b.fx.Ticket).Stdout; strings.Contains(text, "Remove the worktree") || strings.Contains(text, "worktrees[") {
		t.Errorf("jig next after the remove still advises:\n%s", text)
	}
}

// chartWtOut is the part of `jig next <chart> --json` this test reads.
type chartWtOut struct {
	Run     [][]string `json:"run"`
	Tickets []struct {
		ID        string  `json:"id"`
		Worktrees []wtRow `json:"worktrees"`
	} `json:"tickets"`
}

// worktreesOf is the rows the standing gives ticket id.
func (c chartWtOut) worktreesOf(t *testing.T, id string) []wtRow {
	t.Helper()
	for _, row := range c.Tickets {
		if row.ID == id {
			return row.Worktrees
		}
	}
	t.Fatalf("the standing has no ticket %s: %+v", id, c.Tickets)
	return nil
}

// TestChartCarriesWorktrees: `jig next <chart>` gives each ticket of the run its
// worktrees, so a session that takes the chart on has every add command at
// once: the first ticket from the target, the second, stacked on it, from its
// parent's branch (as the origin will have it, until the clone holds it).
// Running the first's command makes it present; `jig status <chart>` carries
// none.
func TestChartCarriesWorktrees(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	chartDir := filepath.Join(fx.StoreDir, "charts", "c")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(chartDir, "tickets.yaml"), []byte("tickets:\n  - title: First\n  - title: Second\n    blocked_by:\n      - ref: \"#1\"\n        kind: stacked\n"))
	if r := runJig(t, env, fx.StoreDir, "graduate", "c"); r.Code != 0 {
		t.Fatalf("jig graduate c: exit %d\n%s", r.Code, r.Stdout)
	}
	pos := nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if len(pos.Tickets) != 2 {
		t.Fatalf("the standing has %d tickets, want 2: %+v", len(pos.Tickets), pos.Tickets)
	}
	first, second := pos.Tickets[0].ID, pos.Tickets[1].ID
	briefed(t, st, first)
	briefed(t, st, second)
	firstBranch := nextJSON[wtNextOut](t, env, fx.RepoDir, first).Branch
	secondBranch := nextJSON[wtNextOut](t, env, fx.RepoDir, second).Branch
	clone := wtPath(fx.RepoDir)
	dirOf := func(id string) string { return clone + ".worktrees/" + id }
	quoted := wtQuoted(fx.RepoDir)

	c := nextJSON[chartWtOut](t, env, fx.RepoDir, "c")
	if want := [][]string{{first}, {second}}; !reflect.DeepEqual(c.Run, want) {
		t.Fatalf("run = %v, want %v", c.Run, want)
	}
	wantFirst := wtRow{
		Repo: "fixture-repo", Action: "add", Branch: firstBranch, Clone: clone, Dir: dirOf(first), Base: "origin/main",
		Command: "git -C " + quoted + " worktree add " + wtQuoted(dirOf(first)) + " -b " + firstBranch + " origin/main",
	}
	wantSecond := wtRow{
		Repo: "fixture-repo", Action: "add", Branch: secondBranch, Clone: clone, Dir: dirOf(second), Base: "origin/" + firstBranch,
		Command: "git -C " + quoted + " worktree add " + wtQuoted(dirOf(second)) + " -b " + secondBranch + " origin/" + firstBranch,
	}
	if got := c.worktreesOf(t, first); !reflect.DeepEqual(got, []wtRow{wantFirst}) {
		t.Errorf("%s worktrees = %+v, want %+v", first, got, wantFirst)
	}
	if got := c.worktreesOf(t, second); !reflect.DeepEqual(got, []wtRow{wantSecond}) {
		t.Errorf("%s worktrees = %+v, want %+v", second, got, wantSecond)
	}
	// The second is held by the first until the first's gate has a clean round,
	// so `jig next <second>` advises nothing yet: the chart names its add ahead
	// of its wave, and the session asks again when the wave starts.
	if held := nextJSON[wtNextOut](t, env, fx.RepoDir, second); len(held.Worktrees) != 0 {
		t.Errorf("jig next %s while its parent has no clean round: worktrees %+v, want none", second, held.Worktrees)
	}

	text := runJig(t, env, fx.RepoDir, "next", "c").Stdout
	for _, want := range []string{
		"worktrees[2]{ticket,repo,action,dir,base,command}:",
		"  " + first + ",fixture-repo,add,",
		"  " + second + ",fixture-repo,add,",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("jig next c lacks %q:\n%s", want, text)
		}
	}
	if run, table := strings.Index(text, "run[2]"), strings.Index(text, "worktrees[2]"); run < 0 || table < run {
		t.Errorf("the worktrees table must follow the run table:\n%s", text)
	}
	if status := runJig(t, env, fx.RepoDir, "status", "c"); status.Code != 0 || strings.Contains(status.Stdout, "worktrees") {
		t.Errorf("jig status c: exit %d, want the standing with no worktrees\n%s", status.Code, status.Stdout)
	}

	// The first's branch now exists in the clone (cut by hand, say): the second
	// starts from it, and the first's add is the existing-branch form.
	gitLog(t, fx.RepoDir, "branch", firstBranch, "main")
	c = nextJSON[chartWtOut](t, env, fx.RepoDir, "c")
	firstRows, secondRows := c.worktreesOf(t, first), c.worktreesOf(t, second)
	wantExisting := "git -C " + quoted + " worktree add " + wtQuoted(dirOf(first)) + " " + firstBranch
	if len(firstRows) != 1 || firstRows[0].Action != "add" || firstRows[0].Command != wantExisting || firstRows[0].Base != "" {
		t.Fatalf("%s after its branch exists: %+v, want the add %q", first, firstRows, wantExisting)
	}
	if len(secondRows) != 1 || secondRows[0].Base != firstBranch || !strings.HasSuffix(secondRows[0].Command, " -b "+secondBranch+" "+firstBranch) {
		t.Fatalf("%s after its parent's branch exists: %+v, want the base %s", second, secondRows, firstBranch)
	}
	runAdvised(t, firstRows[0].Command)
	c = nextJSON[chartWtOut](t, env, fx.RepoDir, "c")
	if got := c.worktreesOf(t, first); len(got) != 1 || got[0].Action != "present" || !gitx.SameDir(got[0].Dir, dirOf(first)) || got[0].Command != "" {
		t.Errorf("%s after the add: %+v, want present at %s", first, got, dirOf(first))
	}
	if got := c.worktreesOf(t, second); len(got) != 1 || got[0].Action != "add" {
		t.Errorf("%s after the first's add: %+v, want its add still advised", second, got)
	}
}
