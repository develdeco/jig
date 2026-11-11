package verifydeliver

import (
	"os"
	"path/filepath"
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
