package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// targetMoves lands a commit on the target (origin's main, which the gate's
// lease reads) and merges it into the ticket's branch in the operator's clone
// without a conflict, returning the branch's new head.
func targetMoves(t *testing.T, fx *fixture.Fixture, name string) string {
	t.Helper()
	run(t, fx.RepoDir, "checkout", "main")
	writeAndCommit(t, fx.RepoDir, name, "from the target", "the target moves: "+name)
	run(t, fx.RepoDir, "push", "origin", "main")
	run(t, fx.RepoDir, "checkout", ticketBranch(fx.Ticket))
	run(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "main")
	return run(t, fx.RepoDir, "rev-parse", "HEAD")
}

func readReport(t *testing.T, d Deps, ticket string, n int) reportYAML {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, ticket, n), "report.yaml"))
	if err != nil {
		t.Fatalf("read round %d report.yaml: %v", n, err)
	}
	var rep reportYAML
	if err := yaml.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

// TestGatePrepareCarriesACleanMergeOfTheTarget: a head that only merges the
// target into the head a reviewer read is recorded as a clean round carrying
// that verdict over - no round left open, no reviewer - and a second merge
// carries over from the same round, not from the carried one.
func TestGatePrepareCarriesACleanMergeOfTheTarget(t *testing.T) {
	t.Parallel()
	fx, d, reviewed := reviewedInTheClone(t)
	first := readReport(t, d, fx.Ticket, 1)

	merged := targetMoves(t, fx, "main1.txt")
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Carried == nil || p.Carried.CarriedFrom != 1 || p.Carried.Verdict != "clean" || p.Round != 2 {
		t.Fatalf("Prepared = %+v, want round 2 recorded clean, carried over from round 1", p)
	}
	rep := readReport(t, d, fx.Ticket, 2)
	if rep.Verdict != "clean" || rep.CarriedFrom != 1 || rep.ReviewedSHA["fixture-repo"] != merged {
		t.Errorf("report.yaml = %+v, want clean, carried_from 1, reviewed_sha the merge %s (reviewed head was %s)", rep, merged, reviewed)
	}
	if rep.Independent != first.Independent || rep.Reviewer == nil || first.Reviewer == nil || *rep.Reviewer != *first.Reviewer {
		t.Errorf("round 2 independence/reviewer = %q %+v, want round 1's %q %+v", rep.Independent, rep.Reviewer, first.Independent, first.Reviewer)
	}
	if rep.Model != "" || rep.Effort != "" {
		t.Errorf("a carried round names a model/effort (%q, %q)", rep.Model, rep.Effort)
	}
	md, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, 2), "findings.md"))
	if err != nil || !strings.Contains(string(md), "Carried over from round 1: head "+merged+" merges origin/main (") || !strings.Contains(string(md), "and its tree equals git's own merge; the oracle ran and passed.") {
		t.Errorf("findings.md = %q (%v), want it to say the round carries over from round 1", md, err)
	}
	if _, ok, _ := readOpenFile(d.Store, fx.Ticket, 2); ok {
		t.Error("a carried round left an open record")
	}
	if cur, open, err := CurrentRound(d, fx.Ticket); err != nil || open {
		t.Errorf("CurrentRound = %+v, %v, %v, want none open", cur, open, err)
	}
	lines, _ := journal.Read(d.Store, fx.Ticket)
	var clean []journal.Line
	for _, l := range lines {
		if l.Event == journal.EventGateClean {
			clean = append(clean, l)
		}
	}
	if len(clean) != 2 || clean[1].Carried != 1 || clean[1].Attempt != 2 || clean[1].Model != "" || clean[1].Session != "" || clean[1].Agent != "" || clean[0].Carried != 0 {
		t.Errorf("gate-clean lines = %+v, want round 2's to carry round 1 with no model, session or agent", clean)
	}

	// The target moves again: a second carried round names the round that was
	// really reviewed.
	merged2 := targetMoves(t, fx, "main2.txt")
	p, err = GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil || p.Carried == nil || p.Carried.CarriedFrom != 1 {
		t.Fatalf("GatePrepare after a second merge = %+v, %v, want round 3 carried over from round 1", p, err)
	}
	if rep := readReport(t, d, fx.Ticket, 3); rep.ReviewedSHA["fixture-repo"] != merged2 || rep.CarriedFrom != 1 {
		t.Errorf("round 3 report.yaml = %+v, want the second merge %s carried from round 1", rep, merged2)
	}
}

