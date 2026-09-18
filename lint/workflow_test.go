// This file's tests parse .github/workflows/{ci,release,smoke}.yml with
// yaml.v3 (no new dependency: the repo already vendors it) and assert
// shape, not exact strings, so a routine edit to a workflow does not churn
// these tests. yaml.v3 decodes a YAML mapping into interface{} as
// map[string]interface{} and, unlike yaml.v2, keeps the "on" key as the
// literal string "on" rather than coercing an unquoted key to a YAML 1.1
// boolean, so doc["on"] is reachable like any other key below.
package lint

import (
	"os"
	"path/filepath"
	"regexp"
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

// TestCIWorkflowTestJob asserts the "test" job's matrix covers all three
// supported platforms (a macOS-only regression has shipped before, commit
// d920a72) and that it actually enforces formatting, vet, and tests rather
// than just building.
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

	strategy, _ := yamlMap(job["strategy"])
	matrix, _ := yamlMap(strategy["matrix"])
	osList, ok := yamlSlice(matrix["os"])
	if !ok {
		t.Fatalf("ci.yml: test job has no strategy.matrix.os list, so it would not run cross-platform at all")
	}
	var haveUbuntu, haveWindows, haveMacos bool
	for _, v := range osList {
		s, ok := yamlString(v)
		if !ok {
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
		t.Errorf("ci.yml: test job matrix has no ubuntu-* leg, so a Linux-only regression would merge unnoticed")
	}
	if !haveWindows {
		t.Errorf("ci.yml: test job matrix has no windows-* leg, so a Windows-only regression would merge unnoticed")
	}
	if !haveMacos {
		t.Errorf("ci.yml: test job matrix has no macos-* leg, so a macOS-only regression would merge unnoticed (it has shipped before, commit d920a72)")
	}

	steps, ok := yamlSlice(job["steps"])
	if !ok {
		t.Fatalf("ci.yml: test job has no steps")
	}
	var haveGofmt, haveVet, haveTest bool
	for _, sv := range steps {
		step, ok := yamlMap(sv)
		if !ok {
			continue
		}
		run, _ := yamlString(step["run"])
		if strings.Contains(run, "gofmt -l") {
			haveGofmt = true
		}
		if strings.Contains(run, "go vet") {
			haveVet = true
		}
		if strings.Contains(run, "go test") {
			haveTest = true
		}
	}
	if !haveGofmt {
		t.Errorf("ci.yml: test job has no step running gofmt -l, so unformatted code would merge")
	}
	if !haveVet {
		t.Errorf("ci.yml: test job has no step running go vet, so a vet failure would merge")
	}
	if !haveTest {
		t.Errorf("ci.yml: test job has no step running go test, so a test failure would merge")
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

// TestReleaseWorkflowReusesCI asserts a job that reuses ./.github/workflows/ci.yml
// exists, and that the job which actually publishes (runs goreleaser-action)
// depends on it, directly or transitively, so a release can never publish
// before ci has passed.
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

	publishJob := ""
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
				publishJob = name
			}
		}
	}
	if publishJob == "" {
		t.Fatalf("release.yml: no job runs goreleaser-action, so there is no publishing step to order after ci")
	}

	if !dependsOn(jobs, publishJob, ciJob) {
		t.Errorf("release.yml: publishing job %q does not depend, directly or transitively, on %q (which reuses ci.yml), so a release could publish before ci finishes", publishJob, ciJob)
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

// smokeVersionCheck matches a run step that invokes the jig binary with a
// version argument, allowing for quoting and path variables (the real
// steps read like `"$JIG_INSTALL_DIR/jig" version`).
var smokeVersionCheck = regexp.MustCompile(`(?i)jig[^\n]*\bversion\b`)

// TestSmokeWorkflowChecksVersion asserts at least one step actually
// verifies the installed jig binary's version, which is the point of a
// smoke test: proving the published artifact reports the tag it was built
// from.
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
			if smokeVersionCheck.MatchString(run) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("smoke.yml: no step runs jig with a version argument, so smoke would never check the installed binary actually reports the tag it was built from")
	}
}
