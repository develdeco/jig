package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testModule = "github.com/develdeco/jig"

// fixture writes a count dir holding n trace files for each package, and
// returns it.
func fixture(t *testing.T, counts map[string]int) string {
	t.Helper()
	dir := t.TempDir()
	for pkg, n := range counts {
		pd := filepath.Join(dir, countDirName(pkg))
		if err := os.MkdirAll(pd, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range n {
			if err := os.WriteFile(filepath.Join(pd, fmt.Sprintf("t%d", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCheckVerdictsAndTheThresholdEdge runs check over fixture count dirs
// and baselines: every verdict, and the edge where exactly the limit passes
// and one over fails.
func TestCheckVerdictsAndTheThresholdEdge(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	base := writeFile(t, "baseline.yaml", "packages:\n  big: 400\n  small: 10\n  gone: 5\n  shrunk: 50\n  odd: 401\n")
	for _, c := range []struct {
		name     string
		counts   map[string]int
		accepted bool
		code     int
		verdicts map[string]string
	}{
		{"within the percent of a big package", map[string]int{"big": 420}, false, 0, map[string]string{"big": "ok"}},
		{"exactly at the limit (5% of 400 is 20; the slack of 10 is the larger for small)", map[string]int{"big": 420, "small": 20}, false, 0, map[string]string{"big": "ok", "small": "ok"}},
		{"one over the limit of a big package", map[string]int{"big": 421}, false, 1, map[string]string{"big": "over"}},
		{"the percent of 401 rounds up (5% is 20.05, allowed 21): 422 passes", map[string]int{"odd": 422}, false, 0, map[string]string{"odd": "ok"}},
		{"the percent of 401 rounds up: 423 is one over", map[string]int{"odd": 423}, false, 1, map[string]string{"odd": "over"}},
		{"one over the slack of a small package", map[string]int{"small": 21}, false, 1, map[string]string{"small": "over"}},
		{"a decrease never fails", map[string]int{"shrunk": 0, "big": 1}, false, 0, map[string]string{"shrunk": "ok", "big": "ok"}},
		{"new package within the slack", map[string]int{"fresh": 10}, false, 0, map[string]string{"fresh": "new"}},
		{"new package past the slack", map[string]int{"fresh": 11}, false, 1, map[string]string{"fresh": "over"}},
		{"a package with no git work and no entry is not listed", map[string]int{"quiet": 0}, false, 0, map[string]string{}},
		{"a baseline package that did not run", map[string]int{"small": 10}, false, 0, map[string]string{"small": "ok", "gone": "not run", "big": "not run", "shrunk": "not run"}},
		{"accepted turns an over into a pass", map[string]int{"big": 900, "small": 10}, true, 0, map[string]string{"big": "accepted", "small": "ok"}},
	} {
		var stdout, stderr bytes.Buffer
		counts := fixture(t, c.counts)
		var pkgs []string
		for pkg := range c.counts {
			pkgs = append(pkgs, testModule+"/"+pkg)
		}
		args := []string{"check"}
		if c.accepted {
			args = append(args, "-accepted")
		}
		args = append(append(args, cfg, base, counts), pkgs...)
		code := run(args, func(string) string { return "" }, &stdout, &stderr)
		if code != c.code {
			t.Errorf("%s: exit %d, want %d:\n%s%s", c.name, code, c.code, stdout.String(), stderr.String())
		}
		got := map[string]string{}
		inTable := false
		for _, line := range strings.Split(stdout.String(), "\n") {
			f := strings.Fields(line)
			switch {
			case len(f) == 0:
				inTable = false
			case inTable:
				got[f[0]] = strings.Join(f[5:], " ")
			case f[0] == "package":
				inTable = true
			}
		}
		for pkg, want := range c.verdicts {
			if got[pkg] != want {
				t.Errorf("%s: %s verdict = %q, want %q:\n%s", c.name, pkg, got[pkg], want, stdout.String())
			}
		}
		for pkg := range got {
			// The baseline packages a case does not count show as not run.
			if _, ok := c.verdicts[pkg]; !ok && got[pkg] != "not run" {
				t.Errorf("%s: unexpected row for %s:\n%s", c.name, pkg, stdout.String())
			}
		}
	}
}

func TestCheckWithoutABaselinePassesAndSaysSo(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	counts := fixture(t, map[string]int{"e2e": 70})
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	for _, base := range []string{"-", missing} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"check", cfg, base, counts, testModule + "/e2e"}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String(), "no main baseline") || !strings.Contains(stdout.String(), "70") {
			t.Errorf("baseline %q: exit %d, want 0 with the no-baseline line and the counts:\n%s%s", base, code, stdout.String(), stderr.String())
		}
	}
}

// A check with no baseline still writes the job summary: the counts and the
// no-baseline line, in Markdown, at $GITHUB_STEP_SUMMARY.
func TestCheckWithoutABaselineStillWritesTheSummary(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	counts := fixture(t, map[string]int{"e2e": 70})
	summary := filepath.Join(t.TempDir(), "summary.md")
	env := func(k string) string {
		if k == "GITHUB_STEP_SUMMARY" {
			return summary
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check", cfg, "-", counts, testModule + "/e2e"}, env, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, want 0: %s%s", code, stdout.String(), stderr.String())
	}
	got, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("no summary written: %v", err)
	}
	if !strings.Contains(string(got), noBaselineNote) || !strings.Contains(string(got), "| e2e | 70 |") {
		t.Errorf("summary:\n%s", got)
	}
}

func TestCheckWritesTheMarkdownSummary(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	base := writeFile(t, "baseline.yaml", "packages:\n  e2e: 100\n")
	counts := fixture(t, map[string]int{"e2e": 150})
	summary := filepath.Join(t.TempDir(), "summary.md")
	env := func(k string) string {
		if k == "GITHUB_STEP_SUMMARY" {
			return summary
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check", cfg, base, counts, testModule + "/e2e"}, env, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1: %s%s", code, stdout.String(), stderr.String())
	}
	got, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "| package | baseline | count | change | percent | verdict |") || !strings.Contains(string(got), "| e2e | 100 | 150 | +50 | +50.0% | over |") {
		t.Errorf("summary:\n%s", got)
	}
}

func TestWriteThenCheckRoundTrip(t *testing.T) {
	counts := fixture(t, map[string]int{"e2e": 12, "cmd/jig": 7, "lint": 0})
	path := filepath.Join(t.TempDir(), "baseline.yaml")
	var stdout, stderr bytes.Buffer
	pkgs := []string{testModule + "/e2e", testModule + "/cmd/jig", testModule + "/lint"}
	if code := run(append([]string{"write", path, counts}, pkgs...), func(string) string { return "" }, &stdout, &stderr); code != 0 {
		t.Fatalf("write: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	b, err := readBaseline(path)
	if err != nil || len(b.Packages) != 2 || b.Packages["e2e"] != 12 || b.Packages["cmd/jig"] != 7 {
		t.Errorf("baseline = %+v, %v; want e2e 12 and cmd/jig 7, lint (no git work) left out", b, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), baselineHeader) || !strings.Contains(string(data), "  cmd/jig: 7\n  e2e: 12\n") {
		t.Errorf("baseline file:\n%s", data)
	}
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	if code := run(append([]string{"check", cfg, path, counts}, pkgs...), func(string) string { return "" }, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Errorf("check against the baseline just written: exit %d, want 0", code)
	}
}

func TestReadConfigAndBaselineRefuseWhatTheyCannotJudge(t *testing.T) {
	for name, body := range map[string]string{
		"old packages map": "margin: 5\nslack: 10\npackages:\n  e2e: 1\n",
		"margin over":      "margin: 101\nslack: 10\n",
		"negative margin":  "margin: -1\nslack: 10\n",
		"negative slack":   "margin: 5\nslack: -1\n",
		"not a mapping":    "- e2e\n",
	} {
		if _, err := readConfig(writeFile(t, "c.yaml", body)); err == nil {
			t.Errorf("config %s: readConfig accepted %q", name, body)
		}
	}
	for name, body := range map[string]string{
		"unknown key":   "packages: {}\nslack: 3\n",
		"zero count":    "packages:\n  e2e: 0\n",
		"not a mapping": "- e2e\n",
	} {
		if _, err := readBaseline(writeFile(t, "b.yaml", body)); err == nil {
			t.Errorf("baseline %s: readBaseline accepted %q", name, body)
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
	if err != nil || module != testModule || rel != "internal/gitbudget" {
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
// then writes the baseline from what it counted and checks against it.
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
	// The process is started as gitx starts one while git traces: the caller
	// and directory in its environment, and a config that lists them.
	cfgFile := writeFile(t, "gitconfig", "[trace2]\n\tenvVars = JIG_GIT_CALLER,JIG_GIT_DIR\n")
	wrapped.Env = append(os.Environ(), "GIT_TRACE2_EVENT="+filepath.Join(tmp, "outer"), "GITBUDGET_DIR="+counts,
		"GIT_CONFIG_GLOBAL="+cfgFile, "GIT_CONFIG_NOSYSTEM=1",
		"JIG_GIT_CALLER=example.com/m/pkg.Asker", "JIG_GIT_DIR="+filepath.Join(tmp, "TestWrapped7", "001"))
	out, err := wrapped.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "git version ") {
		t.Fatalf("gitbudget git version: %v\n%s", err, out)
	}
	n, err := countTraces(filepath.Join(counts, "internal%2Fgitbudget"))
	if err != nil || n < 1 {
		t.Fatalf("%d traces counted for one git process (%v)", n, err)
	}
	a, err := attribute(filepath.Join(counts, "internal%2Fgitbudget"))
	if err != nil || a.total != n || a.callers["example.com/m/pkg.Asker"] != n || a.tests["TestWrapped"] != n {
		t.Errorf("the git process is not under its caller and test: %+v (%v), %d traces", a, err, n)
	}

	const pkg = testModule + "/internal/gitbudget"
	baselineFile := filepath.Join(tmp, "baseline.yaml")
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 0\n")
	noenv := func(string) string { return "" }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"write", baselineFile, counts, pkg}, noenv, &stdout, &stderr); code != 0 {
		t.Fatalf("write: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	if code := run([]string{"check", cfg, baselineFile, counts, pkg}, noenv, &stdout, &stderr); code != 0 {
		t.Errorf("check after write: exit %d, want 0:\n%s%s", code, stdout.String(), stderr.String())
	}
	// check's own report, for a package whose path has a slash, names the
	// caller and the test of the process the wrapper counted.
	for _, want := range []string{"example.com/m/pkg.Asker", "TestWrapped", fmt.Sprintf("(%d git processes)", n)} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("check's output lacks %q:\n%s", want, stdout.String())
		}
	}
	tsv, err := os.ReadFile(filepath.Join(counts, callersFile))
	if err != nil || !strings.Contains(string(tsv), fmt.Sprintf("internal/gitbudget\tcaller\texample.com/m/pkg.Asker\t%d\n", n)) ||
		!strings.Contains(string(tsv), fmt.Sprintf("internal/gitbudget\ttest\tTestWrapped\t%d\n", n)) {
		t.Errorf("callers.tsv (%v):\n%s", err, tsv)
	}
	stderr.Reset()
	if code := run([]string{"check", cfg, "-", counts, "example.com/other"}, noenv, new(bytes.Buffer), &stderr); code != 1 {
		t.Errorf("check of a package outside the module: exit %d, want 1: %s", code, stderr.String())
	}
}

// traced writes a count dir whose packages hold trace files that name their
// callers and directories, from a list of caller@dir per package.
func traced(t *testing.T, pkgs map[string][][2]string) string {
	t.Helper()
	dir := t.TempDir()
	for pkg, procs := range pkgs {
		pd := filepath.Join(dir, countDirName(pkg))
		if err := os.MkdirAll(pd, 0o755); err != nil {
			t.Fatal(err)
		}
		for i, p := range procs {
			body := ""
			if p[0] != "" {
				body = defParams(p[0], p[1])
			}
			if err := os.WriteFile(filepath.Join(pd, fmt.Sprintf("t%d", i)), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// TestCheckReportsCallersAndTests proves check prints and summarizes who
// started the processes after the table, writes every count to callers.tsv,
// and leaves the verdict and the exit code to the totals alone.
func TestCheckReportsCallersAndTests(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 0\n")
	procs := map[string][][2]string{
		"e2e": {
			{"x.Setup", "/tmp/TestA1/001"}, {"x.Setup", "/tmp/TestA2/001"}, {"x.Setup", "/tmp/TestB3/001"},
			{"x.Commit", "/tmp/TestB4/001"}, {"", ""},
		},
		"internal/lint": {{"x.Setup", "/tmp/elsewhere"}},
	}
	for _, c := range []struct {
		name, base string
		code       int
	}{
		{"within", "packages:\n  e2e: 5\n  internal/lint: 1\n", 0},
		{"over", "packages:\n  e2e: 2\n  internal/lint: 1\n", 1},
		{"none", "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			counts := traced(t, procs)
			base := "-"
			if c.base != "" {
				base = writeFile(t, "baseline.yaml", c.base)
			}
			summary := filepath.Join(t.TempDir(), "summary.md")
			env := func(k string) string {
				if k == "GITHUB_STEP_SUMMARY" {
					return summary
				}
				return ""
			}
			var stdout, stderr bytes.Buffer
			code := run([]string{"check", cfg, base, counts, testModule + "/e2e", testModule + "/internal/lint"}, env, &stdout, &stderr)
			if code != c.code {
				t.Fatalf("exit %d, want %d (attribution never changes the verdict):\n%s%s", code, c.code, stdout.String(), stderr.String())
			}
			md, err := os.ReadFile(summary)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"top 20 callers across the packages (6 git processes)",
				"       4   66.7%  x.Setup",
				"e2e: top callers (5 git processes)",
				"       2   40.0%  TestA",
				"       1   20.0%  (unattributed)",
				"internal/lint: top tests (1 git processes)",
				"       1  100.0%  (other)",
			} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, stdout.String())
				}
			}
			for _, want := range []string{
				"### Top 20 callers across the packages",
				"| x.Setup | 4 | 66.7% |",
				"#### e2e: top tests (5 git processes)",
				"| TestA | 2 | 40.0% |",
			} {
				if !strings.Contains(string(md), want) {
					t.Errorf("summary lacks %q:\n%s", want, md)
				}
			}
			if tableAt, callersAt := strings.Index(string(md), "### Git processes per package"), strings.Index(string(md), "### Top 20"); tableAt < 0 || callersAt < tableAt {
				t.Errorf("the callers section must follow the package table:\n%s", md)
			}
			tsv, err := os.ReadFile(filepath.Join(counts, callersFile))
			if err != nil {
				t.Fatalf("no callers.tsv: %v", err)
			}
			wantTSV := "e2e\tcaller\tx.Setup\t3\n" + "e2e\tcaller\t(unattributed)\t1\n" + "e2e\tcaller\tx.Commit\t1\n" +
				"e2e\ttest\tTestA\t2\n" + "e2e\ttest\tTestB\t2\n" + "e2e\ttest\t(unattributed)\t1\n" +
				"internal/lint\tcaller\tx.Setup\t1\n" + "internal/lint\ttest\t(other)\t1\n"
			if string(tsv) != wantTSV {
				t.Errorf("callers.tsv:\n%s\nwant:\n%s", tsv, wantTSV)
			}
		})
	}
}

// A package with no trace files is omitted from callers.tsv, and a check over
// untraced counts writes nothing there.
func TestCheckOmitsPackagesWithoutTraces(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	counts := traced(t, map[string][][2]string{"e2e": {{"x.A", "/tmp/TestA1"}}, "lint": nil})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check", cfg, "-", counts, testModule + "/e2e", testModule + "/lint", testModule + "/nothing"}, func(string) string { return "" }, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	tsv, _ := os.ReadFile(filepath.Join(counts, callersFile))
	if strings.Contains(string(tsv), "lint") || strings.Contains(string(tsv), "nothing") || !strings.Contains(string(tsv), "e2e\tcaller\tx.A\t1\n") {
		t.Errorf("callers.tsv:\n%s", tsv)
	}
}

// The report cuts at 20 callers overall and 10 per package; callers.tsv
// keeps every row.
func TestReportCutsAreAppliedAndTheTSVKeepsEverything(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	var procs [][2]string
	for i := range 25 {
		procs = append(procs, [2]string{fmt.Sprintf("x.F%02d", i), "/tmp/TestA1"})
	}
	counts := traced(t, map[string][][2]string{"internal/big": procs})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check", cfg, "-", counts, testModule + "/internal/big"}, func(string) string { return "" }, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	overall, perPkg, _ := strings.Cut(out, "internal/big: top callers")
	if n := strings.Count(overall, "x.F"); n != topOverall {
		t.Errorf("%d callers in the overall section, want %d:\n%s", n, topOverall, out)
	}
	callers, _, _ := strings.Cut(perPkg, "internal/big: top tests")
	if n := strings.Count(callers, "x.F"); n != topPackage {
		t.Errorf("%d callers in the package section, want %d", n, topPackage)
	}
	tsv, err := os.ReadFile(filepath.Join(counts, callersFile))
	if err != nil || strings.Count(string(tsv), "\tcaller\t") != 25 {
		t.Errorf("callers.tsv should hold all 25 callers (%v):\n%s", err, tsv)
	}
}

// With no traces at all the report is empty and no callers.tsv is written.
func TestCheckWithoutTracesPrintsNoReport(t *testing.T) {
	cfg := writeFile(t, "git-budget.yaml", "margin: 5\nslack: 10\n")
	counts := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check", cfg, "-", counts, testModule + "/internal/none"}, func(string) string { return "" }, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "callers") {
		t.Errorf("report for no traces:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(counts, callersFile)); err == nil {
		t.Error("callers.tsv written with no traces")
	}
}
