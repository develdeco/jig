package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestAGateRoundResolvesAFixSlicesOracleAtTheHead: the fix slice a round
// queues runs the oracles its findings name, each resolved against the repo's
// manifest at the round's head, though the clone is checked out on a commit
// whose manifest does not declare it.
func TestAGateRoundResolvesAFixSlicesOracleAtTheHead(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	declareHeadOnlyOracle(t, fx.RepoDir)
	run(t, fx.RepoDir, "checkout", "main")

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	res := cleanReport(p)
	file := "alpha/alpha.go"
	for i, oracle := range []string{"test", headOnlyOracle} {
		res.Findings = append(res.Findings, ResultFinding{File: file, Line: i + 1, Title: "t" + oracle, Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r", Oracle: oracle})
	}
	_, err = GateFinish(d, p, res, Provenance{Session: "s-reviewer", How: ReviewerByFlag})
	if err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	queued, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	var fix *store.Slice
	for i := range queued {
		if queued[i].FromGate == p.Round {
			fix = &queued[i]
		}
	}
	if fix == nil {
		t.Fatalf("the round queued no fix slice: %+v", queued)
	}
	if !strings.Contains(fix.Oracle, "echo only the head declares this") {
		t.Errorf("the fix slice's oracle is %q, want the head's command for %s", fix.Oracle, headOnlyOracle)
	}
}

// TestAKeptFindingsFixSliceResolvesItsOraclesAtTheHead: the fix slice a gate
// queues for the findings a person kept runs the oracles they name, resolved
// against the repo's manifest at the branch's head, though the clone is
// checked out on a commit whose manifest does not declare them.
func TestAKeptFindingsFixSliceResolvesItsOraclesAtTheHead(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	declareHeadOnlyOracle(t, fx.RepoDir)
	run(t, fx.RepoDir, "checkout", "main")

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	res := cleanReport(p)
	oracles := []string{"test", headOnlyOracle}
	for i, oracle := range oracles {
		res.Findings = append(res.Findings, ResultFinding{File: "alpha/alpha.go", Line: i + 1, Title: "t" + oracle, Detail: "d", Action: ActionNote, Risk: RiskLow, RiskRationale: "r", Oracle: oracle})
	}
	if _, err := GateFinish(d, p, res, Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	for i, oracle := range oracles {
		keep := journal.Line{Event: journal.EventTriage, Ref: fmt.Sprintf("r1-f%d", i+1), Outcome: TriageKeep, Command: TriageTarget("alpha", oracle), Provenance: "person"}
		if err := journal.Append(d.Store, fx.Ticket, keep); err != nil {
			t.Fatal(err)
		}
	}

	again, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare after the keeps: %v", err)
	}
	if len(again.Queued) != 1 {
		t.Fatalf("the gate queued %v, want one fix slice for the kept findings", again.Queued)
	}
	all, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.ID == again.Queued[0] && !strings.Contains(s.Oracle, "echo only the head declares this") {
			t.Errorf("the fix slice's oracle is %q, want the head's command for %s", s.Oracle, headOnlyOracle)
		}
	}
}

// TestAReviewedPathInAWorktreeOfTheCloneCountsAsRead: a reviewer that reports
// the files it read by their absolute paths in the worktree its harness made
// for it (a linked worktree of the clone, wherever the harness put it), or in
// the one jig advised, has them counted as the repo's files; a path in the
// clone itself, or anywhere else, is not one.
func TestAReviewedPathInAWorktreeOfTheCloneCountsAsRead(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		dir  func(t *testing.T, fx *fixture.Fixture, head string) string
		ok   bool
	}{
		{"a linked worktree the harness made", func(t *testing.T, fx *fixture.Fixture, head string) string {
			dir := filepath.Join(t.TempDir(), "agent-worktree")
			run(t, fx.RepoDir, "worktree", "add", "--detach", dir, head)
			return dir
		}, true},
		{"the directory jig advised", func(t *testing.T, fx *fixture.Fixture, head string) string {
			return ReviewWorktreeDir(fx.RepoDir, fx.Ticket)
		}, true},
		{"a directory that is no worktree of the clone", func(t *testing.T, fx *fixture.Fixture, head string) string {
			return t.TempDir()
		}, false},
		{"the clone itself", func(t *testing.T, fx *fixture.Fixture, head string) string {
			return fx.RepoDir
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx, d, head := cloneBuilt(t)
			p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
			if err != nil {
				t.Fatalf("GatePrepare: %v", err)
			}
			dir := c.dir(t, fx, head)
			res := cleanReport(p)
			res.ReviewedPaths = nil
			for _, f := range p.Request.MustReview {
				res.ReviewedPaths = append(res.ReviewedPaths, filepath.Join(dir, filepath.FromSlash(f)))
			}
			_, err = GateFinish(d, p, res, Provenance{Session: "s-reviewer", How: ReviewerByFlag})
			if c.ok && err != nil {
				t.Fatalf("GateFinish with paths in %s: %v, want them counted as read", dir, err)
			}
			if !c.ok {
				wantAxiCode(t, err, "REVIEW_INVALID")
			}
		})
	}
}