// TestGatePrepareCarriesAChainOfMergesInOneRound: the target moved twice before
// the gate ran again; both merges carry over in the one round.
func TestGatePrepareCarriesAChainOfMergesInOneRound(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	targetMoves(t, fx, "main1.txt")
	merged := targetMoves(t, fx, "main2.txt")
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil || p.Carried == nil || p.Round != 2 || p.Carried.CarriedFrom != 1 {
		t.Fatalf("GatePrepare = %+v, %v, want round 2 carried over from round 1", p, err)
	}
	if rep := readReport(t, d, fx.Ticket, 2); rep.ReviewedSHA["fixture-repo"] != merged {
		t.Errorf("reviewed_sha = %q, want the tip merge %s", rep.ReviewedSHA["fixture-repo"], merged)
	}
}

// TestGatePrepareOpensARoundForWhatIsNotAMergeOfTheTarget: a merge resolved by
// hand, or a clean merge of a branch that is not the target, is not carried
// over: the round opens, and a reviewer reads it.
func TestGatePrepareOpensARoundForWhatIsNotAMergeOfTheTarget(t *testing.T) {
	t.Parallel()
	t.Run("a merge resolved by hand", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := reviewedInTheClone(t)
		run(t, fx.RepoDir, "checkout", "main")
		writeAndCommit(t, fx.RepoDir, "main1.txt", "from the target", "the target moves")
		run(t, fx.RepoDir, "push", "origin", "main")
		run(t, fx.RepoDir, "checkout", ticketBranch(fx.Ticket))
		run(t, fx.RepoDir, "merge", "--no-ff", "--no-commit", "main")
		writeAndCommit(t, fx.RepoDir, "hand.txt", "an edit the merge did not need", "merge main, by hand")
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("GatePrepare: %v", err)
		}
		if p.Carried != nil || p.Round != 2 || p.Scope != "delta" {
			t.Fatalf("Prepared = %+v, want an open delta round 2", p)
		}
		if got := strings.Join(p.Request.MustReview, ","); got != "hand.txt" {
			t.Errorf("must_review = %q, want only the hand edit, none of what the target brought in", got)
		}
	})
	t.Run("a merge of a branch that is not the target", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := reviewedInTheClone(t)
		branch := ticketBranch(fx.Ticket)
		run(t, fx.RepoDir, "checkout", "-b", "other", "main")
		writeAndCommit(t, fx.RepoDir, "other.txt", "work that is not on the target", "other")
		run(t, fx.RepoDir, "checkout", branch)
		run(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "other")
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("GatePrepare: %v", err)
		}
		if p.Carried != nil || p.Round != 2 {
			t.Fatalf("Prepared = %+v, want an open round 2: a merge of another branch is read", p)
		}
	})
}

