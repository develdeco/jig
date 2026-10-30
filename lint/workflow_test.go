// This file's tests parse .github/workflows/{ci,release,smoke}.yml with
// yaml.v3 (no new dependency: the repo already vendors it) and assert
// shape, not exact strings, so a routine edit to a workflow does not churn
// these tests. yaml.v3 decodes a YAML mapping into interface{} as
// map[string]interface{} and, unlike yaml.v2, keeps the "on" key as the
// literal string "on" rather than coercing an unquoted key to a YAML 1.1
// boolean, so doc["on"] is reachable like any other key below.
package lint

import (
	"encoding/json"
	"go/build/constraint"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflowsDir is relative to the repo root.
const workflowsDir = ".github/workflows"

// loadWorkflow parses one workflow file into a generic YAML mapping.
func loadWorkflow(t *testing.T, root, name string) map[string]interface{} {
	t.Helper()
	path := filepath.Join(root, workflowsDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if doc == nil {
		t.Fatalf("%s: parsed to an empty document", path)
	}
	return doc
}

// yamlMap type-asserts v as a YAML mapping. A workflow file that does not
// match the assumed shape (a scalar or list where a mapping was expected)
// returns ok=false so the caller can fail with a message naming what it
// was looking for, rather than a bare panic.
func yamlMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}

// yamlSlice type-asserts v as a YAML sequence.
func yamlSlice(v interface{}) ([]interface{}, bool) {
	s, ok := v.([]interface{})
	return s, ok
}

// yamlString type-asserts v as a YAML scalar string.
func yamlString(v interface{}) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// sortedKeys returns m's keys sorted, for a deterministic error message.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// needsList normalizes a job's "needs" field, which GitHub Actions accepts
// as either a bare string or a list of strings.
func needsList(v interface{}) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := yamlString(e); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// dependsOn reports whether job "from" depends on job "target", directly
// or transitively, via "needs" chains.
func dependsOn(jobs map[string]interface{}, from, target string) bool {
	visited := map[string]bool{}
	var walk func(name string) bool
	walk = func(name string) bool {
		if visited[name] {
			return false
		}
		visited[name] = true
		job, ok := yamlMap(jobs[name])
		if !ok {
			return false
		}
		for _, n := range needsList(job["needs"]) {
			if n == target || walk(n) {
				return true
			}
		}
		return false
	}
	return walk(from)
}

// TestCIWorkflowTriggers asserts ci.yml runs on every push to main and on
// every pull request, and exposes workflow_call so release.yml can reuse it
// as a gate before publishing.
func TestCIWorkflowTriggers(t *testing.T) {
	root := repoRoot(t)
	doc := loadWorkflow(t, root, "ci.yml")

	on, ok := yamlMap(doc["on"])
	if !ok {
		t.Fatalf("ci.yml: \"on\" is not a mapping, so its triggers could not be checked")
	}

	push, ok := yamlMap(on["push"])
	if !ok {
		t.Fatalf("ci.yml: no push trigger, so a push to main would never run ci")
	}
	branches, ok := yamlSlice(push["branches"])
	haveMain := false
	for _, b := range branches {
		if s, ok := yamlString(b); ok && s == "main" {
			haveMain = true
		}
	}
	if !ok || !haveMain {
		t.Errorf("ci.yml: push trigger does not list branch \"main\", so a push to main would not run ci")
	}

	if _, ok := on["pull_request"]; !ok {
		t.Errorf("ci.yml: no pull_request trigger, so a PR could merge without ci running")
	}

	if _, ok := on["workflow_call"]; !ok {
		t.Errorf("ci.yml: no workflow_call trigger, so release.yml could not reuse this workflow as its own gate before publishing")
	}
}

// TestCIWorkflowTestJob asserts the "test" job's legs are the ones the plan
// job names (TestCIWorkflowPlan: all three platforms on every push to main,
// Linux and macOS on every pull request; a macOS-only regression has
// shipped before, commit d920a72), that its legs report the check names
// main's ruleset requires, and that it actually enforces formatting, vet,
// and tests rather than just building.
func TestCIWorkflowTestJob(t *testing.T) {
	root := repoRoot(t)
	doc := loadWorkflow(t, root, "ci.yml")

	jobs, ok := yamlMap(doc["jobs"])
	if !ok {
		t.Fatalf("ci.yml: no jobs mapping")
	}
	job, ok := yamlMap(jobs["test"])
	if !ok {
		t.Fatalf("ci.yml: no \"test\" job, so there is nothing to run the cross-platform and gate checks below")
	}
	// The matrix is the plan's os output and nothing else, so no include or
	// exclude can add or drop a leg the plan did not name.
	if !slices.Contains(needsList(job["needs"]), planJob) {
		t.Errorf("ci.yml: the test job does not need the %s job, so its matrix cannot read the legs that job plans", planJob)
	}
	strategy, _ := yamlMap(job["strategy"])
	matrix, _ := yamlMap(strategy["matrix"])
	if got, _ := yamlString(matrix["os"]); got != planMatrixOS || len(matrix) != 1 {
		t.Errorf("ci.yml: the test job's matrix is %v, want only os: %s, the legs the plan job names", matrix, planMatrixOS)
	}

	// Main's ruleset requires test (ubuntu-latest) and test (macos-latest) by
	// name, and every pull request reports both (TestCIWorkflowPlan), so the
	// name is pinned, not just the platforms: another name would never
	// report a required check.
	if name, _ := yamlString(job["name"]); name != "test (${{ matrix.os }})" {
		t.Errorf("ci.yml: the test job is named %q, want \"test (${{ matrix.os }})\", which reports the checks main's ruleset requires, so a pull request would wait on a check that is never reported", name)
	}

	steps, ok := yamlSlice(job["steps"])
	if !ok {
		t.Fatalf("ci.yml: test job has no steps")
	}

	// go vet and go test must actually enforce on every matrix leg: no
	// step-level "if" (which would silently confine either to a subset of
	// the three OSes, the same failure mode the matrix check above guards
	// against) and no "continue-on-error" (which would let either fail
	// without failing the job). gofmt is deliberately scoped to one leg
	// (running it three times over identical source would be redundant),
	// so it is checked precisely against that leg instead of "every leg".
	requireUnconditionalStep(t, steps, "go vet", "go vet ./...")
	requireUnconditionalStep(t, steps, "go test", "go test ")
	requireStepOnExactly(t, steps, "gofmt", "gofmt -l", "runner.os == 'Linux'")
}

// planJob is the ci.yml job that names a run's test legs, planMatrixOS the
// test job's matrix that reads them, and windowsPlanned the job-level if of
// a job that runs when, and only when, the plan includes the Windows leg.
const (
	planJob        = "plan"
	planMatrixOS   = "${{ fromJSON(needs.plan.outputs.os) }}"
	windowsPlanned = "needs.plan.outputs.windows == 'true'"
	codePlanned    = "needs.plan.outputs.docs == 'false'"
)

// planOutcome is what the plan job's step wrote to GITHUB_OUTPUT.
type planOutcome struct {
	legs    []string
	windows string
	docs    string
}

// runPlan runs the plan job's step with bash, as the runner would on event
// for a change touching changed (paths relative to the checkout, which is
// a temporary directory holding files, path to content), and returns what
// it wrote to GITHUB_OUTPUT. The test skips without bash.
func runPlan(t *testing.T, jobs map[string]interface{}, event string, changed []string, files map[string]string) planOutcome {
	t.Helper()
	job, ok := yamlMap(jobs[planJob])
	if !ok {
		t.Fatalf("ci.yml: no %s job, so nothing names a run's test legs", planJob)
	}
	var script string
	steps, _ := yamlSlice(job["steps"])
	for _, sv := range steps {
		step, _ := yamlMap(sv)
		if id, _ := yamlString(step["id"]); id == "plan" {
			script, _ = yamlString(step["run"])
		}
	}
	if script == "" {
		t.Fatalf("ci.yml: the %s job has no step with id plan, whose outputs the job passes on", planJob)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash to run the %s job's step: %v", planJob, err)
	}
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(dir, "github-output")
	cmd := exec.Command(bash, "-e", "-o", "pipefail", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "EVENT="+event, "CHANGED_FILES="+strings.Join(changed, "\n"), "GITHUB_OUTPUT="+filepath.ToSlash(output))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ci.yml: the %s job's step on %s for %v failed: %v: %s", planJob, event, changed, err, out)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ci.yml: the %s job's step wrote no GITHUB_OUTPUT: %v", planJob, err)
	}
	var got planOutcome
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "os":
			if err := json.Unmarshal([]byte(value), &got.legs); err != nil {
				t.Fatalf("ci.yml: the %s job's os output %q is not a JSON list: %v", planJob, value, err)
			}
		case "windows":
			got.windows = value
		case "docs":
			got.docs = value
		}
	}
	return got
}

