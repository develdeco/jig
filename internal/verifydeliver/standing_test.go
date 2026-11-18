package verifydeliver

import "testing"

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
// stands; a commit on the branch moves it off.
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
	if st.Round != p.Round || st.Heads["fixture-repo"] != head {
		t.Errorf("Standing = %+v, want round %d at %s", st, p.Round, head)
	}

	writeAndCommit(t, fx.RepoDir, "more.txt", "more", "the branch moves")
	if st, err := StandingRound(d, fx.Ticket); err != nil || st != nil {
		t.Fatalf("after the branch moved: %+v, %v; want none", st, err)
	}
}

// TestStandingRoundNeverStandsOnceRecorded: a recorded round at the branch's
// head does not stand: a gate after it applies the person's decisions and
// opens the next round.
func TestStandingRoundNeverStandsOnceRecorded(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTheClone(t)
	if st, err := StandingRound(d, fx.Ticket); err != nil || st != nil {
		t.Fatalf("StandingRound = %+v, %v; want none over a recorded round", st, err)
	}
}

// TestStandingRoundSeveralRepos: with two repos the open round stands only
// while both branches are at its heads.
func TestStandingRoundSeveralRepos(t *testing.T) {
	t.Parallel()
	fx, d, _ := reviewedInTwoRepos(t)
	writeAndCommit(t, fx.RepoDir, "next.txt", "next", "round 2 content")
	if _, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket, Early: true}); err != nil {
		t.Fatalf("GatePrepare round 2: %v", err)
	}
	st, err := StandingRound(d, fx.Ticket)
	if err != nil || st == nil || len(st.Heads) != 2 {
		t.Fatalf("StandingRound = %+v, %v; want round 2 over two repos", st, err)
	}
	writeAndCommit(t, fx.SecondRepoDir, "more.txt", "more", "the second repo moves")
	if st, err := StandingRound(d, fx.Ticket); err != nil || st != nil {
		t.Fatalf("after one repo moved: %+v, %v; want none", st, err)
	}
}