// TestGatePrepareOpensARoundWhenTheIntentChangedSinceTheReviewedRound: a brief
// edited after the reviewed round is an intent no reviewer read, so a clean
// merge of the target is not carried over: publish holds the last clean round
// to the intent it pinned (PUBLISH_UNREVIEWED_INTENT), and a carried round
// that pinned the new brief would launder the edit.
func TestGatePrepareOpensARoundWhenTheIntentChangedSinceTheReviewedRound(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	briefPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")
	original, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, append(original, []byte("\nEdited after the clean round.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Store.Push(fx.Ticket + ": edit brief.md after the clean round"); err != nil {
		t.Fatal(err)
	}
	targetMoves(t, fx, "main1.txt")
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Carried != nil || p.Round != 2 {
		t.Fatalf("Prepared = %+v, want an open round 2: the brief changed since round 1", p)
	}
}

// TestGatePrepareOpensARoundForAPendingDecision: a decision a person made since
// the last closed round is applied by the next round that closes. A carried
// round applies nothing, so with one pending the gate opens a round.
func TestGatePrepareOpensARoundForAPendingDecision(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	if err := journal.Append(d.Store, fx.Ticket, journal.Line{Event: journal.EventTriage, Ref: "r1-f1", Outcome: TriageKeep, Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
	targetMoves(t, fx, "main1.txt")
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Carried != nil || p.Round != 2 {
		t.Fatalf("Prepared = %+v, want an open round 2: a keep is pending", p)
	}
}

// TestReviewerLinesSayACarriedRoundWasNotReviewed: the pull request's review
// notes word a carried round as carried over, never as reviewed by round N's
// reviewer.
func TestReviewerLinesSayACarriedRoundWasNotReviewed(t *testing.T) {
	t.Parallel()
	who := Provenance{Session: "s-rev"}
	lines := reviewerLines([]RoundRecord{
		{Round: 1, Scope: "full", Base: "aaaaaaa1", Head: "bbbbbbb2", Reviewer: who, Independent: IndependentYes},
		{Round: 2, Scope: "delta", Base: "bbbbbbb2", Head: "ccccccc3", Reviewer: who, Independent: IndependentYes, CarriedFrom: 1},
	})
	if len(lines) != 2 || !strings.Contains(lines[0], "reviewed by") {
		t.Fatalf("lines = %q, want round 1 reviewed by its reviewer", lines)
	}
	if !strings.Contains(lines[1], "Round 2") || !strings.Contains(lines[1], "carried over from round 1") || strings.Contains(lines[1], "reviewed by") {
		t.Errorf("round 2 line = %q, want it carried over from round 1 and not reviewed by anyone", lines[1])
	}
}

// TestGatePrepareHoldsAStackedTicketToItsParentsBranch: for a ticket stacked on
// a parent that has not merged, the branch a merge must bring in is the
// parent's, as publish reads it: a merge of the moved parent branch
// is carried over, and a merge of main, which is not where the pull request
// goes, is read.
func TestGatePrepareHoldsAStackedTicketToItsParentsBranch(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	d.Stack = stackedOnJIG0
	branch := ticketBranch(fx.Ticket)

	// The parent's branch is on origin and moves; the ticket merges it in.
	run(t, fx.RepoDir, "checkout", "-b", "jig/JIG-0", "main")
	writeAndCommit(t, fx.RepoDir, "parent.txt", "from the parent", "the parent moves")
	run(t, fx.RepoDir, "push", "origin", "jig/JIG-0")
	run(t, fx.RepoDir, "checkout", branch)
	run(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "jig/JIG-0")
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare after merging the parent's branch: %v", err)
	}
	if p.Carried == nil || p.Carried.CarriedFrom != 1 {
		t.Fatalf("Prepared = %+v, want round 2 carried over: the parent's branch is the stacked ticket's target", p)
	}

	// Main moves and the ticket merges it: not the branch its pull request
	// goes into, so a reviewer reads it.
	targetMoves(t, fx, "main1.txt")
	p, err = GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare after merging main: %v", err)
	}
	if p.Carried != nil || p.Round != 3 {
		t.Fatalf("Prepared = %+v, want an open round 3: a merge of main is read for a ticket stacked on a parent", p)
	}
}

// reviewedInTwoRepos is a two-repo ticket taken through one clean round: the
// ticket's branch is cut in both clones with a scenario patch each, every
// slice is green, and round 1 reviewed both heads.
func reviewedInTwoRepos(t *testing.T) (*fixture.Fixture, Deps, ReviewResult) {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), SecondRepo: true})
	d := newDeps(t, fx)
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir, "second-repo": fx.SecondRepoDir}}
	branch := ticketBranch(fx.Ticket)
	run(t, fx.RepoDir, "checkout", "-b", branch)
	applyScenarioPatch(t, fx, fx.RepoDir, fx.Ticket, "a", 1)
	run(t, fx.SecondRepoDir, "checkout", "-b", branch)
	applyScenarioPatch(t, fx, fx.SecondRepoDir, fx.Ticket, "e", 1)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := d.Store.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatalf("write slice state %s: %v", id, err)
		}
	}
	if err := d.Store.Push(fx.Ticket + ": every slice green"); err != nil {
		t.Fatalf("push the slice states: %v", err)
	}
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare round 1: %v", err)
	}
	res := cleanReport(p)
	if _, err := GateFinish(d, p, res, Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish round 1: %v", err)
	}
	return fx, d, res
}