// allLegs and pullRequestLegs are the test job's legs on every run of
// ci.yml but a pull request's, and on a pull request that changes nothing
// Windows builds or runs differently: test (ubuntu-latest) and
// test (macos-latest) are the checks main's ruleset requires.
var (
	allLegs         = []string{"macos-latest", "ubuntu-latest", "windows-latest"}
	pullRequestLegs = []string{"macos-latest", "ubuntu-latest"}
)

// TestCIWorkflowPlan runs the plan job's step, with synthetic changes, and
// asserts the legs it names: all three platforms for every event but a pull
// request, and for a pull request that changes a file Windows builds
// differently (a _windows file, a Go file with a build constraint), go.mod
// or go.sum, .gitattributes or a workflow; Linux and macOS for any other
// pull request. The plan's windows output says whether the Windows leg is
// in, for the jobs that run beside it. A pull request that changes only
// docs - markdown at the root or in .github, docs/, LICENSE, which no test
// but lint's reads - plans docs=true, and the Windows leg out; markdown
// anywhere else (a skill go:embed builds into jig, testdata) is not docs.
func TestCIWorkflowPlan(t *testing.T) {
	jobs := workflowJobs(t, repoRoot(t), "ci.yml")
	for _, c := range []struct {
		event   string
		changed []string
		files   map[string]string
		windows bool
		docs    bool
	}{
		{"push", nil, nil, true, false},
		{"push", []string{"README.md"}, nil, true, false},
		{"schedule", nil, nil, true, false},
		{"workflow_dispatch", nil, nil, true, false},
		{"pull_request", []string{"internal/store/store.go", "README.md"}, nil, false, false},
		{"pull_request", nil, nil, false, false},
		{"pull_request", []string{"internal/gitx/stat_windows.go"}, nil, true, false},
		{"pull_request", []string{"e2e/proc_alive_windows_test.go"}, nil, true, false},
		{"pull_request", []string{"internal/envrun/kill_windows_group.go"}, nil, true, false},
		{"pull_request", []string{"go.mod"}, nil, true, false},
		{"pull_request", []string{"go.sum"}, nil, true, false},
		{"pull_request", []string{".gitattributes"}, nil, true, false},
		{"pull_request", []string{".github/workflows/smoke.yml"}, nil, true, false},
		{"pull_request", []string{"internal/x/a.go"}, map[string]string{"internal/x/a.go": "//go:build windows\n\npackage x\n"}, true, false},
		{"pull_request", []string{"internal/x/b.go"}, map[string]string{"internal/x/b.go": "//go:build !linux && !darwin\n\npackage x\n"}, true, false},
		{"pull_request", []string{"internal/x/c.go"}, map[string]string{"internal/x/c.go": "package x\n\n// //go:build windows is not a constraint here.\n"}, false, false},
		{"pull_request", []string{"internal/x/gone.go"}, nil, false, false},
		{"pull_request", []string{"README.md", "DECISIONS.md", "docs/adr/0001-x.md", ".github/CONTRIBUTING.md", "LICENSE"}, nil, false, true},
		{"pull_request", []string{"docs/adr/0001-x.md"}, nil, false, true},
		{"pull_request", []string{"skills/intake/SKILL.md"}, nil, false, false},
		{"pull_request", []string{"internal/x/testdata/a.md"}, nil, false, false},
		{"pull_request", []string{"README.md", "go.mod"}, nil, true, false},
		{"pull_request", []string{"README.md", ".github/workflows/ci.yml"}, nil, true, false},
	} {
		got := runPlan(t, jobs, c.event, c.changed, c.files)
		want, wantWindows, wantDocs := pullRequestLegs, "false", "false"
		if c.windows {
			want, wantWindows = allLegs, "true"
		}
		if c.docs {
			wantDocs = "true"
		}
		legs := slices.Clone(got.legs)
		sort.Strings(legs)
		if !slices.Equal(legs, want) || got.windows != wantWindows || got.docs != wantDocs {
			t.Errorf("ci.yml: the %s job plans legs %v, windows=%q and docs=%q on %s for %v, want %v, windows=%s and docs=%s", planJob, got.legs, got.windows, got.docs, c.event, c.changed, want, wantWindows, wantDocs)
		}
	}
}

