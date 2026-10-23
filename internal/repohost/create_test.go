package repohost_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/repohost"
)

// ghStubBinary builds the fake gh and returns its path.
func ghStubBinary(t *testing.T) string {
	t.Helper()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	return filepath.Join(fixture.GhStub(t), name)
}

// TestCreateRepoAsksGhForAPrivateRepositoryAsTheOrigin: gh is asked, with the
// source directory and the origin remote, for a private repository of the
// name; nothing else.
func TestCreateRepoAsksGhForAPrivateRepositoryAsTheOrigin(t *testing.T) {
	t.Parallel()
	bin := ghStubBinary(t)
	dir := t.TempDir()
	if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "gh.log")
	env := append(os.Environ(), "GH_STUB_LOG="+logFile, "GH_STUB_REPO_URLS={\"demo\":\"https://github.example/me/demo.git\"}")

	if err := repohost.CreateRepo(bin, env, "demo", dir); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if got, err := gitx.Run(dir, "remote", "get-url", "origin"); err != nil || got != "https://github.example/me/demo.git" {
		t.Errorf("origin = %q, %v; want the URL gh gave the repository", got, err)
	}
	calls := readLoggedArgv(t, logFile)
	if len(calls) != 1 {
		t.Fatalf("gh was run %d times: %v", len(calls), calls)
	}
	argv := strings.Join(calls[0], " ")
	for _, want := range []string{"repo create demo --private --source " + dir, "--remote origin"} {
		if !strings.Contains(argv, want) {
			t.Errorf("gh argv %q lacks %q", argv, want)
		}
	}
}

func TestCreateRepoReportsWhatGhSaid(t *testing.T) {
	t.Parallel()
	bin := ghStubBinary(t)
	env := append(os.Environ(), "GH_STUB_FAIL=repo create")
	err := repohost.CreateRepo(bin, env, "demo", t.TempDir())
	var ae *axi.Error
	if err == nil || !errors.As(err, &ae) || ae.Code != "HOST_CREATE_FAILED" || !strings.Contains(ae.Msg, `"demo"`) || !strings.Contains(ae.Msg, "failed on request") {
		t.Errorf("CreateRepo err = %v, want HOST_CREATE_FAILED naming the repository and what gh printed", err)
	}
	if len(ae.Help) == 0 {
		t.Error("the refusal has no help")
	}
}
