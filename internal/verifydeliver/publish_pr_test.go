package verifydeliver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// githubRemote is the github.com remote useGithubHost points the fixture's
// single repo at. Its repo name is "fixture-repo", matching the fixture's
// real remote (fixture.Build's repoRemote), so repo.Name() - the repoName
// every pool lease directory and report map is keyed by - stays the same
// before and after the swap.
const githubRemote = "git@github.com:owner/fixture-repo.git"

// useGithubHost makes the fixture's single repo resolve to a GitHub pull
// request host for Publish (repohost.New parses a repo's own remote): it
// points d.Cfg.Repos[0].Remote at githubRemote, and tells git, through the
// GIT_CONFIG_COUNT/KEY/VALUE discrete config variables (layered additively
// over gittest's own hermetic global config, never replacing it), to treat
// that URL as an alias for the fixture's real local remote - so every
// clone, fetch and push Publish makes still reaches the real bare repo,
// while repohost.New sees a github.com URL. It also puts the fake gh on
// PATH, which lists pulls (fixture.GhPulls) as the repo's pull requests (""
// for none) and fails the call named by fail ("" for none), and returns the
// file it logs every argv to. It sets the process environment, so the test
// using it must not be parallel.
func useGithubHost(t *testing.T, d *Deps, pulls, fail string) string {
	t.Helper()
	stubDir := fixture.GhStub(t)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "gh.log")
	t.Setenv("GH_STUB_LOG", logFile)
	t.Setenv("GH_STUB_STATE", filepath.Join(t.TempDir(), "gh.state"))
	t.Setenv("GH_STUB_BODY_STATE", filepath.Join(t.TempDir(), "gh-body.state"))
	t.Setenv("GH_STUB_PULLS", pulls)
	t.Setenv("GH_STUB_FAIL", fail)

	real := d.Cfg.Repos[0].Remote
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+filepath.ToSlash(real)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", githubRemote)
	d.Cfg.Repos[0].Remote = githubRemote
	return logFile
}

// ghLogLine is one line of the fake gh's log: its argv (argv[0] the stub
// binary itself) and the working directory it ran in.
type ghLogLine struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir"`
}

// loggedGhCalls reads the fake gh's log in full, one ghLogLine per call.
func loggedGhCalls(t *testing.T, logFile string) []ghLogLine {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read the gh log: %v", err)
	}
	var out []ghLogLine
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var c ghLogLine
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("parse gh log line %q: %v", line, err)
		}
		out = append(out, c)
	}
	return out
}

// loggedGh reads the fake gh's log: one argv per call, the stub binary
// itself (argv[0]) dropped.
func loggedGh(t *testing.T, logFile string) [][]string {
	t.Helper()
	calls := loggedGhCalls(t, logFile)
	out := make([][]string, len(calls))
	for i, c := range calls {
		out[i] = c.Argv[1:]
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

// existingPR is the URL fixture.GhPulls bakes into a fake pull request's
// html_url, "owner/repo" regardless of the actual repo the lookup asked
// about (internal/fixture/ghpulls.go) - unlike the --repo flag and the
// lookup's own request path below, which do carry the repo repohost.New
// actually parsed (owner/fixture-repo, githubRemote's own repo name).
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
	return []string{"api", "repos/owner/fixture-repo/pulls", "--method", "GET", "-f", "head=owner:" + branch, "-f", "base=main", "-f", "state=open"}
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
	logFile := useGithubHost(t, &d, openPullOf(t, ticketBranch(fx.Ticket)), "")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGh(t, logFile)
	if n := ghCalls(calls, lookupCall(branch)...); n != 1 {
		t.Errorf("the lookup of %s's open pull request into main called %d time(s), want once: %v", branch, n, calls)
	}
	body := filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"]))
	if n := ghCalls(calls, "pr", "edit", existingPR, "--repo", "owner/fixture-repo", "--body-file", body); n != 1 {
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
	logFile := useGithubHost(t, &d, "", "")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGh(t, logFile)
	if ghCalls(calls, lookupCall(ticketBranch(fx.Ticket))...) != 1 || ghCalls(calls, "pr", "create", "--repo", "owner/fixture-repo") != 1 || ghCalls(calls, "pr", "edit") != 0 {
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
			logFile := useGithubHost(t, &d, pulls, "")

			report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}

			calls := loggedGh(t, logFile)
			if ghCalls(calls, lookupCall(branch)...) != 1 ||
				ghCalls(calls, "pr", "create", "--repo", "owner/fixture-repo", "--title") != 1 || ghCalls(calls, "pr", "edit") != 0 {
				t.Errorf("gh calls = %v, want one lookup into main, one pr create and no pr edit", calls)
			}
			if report.PRUpdated["fixture-repo"] || report.PRURL["fixture-repo"] == "" || report.PRURL["fixture-repo"] == existingPR {
				t.Errorf("report: PR %q updated=%v, want a new pull request opened and %s left alone", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"], existingPR)
			}
		})
	}
}

