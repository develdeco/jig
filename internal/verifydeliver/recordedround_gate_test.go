package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// roundByRound is a reviewer that answers the nth round it is handed with
// answer(n, the round).
func roundByRound(answer func(n int, p Prepared) ReviewResult) func(p Prepared) ReviewResult {
	n := 0
	return func(p Prepared) ReviewResult {
		n++
		return answer(n, p)
	}
}

// alphaFinding is a finding of alpha/alpha.go that cites oracle "test".
func alphaFinding(title, prior, action, risk string) ResultFinding {
	return ResultFinding{
		File: "alpha/alpha.go", Line: 1, Title: title, Detail: "d", Action: action,
		Risk: risk, RiskRationale: "r", Oracle: "test", Prior: prior,
	}
}

// TestRecordedRoundKeepsTheRecurrenceBoundAcrossANote: a finding noted in
// between stays a citable id, so the same problem reported again is the same
// finding at one more recurrence, and the second recurrence asks (routed_as
// ask) whatever the round's own label. Dropping a noted finding from the open
// list would let alternating fix and note evade the bound.
func TestRecordedRoundKeepsTheRecurrenceBoundAcrossANote(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	o := GateOpts{Ticket: fx.Ticket, Yes: true}

	reviewer := roundByRound(func(n int, p Prepared) ReviewResult {
		req := p.Request
		switch n {
		case 1:
			return ReviewResult{Findings: []ResultFinding{alphaFinding("needs a fix", "", ActionFix, RiskMedium)}, ReviewedPaths: req.MustReview, Summary: "round 1"}
		case 2:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 0 {
				t.Fatalf("round 2 review.json Open = %+v, want r1-f1 at 0 recurrences", req.Open)
			}
			return ReviewResult{Findings: []ResultFinding{alphaFinding("still there, harmless now", "r1-f1", ActionNote, RiskLow)}, ReviewedPaths: req.MustReview, Summary: "round 2"}
		case 3:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 1 || req.Open[0].Action != ActionNote {
				t.Fatalf("round 3 review.json Open = %+v, want r1-f1 (noted, 1 recurrence) fed back", req.Open)
			}
			return ReviewResult{Findings: []ResultFinding{alphaFinding("back again", "r1-f1", ActionFix, RiskMedium)}, ReviewedPaths: req.MustReview, Summary: "round 3"}
		}
		t.Fatalf("unexpected round %d", n)
		return ReviewResult{}
	})

	report1 := recordedRound(t, d, o, reviewer)
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want exactly one", report1.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report1.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 1 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report1.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 1 fix slice state: %v", err)
	}

	report2 := recordedRound(t, d, o, reviewer)
	if len(report2.FixSlices) != 0 {
		t.Fatalf("round 2 FixSlices = %v, want none (a note is never routed)", report2.FixSlices)
	}
	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("read round 2 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff2.Findings) != 1 || ff2.Findings[0].Status != StatusNoted || ff2.Findings[0].Recurrences != 1 {
		t.Fatalf("round 2 findings = %+v, want [r1-f1 noted, 1 recurrence]", ff2.Findings)
	}

	// A noted finding is not outstanding work, so it no longer forces a round to
	// read on its own: an unrelated commit gives round 3 a reason to.
	buildDir := buildLeaseDir(t, fx)
	if err := os.WriteFile(filepath.Join(buildDir, "beta", "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatalf("write unrelated.txt: %v", err)
	}
	builder(t).Commit(buildDir, "add unrelated.txt")
	builder(t).Push(buildDir, "origin", ticketBranch(fx.Ticket))

	report3 := recordedRound(t, d, o, reviewer)
	if len(report3.Findings) != 1 || report3.Findings[0].ID != "r1-f1" {
		t.Fatalf("round 3 Findings = %+v, want exactly [r1-f1]", report3.Findings)
	}
	if f := report3.Findings[0]; f.RoutedAs != ActionAsk || f.Recurrences != 2 {
		t.Fatalf("round 3 finding = %+v, want routed_as ask, 2 recurrences (the bound reached)", f)
	}
	if len(report3.FixSlices) != 1 || report3.FixSlices[0] != "fix-3-alpha" {
		t.Fatalf("round 3 FixSlices = %v, want exactly [fix-3-alpha] (the ask grouped into the workspace slice)", report3.FixSlices)
	}
	md, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, 3), "findings.md"))
	if err != nil || !strings.Contains(string(md), "- recurrences: 2\n") {
		t.Errorf("round 3 findings.md (%v) does not say the finding recurred twice:\n%s", err, md)
	}
}