// TestReviewerWorktreesAreTheAdvisedDirAndTheLinkedOnesOfTheClone: a clone that
// cannot list its worktrees (it is no repository) has the directory jig advised
// alone, and one that can lists the linked worktrees after it, never the
// clone's own.
func TestReviewerWorktreesAreTheAdvisedDirAndTheLinkedOnesOfTheClone(t *testing.T) {
	t.Parallel()
	notARepo := t.TempDir()
	if got, want := reviewerWorktrees(notARepo, "T-1"), []string{ReviewWorktreeDir(notARepo, "T-1")}; !slices.Equal(got, want) {
		t.Errorf("reviewerWorktrees of a directory that is no repository = %v, want %v", got, want)
	}
	clone, _ := tinyRepo(t)
	linked := filepath.ToSlash(filepath.Join(t.TempDir(), "linked"))
	run(t, clone, "worktree", "add", "--detach", linked)
	got := reviewerWorktrees(clone, "T-1")
	if len(got) != 2 || got[0] != ReviewWorktreeDir(clone, "T-1") || filepath.ToSlash(got[1]) != linked {
		t.Errorf("reviewerWorktrees = %v, want the advised directory then %s", got, linked)
	}
}

// headOnlyOracle is an oracle only the ticket's head declares.
const headOnlyOracle = "headonly"

// declareHeadOnlyOracle commits, on the branch dir is on, a .claude/jig.yaml
// that also declares headOnlyOracle, and returns the new head. A manifest read
// from the commit has it; one read from a checkout of another commit does not.
func declareHeadOnlyOracle(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, ".claude", "jig.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["oracles"].(map[string]any)[headOnlyOracle] = "echo only the head declares this"
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	builder(t).Commit(dir, "declare an oracle only the head has")
	return run(t, dir, "rev-parse", "HEAD")
}

// TestAGateRoundTakesNoLease: a gate round works in the mapped clone and
// changes none of what the person left there: no directory is made under the
// jig home's pool, the clone's checkout, index and files (a modified tracked
// file and an untracked one among them) and its local branches are as they
// were, and the only refs it gains are origin's, under refs/remotes/origin. A
// repo with no clone mapped is refused CLONE_NOT_MAPPED. The clone is checked
// out on another commit than the head, as a person's is once the ticket's
// branch lives in a worktree, and what the round reads (the manifest) is the
// head's, never the checkout's.
func TestAGateRoundTakesNoLease(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	head := declareHeadOnlyOracle(t, fx.RepoDir)
	run(t, fx.RepoDir, "checkout", "main")
	if man, err := manifest.Resolve(fx.RepoDir); err != nil || man.Oracles[headOnlyOracle] != "" {
		t.Fatalf("test setup: the checkout declares %q (%v), want it declared at the head only", headOnlyOracle, err)
	}
	// What a person has in progress in the clone.
	modified := filepath.Join(fx.RepoDir, "alpha", "alpha.go")
	data, err := os.ReadFile(modified)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("// unsaved thought\n")...)
	if err := os.WriteFile(modified, data, 0o644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(fx.RepoDir, "scratch.txt")
	if err := os.WriteFile(untracked, []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := struct{ status, heads, head string }{
		run(t, fx.RepoDir, "status", "--porcelain"),
		run(t, fx.RepoDir, "for-each-ref", "refs/heads"),
		run(t, fx.RepoDir, "rev-parse", "HEAD"),
	}

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Head != head {
		t.Fatalf("the round reviews %s, want the clone's head %s", p.Head, head)
	}
	if !slices.Contains(p.Request.Oracles, headOnlyOracle) {
		t.Fatalf("the request's oracles are %v, want the head's manifest (%s) and not the checkout's", p.Request.Oracles, headOnlyOracle)
	}
	if _, err := os.Stat(filepath.Join(fx.Home, "pool")); err == nil {
		t.Fatal("the round made a pool directory under the jig home")
	}
	after := struct{ status, heads, head string }{
		run(t, fx.RepoDir, "status", "--porcelain"),
		run(t, fx.RepoDir, "for-each-ref", "refs/heads"),
		run(t, fx.RepoDir, "rev-parse", "HEAD"),
	}
	if after != before {
		t.Fatalf("the clone changed:\nbefore %+v\nafter  %+v", before, after)
	}
	if got, err := os.ReadFile(modified); err != nil || string(got) != string(data) {
		t.Fatalf("the modified file changed: %v", err)
	}
	if got, err := os.ReadFile(untracked); err != nil || string(got) != "notes\n" {
		t.Fatalf("the untracked file changed: %v", err)
	}
	if got := run(t, fx.RepoDir, "rev-parse", "refs/remotes/origin/main"); got != originRef(t, fx.RepoRemote, "refs/heads/main") {
		t.Errorf("the round did not fetch origin's main into the clone: %s", got)
	}

	// No clone mapped: nothing to read.
	d.Machine = project.MachineProject{}
	_, err = GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "CLONE_NOT_MAPPED")
	if ae, ok := err.(*axi.Error); ok && len(ae.Help) == 0 {
		t.Error("CLONE_NOT_MAPPED names no way to map a clone")
	}
}