// TestPublishWithNoHostMakesNoGhCall: the fixture's own remote is a plain
// local path, the configuration this store itself runs under - repohost.New
// finds no pull-request host there (repohost.TestNewReturnsNilForNonGitHub
// pins that inference alone). The fake gh goes on PATH, but the remote is
// never swapped to githubRemote the way useGithubHost would: Publish must
// make no gh call at all over it, not even to check gh is installed
// (repohost.New returns nil before it ever looks). It asks the push-only
// confirmation question - no open PR to name and no host - and journals `pr`
// with none:no-host, not opened or updated.
//
// This test must stay serial: it puts the fake gh on PATH and swaps the
// package-level confirm hook, which every parallel test's Publish reads.
func TestPublishWithNoHostMakesNoGhCall(t *testing.T) {
	origConfirm := confirm
	defer func() { confirm = origConfirm }()

	stubDir := fixture.GhStub(t)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "gh.log")
	t.Setenv("GH_STUB_LOG", logFile)

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

	var gotBranch, gotPR string
	var gotHasHost bool
	confirm = func(branch, _, openPR string, hasHost bool) bool {
		gotBranch, gotPR, gotHasHost = branch, openPR, hasHost
		return true
	}

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if gotBranch != ticketBranch(fx.Ticket) || gotPR != "" || gotHasHost {
		t.Errorf("confirm asked (branch=%q, openPR=%q, hasHost=%v), want the ticket's branch, no open PR and hasHost=false", gotBranch, gotPR, gotHasHost)
	}
	if calls := loggedGh(t, logFile); len(calls) != 0 {
		t.Errorf("gh calls = %v, want none: the fixture's remote has no pull-request host", calls)
	}
	if report.PRURL["fixture-repo"] != "" || report.PRUpdated["fixture-repo"] {
		t.Errorf("report: PR %q updated=%v, want empty and false: no pull-request host", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"])
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(fx.Ticket)); got == "" {
		t.Errorf("origin has no %s after a confirmed publish with no pull-request host, want the branch pushed", ticketBranch(fx.Ticket))
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
	if len(prLines) != 1 || prLines[0].Outcome != "none:no-host" {
		t.Errorf("journal pr lines = %+v, want exactly one recording none:no-host", prLines)
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
	logFile := useGithubHost(t, &d, "", "api repos/owner/fixture-repo/pulls")
	journalBefore, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err == nil || !strings.Contains(err.Error(), "gh api repos/owner/fixture-repo/pulls") {
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
	logFile := useGithubHost(t, &d, openPullOf(t, ticketBranch(fx.Ticket)), "")

	var gotBranch, gotPR string
	confirm = func(branch, _, openPR string, _ bool) bool {
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
			useGithubHost(t, &d, tc.pulls(t, ticketBranch(fx.Ticket)), tc.fail)

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

// reviewNotesCommentCalls returns every logged "pr comment" call, so a test
// can check not just how many there were but what they posted.
func reviewNotesCommentCalls(calls [][]string) [][]string {
	var out [][]string
	for _, argv := range calls {
		if len(argv) >= 2 && argv[0] == "pr" && argv[1] == "comment" {
			out = append(out, argv)
		}
	}
	return out
}

// wantReviewNotesBodyFile fails unless argv carries --body-file with a path
// ending in pr/review-notes.md: the rule the brief states ("A new optional
// tracker capability posts pr/review-notes.md as a comment"), not merely
// that some comment was posted once. A wrong file (pr/<repo>.md, say) or a
// call with no --body-file at all must fail this, not just the call count.
func wantReviewNotesBodyFile(t *testing.T, argv []string) {
	t.Helper()
	for i, arg := range argv {
		if arg == "--body-file" {
			if i+1 >= len(argv) {
				t.Fatalf("pr comment argv %v has --body-file with no value", argv)
			}
			file := filepath.ToSlash(argv[i+1])
			if !strings.HasSuffix(file, "pr/review-notes.md") {
				t.Fatalf("pr comment --body-file = %q, want it to end in pr/review-notes.md", file)
			}
			return
		}
	}
	t.Fatalf("pr comment argv %v has no --body-file", argv)
}

// TestPublishPostsReviewNotesAsCommentOnCreate: publish posts the review notes
// as a comment on a newly opened pull request, exactly once, with
// pr/review-notes.md as its --body-file.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishPostsReviewNotesAsCommentOnCreate(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	logFile := useGithubHost(t, &d, "", "")

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGh(t, logFile)
	commentCalls := reviewNotesCommentCalls(calls)
	if len(commentCalls) != 1 {
		t.Fatalf("pr comment called %d time(s), want exactly once: %v", len(commentCalls), calls)
	}
	wantReviewNotesBodyFile(t, commentCalls[0])
}

// TestPublishPostsReviewNotesAsCommentOnUpdate: publish posts the review notes
// as a comment on an existing pull request that it updates, exactly once,
// with pr/review-notes.md as its --body-file.
//
// This test must stay serial: it puts the fake gh on PATH.
func TestPublishPostsReviewNotesAsCommentOnUpdate(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)
	run(t, buildLeaseDir(t, fx), "push", "origin", branch)
	logFile := useGithubHost(t, &d, openPullOf(t, ticketBranch(fx.Ticket)), "")

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	calls := loggedGh(t, logFile)
	commentCalls := reviewNotesCommentCalls(calls)
	if len(commentCalls) != 1 {
		t.Fatalf("pr comment called %d time(s), want exactly once: %v", len(commentCalls), calls)
	}
	wantReviewNotesBodyFile(t, commentCalls[0])
}

// TestPublishContinuesWhenCommentPostingFails: when posting the review notes
// as a comment fails, publish still succeeds: the pull request stands, the file
// is kept for manual posting, the operator is warned (prefixed "jig:", like
// every other stderr warning, and naming the file and the pull request), and
// the journal and store are committed with the publish complete.
//
// This test must stay serial: it puts the fake gh on PATH and swaps the
// package-level warn hook, which every parallel test's Publish could call.
func TestPublishContinuesWhenCommentPostingFails(t *testing.T) {
	origWarn := warn
	defer func() { warn = origWarn }()
	var warning string
	warn = func(format string, args ...any) { warning = fmt.Sprintf(format, args...) }

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	logFile := useGithubHost(t, &d, "", "pr comment")

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v (expected to succeed despite comment failure)", err)
	}

	reviewNotesPath := filepath.Join(d.Store.Root, fx.Ticket, "pr", "review-notes.md")
	if !strings.HasPrefix(warning, "jig: ") {
		t.Errorf("warning = %q, want it prefixed %q like every other stderr warning", warning, "jig: ")
	}
	if !strings.Contains(warning, reviewNotesPath) {
		t.Errorf("warning = %q, want it to name %s", warning, reviewNotesPath)
	}
	if !strings.Contains(warning, "gh pr comment") {
		t.Errorf("warning = %q, want it to carry the failed call's own error", warning)
	}

	calls := loggedGh(t, logFile)
	if ghCalls(calls, "pr", "create") != 1 {
		t.Errorf("pr create not called or called multiple times: %v", calls)
	}

	if _, err := os.Stat(reviewNotesPath); os.IsNotExist(err) {
		t.Errorf("review notes file was not created at %s", reviewNotesPath)
	} else if err != nil {
		t.Errorf("could not stat review notes: %v", err)
	}

	lines, jerr := journal.Read(d.Store, fx.Ticket)
	if jerr != nil {
		t.Fatalf("journal.Read: %v", jerr)
	}
	var publishDone bool
	for _, l := range lines {
		if l.Event == "publish-done" {
			publishDone = true
			break
		}
	}
	if !publishDone {
		t.Errorf("journal has no publish-done line despite successful publish")
	}
}

// TestPublishWarnsWhenTheBriefHasNoIntentToPublish: a brief that bound as the
// intent source but has no "## " section to publish as the body's ## Intent
// publishes a body without that section - the owner's rule, pinned by the
// render tests - and publish warns the operator that it did, prefixed "jig:"
// like every other publish warning and naming the brief. The omission is
// deliberate, so the publish still succeeds; without the warning it is also
// silent, and the body loses the one section that says why the change exists
// with nothing in the run, the report or the store recording that it was ever
// there to lose.
//
// This test must stay serial: it swaps the package-level warn hook, which
// every parallel test's Publish could call.
func TestPublishWarnsWhenTheBriefHasNoIntentToPublish(t *testing.T) {
	origWarn := warn
	defer func() { warn = origWarn }()
	var warnings []string
	warn = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	briefPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")
	brief := "# Fixture ticket brief\n\nTwo small fixes, stated in prose with no \"## \" heading anywhere.\n"
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief.md: %v", err)
	}

	body, _ := publishScriptedTicket(t, fx, d, IntentSourceBrief, "", "")

	if strings.Contains(body, "## Intent") {
		t.Fatalf("pr/fixture-repo.md has an ## Intent section for a brief with none to publish:\n%s", body)
	}
	var warning string
	for _, w := range warnings {
		if strings.Contains(w, "Intent") {
			warning = w
			break
		}
	}
	if warning == "" {
		t.Fatalf("publish warned %q, want a warning that the body's ## Intent section was left out", warnings)
	}
	if !strings.HasPrefix(warning, "jig: ") {
		t.Errorf("warning = %q, want it prefixed %q like every other publish warning", warning, "jig: ")
	}
	if !strings.Contains(warning, briefPath) {
		t.Errorf("warning = %q, want it to name %s, the brief the section would have come from", warning, briefPath)
	}
	if !strings.Contains(warning, `no "## " section with text to publish as one`) {
		t.Errorf("warning = %q, want it to say why the section was left out: the brief has no \"## \" section with text to publish as one", warning)
	}
}

// TestPublishWarnsAboutTheOmittedIntentBeforeTheConfirmationPrompt: the whole
// point of the warning is that the operator can still answer "n" - editing the
// brief and gating again is cheaper than editing a published pull request
// (DECISIONS.md, and ARCHITECTURE.md's publish step: "`warn`, before the
// confirmation prompt") - so it has to be out by the time confirm asks, not
// after it, after the push, or among the post-PR work. The confirm hook here
// declines and records what had been warned by the time it was asked. The warn
// test above cannot hold this rule: it publishes with Yes, so confirm never
// runs and the warn block passes wherever in Publish it sits.
//
// The brief driven here is the other brief the omission covers: one with `## `
// headings whose first section is empty, which firstBriefSection reads the
// same way as a brief with no heading at all (DECISIONS.md, and
// TestFirstBriefSectionEmptyForAnEmptyFirstSection). That is the brief the
// warning's wording has to fit - "no \"## \" section with text to publish as
// one", since "no \"## \" section" would send its operator to add a heading
// the brief already has.
//
// This test must stay serial: it swaps the package-level warn and confirm
// hooks, which every parallel test's Publish reads.
func TestPublishWarnsAboutTheOmittedIntentBeforeTheConfirmationPrompt(t *testing.T) {
	origWarn, origConfirm := warn, confirm
	defer func() { warn, confirm = origWarn, origConfirm }()
	var warnings []string
	warn = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	asked := false
	var warnedWhenAsked []string
	confirm = func(string, string, string, bool) bool {
		asked = true
		warnedWhenAsked = append(warnedWhenAsked, warnings...)
		return false
	}

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	briefPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")
	brief := "# Fixture ticket brief\n\n## Intent\n\n## Plan\n\nTwo small fixes, under the second heading only.\n"
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief.md: %v", err)
	}
	gateToClean(t, fx, d)

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "PUBLISH_DECLINED")
	if !asked {
		t.Fatal("confirm was never asked, so nothing here can say when the warning came")
	}
	if body, _ := readPRFiles(t, d, fx.Ticket, "fixture-repo"); strings.Contains(body, "## Intent") {
		t.Fatalf("pr/fixture-repo.md has an ## Intent section for a brief whose first section is empty:\n%s", body)
	}
	var warning string
	for _, w := range warnedWhenAsked {
		if strings.Contains(w, "## Intent") {
			warning = w
			break
		}
	}
	if warning == "" {
		t.Fatalf("publish had warned %q by the confirmation prompt, want the omitted ## Intent section already warned about there, while an operator can still decline", warnedWhenAsked)
	}
	if !strings.Contains(warning, `no "## " section with text to publish as one`) {
		t.Errorf("warning = %q, want a reason that fits a brief whose first \"## \" section is merely empty", warning)
	}
}