// TestRecordedRoundKeepsAFileOutOfMustReviewOnceItIsIgnored: an open finding's
// file that the branch later untracked and ignored is absent at head whatever a
// test left in the working tree, so the next rounds read the tree alone, do not
// ask for the file to be reviewed, and clear the finding instead of wedging.
func TestRecordedRoundKeepsAFileOutOfMustReviewOnceItIsIgnored(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	if err := os.WriteFile(filepath.Join(buildDir, "alpha", "gen.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("write gen.txt: %v", err)
	}
	builder(t).Commit(buildDir, "add tracked gen.txt")
	builder(t).Push(buildDir, "origin", branch)

	d := newDeps(t, fx)
	o := GateOpts{Ticket: fx.Ticket, Yes: true}
	reviewer := roundByRound(func(n int, p Prepared) ReviewResult {
		if n == 1 {
			return ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/gen.txt", Line: 1, Title: "a generated file was committed", Detail: "gen.txt should not be tracked",
					Action: ActionFix, Risk: RiskHigh, RiskRationale: "build artifact churn", Oracle: "test",
				}},
				ReviewedPaths: p.Request.MustReview, Summary: "round 1 summary",
			}
		}
		if containsString(p.Request.MustReview, "alpha/gen.txt") {
			t.Fatalf("round %d must_review = %v, want it to exclude alpha/gen.txt (absent at head)", n, p.Request.MustReview)
		}
		return ReviewResult{ReviewedPaths: p.Request.MustReview, Summary: "later round"}
	})

	report1 := recordedRound(t, d, o, reviewer)
	if report1.Verdict != "fix-slices" {
		t.Fatalf("round 1 Verdict = %q, want fix-slices", report1.Verdict)
	}
	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok || len(ff1.Findings) != 1 || ff1.Findings[0].Status != StatusOpen {
		t.Fatalf("round 1 findings.yaml = %+v (ok %v, err %v), want one open finding", ff1.Findings, ok, err)
	}

	// Untrack gen.txt, ignore it, and have the alpha test regenerate it, as a
	// generated-artifact test would leave it sitting in the lease.
	if _, err := gitx.Run(buildDir, "rm", "--cached", "alpha/gen.txt"); err != nil {
		t.Fatalf("git rm --cached gen.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, ".gitignore"), []byte("alpha/gen.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	alphaTest, err := os.ReadFile(filepath.Join(buildDir, "alpha", "alpha_test.go"))
	if err != nil {
		t.Fatalf("read alpha_test.go: %v", err)
	}
	regen := strings.Replace(string(alphaTest), "import \"testing\"", "import (\n\t\"os\"\n\t\"testing\"\n)", 1)
	regen += "\nfunc TestZZRegenGen(t *testing.T) {\n" +
		"\tif err := os.WriteFile(\"gen.txt\", []byte(\"regenerated\\n\"), 0o644); err != nil {\n" +
		"\t\tt.Fatalf(\"write gen.txt: %v\", err)\n" +
		"\t}\n}\n"
	if err := os.WriteFile(filepath.Join(buildDir, "alpha", "alpha_test.go"), []byte(regen), 0o644); err != nil {
		t.Fatalf("write regenerating alpha_test.go: %v", err)
	}
	builder(t).Commit(buildDir, "untrack and regenerate gen.txt")
	builder(t).Push(buildDir, "origin", branch)

	// Round 1's fix slice is still queued; the rounds that follow run early.
	o.Early = true
	report2 := recordedRound(t, d, o, reviewer)
	if report2.Verdict != "clean" {
		t.Fatalf("round 2 Verdict = %q, want clean (the ignored, regenerated file must clear, not wedge the round)", report2.Verdict)
	}
	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok || !equalStrings(ff2.Cleared, []string{ff1.Findings[0].ID}) {
		t.Fatalf("round 2 cleared = %v (ok %v, err %v), want [%s]", ff2.Cleared, ok, err, ff1.Findings[0].ID)
	}
	if report3 := recordedRound(t, d, o, reviewer); report3.Verdict != "clean" {
		t.Fatalf("round 3 Verdict = %q, want clean: the file stays absent at head for good", report3.Verdict)
	}
}

// TestRecordedRoundKeepsTheFixBudget: with fix_rounds 1 and nothing used, round 1
// queues its fix slice; round 2's own open fix, the budget now reached, parks
// as an ask a person has to take, and the budget used is what the next round
// reads.
func TestRecordedRoundKeepsTheFixBudget(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	fr := 1
	d.Cfg.Gate = &project.GateConfig{FixRounds: &fr}
	o := GateOpts{Ticket: fx.Ticket, Yes: true}

	reviewer := roundByRound(func(n int, p Prepared) ReviewResult {
		return ReviewResult{
			Findings:      []ResultFinding{alphaFinding(fmt.Sprintf("round %d finding", n), "", ActionFix, RiskHigh)},
			ReviewedPaths: p.Request.MustReview, Summary: fmt.Sprintf("round %d", n),
		}
	})
	report1 := recordedRound(t, d, o, reviewer)
	if report1.Verdict != "fix-slices" || len(report1.FixSlices) != 1 || report1.BudgetParked != 0 {
		t.Fatalf("round 1 = %+v, want fix-slices with one fix slice and nothing parked (the budget is not used yet)", report1)
	}

	o.Early = true
	report2 := recordedRound(t, d, o, reviewer)
	if len(report2.FixSlices) != 0 {
		t.Fatalf("round 2 FixSlices = %v, want none (the budget is reached)", report2.FixSlices)
	}
	if report2.BudgetParked != 1 || report2.BudgetUsed != 1 || report2.BudgetLimit != 1 {
		t.Fatalf("round 2 budget = used %d limit %d parked %d, want 1/1/1", report2.BudgetUsed, report2.BudgetLimit, report2.BudgetParked)
	}
	if len(report2.NeedsHuman) != 1 {
		t.Fatalf("round 2 NeedsHuman = %+v, want the parked finding", report2.NeedsHuman)
	}
	if parked := report2.NeedsHuman[0]; parked.Status != StatusAsked || parked.RoutedAs != ActionAsk || parked.RoutedWhy != RoutedWhyBudget {
		t.Fatalf("parked finding = %+v, want asked/routed_as ask/routed_why budget", parked)
	}
	if used, unreadable := UsedFixBudget(d.Store, fx.Ticket, 3); used != 1 || len(unreadable) != 0 {
		t.Fatalf("UsedFixBudget before round 3 = %d (unreadable %v), want 1 and none unreadable", used, unreadable)
	}
}

// TestRecordedRoundKeepsTheProbesAndSkipsInThePullRequest: what a clean round
// probed and set aside reaches the review notes a publish renders, with the
// probe's file as the repo spells it.
func TestRecordedRoundKeepsTheProbesAndSkipsInThePullRequest(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	useGithubHost(t, &d, "", "")

	report := recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, func(p Prepared) ReviewResult {
		h := p.Request.Hunks[0]
		skipped := ""
		for _, o := range p.Request.Hunks {
			if o.File != h.File {
				skipped = o.File
			}
		}
		if skipped == "" {
			t.Fatalf("hunks = %+v, want two files to probe one and set the other aside", p.Request.Hunks)
		}
		res := cleanReport(p)
		res.Probes = []ResultProbe{{Seam: "the clamp", File: h.File, Line: h.From, Mutation: "upper bound 100 to 101", Command: "go test ./alpha", Outcome: ProbeCaught}}
		res.Skipped = []ResultSkip{{File: skipped, Why: "tests"}}
		return res
	})
	if report.Verdict != "clean" || report.Probes != 1 {
		t.Fatalf("round = verdict %q, probes %d, want a clean round with its probe", report.Verdict, report.Probes)
	}

	pub, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	notes, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(pub.Repos[0].ReviewNotes)))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Probes current at the reviewed head:\n\n- ", "caught: upper bound 100 to 101 (the clamp; round 1)", "Set aside as on no critical path:\n\n- "} {
		if !strings.Contains(string(notes), want) {
			t.Errorf("the review notes do not hold %q:\n%s", want, notes)
		}
	}
}

