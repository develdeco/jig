package repohost_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/repohost"
)

// ghLogLine is one line of the fake gh's log.
type ghLogLine struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir"`
}

// loggedGhCalls reads the fake gh's log in full.
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

// hostUnderStub builds a GitHub host with the fake gh first on PATH.
// pulls is the JSON the stub lists as the repo's pull requests ("" for none);
// fail names the call it fails on ("" for none).
func hostUnderStub(t *testing.T, pulls, fail string) (repohost.Host, string) {
	t.Helper()
	stubDir := fixture.GhStub(t)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "gh.log")
	t.Setenv("GH_STUB_LOG", logFile)
	t.Setenv("GH_STUB_STATE", filepath.Join(t.TempDir(), "gh.state"))
	t.Setenv("GH_STUB_BODY_STATE", filepath.Join(t.TempDir(), "gh-body.state"))
	t.Setenv("GH_STUB_PULLS", pulls)
	t.Setenv("GH_STUB_FAIL", fail)

	h, err := repohost.New("git@github.com:owner/repo.git")
	if err != nil {
		t.Fatalf("repohost.New: %v", err)
	}
	return h, logFile
}

var (
	openPull      = fixture.GhPull{Number: 7, Owner: "owner", Head: "jig/JIG-1", Base: "main"}
	openPullCased = fixture.GhPull{Number: 9, Owner: "Owner", Head: "jig/JIG-1", Base: "main"}
	forkPull      = fixture.GhPull{Number: 8, Owner: "someone-else", Head: "jig/JIG-1", Base: "main"}
	closedPull    = fixture.GhPull{Number: 5, State: "closed", Owner: "owner", Head: "jig/JIG-1", Base: "main"}
	mergedPull    = fixture.GhPull{Number: 4, State: "closed", Merged: true, Owner: "owner", Head: "jig/JIG-1", Base: "main"}
	otherBasePull = fixture.GhPull{Number: 3, Owner: "owner", Head: "jig/JIG-1", Base: "release"}
	otherHeadPull = fixture.GhPull{Number: 2, Owner: "owner", Head: "other", Base: "main"}
)

func forkPulls(n int) []fixture.GhPull {
	out := make([]fixture.GhPull, n)
	for i := range out {
		out[i] = fixture.GhPull{Number: 100 + i, Owner: "fork-" + strings.Repeat("x", i+1), Head: "jig/JIG-1", Base: "main"}
	}
	return out
}

// readLoggedArgv reads the gh stub's logged argv from the log file,
// dropping the binary itself (argv[0]) from each call.
func readLoggedArgv(t *testing.T, logFile string) [][]string {
	t.Helper()
	calls := loggedGhCalls(t, logFile)
	out := make([][]string, len(calls))
	for i, c := range calls {
		out[i] = c.Argv[1:]
	}
	return out
}

