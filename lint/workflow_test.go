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
func requireUnconditionalStep(t *testing.T, steps []interface{}, label, substr string) {
	t.Helper()
	step, ok := findStepByRun(steps, substr)
	if !ok {
		t.Errorf("ci.yml: test job has no step running %s, so a %s failure would merge", substr, label)
		return
	}
	if _, hasIf := step["if"]; hasIf {
		t.Errorf("ci.yml: the %s step has a step-level \"if\", so it could silently skip on some matrix legs instead of gating every one", label)
	}
	if hasContinueOnError(step) {
		t.Errorf("ci.yml: the %s step has continue-on-error, so it could fail without failing the job", label)
	}
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
		t.Errorf("ci.yml: test job has no step running %s, so unformatted code would merge", substr)
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

// unquoteShellWord strips one layer of surrounding '"' or '\” from tok, if
// both ends have it.
func unquoteShellWord(tok string) string {
	if len(tok) >= 2 {
		if (tok[0] == '"' && tok[len(tok)-1] == '"') || (tok[0] == '\'' && tok[len(tok)-1] == '\'') {
			return tok[1 : len(tok)-1]
		}
	}
	return tok
}

// shellWords splits line into whitespace-separated words, treating a
// '"'- or '\”-quoted run (which may itself contain whitespace, e.g. an
// echo message) as one word - just enough structure to tell an actual
// command's argv apart from prose inside a quoted string, without a real
// shell parser.
func shellWords(line string) []string {
	var words []string
	var b strings.Builder
	var quote byte
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
			if b.Len() > 0 {
				words = append(words, b.String())
				b.Reset()
			}
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 {
		words = append(words, b.String())
	}
	return words
}

// jigVersionInvocation reports whether run - one step's shell script (sh,
// bash or powershell; smoke.yml's steps use all three) - structurally
// invokes the installed jig binary with "version" as an argument: some
// line's words contain a (program, "version") adjacent pair whose program
// word names the binary (jigBinaryToken) or, for the bash steps that
// assign it to a variable first ("jig_bin=.../jig.exe", later "$jig_bin
// version"), a simple same-script alias resolved back to it. Walking words
// this way, rather than matching "jig" and "version" anywhere in the
// script's text, means a step whose only mention of either word is inside
// an unrelated echo or Write-Error message (both appear in these scripts,
// right next to the real invocation) does not count.
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
		words := shellWords(line)
		for i := 0; i+1 < len(words); i++ {
			if words[i+1] != "version" {
				continue
			}
			prog := unquoteShellWord(words[i])
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
// against both real script fragments from smoke.yml/ci.yml and the exact
// false positive the prose regex it replaced was vulnerable to: an
// unrelated echo/Write-Error line that happens to contain both "jig" and
// "version" as separate words, right next to the real invocation these
// scripts always pair it with.
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
		{"echo prose only, no real invocation on any line", `echo "installed jig version does not match $JIG_TAG"`, false},
		{"Write-Error prose only", `Write-Error "installed jig version does not match $env:JIG_TAG"`, false},
		{"go install, not a version check", `go install "github.com/develdeco/jig/cmd/jig@$JIG_TAG"`, false},
		{"version not the adjacent word", `jig --version-check`, false},
		{"unrelated binary named version", `versioner check`, false},
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
