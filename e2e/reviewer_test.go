package e2e

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// runJigStdin is runJig with an explicit stdin reader, so a test can pin
// down whether the child process sees a terminal or not instead of relying
// on the platform's null-device fallback (which reports as a character
// device on at least one Windows environment this suite runs on - see the
// deviation noted in the S3 stage report).
func runJigStdin(t *testing.T, cwd string, stdin io.Reader, args ...string) jigResult {
	t.Helper()
	cmd := exec.Command(jigBinary, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	cmd.Stdin = stdin
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return jigResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: exitCodeOf(t, err)}
}

// gateReportYAMLMirror mirrors verifydeliver's unexported reportYAML shape
// (gate/round-<n>/report.yaml's exact on-disk fields) for test assertions.
type gateReportYAMLMirror struct {
	Round       int               `yaml:"round"`
	Verdict     string            `yaml:"verdict"`
	Model       string            `yaml:"model"`
	TargetSHA   map[string]string `yaml:"target_sha"`
	ReviewedSHA map[string]string `yaml:"reviewed_sha,omitempty"`
}

// findingsFileMirror mirrors verifydeliver's unexported findingsFile shape
// (gate/round-<n>/findings.yaml).
type findingsFileMirror struct {
	Findings []verifydeliver.Finding `yaml:"findings"`
	Closures []verifydeliver.Closure `yaml:"closures"`
}

func readReviewJSON(t *testing.T, storeDir, ticket string, round int) verifydeliver.ReviewRequest {
	t.Helper()
	data := readFileOrFatal(t, joinPath(storeDir, ticket, "work", "gate.round-"+strconv.Itoa(round)+".review.json"))
	var req verifydeliver.ReviewRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("parse review.json round %d: %v\n%s", round, err, data)
	}
	return req
}

func readGateReport(t *testing.T, storeDir, ticket string, round int) gateReportYAMLMirror {
	t.Helper()
	data := readFileOrFatal(t, joinPath(storeDir, ticket, "gate", "round-"+strconv.Itoa(round), "report.yaml"))
	var rep gateReportYAMLMirror
	if err := yaml.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse report.yaml round %d: %v\n%s", round, err, data)
	}
	return rep
}

func readFindingsYAML(t *testing.T, storeDir, ticket string, round int) findingsFileMirror {
	t.Helper()
	data := readFileOrFatal(t, joinPath(storeDir, ticket, "gate", "round-"+strconv.Itoa(round), "findings.yaml"))
	var ff findingsFileMirror
	if err := yaml.Unmarshal(data, &ff); err != nil {
		t.Fatalf("parse findings.yaml round %d: %v\n%s", round, err, data)
	}
	return ff
}

func sliceByID(slices []store.Slice, id string) (store.Slice, bool) {
	for _, s := range slices {
		if s.ID == id {
			return s, true
		}
	}
	return store.Slice{}, false
}

