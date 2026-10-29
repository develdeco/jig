package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckHoldsEachCountToItsMarginBothWays(t *testing.T) {
	b := &budget{Margin: 10, Packages: map[string]int{"e2e": 1000, "lint": 4, "gone": 50}}
	for _, c := range []struct {
		name   string
		counts map[string]int
		ok     bool
		want   string
	}{
		{"within", map[string]int{"e2e": 1100, "lint": 6}, true, ""},
		{"over", map[string]int{"e2e": 1101}, false, "e2e"},
		{"under", map[string]int{"e2e": 899}, false, "e2e"},
		{"a handful moves by the minimum", map[string]int{"lint": 2}, true, ""},
		{"a handful past the minimum", map[string]int{"lint": 7}, false, "lint"},
		{"new git work in a package with no entry", map[string]int{"internal/tui": 3}, false, "internal/tui"},
		{"no git work and no entry", map[string]int{"internal/tui": 0}, true, ""},
		{"a package that did not run is not judged", map[string]int{}, true, ""},
	} {
		rows, ok := b.check(c.counts)
		if ok != c.ok {
			t.Errorf("%s: check(%v) ok = %v, want %v: %v", c.name, c.counts, ok, c.ok, rows)
		}
		for _, r := range rows {
			if (r.verdict != "ok") != (r.pkg == c.want) {
				t.Errorf("%s: row %+v, want only %q failing", c.name, r, c.want)
			}
		}
	}
}

func TestSetAndEncodeRoundTrip(t *testing.T) {
	b := &budget{Margin: 10, Packages: map[string]int{"e2e": 1000, "lint": 4}}
	b.set(map[string]int{"e2e": 900, "lint": 0, "cmd/jig": 12})
	path := filepath.Join(t.TempDir(), "budget.yaml")
	if err := os.WriteFile(path, b.encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Margin != 10 || len(got.Packages) != 2 || got.Packages["e2e"] != 900 || got.Packages["cmd/jig"] != 12 {
		t.Errorf("round trip = %+v, want e2e 900 and cmd/jig 12, lint dropped", got)
	}
	if !strings.HasPrefix(string(b.encode()), budgetHeader) || !strings.Contains(string(b.encode()), "  cmd/jig: 12\n  e2e: 900\n") {
		t.Errorf("encode:\n%s", b.encode())
	}
}

func TestReadBudgetRefusesWhatItCannotJudge(t *testing.T) {
	for name, body := range map[string]string{
		"unknown key":   "margin: 10\npackages: {}\nslack: 3\n",
		"zero budget":   "margin: 10\npackages:\n  e2e: 0\n",
		"margin over":   "margin: 101\npackages: {}\n",
		"not a mapping": "- e2e\n",
	} {
		path := filepath.Join(t.TempDir(), "budget.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readBudget(path); err == nil {
			t.Errorf("%s: readBudget accepted %q", name, body)
		}
	}
}

func TestSetEnvReplacesEveryEarlierEntry(t *testing.T) {
	got := setEnv([]string{"A=1", "GIT_TRACE2_EVENT=/outer", "B=2", "git_trace2_event=/other"}, "GIT_TRACE2_EVENT", "/mine")
	if strings.Join(got, " ") != "A=1 B=2 GIT_TRACE2_EVENT=/mine" {
		t.Errorf("setEnv = %v", got)
	}
}

func TestCountDirNameAndModule(t *testing.T) {
	if got := countDirName("internal/gitx"); got != "internal%2Fgitx" {
		t.Errorf("countDirName = %q", got)
	}
	module, rel, err := moduleOf(".")
	if err != nil || module != "github.com/develdeco/jig" || rel != "internal/gitbudget" {
		t.Errorf("moduleOf(.) = %q, %q, %v, want github.com/develdeco/jig internal/gitbudget", module, rel, err)
	}
	for pkg, want := range map[string]string{"github.com/develdeco/jig/e2e": "e2e", "github.com/develdeco/jig": ".", "github.com/develdeco/jigx/e2e": "", "example.com/e2e": ""} {
		got, ok := relPackage("github.com/develdeco/jig", pkg)
		if got != want && ok {
			t.Errorf("relPackage(%s) = %q, want %q", pkg, got, want)
		}
		if ok != (want != "") {
			t.Errorf("relPackage(%s) ok = %v", pkg, ok)
		}
	}
}

// TestCountsTheGitProcessesOfTheBinaryItRuns builds gitbudget, has it run
// git as go test would run a test binary, in this package's directory, and
// then checks and writes the budget from what it counted.
func TestCountsTheGitProcessesOfTheBinaryItRuns(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("no git: %v", err)
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "gitbudget")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	counts := filepath.Join(tmp, "counts")
	wrapped := exec.Command(bin, git, "version")
	// An outer trace directory, as this test has when CI counts its own
	// package, must not take the wrapped binary's traces.
	wrapped.Env = append(os.Environ(), "GIT_TRACE2_EVENT="+filepath.Join(tmp, "outer"), "GITBUDGET_DIR="+counts)
	out, err := wrapped.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "git version ") {
		t.Fatalf("gitbudget git version: %v\n%s", err, out)
	}
	n, err := countTraces(filepath.Join(counts, "internal%2Fgitbudget"))
	if err != nil || n < 1 {
		t.Fatalf("%d traces counted for one git process (%v)", n, err)
	}

	const pkg = "github.com/develdeco/jig/internal/gitbudget"
	budgetFile := filepath.Join(tmp, "budget.yaml")
	runCheck := func(cmd string) (int, string) {
		var stdout, stderr bytes.Buffer
		code := run([]string{cmd, budgetFile, counts, pkg}, &stdout, &stderr)
		return code, stdout.String() + stderr.String()
	}
	if err := os.WriteFile(budgetFile, []byte("margin: 10\npackages:\n  internal/gitbudget: 50\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runCheck("check"); code != 1 || !strings.Contains(out, "under") || !strings.Contains(out, "internal/gitbudget: ") {
		t.Errorf("check against a stale budget: exit %d, want 1 with the budget to put in place:\n%s", code, out)
	}
	if code, out := runCheck("write"); code != 0 {
		t.Fatalf("write: exit %d\n%s", code, out)
	}
	if code, out := runCheck("check"); code != 0 {
		t.Errorf("check after write: exit %d, want 0:\n%s", code, out)
	}
	var stderr bytes.Buffer
	if code := run([]string{"check", budgetFile, counts, "example.com/other"}, new(bytes.Buffer), &stderr); code != 1 {
		t.Errorf("check of a package outside the module: exit %d, want 1: %s", code, stderr.String())
	}
}
