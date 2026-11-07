package lint

import (
	"os"
	"os/exec"
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

// runStep runs a step's run script with bash, as the runner would, in a
// temporary directory, with env added; the test skips without bash.
func runStep(t *testing.T, script, dir string, env ...string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash to run a workflow step: %v", err)
	}
	cmd := exec.Command(bash, "-e", "-o", "pipefail", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("step failed: %v: %s\n%s", err, out, script)
	}
}

// writeScript writes an executable shell script named name into dir.
func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestCIWorkflowGitBudgetStepsDoWhatTheyName runs the label step with a
// fake gh and the check step with a fake gitbudget, as the runner would,
// and asserts the wiring between them: the label puts -accepted in the
// step's output only when gh says the label is there, the check step's env
// takes exactly that output, -accepted comes first when set, and the
// baseline argument is the restored file when it exists and "-" when not.
func TestCIWorkflowGitBudgetStepsDoWhatTheyName(t *testing.T) {
	jobs := workflowJobs(t, repoRoot(t), "ci.yml")
	test, _ := yamlMap(jobs["test"])
	steps, _ := yamlSlice(test["steps"])
	label, ok := stepNamed(steps, "read the git-budget-accepted label")
	check, ok2 := stepNamed(steps, "git processes against main's baseline")
	if !ok || !ok2 {
		t.Fatalf("ci.yml: the test job lacks the label step or the git budget check step")
	}
	labelEnv, _ := yamlMap(label["env"])
	if got, _ := yamlString(labelEnv["PULL_NUMBER"]); got != "${{ github.event.pull_request.number }}" {
		t.Errorf("ci.yml: the label step's PULL_NUMBER = %q, want the pull request's number", got)
	}
	checkEnv, _ := yamlMap(check["env"])
	if got, _ := yamlString(checkEnv["ACCEPTED"]); got != "${{ steps.accepted.outputs.flag }}" {
		t.Fatalf("ci.yml: the check step's ACCEPTED = %q, want the label step's flag output", got)
	}
	labelRun, _ := yamlString(label["run"])
	checkRun, _ := yamlString(check["run"])
	checkRun = strings.ReplaceAll(checkRun, "${{ steps.packages.outputs.list }}", "pkg/a pkg/b")

	for _, c := range []struct {
		name        string
		hasLabel    bool
		hasBaseline bool
	}{
		{"label and baseline", true, true},
		{"label, no baseline", true, false},
		{"no label, baseline", false, true},
		{"no label, no baseline", false, false},
	} {
		dir := t.TempDir()
		bin := filepath.Join(dir, "bin")
		temp := filepath.Join(dir, "temp")
		for _, d := range []string{bin, temp} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		answer := "false"
		if c.hasLabel {
			answer = "true"
		}
		writeScript(t, bin, "gh", "printf '%s\\n' \"$@\" > \"$(dirname \"$0\")/argv\"\necho "+answer+"\n")
		writeScript(t, temp, "gitbudget", "printf '%s\n' \"$@\" > \"$(dirname \"$0\")/argv\"\n")
		output := filepath.ToSlash(filepath.Join(dir, "output"))
		if err := os.WriteFile(output, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		runStep(t, labelRun, dir, "PATH="+filepath.ToSlash(bin)+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_TOKEN=x", "PULL_NUMBER=7", "GITHUB_REPOSITORY=o/r", "GITHUB_OUTPUT="+output)
		ghArgv, _ := os.ReadFile(filepath.Join(bin, "argv"))
		wantGh := []string{"api", "repos/o/r/pulls/7", "--jq", labelFilter}
		if got := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(ghArgv), "\r", "")), "\n"); strings.Join(got, "\x00") != strings.Join(wantGh, "\x00") {
			t.Errorf("%s: the label step ran gh with %q, want %q", c.name, got, wantGh)
		}
		data, _ := os.ReadFile(output)
		flag := ""
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "flag="); ok {
				flag = v
			}
		}
		if want := map[bool]string{true: "-accepted", false: ""}[c.hasLabel]; flag != want {
			t.Errorf("%s: the label step's flag output = %q, want %q", c.name, flag, want)
		}

		tempSlash := filepath.ToSlash(temp)
		if c.hasBaseline {
			if err := os.WriteFile(filepath.Join(temp, "git-baseline.yaml"), []byte("packages: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		runStep(t, checkRun, dir, "RUNNER_TEMP="+tempSlash, "ACCEPTED="+flag)
		argv, err := os.ReadFile(filepath.Join(temp, "argv"))
		if err != nil {
			t.Fatalf("%s: the fake gitbudget was not run: %v", c.name, err)
		}
		baseline := "-"
		if c.hasBaseline {
			baseline = tempSlash + "/git-baseline.yaml"
		}
		want := []string{gitBudgetFile, baseline, tempSlash + "/gitcount", "pkg/a", "pkg/b"}
		if c.hasLabel {
			want = append([]string{"-accepted"}, want...)
		}
		want = append([]string{"check"}, want...)
		if got := strings.Fields(strings.ReplaceAll(string(argv), "\r", "")); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: gitbudget ran with %v, want %v", c.name, got, want)
		}
	}
}

// TestReleaseGrantsWhatCIRequests asserts release.yml's ci job grants every
// permission a job of ci.yml requests: a called workflow can only narrow
// its caller's token, so a request the caller does not grant refuses the
// release run at startup, which no pull request run shows.
func TestReleaseGrantsWhatCIRequests(t *testing.T) {
	root := repoRoot(t)
	release, _ := yamlMap(workflowJobs(t, root, "release.yml")["ci"])
	if uses, _ := yamlString(release["uses"]); uses != "./.github/workflows/ci.yml" {
		t.Fatalf("release.yml: the ci job uses %q, want the local ci.yml", uses)
	}
	granted, _ := yamlMap(release["permissions"])
	rank := map[string]int{"none": 0, "read": 1, "write": 2}
	for name, jv := range workflowJobs(t, root, "ci.yml") {
		job, _ := yamlMap(jv)
		requested, _ := yamlMap(job["permissions"])
		for scope, lv := range requested {
			want, _ := yamlString(lv)
			got, _ := yamlString(granted[scope])
			if rank[got] < rank[want] {
				t.Errorf("release.yml: the ci job grants %s: %q, but ci.yml's %s job requests %q, so GitHub refuses the release run at startup", scope, got, name, want)
			}
		}
	}
}

// labelFilter is the jq filter the label step has gh apply to the pull
// request: true only when a label is named git-budget-accepted exactly.
const labelFilter = `.labels | map(.name) | index("git-budget-accepted") != null`

// TestLabelFilterMatchesWholeLabelNames runs the filter the label step is
// held to (TestCIWorkflowGitBudgetStepsDoWhatTheyName pins the step to it)
// over canned pull request payloads, when jq is on PATH.
func TestLabelFilterMatchesWholeLabelNames(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skipf("no jq to run the label filter: %v", err)
	}
	for payload, want := range map[string]string{
		`{"labels":[{"name":"git-budget-accepted"}]}`:                "true",
		`{"labels":[{"name":"bug"},{"name":"git-budget-accepted"}]}`: "true",
		`{"labels":[{"name":"other"}]}`:                              "false",
		`{"labels":[{"name":"git-budget-accepted-later"}]}`:          "false",
		`{"labels":[]}`: "false",
	} {
		cmd := exec.Command(jq, labelFilter)
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Errorf("filter over %s = %q (%v), want %s", payload, out, err, want)
		}
	}
}