// TestGateCarriesATwoRepoRoundWhenTheTargetMovedInOne: the target moves in one
// repo only and merges cleanly there; the round carries over, naming the head
// of each repo.
func TestGateCarriesATwoRepoRoundWhenTheTargetMovedInOne(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTwoRepos(t)
	first := readReport(t, d, fx.Ticket, 1)
	if first.ReviewedSHA["fixture-repo"] == "" || first.ReviewedSHA["second-repo"] == "" {
		t.Fatalf("round 1 reviewed_sha = %v, want both repos", first.ReviewedSHA)
	}

	run(t, fx.SecondRepoDir, "checkout", "main")
	writeAndCommit(t, fx.SecondRepoDir, "main1.txt", "from the target", "the target moves in the second repo")
	run(t, fx.SecondRepoDir, "push", "origin", "main")
	run(t, fx.SecondRepoDir, "checkout", ticketBranch(fx.Ticket))
	run(t, fx.SecondRepoDir, "merge", "--no-ff", "--no-edit", "main")
	merged := run(t, fx.SecondRepoDir, "rev-parse", "HEAD")

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Carried == nil || p.Round != 2 || p.Carried.CarriedFrom != 1 {
		t.Fatalf("Prepared = %+v, want round 2 carried over from round 1", p)
	}
	rep := readReport(t, d, fx.Ticket, 2)
	if rep.ReviewedSHA["fixture-repo"] != first.ReviewedSHA["fixture-repo"] || rep.ReviewedSHA["second-repo"] != merged {
		t.Errorf("reviewed_sha = %v, want fixture-repo unchanged (%s) and second-repo the merge %s", rep.ReviewedSHA, first.ReviewedSHA["fixture-repo"], merged)
	}
}

// TestGateCarriesARoundWhoseStackedRepoMergedNothing: a two-repo ticket stacked
// on a parent in one repo, whose parent branch is not on origin, still carries
// over when the target moved and merged in the other repo only. The repo that
// merged nothing has no merge ref to resolve.
func TestGateCarriesARoundWhoseStackedRepoMergedNothing(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTwoRepos(t)
	d.Stack = stackedOnJIG0
	first := readReport(t, d, fx.Ticket, 1)

	run(t, fx.SecondRepoDir, "checkout", "main")
	writeAndCommit(t, fx.SecondRepoDir, "main1.txt", "from the target", "the target moves in the second repo")
	run(t, fx.SecondRepoDir, "push", "origin", "main")
	run(t, fx.SecondRepoDir, "checkout", ticketBranch(fx.Ticket))
	run(t, fx.SecondRepoDir, "merge", "--no-ff", "--no-edit", "main")
	merged := run(t, fx.SecondRepoDir, "rev-parse", "HEAD")

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Carried == nil || p.Round != 2 || p.Carried.CarriedFrom != 1 {
		t.Fatalf("Prepared = %+v, want round 2 carried over from round 1", p)
	}
	md, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, 2), "findings.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"fixture-repo: head " + first.ReviewedSHA["fixture-repo"] + " is unchanged since round 1",
		"second-repo: head " + merged + " merges origin/main",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("findings.md = %q, want it to contain %q", md, want)
		}
	}
}

// TestReviewerLinesNameTheCarriedMerge: a round that carried a merge of the
// target and read what followed it says which merge; a round over several repos
// puts the repo first; a round without the record, and a carried round, read as
// before.
func TestReviewerLinesNameTheCarriedMerge(t *testing.T) {
	t.Parallel()
	who := Provenance{Session: "s-rev"}
	one := reviewerLines([]RoundRecord{
		{Round: 2, Scope: "delta", Base: "bbbbbbb2", Head: "ccccccc3", Reviewer: who, Independent: IndependentYes,
			CarriedMerge: map[string]CarriedMerge{"app": {SHA: "1234567890abcdef", Ref: "origin/main"}}},
		{Round: 3, Scope: "delta", Base: "bbbbbbb2", Head: "ddddddd4", Reviewer: who, Independent: IndependentYes},
		{Round: 4, Scope: "delta", Base: "bbbbbbb2", Head: "eeeeeee5", Reviewer: who, Independent: IndependentYes, CarriedFrom: 1,
			CarriedMerge: map[string]CarriedMerge{"app": {SHA: "1234567890abcdef", Ref: "origin/main"}}},
	})
	if len(one) != 3 || !strings.HasSuffix(one[0], "none of the builders; the merge of origin/main at 1234567 was carried, not read.") {
		t.Errorf("round 2 line = %q, want it to end naming the carried merge", one)
	}
	if strings.Contains(one[1], "carried") || !strings.HasSuffix(one[1], "none of the builders.") {
		t.Errorf("round 3 line = %q, want it unchanged without the record", one[1])
	}
	if strings.Contains(one[2], "was carried, not read") {
		t.Errorf("round 4 line = %q, want the carried-over wording alone", one[2])
	}

	several := reviewerLines([]RoundRecord{{
		Round: 2, Scope: "delta", Head: "app:ccccccc3+lib:ddddddd4", Reviewer: who, Independent: IndependentYes,
		CarriedMerge: map[string]CarriedMerge{
			"lib": {SHA: "abcdef1234567890", Ref: "origin/trunk"},
			"app": {SHA: "1234567890abcdef", Ref: "origin/main"},
		},
	}})
	want := "none of the builders; app: the merge of origin/main at 1234567 was carried, not read; lib: the merge of origin/trunk at abcdef1 was carried, not read."
	if len(several) != 1 || !strings.HasSuffix(several[0], want) {
		t.Errorf("lines = %q, want them to end %q", several, want)
	}
}

