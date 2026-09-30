package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/tracker"
)

// githubPRs is a tracker adapter for a publish test that wants the real
// github adapter's pull request calls and nothing else of github's: the local
// adapter mints, projects and comments (a fixture ticket is JIG-1, not the
// issue number the github adapter projects onto), and the github adapter,
// running against the fake gh, finds, opens and updates the pull request.
type githubPRs struct {
	tracker.Adapter
	gh tracker.Adapter
}

func (a githubPRs) CreatePR(head, base, title, bodyFile string) (string, error) {
	return a.gh.(tracker.PRCreator).CreatePR(head, base, title, bodyFile)
}

func (a githubPRs) FindOpenPR(head, base string) (string, error) {
	return a.gh.(tracker.PRUpdater).FindOpenPR(head, base)
}

func (a githubPRs) UpdatePR(url, bodyFile string) error {
	return a.gh.(tracker.PRUpdater).UpdatePR(url, bodyFile)
}

// useGithubPRs points d at the fake gh, which lists pulls (fixture.GhPulls) as
// the repo's pull requests ("" for none) and fails the call named by fail ("" for
// none), and returns the file it logs every argv to. It sets the process
// environment, so the test using it must not be parallel.
func useGithubPRs(t *testing.T, d *Deps, pulls, fail string) string {
	t.Helper()
	stubDir := fixture.GhStub(t)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "gh.log")
	t.Setenv("GH_STUB_LOG", logFile)
	t.Setenv("GH_STUB_STATE", filepath.Join(t.TempDir(), "gh.state"))
	t.Setenv("GH_STUB_PULLS", pulls)
	t.Setenv("GH_STUB_FAIL", fail)

	local, err := tracker.New(d.Cfg, d.Store)
	if err != nil {
		t.Fatalf("tracker.New local: %v", err)
	}
	ghCfg := d.Cfg
	ghCfg.Tracker = "github"
	ghCfg.Repos = []project.Repo{{Remote: "git@github.com:owner/repo.git"}}
	gh, err := tracker.New(ghCfg, d.Store)
	if err != nil {
		t.Fatalf("tracker.New github: %v", err)
	}
	d.Tracker = githubPRs{Adapter: local, gh: gh}
	return logFile
}

// loggedGh reads the fake gh's log: one argv per call, argv[0] the binary.
func loggedGh(t *testing.T, logFile string) [][]string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read the gh log: %v", err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var argv []string
		if err := json.Unmarshal([]byte(line), &argv); err != nil {
			t.Fatalf("parse gh log line %q: %v", line, err)
		}
		out = append(out, argv[1:])
	}
	return out
}

// ghCalls counts the logged calls that start with the given words.
func ghCalls(calls [][]string, words ...string) int {
	n := 0
	for _, argv := range calls {
		if len(argv) >= len(words) && slices.Equal(argv[:len(words)], words) {
			n++
		}
	}
	return n
}

const existingPR = "https://github.example/owner/repo/pull/7"

// openPullOf lists the pull request existingPR names: open, from the repo's
// own branch into main.
func openPullOf(t *testing.T, branch string) string {
	t.Helper()
	return fixture.GhPulls(t, fixture.GhPull{Number: 7, Owner: "owner", Head: branch, Base: "main"})
}

// lookupCall is the call publish makes to find branch's open pull request into
// main: the pull requests endpoint, asked for the qualified head, the base and
// the open state.
func lookupCall(branch string) []string {
	return []string{"api", "repos/owner/repo/pulls", "--method", "GET", "-f", "head=owner:" + branch, "-f", "base=main", "-f", "state=open"}
}

