package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestStandingRoundNoRound: a ticket nobody has gated has no round standing.
func TestStandingRoundNoRound(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	st, err := StandingRound(d, fx.Ticket)
	if err != nil || st != nil {
		t.Fatalf("StandingRound = %+v, %v; want none", st, err)
	}
}

// TestStandingRoundOpenRoundAtTheHead: an open round at the branch's head
// stands, and says it is open; a commit on the branch moves it off.
func TestStandingRoundOpenRoundAtTheHead(t *testing.T) {
	t.Parallel()
	fx, d, head := cloneBuilt(t)
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatal(err)
	}
	st, err := StandingRound(d, fx.Ticket)
	if err != nil || st == nil {
		t.Fatalf("StandingRound = %+v, %v; want the open round", st, err)
	}
	if !st.Open || st.Round != p.Round || st.Heads["fixture-repo"] != head {
		t.Errorf("Standing = %+v, want open round %d at %s", st, p.Round, head)
	}

	writeAndCommit(t, fx.RepoDir, "more.txt", "more", "the branch moves")
	if st, err := StandingRound(d, fx.Ticket); err != nil || st != nil {
		t.Fatalf("after the branch moved: %+v, %v; want none", st, err)
	}
}

// TestStandingRoundRecordedRoundAtTheHead: a recorded round at the branch's
// head stands with its verdict, and a moved branch ends it.
func TestStandingRoundRecordedRoundAtTheHead(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	st, err := StandingRound(d, fx.Ticket)
	if err != nil || st == nil {
		t.Fatalf("StandingRound = %+v, %v; want round 1", st, err)
	}
	if st.Open || st.Round != 1 || st.Verdict != "clean" || st.Heads["fixture-repo"] != head {
		t.Errorf("Standing = %+v, want recorded clean round 1 at %s", st, head)
	}

	writeAndCommit(t, fx.RepoDir, "more.txt", "more", "the branch moves")
	if st, err := StandingRound(d, fx.Ticket); err != nil || st != nil {
		t.Fatalf("after the branch moved: %+v, %v; want none", st, err)
	}
}

// TestStandingRoundSeveralRepos: with two repos the round stands only while
// both branches are at its heads.
func TestStandingRoundSeveralRepos(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTwoRepos(t)
	st, err := StandingRound(d, fx.Ticket)
	if err != nil || st == nil || len(st.Heads) != 2 {
		t.Fatalf("StandingRound = %+v, %v; want round 1 over two repos", st, err)
	}
	writeAndCommit(t, fx.SecondRepoDir, "more.txt", "more", "the second repo moves")
	if st, err := StandingRound(d, fx.Ticket); err != nil || st != nil {
		t.Fatalf("after one repo moved: %+v, %v; want none", st, err)
	}
}

// TestStandingRoundNeverStandsWithoutAReviewedHead: a recorded round with no
// reviewed head (a scripted round, or one an older jig recorded) stands at no
// known head, even when the target's sha, which its report does name, is the
// branch's tip.
func TestStandingRoundNeverStandsWithoutAReviewedHead(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)
	path := filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "report.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rep reportYAML
	if err := yaml.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	rep.ReviewedSHA = nil
	rep.TargetSHA = map[string]string{"fixture-repo": head}
	if data, err = yaml.Marshal(rep); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err := StandingRound(d, fx.Ticket); err != nil || st != nil {
		t.Fatalf("StandingRound = %+v, %v; want none: a round with no reviewed head stands at no head", st, err)
	}
}