// TestCIWorkflowPlansWindowsForWindowsOnlyFiles asserts, for every Go file
// in the repository that Go builds on Windows but not on both Linux and
// macOS (by its file name or its build constraint), that a pull request
// changing it alone runs the Windows leg: no other leg compiles that file
// as Windows does. The plan's own reading of a file is a shell pattern,
// and this holds it to Go's.
func TestCIWorkflowPlansWindowsForWindowsOnlyFiles(t *testing.T) {
	root := repoRoot(t)
	jobs := workflowJobs(t, root, "ci.yml")
	var specific []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if builtFor(d.Name(), data, "windows") && !(builtFor(d.Name(), data, "linux") && builtFor(d.Name(), data, "darwin")) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			specific = append(specific, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(specific) == 0 {
		t.Fatal("found no Go file built on Windows but not on Linux and macOS, so this test checks nothing; the repository has had several")
	}
	for _, rel := range specific {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if got := runPlan(t, jobs, "pull_request", []string{rel}, map[string]string{rel: string(data)}); got.windows != "true" {
			t.Errorf("ci.yml: a pull request changing %s, which Go builds on Windows but not on both Linux and macOS, plans windows=%q, want true", rel, got.windows)
		}
	}
}

// knownOS and knownArch are the GOOS and GOARCH values a Go file name can
// end in (go/build's goodOSArchFile).
var (
	knownOS   = []string{"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos"}
	knownArch = []string{"386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips", "mipsle", "mips64", "mips64le", "ppc", "ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm"}
	unixOS    = []string{"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "linux", "netbsd", "openbsd", "solaris"}
)

// builtFor reports whether go build includes the Go file named name, holding
// data, for goos: its name's _GOOS or _GOOS_GOARCH suffix and its //go:build
// line, read the way go/build reads them, for the GOARCH CI's runner of that
// OS has.
func builtFor(name string, data []byte, goos string) bool {
	arch := "amd64"
	if goos == "darwin" {
		arch = "arm64"
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test"), "_")
	if n := len(parts); n >= 2 {
		last, prev := parts[n-1], parts[n-2]
		switch {
		case n >= 3 && slices.Contains(knownOS, prev) && slices.Contains(knownArch, last):
			if prev != goos || last != arch {
				return false
			}
		case slices.Contains(knownOS, last):
			if last != goos {
				return false
			}
		case slices.Contains(knownArch, last):
			if last != arch {
				return false
			}
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if !constraint.IsGoBuild(line) {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return true
		}
		return expr.Eval(func(tag string) bool {
			return tag == goos || tag == arch || tag == "gc" || (tag == "unix" && slices.Contains(unixOS, goos)) || strings.HasPrefix(tag, "go1.")
		})
	}
	return true
}

// requireAllPlatforms asserts job name in workflow file runs a
// strategy.matrix.os leg on each of the three supported platforms: listed
// in matrix.os, and not taken out again by a matrix.exclude entry naming
// it.
func requireAllPlatforms(t *testing.T, file, name string, job map[string]interface{}) {
	t.Helper()
	strategy, _ := yamlMap(job["strategy"])
	matrix, _ := yamlMap(strategy["matrix"])
	osList, ok := yamlSlice(matrix["os"])
	if !ok {
		t.Fatalf("%s: %s job has no strategy.matrix.os list, so it would not run cross-platform at all", file, name)
	}
	excluded := map[string]bool{}
	excludes, _ := yamlSlice(matrix["exclude"])
	for _, e := range excludes {
		entry, _ := yamlMap(e)
		if s, ok := yamlString(entry["os"]); ok {
			excluded[s] = true
		}
	}
	var haveUbuntu, haveWindows, haveMacos bool
	for _, v := range osList {
		s, ok := yamlString(v)
		if !ok || excluded[s] {
			continue
		}
		lower := strings.ToLower(s)
		if strings.HasPrefix(lower, "ubuntu-") {
			haveUbuntu = true
		}
		if strings.HasPrefix(lower, "windows-") {
			haveWindows = true
		}
		if strings.HasPrefix(lower, "macos-") {
			haveMacos = true
		}
	}
	if !haveUbuntu {
		t.Errorf("%s: %s job matrix has no ubuntu-* leg, so a Linux-only regression would merge unnoticed", file, name)
	}
	if !haveWindows {
		t.Errorf("%s: %s job matrix has no windows-* leg, so a Windows-only regression would merge unnoticed", file, name)
	}
	if !haveMacos {
		t.Errorf("%s: %s job matrix has no macos-* leg, so a macOS-only regression would merge unnoticed (it has shipped before, commit d920a72)", file, name)
	}
}

// workflowJobs parses workflow file and returns its jobs mapping.
func workflowJobs(t *testing.T, root, file string) map[string]interface{} {
	t.Helper()
	jobs, ok := yamlMap(loadWorkflow(t, root, file)["jobs"])
	if !ok {
		t.Fatalf("%s: no jobs mapping", file)
	}
	return jobs
}

// gateCheckName is the check main's ruleset requires beside the test
// job's legs: ci.yml's gate job, which fails unless every job it needs
// succeeded.
const gateCheckName = "ci ok"

// TestCIWorkflowGateJob asserts ci.yml's gate job exists under the check
// name the ruleset requires, always runs (GitHub counts a skipped required
// check as passing), needs every job that runs on every change - so a new
// agent CLI's contract job cannot be added without gating merges - and
// that its step, run for real, fails unless every needed job succeeded. It
// has bitten: claude-cli caught what the test job cannot, but only test's
// three legs were required, so a PR breaking the CLI contract could merge.
// The ruleset keeps those legs required because this test runs in them: a
// pull request's own ci.yml defines the gate it is judged by.
func TestCIWorkflowGateJob(t *testing.T) {
	root := repoRoot(t)
	jobs := workflowJobs(t, root, "ci.yml")
	var gate string
	for _, name := range sortedKeys(jobs) {
		job, _ := yamlMap(jobs[name])
		if n, _ := yamlString(job["name"]); n == gateCheckName {
			gate = name
		}
	}
	if gate == "" {
		t.Fatalf("ci.yml: no job named %q, the check main's ruleset requires, so no pull request could merge", gateCheckName)
	}
	job, _ := yamlMap(jobs[gate])
	if ifExpr, _ := yamlString(job["if"]); ifExpr != "always()" {
		t.Errorf("ci.yml: the %s job's if = %q, want always(): a skipped required check counts as passing", gate, ifExpr)
	}
	needs := map[string]bool{}
	for _, n := range needsList(job["needs"]) {
		needs[n] = true
	}
	for _, name := range sortedKeys(jobs) {
		other, _ := yamlMap(jobs[name])
		// Only the release and dispatch jobs, whose if reads inputs.release
		// (TestReleaseRunsTheHooksTestOnInstalledBinaries), stay out of the
		// gate: they never run on a pull request.
		if ifExpr, _ := yamlString(other["if"]); name == gate || strings.Contains(ifExpr, "inputs.release") {
			continue
		}
		if !needs[name] {
			t.Errorf("ci.yml: the %s job does not need %s, which runs on pull requests, so %s failing would not block a merge", gate, name, name)
		}
	}
	requireGateStepFailsClosed(t, gate, job)
	requireGateResultPerJob(t, gate, job, jobs)
}

// gateResultsEnv returns the name and value of the gate job's step env
// entry that reads the needed jobs' results, or "" for both.
func gateResultsEnv(job map[string]interface{}) (step map[string]interface{}, name, value string) {
	steps, _ := yamlSlice(job["steps"])
	for _, sv := range steps {
		s, _ := yamlMap(sv)
		env, _ := yamlMap(s["env"])
		for _, k := range sortedKeys(env) {
			if v, _ := yamlString(env[k]); strings.Contains(v, "needs.") && strings.Contains(v, ".result") {
				return s, k, v
			}
		}
	}
	return nil, "", ""
}

// gateTerm matches one ${{ }} expression of the gate's results, plainResult
// a needed job's own result, and plannedSkip a job the plan can leave out:
// success in place of skipped when the plan's output says so.
var (
	gateTerm    = regexp.MustCompile(`\$\{\{\s*(.*?)\s*\}\}`)
	plainResult = regexp.MustCompile(`^needs\.([A-Za-z0-9_-]+)\.result$`)
	plannedSkip = regexp.MustCompile(`^needs\.([A-Za-z0-9_-]+)\.result == 'skipped' && needs\.` + planJob + `\.outputs\.([A-Za-z0-9_-]+) == '(true|false)' && 'success' \|\| needs\.([A-Za-z0-9_-]+)\.result$`)
)

// requireGateResultPerJob asserts the gate's results hold one result for
// each job it needs, and nothing else: the job's own, or, for a job whose
// job-level if is that one plan output is true, success in place of
// skipped only when the same output is false. A skip the plan did not
// call for, or a skip after the plan itself failed (its outputs empty),
// fails the gate.
func requireGateResultPerJob(t *testing.T, gate string, job map[string]interface{}, jobs map[string]interface{}) {
	t.Helper()
	_, name, value := gateResultsEnv(job)
	if strings.TrimSpace(gateTerm.ReplaceAllString(value, "")) != "" {
		t.Errorf("ci.yml: the %s job's %s = %q holds more than ${{ }} results, so a word that is not a job's result could pass or fail the gate", gate, name, value)
	}
	seen := map[string]int{}
	for _, m := range gateTerm.FindAllStringSubmatch(value, -1) {
		expr := m[1]
		if r := plainResult.FindStringSubmatch(expr); r != nil {
			seen[r[1]]++
			// A job with a job-level if is skipped on some runs, and its
			// plain result would fail the gate on every one of them.
			if other, _ := yamlMap(jobs[r[1]]); other["if"] != nil {
				t.Errorf("ci.yml: the %s job's results read %s's result as is, but %s runs on if = %v, so every run that skips it fails the gate", gate, r[1], r[1], other["if"])
			}
			continue
		}
		r := plannedSkip.FindStringSubmatch(expr)
		if r == nil || r[1] != r[4] {
			t.Errorf("ci.yml: the %s job's results hold %q, which is neither a needed job's result nor that result with a skip the plan calls for", gate, expr)
			continue
		}
		seen[r[1]]++
		other, _ := yamlMap(jobs[r[1]])
		ifExpr, _ := yamlString(other["if"])
		left := "false"
		if r[3] == "false" {
			left = "true"
		}
		if ifExpr != "needs."+planJob+".outputs."+r[2]+" == '"+left+"'" {
			t.Errorf("ci.yml: the %s job counts %s skipped as succeeded when %s is %s, but %s runs on if = %q, so a skip the plan did not call for would pass the gate", gate, r[1], r[2], r[3], r[1], ifExpr)
		}
	}
	needs := needsList(job["needs"])
	for _, n := range needs {
		if seen[n] != 1 {
			t.Errorf("ci.yml: the %s job's results hold %d results of %s, want exactly one", gate, seen[n], n)
		}
	}
	for _, n := range sortedKeysInt(seen) {
		if !slices.Contains(needs, n) {
			t.Errorf("ci.yml: the %s job's results read %s, which it does not need, so that result is empty", gate, n)
		}
	}
}

// sortedKeysInt returns m's keys sorted.
func sortedKeysInt(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// requireGateStepFailsClosed runs the gate job's step that reads the
// needed jobs' results, with that variable set to synthetic results, and
// asserts it succeeds only when every result is success - failing on a
// failed, cancelled or skipped job and on no results at all, as it would
// if its wiring to the variable broke.
func requireGateStepFailsClosed(t *testing.T, gate string, job map[string]interface{}) {
	t.Helper()
	step, variable, _ := gateResultsEnv(job)
	script, _ := yamlString(step["run"])
	if script == "" {
		t.Fatalf("ci.yml: the %s job has no step reading needs.<job>.result in its env, so it cannot fail on a needed job's result", gate)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash to run the %s job's step: %v", gate, err)
	}
	for _, c := range []struct {
		results string
		pass    bool
	}{
		{"success success", true},
		{"success failure", false},
		{"cancelled success", false},
		{"success skipped", false},
		{"", false},
	} {
		cmd := exec.Command(bash, "-e", "-c", script)
		cmd.Env = append(os.Environ(), variable+"="+c.results)
		out, err := cmd.CombinedOutput()
		if passed := err == nil; passed != c.pass {
			t.Errorf("ci.yml: the %s job's step with %s=%q passed=%v, want %v: %s", gate, variable, c.results, passed, c.pass, out)
		}
	}
}

// TestCIWorkflowClaudeCLIJob asserts ci.yml runs jig's hooks against the
// real Claude Code CLI on all three platforms, on every run of the
// workflow but a docs-only pull request's, and fails the run when they
// fail: a job with no job-level "if" but codePlanned runs them
// (requireLiveJob). It has bitten: v0.1.1 shipped an argv the CLI
// refuses, since the test job's stub `claude` accepted any argv and nothing
// ran the real one; the hook JSON jig prints is likewise what only the real
// CLI can say it reads (TestHooksLive).
func TestCIWorkflowClaudeCLIJob(t *testing.T) {
	root := repoRoot(t)
	jobs := workflowJobs(t, root, "ci.yml")
	for _, name := range sortedKeys(jobs) {
		job, ok := yamlMap(jobs[name])
		if !ok {
			continue
		}
		// It may skip a docs-only pull request, which changes nothing it runs.
		if ifExpr, conditional := job["if"]; conditional && ifExpr != codePlanned {
			continue
		}
		if len(findLiveSteps(job, false)) > 0 {
			requireLiveJob(t, root, "ci.yml", name, job, false, 1)
			return
		}
	}
	t.Errorf("ci.yml: no job without a job-level if (but %s) runs go test with JIG_LIVE_CLAUDE set, so nothing runs against the real Claude Code CLI on every change, or before a release", codePlanned)
}

// TestReleaseRunsTheHooksTestOnInstalledBinaries asserts a release's own
// binaries, as they are installed, run the hooks test (TestHooksLive)
// through the real CLI (requireLiveJob with JIG_E2E_BINARY) both before and
// after it is published: ci.yml's installers job, on the snapshot archives,
// which release.yml's call turns on with release: true, and smoke.yml, on the
// published ones, both the installer's and go install's. It has bitten:
// v0.1.1 installed and printed its version, the most either checked, and
// failed at its first use.
func TestReleaseRunsTheHooksTestOnInstalledBinaries(t *testing.T) {
	root := repoRoot(t)

	ciDoc := loadWorkflow(t, root, "ci.yml")
	on, _ := yamlMap(ciDoc["on"])
	call, _ := yamlMap(on["workflow_call"])
	inputs, _ := yamlMap(call["inputs"])
	if _, ok := inputs["release"]; !ok {
		t.Errorf("ci.yml: workflow_call has no \"release\" input, so release.yml cannot ask for its archives to be installed and run before publishing")
	}
	ciJobs := workflowJobs(t, root, "ci.yml")
	for _, name := range []string{"snapshot", "installers"} {
		job, ok := yamlMap(ciJobs[name])
		if !ok {
			t.Fatalf("ci.yml: no %s job, so a release's archives are never installed and run before publishing", name)
		}
		if ifExpr, _ := yamlString(job["if"]); !strings.Contains(ifExpr, "inputs.release") {
			t.Errorf("ci.yml: the %s job's if = %q does not read inputs.release, so it never runs in a release", name, ifExpr)
		}
	}
	installers, _ := yamlMap(ciJobs["installers"])
	requireLiveJob(t, root, "ci.yml", "installers", installers, true, 1)

	releaseJobs := workflowJobs(t, root, "release.yml")
	for _, name := range sortedKeys(releaseJobs) {
		job, _ := yamlMap(releaseJobs[name])
		if uses, _ := yamlString(job["uses"]); !strings.Contains(uses, "ci.yml") {
			continue
		}
		with, _ := yamlMap(job["with"])
		if release, _ := with["release"].(bool); !release {
			t.Errorf("release.yml: the %s job calls ci.yml without release: true, so a release publishes archives that were never installed and run", name)
		}
	}

	smokeJobs := workflowJobs(t, root, "smoke.yml")
	for _, name := range sortedKeys(smokeJobs) {
		job, ok := yamlMap(smokeJobs[name])
		if !ok {
			continue
		}
		if len(findLiveSteps(job, true)) > 0 {
			requireLiveJob(t, root, "smoke.yml", name, job, true, 2)
			return
		}
	}
	t.Errorf("smoke.yml: no step runs go test with JIG_LIVE_CLAUDE and JIG_E2E_BINARY set, so a published release is only checked for its version, as v0.1.1 was")
}

// requireLiveJob asserts job name in workflow file runs its live CLI tests
// (findLiveSteps) in at least want steps, on all three platforms, gating
// every leg and the run: no job-level continue-on-error, no step-level
// "if" or continue-on-error on a live step, and the CLI installed with
// both official installers. Each live step runs one e2e test by its exact
// name in ./e2e, so a renamed test or a dropped package argument cannot
// turn the step into a run of nothing that passes; with installed set, it
// also names the binary under test in JIG_E2E_BINARY.
func requireLiveJob(t *testing.T, root, file, name string, job map[string]interface{}, installed bool, want int) {
	t.Helper()
	requireAllPlatforms(t, file, name, job)
	if hasContinueOnError(job) {
		t.Errorf("%s: the %s job has a job-level continue-on-error, so its failure would not fail the run, and a release would publish past it", file, name)
	}
	lives := findLiveSteps(job, installed)
	if len(lives) < want {
		t.Errorf("%s: the %s job has %d steps running go test with JIG_LIVE_CLAUDE set (and JIG_E2E_BINARY, for an installed binary), want %d", file, name, len(lives), want)
	}
	for _, live := range lives {
		if _, hasIf := live["if"]; hasIf || hasContinueOnError(live) {
			t.Errorf("%s: a live CLI step of the %s job has a step-level \"if\" or continue-on-error, so it could skip or fail on some legs without failing the job", file, name)
		}
		run, _ := yamlString(live["run"])
		m := goTestRunFlag.FindStringSubmatch(run)
		if m == nil || !e2eTestExists(t, root, m[1]) || !strings.Contains(run, "./e2e") {
			t.Errorf("%s: a live CLI step of the %s job does not run an e2e test in ./e2e by a name that exists (%q), so it can pass having run nothing", file, name, run)
		}
	}
	steps, _ := yamlSlice(job["steps"])
	for _, installer := range []string{"claude.ai/install.sh", "claude.ai/install.ps1"} {
		if _, ok := findStepByRun(steps, installer); !ok {
			t.Errorf("%s: the %s job never runs %s, so a leg would run the live tests without the CLI a user installs", file, name, installer)
		}
	}
}

// goTestRunFlag matches a go test -run flag naming one test function.
var goTestRunFlag = regexp.MustCompile(`-run[ =]\^?(Test\w+)\$?(\s|$)`)

// e2eTestExists reports whether a test function named name is declared in
// the e2e package.
func e2eTestExists(t *testing.T, root, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "e2e", "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\)`)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if decl.Match(data) {
			return true
		}
	}
	return false
}

// findLiveSteps returns the steps of job that run go test with
// JIG_LIVE_CLAUDE set to a non-empty value - and, with installed set,
// JIG_E2E_BINARY too - in the step's own env or the job's. The live tests
// skip on an empty JIG_LIVE_CLAUDE.
func findLiveSteps(job map[string]interface{}, installed bool) []map[string]interface{} {
	jobEnv, _ := yamlMap(job["env"])
	set := func(env map[string]interface{}, key string) bool {
		v, ok := env[key]
		if !ok {
			v = jobEnv[key]
		}
		s, _ := yamlString(v)
		return s != ""
	}
	var out []map[string]interface{}
	steps, _ := yamlSlice(job["steps"])
	for _, sv := range steps {
		step, ok := yamlMap(sv)
		if !ok {
			continue
		}
		run, _ := yamlString(step["run"])
		if !strings.Contains(run, "go test") {
			continue
		}
		env, _ := yamlMap(step["env"])
		if set(env, "JIG_LIVE_CLAUDE") && (!installed || set(env, "JIG_E2E_BINARY")) {
			out = append(out, step)
		}
	}
	return out
}

// findStepByRun returns the first step in steps whose "run" field contains
// substr, and whether one was found.
func findStepByRun(steps []interface{}, substr string) (map[string]interface{}, bool) {
	for _, sv := range steps {
		step, ok := yamlMap(sv)
		if !ok {
			continue
		}
		run, _ := yamlString(step["run"])
		if strings.Contains(run, substr) {
			return step, true
		}
	}
	return nil, false
}

// hasContinueOnError reports whether step's continue-on-error would let it
// fail without failing the job: present and not a literal false. GH Actions
// also accepts an expression string there, which this treats as capable of
// evaluating true rather than assuming it resolves to false.
func hasContinueOnError(step map[string]interface{}) bool {
	v, ok := step["continue-on-error"]
	if !ok {
		return false
	}
	b, isBool := v.(bool)
	return !isBool || b
}

// requireUnconditionalStep asserts ci.yml's test job has a step running
// substr (named label for the failure message) with no step-level "if" and
// no continue-on-error, so it runs, and actually gates, every matrix leg.
// Other steps may run substr too, for one leg only (the Windows leg's
// launcher check runs a narrower go test first); one unconditional,
// gating step is what counts.
func requireUnconditionalStep(t *testing.T, steps []interface{}, label, substr string) {
	t.Helper()
	found := false
	for _, sv := range steps {
		step, ok := yamlMap(sv)
		if !ok {
			continue
		}
		run, _ := yamlString(step["run"])
		if !strings.Contains(run, substr) {
			continue
		}
		found = true
		if _, hasIf := step["if"]; !hasIf && !hasContinueOnError(step) {
			return
		}
	}
	if !found {
		t.Errorf("ci.yml: test job has no step running %s, so a %s failure would merge", substr, label)
		return
	}
	t.Errorf("ci.yml: every %s step has a step-level \"if\" or continue-on-error, so %s could silently skip or fail on some matrix legs instead of gating every one", label, label)
}

// requireStepOnExactly asserts ci.yml's test job has a step running substr
// with no continue-on-error and a step-level "if" equal to exactly want -
// the one matrix leg label names it as required on, rather than either
// every leg (checked by requireUnconditionalStep) or an unconstrained "if"
// that could silently narrow or widen which leg actually runs it.
func requireStepOnExactly(t *testing.T, steps []interface{}, label, substr, want string) {
	t.Helper()
	step, ok := findStepByRun(steps, substr)
	if !ok {
		t.Errorf("ci.yml: test job has no step running %s, so the %s never runs", substr, label)
		return
	}
	if hasContinueOnError(step) {
		t.Errorf("ci.yml: the %s step has continue-on-error, so it could fail without failing the job", label)
	}
	got, _ := yamlString(step["if"])
	if got != want {
		t.Errorf("ci.yml: the %s step's if = %q, want exactly %q (the one leg it is required on)", label, got, want)
	}
}

// goTestListRun matches the test job's go test step: its one package
// argument is the list another step prints, it reuses no cached result
// (TestWorkflowsReuseNoTestResult), and on the Linux leg it runs each test
// binary through the git process counter (TestCIWorkflowCountsGitProcesses).
// listFilter matches a command that could drop packages from that list.
var (
	goTestListRun = regexp.MustCompile(`^go test -count=1 -timeout \S+ \$\{\{ runner\.os == 'Linux' && format\('-exec=\{0\}/gitbudget', runner\.temp\) \|\| '' \}\} \$\{\{ steps\.([A-Za-z0-9_-]+)\.outputs\.list \}\}$`)
	listFilter    = regexp.MustCompile(`\b(awk|grep|sed|head|tail|sort|uniq|cut)\b`)
)

// TestCIWorkflowTestsEveryPackageOnEveryOS asserts every package is tested
// on every OS. The test job's go test step runs the list its list step
// prints: go list ./... less the leg's matrix skip-package. So that step
// pair must stay intact, and each skipped package must be tested on the
// same OS by a job of its own (ci ok needing it is TestCIWorkflowGateJob's).
func TestCIWorkflowTestsEveryPackageOnEveryOS(t *testing.T) {
	root := repoRoot(t)
	jobs := workflowJobs(t, root, "ci.yml")
	test, _ := yamlMap(jobs["test"])
	steps, _ := yamlSlice(test["steps"])
	byID := map[string]map[string]interface{}{}
	var goTest map[string]interface{}
	var id string
	for _, sv := range steps {
		step, _ := yamlMap(sv)
		sid, _ := yamlString(step["id"])
		byID[sid] = step
		run, _ := yamlString(step["run"])
		if m := goTestListRun.FindStringSubmatch(strings.TrimSpace(run)); m != nil {
			goTest, id = step, m[1]
		}
	}
	list := byID[id]
	if goTest == nil || list == nil {
		t.Fatalf("ci.yml: no test job step runs go test -count=1 -timeout with only ${{ steps.<id>.outputs.list }} as its packages, naming a step with that id, so the packages it tests are not the list step's and some could go untested")
	}
	for _, step := range []map[string]interface{}{list, goTest} {
		if _, hasIf := step["if"]; hasIf || hasContinueOnError(step) {
			t.Errorf("ci.yml: the test job's step %q has a step-level \"if\" or continue-on-error, so a leg could run go test on no packages or fail without failing the job", step["name"])
		}
	}
	listRun, _ := yamlString(list["run"])
	env, _ := yamlMap(list["env"])
	skipExpr, _ := yamlString(env["SKIP_PACKAGE"])
	skip := skipPackageExpr.FindStringSubmatch(skipExpr)
	if !strings.Contains(listRun, "$(go list ./...)") || !strings.Contains(listRun, "list=") || skip == nil {
		t.Errorf("ci.yml: step %q does not print go list ./... as an output named list, less the SKIP_PACKAGE its env names for one runner OS (${{ runner.os == '<OS>' && '<package>' || '' }}), so a leg would test the wrong packages", id)
	}
	for _, line := range strings.Split(listRun, "\n") {
		if listFilter.MatchString(line) && !strings.Contains(line, `ENVIRON["SKIP_PACKAGE"]`) {
			t.Errorf("ci.yml: step %q filters its list with %q, which is not by SKIP_PACKAGE, so a leg could drop a package no job tests", id, strings.TrimSpace(line))
		}
	}
	// A docs-only pull request tests lint alone: the list is replaced only
	// under DOCS_ONLY, the plan's docs output, and only by ./lint.
	docsOnly, _ := yamlString(env["DOCS_ONLY"])
	replaced := strings.Count(listRun, "packages=$(go list")
	if docsOnly != "${{ needs."+planJob+".outputs.docs }}" || replaced != 2 || !strings.Contains(listRun, "if [ \"$DOCS_ONLY\" = true ]; then\n  packages=$(go list ./lint)\nfi") {
		t.Errorf("ci.yml: step %q does not replace its list with go list ./lint exactly when DOCS_ONLY, the plan's docs output, is true, and nowhere else, so a leg could test fewer packages than its change needs", id)
	}

	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(string(gomod))[1]
	if skip == nil {
		return
	}
	leg, pkg := runnerLeg[skip[1]], skip[2]
	switch {
	case !strings.HasPrefix(pkg, module+"/"):
		t.Errorf("ci.yml: the test job's %s leg skips %q, which is not an import path under %s, so go list never names it and the list step fails", leg, pkg, module)
	case !testedByAnotherJob(jobs, leg, pkg, "./"+strings.TrimPrefix(pkg, module+"/")):
		t.Errorf("ci.yml: the test job's %s leg skips %s, but no job that runs whenever that leg does tests it on a %s runner (an unconditional go test step naming it, with no -run or -skip), so it would merge untested there", leg, pkg, strings.SplitN(leg, "-", 2)[0])
	}
}

// skipPackageExpr matches the test job's SKIP_PACKAGE: the package one
// runner OS leaves to a job of its own, or none. runnerLeg maps runner.os
// to that leg's label.
var (
	skipPackageExpr = regexp.MustCompile(`^\$\{\{ runner\.os == '(Windows|Linux|macOS)' && '([^']+)' \|\| '' \}\}$`)
	runnerLeg       = map[string]string{"Windows": "windows-latest", "Linux": "ubuntu-latest", "macOS": "macos-latest"}
)

// testedByAnotherJob reports whether a job other than test - with no
// continue-on-error, on a runner of the leg's OS, and with no job-level
// "if" but, for the Windows leg, windowsPlanned, under which it runs
// whenever that leg does - has an unconditional step whose go test line
// names pkg (as an import path or as rel) without -run or -skip.
func testedByAnotherJob(jobs map[string]interface{}, leg, pkg, rel string) bool {
	narrows := func(w string) bool { return strings.HasPrefix(w, "-run") || strings.HasPrefix(w, "-skip") }
	for name, jv := range jobs {
		job, _ := yamlMap(jv)
		runsOn, _ := yamlString(job["runs-on"])
		ifExpr, conditional := job["if"]
		withLeg := !conditional || (strings.HasPrefix(leg, "windows-") && ifExpr == windowsPlanned)
		if name == "test" || !withLeg || hasContinueOnError(job) || !strings.HasPrefix(runsOn, strings.SplitN(leg, "-", 2)[0]+"-") {
			continue
		}
		steps, _ := yamlSlice(job["steps"])
		for _, sv := range steps {
			step, _ := yamlMap(sv)
			if _, hasIf := step["if"]; hasIf || hasContinueOnError(step) {
				continue
			}
			run, _ := yamlString(step["run"])
			for _, line := range strings.Split(run, "\n") {
				words := strings.Fields(line)
				if len(words) > 2 && words[0] == "go" && words[1] == "test" && !slices.ContainsFunc(words, narrows) &&
					(slices.Contains(words, pkg) || slices.Contains(words, rel)) {
					return true
				}
			}
		}
	}
	return false
}

// TestWorkflowsReuseNoTestResult asserts every go test a workflow runs
// passes -count=1. A cacheable go test has each test binary log every file
// its tests open or stat, and checks that log path by path after the binary
// exits, which on Windows took 110-210 s of windows-verifydeliver's step,
// for a result no CI run reuses: setup-go saves its cache once per go.sum.
// A go test is found in each command of a line, after any prefix such as
// env, a variable assignment or a cd joined by &&.
func TestWorkflowsReuseNoTestResult(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matched, err := filepath.Glob(filepath.Join(root, workflowsDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matched...)
	}
	if len(files) == 0 {
		t.Fatalf("no workflow under %s", workflowsDir)
	}
	for _, path := range files {
		file := filepath.Base(path)
		jobs := workflowJobs(t, root, file)
		for _, name := range sortedKeys(jobs) {
			job, _ := yamlMap(jobs[name])
			steps, _ := yamlSlice(job["steps"])
			for _, sv := range steps {
				step, _ := yamlMap(sv)
				run, _ := yamlString(step["run"])
				for _, line := range strings.Split(run, "\n") {
					for _, command := range splitShellCommands(line) {
						if args, ok := goTestArgs(shellWords(command)); ok && !slices.Contains(args, "-count=1") {
							t.Errorf("%s: the %s job runs %q without -count=1, so go test checks every file its tests touched after they finish, for a result no CI run reuses", file, name, strings.TrimSpace(command))
						}
					}
				}
			}
		}
	}
}

// goTestArgs returns the words after go test in one shell command, wherever
// in the command go test starts, and whether it runs go test at all.
func goTestArgs(words []string) ([]string, bool) {
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "go" && words[i+1] == "test" {
			return words[i+2:], true
		}
	}
	return nil, false
}

// TestGoTestArgs pins where TestWorkflowsReuseNoTestResult finds a go test
// in a command, and which commands run none.
func TestGoTestArgs(t *testing.T) {
	for _, c := range []struct {
		line string
		want []string
	}{
		{"go test -count=1 ./lint", []string{"-count=1", "./lint"}},
		{"env GOFLAGS=-v go test ./e2e", []string{"./e2e"}},
		{"CGO_ENABLED=0 go test -run X ./e2e", []string{"-run", "X", "./e2e"}},
		{"go build ./...", nil},
		{"go vet ./...", nil},
		{"echo go", nil},
	} {
		args, ok := goTestArgs(shellWords(c.line))
		if ok != (c.want != nil) || !slices.Equal(args, c.want) {
			t.Errorf("goTestArgs(%q) = %q, %v; want %q", c.line, args, ok, c.want)
		}
	}
	commands := splitShellCommands("cd e2e && go test ./...")
	if len(commands) != 2 {
		t.Fatalf("splitShellCommands split %q into %q, want two commands", "cd e2e && go test ./...", commands)
	}
	if args, ok := goTestArgs(shellWords(commands[1])); !ok || !slices.Equal(args, []string{"./..."}) {
		t.Errorf("goTestArgs(%q) = %q, %v; want [./...] true", commands[1], args, ok)
	}
}

// TestReleaseWorkflowTriggers asserts release.yml fires only on a version
// tag push: no branch filter smuggled into the push trigger, and no other
// trigger (a pull_request trigger, for instance, would let opening a PR
// attempt to publish a release).
func TestReleaseWorkflowTriggers(t *testing.T) {
	root := repoRoot(t)
	doc := loadWorkflow(t, root, "release.yml")

	on, ok := yamlMap(doc["on"])
	if !ok {
		t.Fatalf("release.yml: \"on\" is not a mapping, so its triggers could not be checked")
	}
	for _, k := range sortedKeys(on) {
		if k != "push" {
			t.Errorf("release.yml: \"on\" includes trigger %q besides push, so release could fire without a tag push", k)
		}
	}

	push, ok := yamlMap(on["push"])
	if !ok {
		t.Fatalf("release.yml: no push trigger, so a tag push would never release")
	}
	if _, ok := push["branches"]; ok {
		t.Errorf("release.yml: push trigger has a branches filter, so an ordinary branch push could trigger a release")
	}

	tags, ok := yamlSlice(push["tags"])
	if !ok || len(tags) == 0 {
		t.Fatalf("release.yml: push trigger has no tags list, so release would never fire on a version tag")
	}
	for _, tv := range tags {
		s, ok := yamlString(tv)
		if !ok || !strings.HasPrefix(s, "v") {
			t.Errorf("release.yml: tag pattern %v does not start with \"v\", so a non-version tag push could trigger a release", tv)
		}
	}
}

// jobBypassIf matches a job-level "if" that could let the job run
// regardless of whether the jobs it needs succeeded - always() forces it
// unconditionally, and failure() (with no argument, i.e. checking the
// whole run rather than a named job) is at best a non-sequitur here and at
// worst inverts the gate. A job-level "if" that names specific job or step
// outcomes some other way is not matched, since GitHub Actions has no
// simpler blanket safe/unsafe rule than these two idioms.
var jobBypassIf = regexp.MustCompile(`\b(always|failure)\s*\(\s*\)`)

// TestReleaseWorkflowReusesCI asserts a job that reuses
// ./.github/workflows/ci.yml exists, and that every job which actually
// publishes (runs goreleaser-action - checked for every job with one, not
// just the first or last found) depends on it, directly or transitively,
// so a release can never publish before ci has passed, and carries no
// job-level "if" (always()/failure()) that could let it run regardless of
// whether that dependency actually succeeded.
func TestReleaseWorkflowReusesCI(t *testing.T) {
	root := repoRoot(t)
	doc := loadWorkflow(t, root, "release.yml")

	jobs, ok := yamlMap(doc["jobs"])
	if !ok {
		t.Fatalf("release.yml: no jobs mapping")
	}

	ciJob := ""
	for _, name := range sortedKeys(jobs) {
		job, ok := yamlMap(jobs[name])
		if !ok {
			continue
		}
		uses, _ := yamlString(job["uses"])
		if strings.Contains(uses, "ci.yml") {
			ciJob = name
			break
		}
	}
	if ciJob == "" {
		t.Fatalf("release.yml: no job reuses ./.github/workflows/ci.yml, so a release could publish without ci passing")
	}

	var publishJobs []string
	for _, name := range sortedKeys(jobs) {
		job, ok := yamlMap(jobs[name])
		if !ok {
			continue
		}
		steps, _ := yamlSlice(job["steps"])
		for _, sv := range steps {
			step, ok := yamlMap(sv)
			if !ok {
				continue
			}
			uses, _ := yamlString(step["uses"])
			if strings.Contains(uses, "goreleaser-action") {
				publishJobs = append(publishJobs, name)
				break
			}
		}
	}
	if len(publishJobs) == 0 {
		t.Fatalf("release.yml: no job runs goreleaser-action, so there is no publishing step to order after ci")
	}

	for _, name := range publishJobs {
		if !dependsOn(jobs, name, ciJob) {
			t.Errorf("release.yml: publishing job %q does not depend, directly or transitively, on %q (which reuses ci.yml), so a release could publish before ci finishes", name, ciJob)
		}
		job, _ := yamlMap(jobs[name])
		if ifExpr, _ := yamlString(job["if"]); jobBypassIf.MatchString(ifExpr) {
			t.Errorf("release.yml: publishing job %q has a job-level if = %q, which can run the job regardless of whether %q succeeded", name, ifExpr, ciJob)
		}
	}
}

// TestSmokeWorkflowInputs asserts smoke.yml's workflow_dispatch and
// workflow_call triggers both require a "tag" input: smoke always needs to
// know which release to install and test.
func TestSmokeWorkflowInputs(t *testing.T) {
	root := repoRoot(t)
	doc := loadWorkflow(t, root, "smoke.yml")

	on, ok := yamlMap(doc["on"])
	if !ok {
		t.Fatalf("smoke.yml: \"on\" is not a mapping, so its triggers could not be checked")
	}

	for _, trigger := range []string{"workflow_dispatch", "workflow_call"} {
		trig, ok := yamlMap(on[trigger])
		if !ok {
			t.Errorf("smoke.yml: no %s trigger, so smoke could not be invoked that way", trigger)
			continue
		}
		inputs, ok := yamlMap(trig["inputs"])
		if !ok {
			t.Errorf("smoke.yml: %s trigger has no inputs, so it could not be told which tag to smoke-test", trigger)
			continue
		}
		tagInput, ok := yamlMap(inputs["tag"])
		if !ok {
			t.Errorf("smoke.yml: %s trigger has no \"tag\" input, so callers could not say which release to test", trigger)
			continue
		}
		if required, _ := tagInput["required"].(bool); !required {
			t.Errorf("smoke.yml: %s trigger's \"tag\" input is not required, so smoke could run with no tag to install", trigger)
		}
	}
}

// bashAssignPattern matches a bash/sh "name=value" assignment line (no
// space around "="; a space would make it a command with an env var
// prefix, or a comparison inside "[ ... ]", neither of which this needs to
// follow).
var bashAssignPattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.+)$`)

// jigBinaryToken reports whether tok - a shell word, quotes and all - names
// the jig binary: its last path segment (split on "/" or "\", after
// stripping quotes) is exactly "jig" or "jig.exe", case-insensitively.
// "$JIG_INSTALL_DIR/jig", "$env:JIG_INSTALL_DIR\jig.exe" and a bare "jig"
// all match; "jig_bin" (a variable name, no path separator) does not - that
// case is handled by resolving simple aliases in jigVersionInvocation
// instead.
func jigBinaryToken(tok string) bool {
	tok = unquoteShellWord(tok)
	tok = strings.NewReplacer("\\", "/").Replace(tok)
	base := tok
	if i := strings.LastIndexByte(tok, '/'); i >= 0 {
		base = tok[i+1:]
	}
	base = strings.ToLower(base)
	return base == "jig" || base == "jig.exe"
}

// unquoteShellWord strips one layer of surrounding double or single quotes
// (" or '), from tok, if both ends have it.
func unquoteShellWord(tok string) string {
	if len(tok) >= 2 {
		if (tok[0] == '"' && tok[len(tok)-1] == '"') || (tok[0] == '\'' && tok[len(tok)-1] == '\'') {
			return tok[1 : len(tok)-1]
		}
	}
	return tok
}

// shellWords splits line into words the way a real shell tokenizes one: a
// double- or single-quoted run (which may itself contain whitespace, e.g. an
// echo message) is one word, and an unquoted command separator (|, ||,
// &&, ;, &) is always its own word, even glued to an adjacent word with
// no space (a;b, x|y, p&&q) - the one piece of structure splitShellCommands
// needs to find a command boundary without a real shell parser. A
// separator character inside a quoted string is never treated as one,
// since it is still consumed by the quote branch above the separator
// check.
func shellWords(line string) []string {
	var words []string
	var b strings.Builder
	var quote byte
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
			b.WriteByte(c)
		case c == ' ' || c == '\t':
			flush()
		case c == '|' || c == '&':
			flush()
			if i+1 < len(line) && line[i+1] == c {
				words = append(words, line[i:i+2])
				i++
			} else {
				words = append(words, string(c))
			}
		case c == ';':
			flush()
			words = append(words, ";")
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return words
}

// jigVersionInvocation reports whether run - one step's shell script (sh,
// bash or powershell; smoke.yml's steps use all three) - structurally
// invokes the installed jig binary with "version" as its argument. Each
// line is split into commands on the shell's own separators (|, &&, ||,
// ;), and only a command whose PROGRAM word names the binary
// (jigBinaryToken), or a simple same-script alias of it, with "version"
// as its first argument, counts. Checking the program position, rather
// than any adjacent ("jig", "version") word pair anywhere in the text,
// means an unrelated echo or Write-Error message that happens to contain
// both words - which these scripts print right next to the real
// invocation - cannot stand in for the check itself.
func jigVersionInvocation(run string) bool {
	aliases := map[string]bool{}
	for _, raw := range strings.Split(run, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := bashAssignPattern.FindStringSubmatch(line); m != nil {
			if jigBinaryToken(m[2]) {
				aliases[m[1]] = true
			}
			continue
		}
		for _, command := range splitShellCommands(line) {
			words := shellWords(command)
			// PowerShell's call operator is not the program itself.
			if len(words) > 0 && words[0] == "&" {
				words = words[1:]
			}
			if len(words) < 2 || words[1] != "version" {
				continue
			}
			prog := unquoteShellWord(words[0])
			if jigBinaryToken(prog) {
				return true
			}
			if name, ok := strings.CutPrefix(prog, "$"); ok && aliases[name] {
				return true
			}
		}
	}
	return false
}

// shellSeparatorWords are the shell command separators splitShellCommands
// splits on, once shellWords has already tokenized the line into words and
// standalone separator tokens.
var shellSeparatorWords = map[string]bool{
	"|": true, "||": true, "&&": true, ";": true, "&": true,
}

// splitShellCommands splits one shell line into the commands a shell would
// run, on its own separators (|, ||, &&, ;, &), so a command's program is
// always its own first word. shellWords does the actual scanning - quote
// tracking and all - and always emits an unquoted separator as its own
// token, whether or not it sits glued to an adjacent word (a;b, x|y,
// p&&q); splitShellCommands only groups the words shellWords already
// produced between those separator tokens. A separator character sitting
// inside a quoted string - for example prose inside an echo message - is
// never mistaken for a command boundary, since shellWords never emits one
// from inside its quote branch.
func splitShellCommands(line string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, w := range shellWords(line) {
		if shellSeparatorWords[w] {
			flush()
			continue
		}
		cur = append(cur, w)
	}
	flush()
	return out
}

// TestSmokeWorkflowChecksVersion asserts at least one step actually
// verifies the installed jig binary's version, which is the point of a
// smoke test: proving the published artifact reports the tag it was built
// from. The check is structural (jigVersionInvocation), not a prose regex:
// a regex matching "jig" and "version" anywhere in a step's text also
// matches an unrelated echo/Write-Error message these same scripts print
// on failure ("installed jig version does not match ..."), so it would
// still pass even if the real invocation line were deleted.
func TestSmokeWorkflowChecksVersion(t *testing.T) {
	root := repoRoot(t)
	doc := loadWorkflow(t, root, "smoke.yml")

	jobs, ok := yamlMap(doc["jobs"])
	if !ok {
		t.Fatalf("smoke.yml: no jobs mapping")
	}

	found := false
	for _, jv := range jobs {
		job, ok := yamlMap(jv)
		if !ok {
			continue
		}
		steps, _ := yamlSlice(job["steps"])
		for _, sv := range steps {
			step, ok := yamlMap(sv)
			if !ok {
				continue
			}
			run, _ := yamlString(step["run"])
			if jigVersionInvocation(run) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("smoke.yml: no step runs jig with a version argument, so smoke would never check the installed binary actually reports the tag it was built from")
	}
}

// TestJigVersionInvocation pins jigVersionInvocation's structural check
// against both real script fragments from smoke.yml/ci.yml, the exact false
// positive the prose regex it replaced was vulnerable to (an unrelated
// echo/Write-Error line that happens to contain both "jig" and "version" as
// separate words, right next to the real invocation these scripts always
// pair it with), and prose whose quoted argument itself contains a shell
// separator character (;, | or &): before splitShellCommands tokenized the
// line before splitting, that character split the quoted string apart,
// landing "jig version" at a command's start and reporting a real
// invocation that was never there.
func TestJigVersionInvocation(t *testing.T) {
	cases := []struct {
		name string
		run  string
		want bool
	}{
		{"sh quoted path", `"$JIG_INSTALL_DIR/jig" version`, true},
		{"sh piped into grep", "\"$JIG_INSTALL_DIR/jig\" version | grep -Fxq \"  version: $JIG_TAG\"", true},
		{"powershell call operator", `$out = & "$env:JIG_INSTALL_DIR\jig.exe" version`, true},
		{"bash variable alias resolved same script", "jig_bin=\"$gopath/bin/jig.exe\"\n\"$jig_bin\" version", true},
		{"bare jig", "jig version", true},
		{"echo prose alone, the mutation a word-pair check misses", `echo skipping the jig version check for now`, false},
		{"echo prose only, no real invocation on any line", `echo "installed jig version does not match $JIG_TAG"`, false},
		{"Write-Error prose only", `Write-Error "installed jig version does not match $env:JIG_TAG"`, false},
		{"go install, not a version check", `go install "github.com/develdeco/jig/cmd/jig@$JIG_TAG"`, false},
		{"version not the adjacent word", `jig --version-check`, false},
		{"unrelated binary named version", `versioner check`, false},
		{"quoted separator, semicolon", `echo "ask an admin to run; jig version yourself and compare"`, false},
		{"quoted separator, pipe", `echo "compare output | jig version | by hand"`, false},
		{"quoted separator, ampersand", `echo "run the installer & jig version afterwards"`, false},
		{"pipe glued to the preceding word", `"$JIG_INSTALL_DIR/jig" version|grep -Fxq "  version: $JIG_TAG"`, true},
		{"semicolon glued to the preceding word", `set -eu; "$JIG_INSTALL_DIR/jig" version`, true},
		{"double ampersand glued to the preceding word", `"$JIG_INSTALL_DIR/jig" version&&echo ok`, true},
		{"double ampersand with spaces, control case", `"$JIG_INSTALL_DIR/jig" version && echo ok`, true},
		{"semicolon inside a quoted echo message, glued", `echo "run;jig version now"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jigVersionInvocation(c.run); got != c.want {
				t.Errorf("jigVersionInvocation(%q) = %v, want %v", c.run, got, c.want)
			}
		})
	}
}

