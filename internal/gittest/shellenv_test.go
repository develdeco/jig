package gittest_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// childEnvVar makes a re-run of this test binary report what it sees instead
// of testing; any non-empty value turns the child on, the value names nothing.
const childEnvVar = "GITTEST_CHILD_REPORT"

// shellVars is a person's shell exporting git state, as seen by a test run.
var shellVars = []string{
	"GIT_AUTHOR_NAME=shell-author",
	"GIT_AUTHOR_EMAIL=shell-author@example.invalid",
	"GIT_DIR=/shell/.git",
	"EMAIL=shell@example.invalid",
}

// TestChildReportsGitVariables is the child half of the tests below: run on
// its own it does nothing.
func TestChildReportsGitVariables(t *testing.T) {
	if os.Getenv(childEnvVar) == "" {
		t.Skip("only runs as the child of another test")
	}
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_DIR", "EMAIL"} {
		if v, ok := os.LookupEnv(name); ok {
			t.Logf("SEES %s=%s", name, v)
		}
	}
}

// runChild re-runs this test binary for the child test with shellVars set and
// returns the lines in which it reports a variable it sees.
func runChild(t *testing.T, extra ...string) []string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildReportsGitVariables$", "-test.v")
	cmd.Env = append(append(os.Environ(), shellVars...), childEnvVar+"=1")
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	var seen []string
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "SEES "); i >= 0 {
			seen = append(seen, strings.TrimSpace(line[i+len("SEES "):]))
		}
	}
	return seen
}

func TestRunDropsTheShellsGitVariables(t *testing.T) {
	if os.Getenv(childEnvVar) != "" {
		t.Skip("the child does not run the parent's tests")
	}
	if seen := runChild(t); len(seen) != 0 {
		t.Fatalf("the test binary kept the shell's variables: %v", seen)
	}
}

func TestRunKeepsThePinnedIdentity(t *testing.T) {
	if os.Getenv(childEnvVar) != "" {
		t.Skip("the child does not run the parent's tests")
	}
	seen := runChild(t, pinChildEnvVar+"=1")
	got := strings.Join(seen, "\n")
	for _, want := range []string{"GIT_AUTHOR_NAME=jig-fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=jig-fixture"} {
		if !strings.Contains(got, want) {
			t.Errorf("pinned %q is gone; the child sees %v", want, seen)
		}
	}
	for _, shell := range []string{"shell-author", "/shell/.git", "shell@example.invalid"} {
		if strings.Contains(got, shell) {
			t.Errorf("the shell's %q survived; the child sees %v", shell, seen)
		}
	}
}