// TestGateFinishRecordsTheMergeADeltaCarried: a clean merge of the target and
// then a commit of the branch's own is a round a reviewer reads, from the
// merge; its report.yaml names the merge and the ref it brought in, and the
// review notes say it was carried, not read.
func TestGateFinishRecordsTheMergeADeltaCarried(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	merged := targetMoves(t, fx, "main1.txt")
	writeAndCommit(t, fx.RepoDir, "fix.txt", "a fix of its own", "a fix after the merge")

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Carried != nil || p.Round != 2 || p.Base != merged || p.CarriedMerge != merged {
		t.Fatalf("Prepared = round %d, base %s, carried merge %s, carried round %v, want a round a reviewer reads from the merge %s", p.Round, p.Base, p.CarriedMerge, p.Carried, merged)
	}
	if want := []string{"fix.txt"}; !reflect.DeepEqual(p.Request.MustReview, want) {
		t.Fatalf("must_review = %v, want only the fix's file %v", p.Request.MustReview, want)
	}
	if _, err := GateFinish(d, p, cleanReport(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish: %v", err)
	}

	rep := readReport(t, d, fx.Ticket, 2)
	if got := rep.CarriedMerge["fixture-repo"]; got != (CarriedMerge{SHA: merged, Ref: "origin/main"}) {
		t.Errorf("report.yaml carried_merge = %+v, want the merge %s of origin/main", rep.CarriedMerge, merged)
	}
	rounds, err := ReadRounds(d.Store, fx.Ticket)
	if err != nil || len(rounds) != 2 {
		t.Fatalf("ReadRounds = %d rounds, %v, want 2", len(rounds), err)
	}
	if len(rounds[0].CarriedMerge) != 0 {
		t.Errorf("round 1 carried merge = %+v, want none", rounds[0].CarriedMerge)
	}
	lines := reviewerLines(rounds)
	want := "; the merge of origin/main at " + merged[:7] + " was carried, not read."
	if len(lines) != 2 || !strings.HasSuffix(lines[1], want) || strings.Contains(lines[0], "carried") {
		t.Errorf("review notes lines = %q, want round 2's to end %q", lines, want)
	}
}

// TestRoundsRecordTheMergeADeltaCarriedInEachShape: the carried-merge record
// holds for a merge resolved by hand, for a stacked ticket's parent branch, for
// one repo of a round over two, and for a round no merge led, which names no ref.
func TestRoundsRecordTheMergeADeltaCarriedInEachShape(t *testing.T) {
	t.Parallel()

	t.Run("a merge resolved by hand then a commit", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := reviewedInTheClone(t)
		run(t, fx.RepoDir, "checkout", "main")
		writeAndCommit(t, fx.RepoDir, "main1.txt", "from the target", "the target moves")
		run(t, fx.RepoDir, "push", "origin", "main")
		run(t, fx.RepoDir, "checkout", ticketBranch(fx.Ticket))
		run(t, fx.RepoDir, "merge", "--no-ff", "--no-commit", "main")
		merged := writeAndCommit(t, fx.RepoDir, "hand.txt", "a hand edit", "merge main, by hand")
		writeAndCommit(t, fx.RepoDir, "fix.txt", "a fix", "a fix after the merge")

		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("GatePrepare: %v", err)
		}
		if p.CarriedMerge != merged || p.CarriedRef != "origin/main" {
			t.Fatalf("carried = %q of %q, want the hand-resolved merge %s of origin/main", p.CarriedMerge, p.CarriedRef, merged)
		}
		if _, err := GateFinish(d, p, cleanReport(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
			t.Fatalf("GateFinish: %v", err)
		}
		if got := readReport(t, d, fx.Ticket, 2).CarriedMerge["fixture-repo"]; got != (CarriedMerge{SHA: merged, Ref: "origin/main"}) {
			t.Errorf("carried_merge = %+v, want the merge %s", got, merged)
		}
	})

	t.Run("a stacked ticket's parent branch", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := reviewedInTheClone(t)
		d.Stack = stackedOnJIG0
		branch := ticketBranch(fx.Ticket)
		run(t, fx.RepoDir, "checkout", "-b", "jig/JIG-0", "main")
		writeAndCommit(t, fx.RepoDir, "parent.txt", "from the parent", "the parent moves")
		run(t, fx.RepoDir, "push", "origin", "jig/JIG-0")
		run(t, fx.RepoDir, "checkout", branch)
		run(t, fx.RepoDir, "merge", "--no-ff", "--no-edit", "jig/JIG-0")
		merged := run(t, fx.RepoDir, "rev-parse", "HEAD")
		writeAndCommit(t, fx.RepoDir, "fix.txt", "a fix", "a fix after the merge")

		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("GatePrepare: %v", err)
		}
		if p.CarriedMerge != merged || p.CarriedRef != "origin/jig/JIG-0" {
			t.Fatalf("carried = %q of %q, want the merge %s of the parent's branch origin/jig/JIG-0", p.CarriedMerge, p.CarriedRef, merged)
		}
		if _, err := GateFinish(d, p, cleanReport(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
			t.Fatalf("GateFinish: %v", err)
		}
		if got := readReport(t, d, fx.Ticket, 2).CarriedMerge["fixture-repo"]; got != (CarriedMerge{SHA: merged, Ref: "origin/jig/JIG-0"}) {
			t.Errorf("carried_merge = %+v, want the merge %s of the parent's branch", got, merged)
		}
	})

	t.Run("one repo of a round over two", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := reviewedInTwoRepos(t)
		run(t, fx.SecondRepoDir, "checkout", "main")
		writeAndCommit(t, fx.SecondRepoDir, "main1.txt", "from the target", "the target moves in the second repo")
		run(t, fx.SecondRepoDir, "push", "origin", "main")
		run(t, fx.SecondRepoDir, "checkout", ticketBranch(fx.Ticket))
		run(t, fx.SecondRepoDir, "merge", "--no-ff", "--no-edit", "main")
		merged := run(t, fx.SecondRepoDir, "rev-parse", "HEAD")
		writeAndCommit(t, fx.SecondRepoDir, "fix.txt", "a fix", "a fix after the merge")

		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("GatePrepare: %v", err)
		}
		for _, r := range p.Repos {
			if want := r.Repo == "second-repo"; (r.Carried == merged) != want || (r.Carried != "") != want {
				t.Errorf("repo %s carried %q, want the merge %s in second-repo only", r.Repo, r.Carried, merged)
			}
		}
		if _, err := GateFinish(d, p, cleanReport(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
			t.Fatalf("GateFinish: %v", err)
		}
		want := map[string]CarriedMerge{"second-repo": {SHA: merged, Ref: "origin/main"}}
		if got := readReport(t, d, fx.Ticket, 2).CarriedMerge; !reflect.DeepEqual(got, want) {
			t.Errorf("carried_merge = %+v, want %+v", got, want)
		}
	})

	t.Run("a round no merge led names no ref", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := cloneBuilt(t)
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("GatePrepare: %v", err)
		}
		if p.CarriedMerge != "" || p.CarriedRef != "" {
			t.Errorf("carried = %q of %q, want neither", p.CarriedMerge, p.CarriedRef)
		}
		data, err := os.ReadFile(p.OpenPath)
		if err != nil || strings.Contains(string(data), "carried") {
			t.Errorf("open record = %q (%v), want no carried field", data, err)
		}
	})

	t.Run("a launched round", func(t *testing.T) {
		t.Parallel()
		got := carriedOf([]RepoPrep{{Repo: "a"}, {Repo: "b", Carried: "abc", CarriedRef: "origin/main"}})
		if want := map[string]CarriedMerge{"b": {SHA: "abc", Ref: "origin/main"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("carriedOf = %+v, want %+v", got, want)
		}
		if got := carriedOf([]RepoPrep{{Repo: "a"}}); got != nil {
			t.Errorf("carriedOf with none = %+v, want nil", got)
		}
	})
}
