package tracker_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tracker"
)

// githubUnderStub builds the github adapter for owner/repo with the fake gh
// first on PATH, and returns it with the file the stub logs every argv to.
// pulls is the JSON the stub lists as the repo's pull requests ("" for none);
// fail names the call it fails on ("" for none). It sets the environment, so
// the caller's test cannot be parallel.
func githubUnderStub(t *testing.T, pulls, fail string) (tracker.Adapter, string) {
	t.Helper()
	stubDir := fixture.GhStub(t)
	t.Setenv("JIG_HOME", t.TempDir())
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "gh.log")
	t.Setenv("GH_STUB_LOG", logFile)
	t.Setenv("GH_STUB_STATE", filepath.Join(t.TempDir(), "gh.state"))
	t.Setenv("GH_STUB_PULLS", pulls)
	t.Setenv("GH_STUB_FAIL", fail)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cfg := project.Config{
		SchemaVersion: 1,
		Name:          "fixture",
		TicketFormat:  "JIG-{n}",
		Tracker:       "github",
		Repos:         []project.Repo{{Remote: "git@github.com:owner/repo.git"}},
	}
	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	return a, logFile
}

// findOpenPRCall is the call the adapter makes to find the open pull request:
// the pull requests endpoint, asked with the parameters that decide the
// answer, the qualified head, the base and the state.
var findOpenPRCall = []string{"api", "repos/owner/repo/pulls", "--method", "GET", "-f", "head=owner:jig/JIG-1", "-f", "base=main", "-f", "state=open"}

var (
	openPull      = fixture.GhPull{Number: 7, Owner: "owner", Head: "jig/JIG-1", Base: "main"}
	openPullCased = fixture.GhPull{Number: 9, Owner: "Owner", Head: "jig/JIG-1", Base: "main"}
	forkPull      = fixture.GhPull{Number: 8, Owner: "someone-else", Head: "jig/JIG-1", Base: "main"}
	closedPull    = fixture.GhPull{Number: 5, State: "closed", Owner: "owner", Head: "jig/JIG-1", Base: "main"}
	mergedPull    = fixture.GhPull{Number: 4, State: "closed", Merged: true, Owner: "owner", Head: "jig/JIG-1", Base: "main"}
	otherBasePull = fixture.GhPull{Number: 3, Owner: "owner", Head: "jig/JIG-1", Base: "release"}
	otherHeadPull = fixture.GhPull{Number: 2, Owner: "owner", Head: "other", Base: "main"}
)

// forkPulls is n pull requests from n different forks whose branch is named
// like the ticket's, all into main and all open.
func forkPulls(n int) []fixture.GhPull {
	out := make([]fixture.GhPull, n)
	for i := range out {
		out[i] = fixture.GhPull{Number: 100 + i, Owner: "fork-" + strings.Repeat("x", i+1), Head: "jig/JIG-1", Base: "main"}
	}
	return out
}