// TestPublishUpdatesTheOpenPRInsteadOfOpeningASecond: publishing a branch
// that already has an open pull request replaces that pull request's body and
// opens none, through the fake gh: the lookup asks for the pull request from
// the ticket's branch into the target, `gh pr edit` gets the body file publish
// wrote and the URL found, and there is no `gh pr create`. The report and the
// journal say the pull request was updated, not opened. The branch is on
// origin (pushed from the build lease, the case a first squash would refuse
// before this), so the push is a fast-forward and the body is the only thing
// that changes on the pull request.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishUpdatesTheOpenPRInsteadOfOpeningASecond(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)
	run(t, buildLeaseDir(t, fx), "push", "origin", branch)
	logFile := useGithubPRs(t, &d, openPullOf(t, ticketBranch(fx.Ticket)), "")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGh(t, logFile)
	if n := ghCalls(calls, lookupCall(branch)...); n != 1 {
		t.Errorf("the lookup of %s's open pull request into main called %d time(s), want once: %v", branch, n, calls)
	}
	body := filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"]))
	if n := ghCalls(calls, "pr", "edit", existingPR, "--repo", "owner/repo", "--body-file", body); n != 1 {
		t.Errorf("gh pr edit %s --body-file %s called %d time(s), want once: %v", existingPR, body, n, calls)
	}
	if n := ghCalls(calls, "pr", "create"); n != 0 {
		t.Errorf("gh pr create called %d time(s): a second pull request was opened beside the existing one: %v", n, calls)
	}
	if report.PRURL["fixture-repo"] != existingPR || !report.PRUpdated["fixture-repo"] {
		t.Errorf("report: PR %q updated=%v, want the existing %s updated", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"], existingPR)
	}
	if data, err := os.ReadFile(body); err != nil || strings.TrimSpace(string(data)) == "" {
		t.Errorf("the body file %s that pr edit was given is missing or empty (err %v)", body, err)
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	var prLines []journal.Line
	for _, l := range lines {
		if l.Event == "pr" {
			prLines = append(prLines, l)
		}
	}
	if len(prLines) != 1 || prLines[0].Outcome != "updated" || prLines[0].Commit != report.Head["fixture-repo"] {
		t.Errorf("journal pr lines = %+v, want one recording updated at the head %s", prLines, report.Head["fixture-repo"])
	}
}

// TestPublishOpensAPRWhenTheBranchHasNone is the control: with no open pull
// request the lookup answers none and publish opens one, as it always has.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishOpensAPRWhenTheBranchHasNone(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	logFile := useGithubPRs(t, &d, "", "")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGh(t, logFile)
	if ghCalls(calls, lookupCall(ticketBranch(fx.Ticket))...) != 1 || ghCalls(calls, "pr", "create", "--repo", "owner/repo") != 1 || ghCalls(calls, "pr", "edit") != 0 {
		t.Errorf("gh calls = %v, want one lookup, one pr create and no pr edit", calls)
	}
	if report.PRUpdated["fixture-repo"] || report.PRURL["fixture-repo"] == "" {
		t.Errorf("report: PR %q updated=%v, want a pull request opened", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"])
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	var prLines []journal.Line
	for _, l := range lines {
		if l.Event == "pr" {
			prLines = append(prLines, l)
		}
	}
	if len(prLines) != 1 || prLines[0].Outcome != "opened" {
		t.Errorf("journal pr lines = %+v, want one recording the outcome opened", prLines)
	}
}

// TestPublishOpensAPRWhenTheBranchHasNoOpenOneIntoTheTarget: only an open pull
// request from the branch into the target is the pull request a publish
// updates. A closed one is a decision about that pull request, not about the
// branch's next delivery, a merged one is delivered history, and an open one
// into another base is another delivery (a stacked pull request, say): the
// publish the operator asked for opens one into the target, and touches none
// of them. The fake gh applies the state, head and base the lookup passes, so
// it is GitHub that leaves them out of the answer.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishOpensAPRWhenTheBranchHasNoOpenOneIntoTheTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  string
		merged bool
		base   string
	}{
		{"closed", "closed", false, "main"},
		{"merged", "closed", true, "main"},
		{"open into another base", "open", false, "release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			gateToClean(t, fx, d)
			branch := ticketBranch(fx.Ticket)
			pulls := fixture.GhPulls(t, fixture.GhPull{Number: 7, State: tc.state, Merged: tc.merged, Owner: "owner", Head: branch, Base: tc.base})
			logFile := useGithubPRs(t, &d, pulls, "")

			report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}

			calls := loggedGh(t, logFile)
			if ghCalls(calls, lookupCall(branch)...) != 1 ||
				ghCalls(calls, "pr", "create", "--repo", "owner/repo", "--title") != 1 || ghCalls(calls, "pr", "edit") != 0 {
				t.Errorf("gh calls = %v, want one lookup into main, one pr create and no pr edit", calls)
			}
			if report.PRUpdated["fixture-repo"] || report.PRURL["fixture-repo"] == "" || report.PRURL["fixture-repo"] == existingPR {
				t.Errorf("report: PR %q updated=%v, want a new pull request opened and %s left alone", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"], existingPR)
			}
		})
	}
}

