package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
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
	if err != nil || !strings.Contains(string(md), "Carried over from round 1: head "+merged) || !strings.Contains(string(md), "the oracle ran and passed") {
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
