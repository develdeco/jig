package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// gitBudgetFile is the budget of git processes per package that CI's Linux
// leg holds the suite to (internal/gitbudget).
const gitBudgetFile = ".github/git-budget.yaml"

// TestCIWorkflowCountsGitProcesses asserts the test job's Linux leg counts
// the git processes of every package it tests and holds them to the
// budget: the counter is built on that leg, the go test step runs each test
// binary through it there with its trace directory set (goTestListRun pins
// the -exec and -count=1), and a step after go test, on exactly that leg and
// never allowed to fail quietly, checks the counts of the same package list.
func TestCIWorkflowCountsGitProcesses(t *testing.T) {
	jobs := workflowJobs(t, repoRoot(t), "ci.yml")
	test, _ := yamlMap(jobs["test"])
	steps, _ := yamlSlice(test["steps"])
	requireStepOnExactly(t, steps, "git process counter build", "go build -o \"$RUNNER_TEMP/gitbudget\" ./internal/gitbudget", "runner.os == 'Linux'")
	goTest, id, at := -1, "", -1
	for i, sv := range steps {
		step, _ := yamlMap(sv)
		run, _ := yamlString(step["run"])
		if m := goTestListRun.FindStringSubmatch(strings.TrimSpace(run)); m != nil {
			goTest, id = i, m[1]
			env, _ := yamlMap(step["env"])
			if dir, _ := yamlString(env["GITBUDGET_DIR"]); dir != "${{ runner.os == 'Linux' && format('{0}/gitcount', runner.temp) || '' }}" {
				t.Errorf("ci.yml: the go test step's GITBUDGET_DIR = %q, want the Linux leg's $RUNNER_TEMP/gitcount, where the budget step reads the counts", dir)
			}
		}
		if strings.Contains(run, "gitbudget\" check") {
			at = i
		}
	}
	if goTest < 0 {
		t.Fatalf("ci.yml: no go test step runs the package list through the git process counter on the Linux leg")
	}
	want := "\"$RUNNER_TEMP/gitbudget\" check " + gitBudgetFile + " \"$RUNNER_TEMP/gitcount\" ${{ steps." + id + ".outputs.list }}"
	requireStepOnExactly(t, steps, "git budget check", want, "runner.os == 'Linux'")
	if at < goTest {
		t.Errorf("ci.yml: the git budget check runs before go test, so it reads no counts")
	}
}

// TestGitBudgetNamesPackages asserts the budget file parses as the counter
// reads it, and that each entry names a package directory of this module,
// so a package renamed or removed takes its entry with it.
func TestGitBudgetNamesPackages(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(gitBudgetFile)))
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		Margin   int            `yaml:"margin"`
		Packages map[string]int `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &b); err != nil {
		t.Fatalf("%s: %v", gitBudgetFile, err)
	}
	if b.Margin <= 0 || b.Margin > 50 {
		t.Errorf("%s: margin %d, want a percentage from 1 to 50", gitBudgetFile, b.Margin)
	}
	for pkg := range b.Packages {
		goFiles, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pkg), "*.go"))
		if pkg == "." || strings.Contains(pkg, "..") || len(goFiles) == 0 {
			t.Errorf("%s: %q is not a package directory of this module", gitBudgetFile, pkg)
		}
	}
}
