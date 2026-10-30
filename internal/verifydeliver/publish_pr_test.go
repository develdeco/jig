package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/repohost"
)

// githubRemote is the github.com remote the host useGithubHost hands
// Publish is built from. Its repo name is "fixture-repo", matching the
// fixture's real remote (fixture.Build's repoRemote), so every argv gh is
// asked for names the same repo the fixture itself ships.
const githubRemote = "git@github.com:owner/fixture-repo.git"

// useGithubHost hands d a GitHub pull request host (repohost.NewWithEnv on
// githubRemote) running against the fake gh, which lists pulls
// (fixture.GhPulls) as the repo's pull requests ("" for none) and fails the
// call named by fail ("" for none). It returns the file gh logs every argv
// to and a function that adds environment variables to the gh subprocess.
// d.Cfg's own remote is left as the fixture's local path, which every
// clone, fetch and push Publish makes still reaches, and nothing here edits
// the process environment: the gh binary and the variables that drive it
// are the injected host's own, so a test using it stays parallel.
func useGithubHost(t *testing.T, d *Deps, pulls, fail string) (string, func(...string)) {
	t.Helper()
	stubDir := fixture.GhStub(t)
	ghBin := filepath.Join(stubDir, "gh")
	if runtime.GOOS == "windows" {
		ghBin = filepath.Join(stubDir, "gh.exe")
	}

	logFile := filepath.Join(t.TempDir(), "gh.log")
	stateFile := filepath.Join(t.TempDir(), "gh.state")
	bodyStateFile := filepath.Join(t.TempDir(), "gh-body.state")

	ghEnv := append(append([]string{}, os.Environ()...),
		"GH_STUB_LOG="+logFile,
		"GH_STUB_STATE="+stateFile,
		"GH_STUB_BODY_STATE="+bodyStateFile,
		"GH_STUB_PULLS="+pulls,
		"GH_STUB_FAIL="+fail,
	)

	setHost := func(env []string) {
		host, err := repohost.NewWithEnv(githubRemote, ghBin, env)
		if err != nil {
			t.Fatalf("repohost.NewWithEnv %s: %v", githubRemote, err)
		}
		if host == nil {
			t.Fatalf("repohost.NewWithEnv %s: no host for a github.com remote", githubRemote)
		}
		d.Host = host
	}
	setHost(ghEnv)

	addEnv := func(vars ...string) {
		ghEnv = append(ghEnv, vars...)
		setHost(ghEnv)
	}
	return logFile, addEnv
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