// TestPublishRefusesWhenItCannotTellWhetherThereIsAPR: a lookup that fails is
// not "no pull request", which would open a second one. The publish is refused
// before its first store write and before anything is pushed: the journal is
// as it was, origin has no branch, and no pull request was created.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishRefusesWhenItCannotTellWhetherThereIsAPR(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	logFile := useGithubPRs(t, &d, "", "api repos/owner/repo/pulls")
	journalBefore, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err == nil || !strings.Contains(err.Error(), "gh api repos/owner/repo/pulls") {
		t.Fatalf("Publish over a gh that cannot list: err = %v, want the failed call named", err)
	}

	if calls := loggedGh(t, logFile); ghCalls(calls, "pr", "create") != 0 || ghCalls(calls, "pr", "edit") != 0 {
		t.Errorf("gh calls = %v, want no pull request opened or edited", calls)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(fx.Ticket)); got != "" {
		t.Errorf("origin has %s = %s after the refused publish, want nothing pushed", ticketBranch(fx.Ticket), got)
	}
	journalAfter, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	if len(journalAfter) != len(journalBefore) {
		t.Errorf("the journal grew from %d to %d lines over a refused publish", len(journalBefore), len(journalAfter))
	}
	if got := run(t, d.Store.Root, "rev-parse", "HEAD"); got != storeHead {
		t.Errorf("the store moved from %s to %s over a refused publish", storeHead, got)
	}
}

// TestPublishConfirmNamesTheOpenPR: the question the operator answers says
// which pull request it is about - the one the branch already has open, which
// a yes updates - and a no touches neither the branch nor that pull request.
//
// This test must stay serial: it swaps the package-level confirm hook, which
// every parallel test's Publish reads, and puts the fake gh on PATH.
func TestPublishConfirmNamesTheOpenPR(t *testing.T) {
	origConfirm := confirm
	defer func() { confirm = origConfirm }()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	logFile := useGithubPRs(t, &d, openPullOf(t, ticketBranch(fx.Ticket)), "")

	var gotBranch, gotPR string
	confirm = func(branch, _, openPR string) bool {
		gotBranch, gotPR = branch, openPR
		return false
	}
	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "PUBLISH_DECLINED")
	if gotBranch != ticketBranch(fx.Ticket) || gotPR != existingPR {
		t.Errorf("confirm asked about (%q, %q), want the ticket's branch and the open pull request %s", gotBranch, gotPR, existingPR)
	}
	if calls := loggedGh(t, logFile); ghCalls(calls, "pr", "edit") != 0 || ghCalls(calls, "pr", "create") != 0 {
		t.Errorf("gh calls = %v after a declined publish, want no pull request touched", calls)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(fx.Ticket)); got != "" {
		t.Errorf("origin has %s = %s after a declined publish", ticketBranch(fx.Ticket), got)
	}
}

// TestPublishFailsLoudlyWhenThePRCannotBeWritten: a pull request that could not
// be updated or opened is not reported as done. The publish returns the failed
// call's error, its journal has no `pr` line saying updated or opened (nor the
// route and publish-done lines after it), and the deferred push commits what it
// did write under a subject naming the failure.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishFailsLoudlyWhenThePRCannotBeWritten(t *testing.T) {
	noPulls := func(*testing.T, string) string { return "" }
	for _, tc := range []struct {
		name  string
		pulls func(t *testing.T, branch string) string
		fail  string
		call  string
	}{
		{"update the open one", openPullOf, "pr edit", "gh pr edit"},
		{"open one", noPulls, "pr create", "gh pr create"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			gateToClean(t, fx, d)
			useGithubPRs(t, &d, tc.pulls(t, ticketBranch(fx.Ticket)), tc.fail)

			_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
			if err == nil || !strings.Contains(err.Error(), tc.call) {
				t.Fatalf("Publish over a gh that fails %q: err = %v, want the failed call named", tc.fail, err)
			}

			lines, jerr := journal.Read(d.Store, fx.Ticket)
			if jerr != nil {
				t.Fatalf("journal.Read: %v", jerr)
			}
			for _, l := range lines {
				if l.Event == "pr" || l.Event == "route" || l.Event == "publish-done" {
					t.Errorf("journal has a %q line (%+v) after a publish whose pull request could not be written", l.Event, l)
				}
			}
			wantFailureCommit(t, d, "main", fx.Ticket+": publish failed: INTERNAL")
		})
	}
}