// anyLineHasPrefix checks whether any logged line starts with the given prefix.
func anyLineHasPrefix(lines [][]string, prefix ...string) bool {
	for _, line := range lines {
		if len(line) >= len(prefix) {
			match := true
			for i, p := range prefix {
				if line[i] != p {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// anyLineContainsAll checks whether any logged line contains all the given values.
func anyLineContainsAll(lines [][]string, vals ...string) bool {
	for _, line := range lines {
		hasAll := true
		for _, v := range vals {
			found := false
			for _, l := range line {
				if l == v {
					found = true
					break
				}
			}
			if !found {
				hasAll = false
				break
			}
		}
		if hasAll {
			return true
		}
	}
	return false
}

var findOpenPRCall = []string{"api", "repos/owner/repo/pulls", "--method", "GET", "-f", "head=owner:jig/JIG-1", "-f", "base=main", "-f", "state=open"}

// TestFindsTheOpenPRForABranch: the host finds the open pull request from a
// branch into its base by asking the pull requests endpoint for exactly that.
func TestFindsTheOpenPRForABranch(t *testing.T) {
	const wantSeven = "https://github.example/owner/repo/pull/7"
	cases := []struct {
		name    string
		pulls   []fixture.GhPull
		raw     string
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
			h, logFile := hostUnderStub(t, pulls, "")
			got, err := h.FindOpenPR("jig/JIG-1", "main")
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

// TestFindingAPRFailsLoudly: a gh that cannot answer is an error.
func TestFindingAPRFailsLoudly(t *testing.T) {
	h, _ := hostUnderStub(t, fixture.GhPulls(t, openPull), "api repos/owner/repo/pulls")
	got, err := h.FindOpenPR("jig/JIG-1", "main")
	if err == nil || !strings.Contains(err.Error(), "gh api repos/owner/repo/pulls") {
		t.Fatalf("FindOpenPR over a failing gh: (%q, %v), want an error naming the call", got, err)
	}
}

// TestUpdatesAPRsBody: UpdatePR replaces the pull request's body from a file.
func TestUpdatesAPRsBody(t *testing.T) {
	h, logFile := hostUnderStub(t, "", "")
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte("the new body"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	const url = "https://github.example/owner/repo/pull/7"
	if err := h.UpdatePR(url, bodyFile); err != nil {
		t.Fatalf("UpdatePR: %v", err)
	}

	lines := readLoggedArgv(t, logFile)
	if len(lines) != 1 || !anyLineHasPrefix(lines, "pr", "edit", url, "--repo", "owner/repo", "--body-file", bodyFile) {
		t.Fatalf("logged calls = %v, want exactly `pr edit %s --repo owner/repo --body-file %s`", lines, url, bodyFile)
	}
}

// TestReadPRBody: ReadPRBody reads the body back through gh.
func TestReadPRBody(t *testing.T) {
	h, _ := hostUnderStub(t, "", "")
	t.Setenv("GH_STUB_BODY", "## Demo\n\n- https://github.example/user-attachments/assets/1: it works\n")

	body, err := h.ReadPRBody("https://github.example/owner/repo/pull/7")
	if err != nil {
		t.Fatalf("ReadPRBody: %v", err)
	}
	if body != "## Demo\n\n- https://github.example/user-attachments/assets/1: it works\n" {
		t.Errorf("ReadPRBody = %q, want $GH_STUB_BODY's text", body)
	}
}

// hostWithEnv builds a GitHub host over the fake gh with exactly the
// variables vars add to the process's own environment, never editing the
// environment the test itself runs in.
func hostWithEnv(t *testing.T, vars ...string) (repohost.Host, string) {
	t.Helper()
	stubDir := fixture.GhStub(t)
	ghBin := filepath.Join(stubDir, "gh")
	if runtime.GOOS == "windows" {
		ghBin += ".exe"
	}
	logFile := filepath.Join(t.TempDir(), "gh.log")
	env := append(os.Environ(), "GH_STUB_LOG="+logFile, "GH_STUB_STATE="+filepath.Join(t.TempDir(), "gh.state"))
	h, err := repohost.NewWithEnv("git@github.com:owner/repo.git", ghBin, append(env, vars...))
	if err != nil || h == nil {
		t.Fatalf("repohost.NewWithEnv: (%v, %v)", h, err)
	}
	return h, logFile
}

// TestParsePRURL: a pull request URL on any host reads into the host, the
// repository and the number; a tab, a query or a fragment after the number
// is ignored; anything that is not a pull request's URL is PR_URL_INVALID.
func TestParsePRURL(t *testing.T) {
	good := []struct {
		raw  string
		want repohost.PRRef
	}{
		{"https://github.com/owner/repo/pull/7", repohost.PRRef{Host: "github.com", Owner: "owner", Repo: "repo", Number: 7}},
		{"  https://github.com/owner/repo/pull/7/  ", repohost.PRRef{Host: "github.com", Owner: "owner", Repo: "repo", Number: 7}},
		{"https://github.com/owner/repo/pull/12/files?w=1#diff", repohost.PRRef{Host: "github.com", Owner: "owner", Repo: "repo", Number: 12}},
		{"http://ghe.example.test/team/service/pull/301/commits", repohost.PRRef{Host: "ghe.example.test", Owner: "team", Repo: "service", Number: 301}},
	}
	for _, c := range good {
		got, err := repohost.ParsePRURL(c.raw)
		if err != nil || got != c.want {
			t.Errorf("ParsePRURL(%q) = (%+v, %v), want %+v", c.raw, got, err, c.want)
		}
	}
	bad := []string{
		"", "not a url", "ftp://github.com/owner/repo/pull/7", "https:///owner/repo/pull/7",
		"https://github.com/owner/repo", "https://github.com/owner/repo/issues/7",
		"https://github.com/owner/repo/pull/", "https://github.com/owner/repo/pull/seven",
		"https://github.com/owner/repo/pull/0", "https://github.com//repo/pull/7",
	}
	for _, raw := range bad {
		_, err := repohost.ParsePRURL(raw)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "PR_URL_INVALID" {
			t.Errorf("ParsePRURL(%q) error = %v, want a PR_URL_INVALID", raw, err)
		}
	}
}

// TestPRHeadReadsTheBranchAndTheCommit: PRHead asks gh for the three fields
// jig compares and reads them back, the state upper-cased and OPEN when the
// host does not say.
func TestPRHeadReadsTheBranchAndTheCommit(t *testing.T) {
	const prURL = "https://github.example/owner/repo/pull/7"
	oid := strings.Repeat("a1", 20)
	h, logFile := hostWithEnv(t, "GH_STUB_HEAD_REF=jig/JIG-1", "GH_STUB_HEAD_OID="+oid)

	got, err := h.PRHead(prURL)
	if err != nil {
		t.Fatalf("PRHead: %v", err)
	}
	if want := (repohost.PRHead{Branch: "jig/JIG-1", SHA: oid, State: "OPEN"}); got != want {
		t.Errorf("PRHead = %+v, want %+v", got, want)
	}
	lines := readLoggedArgv(t, logFile)
	if len(lines) != 1 || !anyLineHasPrefix(lines, "pr", "view", prURL, "--repo", "owner/repo", "--json", "headRefName,headRefOid,state") {
		t.Errorf("logged calls = %v, want exactly `pr view %s --repo owner/repo --json headRefName,headRefOid,state`", lines, prURL)
	}

	merged, _ := hostWithEnv(t, "GH_STUB_HEAD_REF=jig/JIG-1", "GH_STUB_HEAD_OID="+oid, "GH_STUB_PR_STATE=merged")
	if got, err := merged.PRHead(prURL); err != nil || got.State != "MERGED" {
		t.Errorf("PRHead of a merged pull request = (%+v, %v), want state MERGED", got, err)
	}
}

// TestPRHeadFailsLoudly: a gh that fails, or a pull request that names no
// head branch, is an error, never an empty head that matches nothing.
func TestPRHeadFailsLoudly(t *testing.T) {
	const prURL = "https://github.example/owner/repo/pull/7"
	failing, _ := hostWithEnv(t, "GH_STUB_FAIL=pr view")
	if got, err := failing.PRHead(prURL); err == nil || !strings.Contains(err.Error(), "pr view") {
		t.Errorf("PRHead over a failing gh = (%+v, %v), want an error naming the call", got, err)
	}
	headless, _ := hostWithEnv(t, "GH_STUB_HEAD_OID="+strings.Repeat("b2", 20))
	if got, err := headless.PRHead(prURL); err == nil || !strings.Contains(err.Error(), "no head branch") {
		t.Errorf("PRHead of a pull request with no head branch = (%+v, %v), want an error saying so", got, err)
	}
}

// TestNewReturnsNilForNonGitHub: New returns nil for non-GitHub remotes.
func TestNewReturnsNilForNonGitHub(t *testing.T) {
	cases := []string{
		"/local/path/to/repo",
		"file:///local/path",
		"git@gitlab.com:owner/repo.git",
		"https://gitlab.com/owner/repo",
	}
	for _, remote := range cases {
		t.Run(remote, func(t *testing.T) {
			h, err := repohost.New(remote)
			if h != nil {
				t.Errorf("New(%q) returned a host, want nil", remote)
			}
			if err != nil {
				t.Errorf("New(%q) returned error %v, want nil", remote, err)
			}
		})
	}
}

// TestNewAcceptsGitHubRemotes: New returns a host for GitHub remotes.
func TestNewAcceptsGitHubRemotes(t *testing.T) {
	cases := []string{
		"git@github.com:owner/repo.git",
		"git@github.com:owner/repo",
		"https://github.com/owner/repo.git",
		"https://github.com/owner/repo",
		"ssh://user@github.com/owner/repo",
		"ssh://user@github.com/owner/repo.git",
	}
	for _, remote := range cases {
		t.Run(remote, func(t *testing.T) {
			// Set up fake gh on PATH
			stubDir := fixture.GhStub(t)
			t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GH_STUB_LOG", filepath.Join(t.TempDir(), "gh.log"))

			h, err := repohost.New(remote)
			if h == nil {
				t.Errorf("New(%q) returned nil, want a host", remote)
			}
			if err != nil {
				t.Errorf("New(%q) returned error %v, want nil", remote, err)
			}
		})
	}
}