// TestGithubFindsTheOpenPRForABranch: the github adapter is a tracker.PRUpdater
// and finds the open pull request from a branch into its base by asking the
// pull requests endpoint for exactly that - the qualified head (owner:branch),
// the base and the open state, which GitHub applies - answering the URL, or ""
// when there is none. A pull request from a fork whose branch has the same name
// is another head, so it is not found, however many of them there are: an
// answer cut short by a page limit would read as no pull request, and a
// publish would open a second one that GitHub refuses. Two open pull requests
// for one head and base cannot exist, so two found is an error, never a pick
// between them.
//
// What is not an open pull request from the branch into the base is not this
// publish's, and is not found: a closed or a merged pull request is history,
// a decision about that pull request and not about the branch's next
// delivery, and an open one into another base is another delivery (a stacked
// one, say). The fake gh applies the parameters the way GitHub does and
// returns one page of 30, so it is GitHub that leaves these out, and the
// arguments the adapter passes that decide it.
func TestGithubFindsTheOpenPRForABranch(t *testing.T) {
	const wantSeven = "https://github.example/owner/repo/pull/7"
	cases := []struct {
		name    string
		pulls   []fixture.GhPull
		raw     string // the listing as it is, when it is not built from pulls
		want    string
		wantErr string
	}{
		{name: "no pull request"},
		{name: "one open pull request", pulls: []fixture.GhPull{openPull}, want: wantSeven},
		{name: "only a closed pull request", pulls: []fixture.GhPull{closedPull}},
		{name: "only a merged pull request", pulls: []fixture.GhPull{mergedPull}},
		{name: "only a pull request into another base", pulls: []fixture.GhPull{otherBasePull}},
		{name: "only a pull request from another branch", pulls: []fixture.GhPull{otherHeadPull}},
		{name: "the open pull request beside closed, merged and other ones", pulls: []fixture.GhPull{closedPull, mergedPull, otherBasePull, otherHeadPull, openPull}, want: wantSeven},
		{name: "the owner's name compared without case", pulls: []fixture.GhPull{openPullCased}, want: "https://github.example/owner/repo/pull/9"},
		{name: "only a fork's pull request", pulls: []fixture.GhPull{forkPull}},
		{name: "a fork's pull request beside this repo's", pulls: []fixture.GhPull{forkPull, openPull}, want: wantSeven},
		{name: "more forks' pull requests than a page holds, this repo's after them", pulls: append(forkPulls(35), openPull), want: wantSeven},
		{name: "two open pull requests", pulls: []fixture.GhPull{openPull, openPullCased}, wantErr: "2 open pull requests"},
		{name: "an answer that is not a list", raw: `{"message":"not a list"}`, wantErr: "parse"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pulls := tc.raw
			if pulls == "" && tc.pulls != nil {
				pulls = fixture.GhPulls(t, tc.pulls...)
			}
			a, logFile := githubUnderStub(t, pulls, "")
			updater, ok := a.(tracker.PRUpdater)
			if !ok {
				t.Fatal("github adapter does not implement tracker.PRUpdater")
			}
			got, err := updater.FindOpenPR("jig/JIG-1", "main")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("FindOpenPR: err = %v, want one containing %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("FindOpenPR: %v", err)
			}
			if got != tc.want {
				t.Errorf("FindOpenPR = %q, want %q", got, tc.want)
			}

			lines := readLoggedArgv(t, logFile)
			if !anyLineHasPrefix(lines, findOpenPRCall...) {
				t.Errorf("no logged `gh %s` call in %v", strings.Join(findOpenPRCall, " "), lines)
			}
			if anyLineHasPrefix(lines, "pr", "create") || anyLineHasPrefix(lines, "pr", "edit") {
				t.Errorf("finding a pull request created or edited one: %v", lines)
			}
		})
	}
}

// TestGithubFindingAPRFailsLoudly: a gh that cannot answer is an error the
// caller sees, never "no pull request", which would open a second one.
func TestGithubFindingAPRFailsLoudly(t *testing.T) {
	a, _ := githubUnderStub(t, fixture.GhPulls(t, openPull), "api repos/owner/repo/pulls")
	got, err := a.(tracker.PRUpdater).FindOpenPR("jig/JIG-1", "main")
	if err == nil || !strings.Contains(err.Error(), "gh api repos/owner/repo/pulls") {
		t.Fatalf("FindOpenPR over a failing gh: (%q, %v), want an error naming the call", got, err)
	}
}

// TestGithubUpdatesAPRsBody: UpdatePR replaces the pull request's body from a
// file with `gh pr edit --body-file`, and touches nothing else about it.
func TestGithubUpdatesAPRsBody(t *testing.T) {
	a, logFile := githubUnderStub(t, "", "")
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte("the new body"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	const url = "https://github.example/owner/repo/pull/7"
	if err := a.(tracker.PRUpdater).UpdatePR(url, bodyFile); err != nil {
		t.Fatalf("UpdatePR: %v", err)
	}

	lines := readLoggedArgv(t, logFile)
	if len(lines) != 1 || !anyLineHasPrefix(lines, "pr", "edit", url, "--repo", "owner/repo", "--body-file", bodyFile) {
		t.Fatalf("logged calls = %v, want exactly `pr edit %s --repo owner/repo --body-file %s`", lines, url, bodyFile)
	}
	if anyLineContainsAll(lines, "--title") {
		t.Errorf("updating a pull request set its title: %v", lines)
	}

	// A failing gh is an error.
	failing, _ := githubUnderStub(t, "", "pr edit")
	if err := failing.(tracker.PRUpdater).UpdatePR(url, bodyFile); err == nil || !strings.Contains(err.Error(), "gh pr edit") {
		t.Fatalf("UpdatePR over a failing gh: %v, want an error naming the call", err)
	}
}

// TestLocalTrackerOpensNoPullRequests: only a tracker with a pull request
// concept has the capability; the local adapter neither creates nor updates
// one, and publish leaves the pull request to the human.
func TestLocalTrackerOpensNoPullRequests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	a, err := tracker.New(project.Config{SchemaVersion: 1, Name: "fixture", TicketFormat: "JIG-{n}", Tracker: "local"}, st)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	if _, ok := a.(tracker.PRUpdater); ok {
		t.Error("the local adapter implements tracker.PRUpdater")
	}
	if _, ok := a.(tracker.PRCreator); ok {
		t.Error("the local adapter implements tracker.PRCreator")
	}
}
