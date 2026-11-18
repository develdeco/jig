package lint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
// the git processes of every package it tests and reports them beside main's
// baseline: the counter is built on that leg, the go test step runs each
// test binary through it there with its trace directory set (goTestListRun
// pins the -exec and -count=1), a step after go test, on exactly that leg and
// never allowed to fail quietly, reports the counts of the same package list
// against the baseline file a pull request restored from the cache (or "-"),
// and only the push to main writes the baseline and saves it. A count never
// fails the leg, so no step reads a label.
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
				t.Errorf("ci.yml: the go test step's GITBUDGET_DIR = %q, want the Linux leg's $RUNNER_TEMP/gitcount, where the report step reads the counts", dir)
			}
		}
		if strings.Contains(run, "gitbudget\" report") {
			at = i
		}
		if strings.Contains(run, "gitbudget\" write") {
			write = i
		}
	}
	if goTest < 0 {
		t.Fatalf("ci.yml: no go test step runs the package list through the git process counter on the Linux leg")
	}
	want := "\"$RUNNER_TEMP/gitbudget\" report \"$baseline\" \"$RUNNER_TEMP/gitcount\" ${{ steps." + id + ".outputs.list }}"
	requireStepOnExactly(t, steps, "git processes against main's baseline", want, "runner.os == 'Linux'")
	if at < goTest {
		t.Errorf("ci.yml: the git report runs before go test, so it reads no counts")
	}
	if _, ok := stepNamed(steps, "read the git-budget-accepted label"); ok {
		t.Errorf("ci.yml: the test job reads a pull request label, but a count never fails the leg")
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
	// report leaves callers.tsv in the count directory; the leg uploads it for
	// a week, after the report and even when the report or a test failed. The
	// report never fails a run, so unlike the budget steps this one is
	// allowed to fail without failing the job: an artifact service error must
	// not fail the leg.
	if upload, ok := stepNamed(steps, "upload who started the git processes"); !ok {
		t.Errorf("ci.yml: the test job has no step named %q", "upload who started the git processes")
	} else {
		if got, _ := yamlString(upload["if"]); got != "always() && runner.os == 'Linux'" {
			t.Errorf("ci.yml: the callers upload's if = %q, want exactly %q", got, "always() && runner.os == 'Linux'")
		}
		if got, _ := yamlString(upload["uses"]); got != "actions/upload-artifact@v7" {
			t.Errorf("ci.yml: the callers upload uses %q, want actions/upload-artifact@v7", got)
		}
		if v, ok := upload["continue-on-error"].(bool); !ok || !v {
			t.Errorf("ci.yml: the callers upload's continue-on-error = %v, want true, since the report never fails a run", upload["continue-on-error"])
		}
		with, _ := yamlMap(upload["with"])
		if p, _ := yamlString(with["path"]); p != "${{ runner.temp }}/gitcount/callers.tsv" {
			t.Errorf("ci.yml: the callers upload's path = %q, want the callers.tsv report writes into the count directory", p)
		}
		if d := fmt.Sprint(with["retention-days"]); d != "7" {
			t.Errorf("ci.yml: the callers upload's retention-days = %q, want 7", d)
		}
		for i, sv := range steps {
			if step, _ := yamlMap(sv); step["name"] == "upload who started the git processes" && i < at {
				t.Errorf("ci.yml: the callers upload runs before the git report, so callers.tsv is not there yet")
			}
		}
	}
	perms, _ := yamlMap(test["permissions"])
	for scope, level := range perms {
		if s, _ := yamlString(level); s != "read" {
			t.Errorf("ci.yml: the test job's %s permission = %q; the job writes nothing to the repository", scope, s)
		}
	}
}

// runStep runs a step's run script with bash, as the runner would, in a
// temporary directory, with env added; the test skips without bash.
func runStep(t *testing.T, script, dir string, env ...string) {
	t.Helper()
	bash := requireBash(t, "a workflow step")
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

// TestCIWorkflowGitReportStepDoesWhatItNames runs the report step with a
// fake gitbudget, as the runner would, and asserts the wiring: it takes no
// label or flag from any other step, and its baseline argument is the
// restored file when it exists and "-" when not.
func TestCIWorkflowGitReportStepDoesWhatItNames(t *testing.T) {
	jobs := workflowJobs(t, repoRoot(t), "ci.yml")
	test, _ := yamlMap(jobs["test"])
	steps, _ := yamlSlice(test["steps"])
	report, ok := stepNamed(steps, "git processes against main's baseline")
	if !ok {
		t.Fatalf("ci.yml: the test job lacks the git report step")
	}
	if env, _ := yamlMap(report["env"]); len(env) != 0 {
		t.Errorf("ci.yml: the report step's env = %v, want none: a count is never judged", env)
	}
	reportRun, _ := yamlString(report["run"])
	reportRun = strings.ReplaceAll(reportRun, "${{ steps.packages.outputs.list }}", "pkg/a pkg/b")

	for _, hasBaseline := range []bool{true, false} {
		dir := t.TempDir()
		temp := filepath.Join(dir, "temp")
		if err := os.MkdirAll(temp, 0o755); err != nil {
			t.Fatal(err)
		}
		writeScript(t, temp, "gitbudget", "printf '%s\n' \"$@\" > \"$(dirname \"$0\")/argv\"\n")
		tempSlash := filepath.ToSlash(temp)
		if hasBaseline {
			if err := os.WriteFile(filepath.Join(temp, "git-baseline.yaml"), []byte("packages: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		runStep(t, reportRun, dir, "RUNNER_TEMP="+tempSlash)
		argv, err := os.ReadFile(filepath.Join(temp, "argv"))
		if err != nil {
			t.Fatalf("baseline %v: the fake gitbudget was not run: %v", hasBaseline, err)
		}
		baseline := "-"
		if hasBaseline {
			baseline = tempSlash + "/git-baseline.yaml"
		}
		want := []string{"report", baseline, tempSlash + "/gitcount", "pkg/a", "pkg/b"}
		if got := strings.Fields(strings.ReplaceAll(string(argv), "\r", "")); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("baseline %v: gitbudget ran with %v, want %v", hasBaseline, got, want)
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
