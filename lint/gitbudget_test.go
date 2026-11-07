package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// gitBudgetFile is the threshold CI's Linux leg holds each package's git
// process count to, against main's measured baseline (internal/gitbudget).
const gitBudgetFile = ".github/git-budget.yaml"

// gitBaselineKeyPrefix is the cache key prefix main's push run saves the
// baseline under, and a pull request's run restores the newest of.
const gitBaselineKeyPrefix = "gitbudget-main-"

// stepNamed returns the step named name.
func stepNamed(steps []interface{}, name string) (map[string]interface{}, bool) {
	for _, sv := range steps {
		step, _ := yamlMap(sv)
		if n, _ := yamlString(step["name"]); n == name {
			return step, true
		}
	}
	return nil, false
}

// requireStepIf asserts the step named name has exactly the condition want
// and, when uses is not empty, runs that action.
func requireStepIf(t *testing.T, steps []interface{}, name, uses, want string) map[string]interface{} {
	t.Helper()
	step, ok := stepNamed(steps, name)
	if !ok {
		t.Errorf("ci.yml: the test job has no step named %q", name)
		return nil
	}
	if hasContinueOnError(step) {
		t.Errorf("ci.yml: the %q step has continue-on-error, so it could fail without failing the job", name)
	}
	if got, _ := yamlString(step["if"]); got != want {
		t.Errorf("ci.yml: the %q step's if = %q, want exactly %q", name, got, want)
	}
	if got, _ := yamlString(step["uses"]); uses != "" && got != uses {
		t.Errorf("ci.yml: the %q step uses %q, want %q", name, got, uses)
	}
	return step
}

// TestCIWorkflowCountsGitProcesses asserts the test job's Linux leg counts
// the git processes of every package it tests and compares them with main's
// baseline: the counter is built on that leg, the go test step runs each
// test binary through it there with its trace directory set (goTestListRun
// pins the -exec and -count=1), a step after go test, on exactly that leg and
// never allowed to fail quietly, checks the counts of the same package list
// against the baseline file a pull request restored from the cache (or "-"),
// and only the push to main writes the baseline and saves it.
func TestCIWorkflowCountsGitProcesses(t *testing.T) {
	jobs := workflowJobs(t, repoRoot(t), "ci.yml")
	test, _ := yamlMap(jobs["test"])
	steps, _ := yamlSlice(test["steps"])
	requireStepOnExactly(t, steps, "git process counter build", "go build -o \"$RUNNER_TEMP/gitbudget\" ./internal/gitbudget", "runner.os == 'Linux'")
	goTest, id, at, write := -1, "", -1, -1
	for i, sv := range steps {
		step, _ := yamlMap(sv)
		run, _ := yamlString(step["run"])
		if m := goTestListRun.FindStringSubmatch(strings.TrimSpace(run)); m != nil {
			goTest, id = i, m[1]
			env, _ := yamlMap(step["env"])
			if dir, _ := yamlString(env["GITBUDGET_DIR"]); dir != "${{ runner.os == 'Linux' && format('{0}/gitcount', runner.temp) || '' }}" {
				t.Errorf("ci.yml: the go test step's GITBUDGET_DIR = %q, want the Linux leg's $RUNNER_TEMP/gitcount, where the budget steps read the counts", dir)
			}
		}
		if strings.Contains(run, "gitbudget\" check") {
			at = i
		}
		if strings.Contains(run, "gitbudget\" write") {
			write = i
		}
	}
	if goTest < 0 {
		t.Fatalf("ci.yml: no go test step runs the package list through the git process counter on the Linux leg")
	}
	want := "\"$RUNNER_TEMP/gitbudget\" check \"${flags[@]}\" " + gitBudgetFile + " \"$baseline\" \"$RUNNER_TEMP/gitcount\" ${{ steps." + id + ".outputs.list }}"
	requireStepOnExactly(t, steps, "git budget check", want, "runner.os == 'Linux'")
	if at < goTest {
		t.Errorf("ci.yml: the git budget check runs before go test, so it reads no counts")
	}

	const pr = "runner.os == 'Linux' && github.event_name == 'pull_request'"
	const main = "runner.os == 'Linux' && github.event_name == 'push' && github.ref == 'refs/heads/main'"
	const baselinePath = "${{ runner.temp }}/git-baseline.yaml"
	if restore := requireStepIf(t, steps, "restore main's git baseline", "actions/cache/restore@v6", pr); restore != nil {
		with, _ := yamlMap(restore["with"])
		if k, _ := yamlString(with["restore-keys"]); k != gitBaselineKeyPrefix {
			t.Errorf("ci.yml: the baseline restore's restore-keys = %q, want %q, which picks the newest main entry", k, gitBaselineKeyPrefix)
		}
		if p, _ := yamlString(with["path"]); p != baselinePath {
			t.Errorf("ci.yml: the baseline restore's path = %q, want %q", p, baselinePath)
		}
	}
	if label := requireStepIf(t, steps, "read the git-budget-accepted label", "", pr); label != nil {
		run, _ := yamlString(label["run"])
		if !strings.Contains(run, "gh api") || !strings.Contains(run, "git-budget-accepted") {
			t.Errorf("ci.yml: the label step must read the pull request's labels through the API (gh api), not the event payload: %q", run)
		}
		if id, _ := yamlString(label["id"]); id != "accepted" {
			t.Errorf("ci.yml: the label step's id = %q, want accepted, which the check step reads", id)
		}
	}
	if write < goTest {
		t.Errorf("ci.yml: the baseline write runs before go test, or is missing, so it records no counts")
	}
	requireStepIf(t, steps, "write main's git baseline", "", main)
	if save := requireStepIf(t, steps, "save main's git baseline", "actions/cache/save@v6", main); save != nil {
		with, _ := yamlMap(save["with"])
		if k, _ := yamlString(with["key"]); k != gitBaselineKeyPrefix+"${{ github.sha }}" {
			t.Errorf("ci.yml: the baseline save's key = %q, want %q", k, gitBaselineKeyPrefix+"${{ github.sha }}")
		}
		if p, _ := yamlString(with["path"]); p != baselinePath {
			t.Errorf("ci.yml: the baseline save's path = %q, want %q", p, baselinePath)
		}
	}
	perms, _ := yamlMap(test["permissions"])
	if p, _ := yamlString(perms["pull-requests"]); p != "read" {
		t.Errorf("ci.yml: the test job's pull-requests permission = %q, want read, for the label step", p)
	}
	for scope, level := range perms {
		if s, _ := yamlString(level); s != "read" {
			t.Errorf("ci.yml: the test job's %s permission = %q; the job writes nothing to the repository", scope, s)
		}
	}
}

// TestGitBudgetIsAThreshold asserts the config holds the threshold only: a
// margin and a slack, and no per-package counts, which live in main's cache.
func TestGitBudgetIsAThreshold(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(gitBudgetFile)))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Margin int `yaml:"margin"`
		Slack  int `yaml:"slack"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("%s: %v (it holds margin and slack only; counts are main's cache)", gitBudgetFile, err)
	}
	if c.Margin <= 0 || c.Margin > 50 {
		t.Errorf("%s: margin %d, want a percentage from 1 to 50", gitBudgetFile, c.Margin)
	}
	if c.Slack <= 0 {
		t.Errorf("%s: slack %d, want a positive number of processes", gitBudgetFile, c.Slack)
	}
}