// TestHasContinueOnError pins the truthiness rule hasContinueOnError uses:
// absent or a literal false does not defeat the step, anything else
// (including an expression GitHub Actions could still resolve true) does.
func TestHasContinueOnError(t *testing.T) {
	cases := []struct {
		name string
		step map[string]interface{}
		want bool
	}{
		{"absent", map[string]interface{}{}, false},
		{"literal false", map[string]interface{}{"continue-on-error": false}, false},
		{"literal true", map[string]interface{}{"continue-on-error": true}, true},
		{"expression string", map[string]interface{}{"continue-on-error": "${{ matrix.experimental }}"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasContinueOnError(c.step); got != c.want {
				t.Errorf("hasContinueOnError(%v) = %v, want %v", c.step, got, c.want)
			}
		})
	}
}

// TestJobBypassIfPattern pins which job-level "if" expressions
// TestReleaseWorkflowReusesCI treats as a bypass: always() or failure()
// appearing anywhere in the expression, not an exact match, since either
// can be combined with other conditions via || and still bypass the gate.
func TestJobBypassIfPattern(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"", false},
		{"always()", true},
		{"failure()", true},
		{"github.event_name == 'workflow_dispatch'", false},
		{"needs.ci.result == 'success' || always()", true},
	}
	for _, c := range cases {
		if got := jobBypassIf.MatchString(c.expr); got != c.want {
			t.Errorf("jobBypassIf.MatchString(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

// TestDependsOnTransitive pins dependsOn's own transitive-needs walk
// directly, independent of any real workflow file: a job with no "needs"
// at all must not be reported as depending on anything.
func TestDependsOnTransitive(t *testing.T) {
	jobs := map[string]interface{}{
		"a": map[string]interface{}{},
		"b": map[string]interface{}{"needs": "a"},
		"c": map[string]interface{}{"needs": []interface{}{"b"}},
		"d": map[string]interface{}{},
	}
	if !dependsOn(jobs, "c", "a") {
		t.Error("dependsOn(c, a) = false, want true (transitive via b)")
	}
	if dependsOn(jobs, "d", "a") {
		t.Error("dependsOn(d, a) = true, want false (d has no needs at all)")
	}
}