// TestRecordedRoundKeepsTheStoreCommittedWhenItFailsToOpen: a round that fails
// after its gate-open line names its own round in the commit the failure
// leaves, never "round 0", and neither a lease's absolute path nor the raw
// manifest error.
func TestRecordedRoundKeepsTheStoreCommittedWhenItFailsToOpen(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	o := GateOpts{Ticket: fx.Ticket, Yes: true}
	recordedRound(t, d, o, func(p Prepared) ReviewResult { return cleanReport(p) })

	buildDir := buildLeaseDir(t, fx)
	claudeDir := filepath.Join(buildDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "jig.yaml"), []byte("workspaces: [this is not valid yaml"), 0o644); err != nil {
		t.Fatalf("write broken jig.yaml: %v", err)
	}
	builder(t).Commit(buildDir, "break manifest")
	branch := ticketBranch(fx.Ticket)
	builder(t).Push(buildDir, "origin", branch)
	gateLease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("reacquire gate lease: %v", err)
	}

	if _, err := GatePrepare(d, o); err == nil {
		t.Fatal("GatePrepare of round 2: want an error (broken manifest)")
	}
	subject, err := gitx.Run(d.Store.Root, "log", "-1", "--format=%s")
	if err != nil {
		t.Fatalf("read store log: %v", err)
	}
	if strings.Contains(subject, "round 0") || !strings.Contains(subject, "round 2") {
		t.Fatalf("push subject = %q, want it to name the real round 2", subject)
	}
	if strings.Contains(subject, gateLease.Dir) || strings.Contains(subject, "yaml") {
		t.Fatalf("push subject = %q, leaked the gate lease's path or the raw manifest error", subject)
	}
}

// TestRecordedRoundKeepsAScriptedRoundsFailureNamedByItsRound: a source that
// cannot give a round fails Gate with the round's number, so the error says
// which round to look at.
func TestRecordedRoundKeepsAScriptedRoundsFailureNamedByItsRound(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	scenario := t.TempDir()
	dir := filepath.Join(scenario, "gate", "round-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("no findings.md here"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Gate(d, NewFakeGateSource(scenario), GateOpts{Ticket: fx.Ticket})
	if err == nil || !strings.Contains(err.Error(), "gate: read round 1") {
		t.Fatalf("Gate over a scripted round with no findings.md: err = %v, want one naming round 1", err)
	}
}