// TestGateReviewerTwoRounds drives the real, session-dispatched gate
// reviewer (played back by the fake session backend) across two rounds: a
// full-scope round 1 that raises one mechanical and one intent finding
// (bundled and synthesized into fix slices, one pinned to the cheapest
// rung), then a delta-scope round 2 that closes both and lands clean. The
// old scripted `--scenario` path (with no --backend) is unaffected - that
// is TestEndToEndTwice's own proof, run separately.
func TestGateReviewerTwoRounds(t *testing.T) {
	fx, home := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	ticket := fx.Ticket
	repoName := (project.Repo{Remote: fx.RepoRemote}).Name()

	// --- 1. run to first pause, then answer to full green (same as
	// TestEndToEndTwice's steps 1-2; the reviewer branch does not touch
	// slices a-d).
	r1 := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}
	r2 := runJig(t, fx.StoreDir, "run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code != 0 {
		t.Fatalf("run 2 (answer) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}

	buildLeaseDir := poolBuildLeaseDir(home, repoName, ticket)
	headBeforeRound1 := gitLog(t, buildLeaseDir, "rev-parse", "HEAD")
	startSHA := strings.TrimSpace(string(readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "start."+repoName+".sha"))))

	// --- 2. gate round 1: real reviewer via the fake backend, explicit
	// empty stdin so it is unambiguously not a terminal (see runJigStdin).
	r3 := runJigStdin(t, fx.StoreDir, strings.NewReader(""), "gate", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r3.Code != 0 {
		t.Fatalf("gate round 1 exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r3.Code, r3.Stdout, r3.Stderr)
	}
	if !strings.Contains(r3.Stdout, "triage: kept all 2 findings") {
		t.Fatalf("gate round 1 stdout missing the triage note:\n%s", r3.Stdout)
	}
	if !strings.Contains(r3.Stdout, "scope: full") {
		t.Fatalf("gate round 1 stdout missing scope:\n%s", r3.Stdout)
	}

	req1 := readReviewJSON(t, fx.StoreDir, ticket, 1)
	if req1.Scope != "full" {
		t.Fatalf("round 1 review.json scope = %q, want full", req1.Scope)
	}
	if req1.BaseSHA != startSHA {
		t.Fatalf("round 1 review.json base_sha = %q, want start sha %q", req1.BaseSHA, startSHA)
	}
	if req1.HeadSHA != headBeforeRound1 {
		t.Fatalf("round 1 review.json head_sha = %q, want build lease HEAD %q", req1.HeadSHA, headBeforeRound1)
	}
	if len(req1.PriorFindings) != 0 || len(req1.Dismissed) != 0 {
		t.Fatalf("round 1 review.json should have no prior findings/dismissed, got %+v / %+v", req1.PriorFindings, req1.Dismissed)
	}

	ff1 := readFindingsYAML(t, fx.StoreDir, ticket, 1)
	if len(ff1.Findings) != 2 {
		t.Fatalf("round 1 findings.yaml has %d findings, want 2:\n%+v", len(ff1.Findings), ff1.Findings)
	}
	byID := map[string]verifydeliver.Finding{}
	for _, f := range ff1.Findings {
		byID[f.ID] = f
	}
	f1, ok := byID["r1-f1"]
	if !ok || f1.Class != verifydeliver.ClassMechanical || f1.Status != verifydeliver.StatusOpen {
		t.Fatalf("round 1 finding r1-f1 = %+v, want mechanical/open", f1)
	}
	f2, ok := byID["r1-f2"]
	if !ok || f2.Class != verifydeliver.ClassIntent || f2.Status != verifydeliver.StatusOpen {
		t.Fatalf("round 1 finding r1-f2 = %+v, want intent/open", f2)
	}

	findingsMD1 := string(readFileOrFatal(t, joinPath(fx.StoreDir, ticket, "gate", "round-1", "findings.md")))
	if !strings.Contains(findingsMD1, "r1-f1") || !strings.Contains(findingsMD1, "r1-f2") {
		t.Fatalf("round 1 findings.md missing rendered finding ids:\n%s", findingsMD1)
	}

	rep1 := readGateReport(t, fx.StoreDir, ticket, 1)
	if rep1.Verdict != "fix-slices" {
		t.Fatalf("round 1 report.yaml verdict = %q, want fix-slices", rep1.Verdict)
	}
	if rep1.ReviewedSHA[repoName] != headBeforeRound1 {
		t.Fatalf("round 1 report.yaml reviewed_sha[%s] = %q, want %q", repoName, rep1.ReviewedSHA[repoName], headBeforeRound1)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	slices1, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	mech, ok := sliceByID(slices1, "fix-1-mech")
	if !ok {
		t.Fatalf("expected slice fix-1-mech; got %+v", slices1)
	}
	if mech.Rung != "cheapest" || mech.FromGate != 1 {
		t.Fatalf("fix-1-mech = %+v, want rung cheapest, from_gate 1", mech)
	}
	fixIntent, ok := sliceByID(slices1, "fix-1-2")
	if !ok {
		t.Fatalf("expected slice fix-1-2; got %+v", slices1)
	}
	if fixIntent.Rung != "" || fixIntent.FromGate != 1 {
		t.Fatalf("fix-1-2 = %+v, want no rung, from_gate 1", fixIntent)
	}

	statusR3 := runJig(t, fx.StoreDir, "status", ticket)
	assertGolden(t, "status-reviewer-round1.txt", statusR3.Stdout)

	// --- 3. drive both fix slices green.
	r4 := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r4.Code != 0 {
		t.Fatalf("run (fix slices) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r4.Code, r4.Stdout, r4.Stderr)
	}
	assertSliceState(t, st, ticket, "fix-1-mech", "green", 1)
	assertSliceState(t, st, ticket, "fix-1-2", "green", 1)

	lines := readJournal(t, fx, ticket)
	dl, ok := findJournalLine(lines, "dispatch", "fix-1-mech", "")
	if !ok {
		t.Fatalf("no dispatch journal line for fix-1-mech; journal:\n%+v", lines)
	}
	if dl.Model != "claude-haiku-4-5" {
		t.Fatalf("fix-1-mech dispatch model = %q, want the cheapest rung's model claude-haiku-4-5", dl.Model)
	}

	headBeforeRound2 := gitLog(t, buildLeaseDir, "rev-parse", "HEAD")

	// --- 4. gate round 2: delta scope, closes both findings, clean.
	r5 := runJigStdin(t, fx.StoreDir, strings.NewReader(""), "gate", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r5.Code != 0 {
		t.Fatalf("gate round 2 exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r5.Code, r5.Stdout, r5.Stderr)
	}

	req2 := readReviewJSON(t, fx.StoreDir, ticket, 2)
	if req2.Scope != "delta" {
		t.Fatalf("round 2 review.json scope = %q, want delta", req2.Scope)
	}
	if req2.BaseSHA != headBeforeRound1 {
		t.Fatalf("round 2 review.json base_sha = %q, want round 1's reviewed sha %q", req2.BaseSHA, headBeforeRound1)
	}
	if req2.HeadSHA != headBeforeRound2 {
		t.Fatalf("round 2 review.json head_sha = %q, want build lease HEAD %q", req2.HeadSHA, headBeforeRound2)
	}
	priorIDs := map[string]bool{}
	for _, pf := range req2.PriorFindings {
		priorIDs[pf.ID] = true
	}
	if !priorIDs["r1-f1"] || !priorIDs["r1-f2"] {
		t.Fatalf("round 2 review.json prior_findings = %+v, want r1-f1 and r1-f2", req2.PriorFindings)
	}

	ff2 := readFindingsYAML(t, fx.StoreDir, ticket, 2)
	closed := map[string]string{}
	for _, c := range ff2.Closures {
		closed[c.ID] = c.Status
	}
	if closed["r1-f1"] != "closed" || closed["r1-f2"] != "closed" {
		t.Fatalf("round 2 findings.yaml closures = %+v, want r1-f1 and r1-f2 closed", ff2.Closures)
	}

	rep2 := readGateReport(t, fx.StoreDir, ticket, 2)
	if rep2.Verdict != "clean" {
		t.Fatalf("round 2 report.yaml verdict = %q, want clean", rep2.Verdict)
	}
	if rep2.ReviewedSHA[repoName] != headBeforeRound2 {
		t.Fatalf("round 2 report.yaml reviewed_sha[%s] = %q, want %q", repoName, rep2.ReviewedSHA[repoName], headBeforeRound2)
	}

	statusR5 := runJig(t, fx.StoreDir, "status", ticket)
	if !strings.Contains(statusR5.Stdout, "jig publish "+ticket) {
		t.Fatalf("status after round 2 (clean) does not point at publish:\n%s", statusR5.Stdout)
	}
}
