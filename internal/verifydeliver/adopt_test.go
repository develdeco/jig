package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// reviewedHead is the head a gate round over ticket's branch reviews in the
// fixture's repo: the head of the copy the round chooses (chooseCopy), which
// the round reads at, so a test says what a round reviewed without reading a
// checkout.
func reviewedHead(t *testing.T, d Deps, ticket string) string {
	t.Helper()
	branch, err := d.Store.TicketBranch(ticket, "main")
	if err != nil {
		t.Fatalf("resolve %s's branch: %v", ticket, err)
	}
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("read %s's journal: %v", ticket, err)
	}
	rc, err := chooseCopy(d, "fixture-repo", branch, journal.BuiltCommits(lines), "gate")
	if err != nil {
		t.Fatalf("choose the copy of %s: %v", branch, err)
	}
	return rc.Head
}

// jigBuildsOn plays a build session on branch for ticket on this machine: the
// operator's clone is put on the branch (cut from origin's copy of it, else
// from origin's main), one commit is made there, and the journal records it the
// way frontier does. It returns the commit. Nothing is pushed: jig's commits
// stay in the clone until someone pushes them.
func jigBuildsOn(t *testing.T, d Deps, fx *fixture.Fixture, ticket, branch string) string {
	t.Helper()
	run(t, fx.RepoDir, "fetch", "origin")
	switch {
	case func() bool { _, found, _ := gitx.Resolve(fx.RepoDir, "refs/heads/"+branch); return found }():
		run(t, fx.RepoDir, "checkout", branch)
	case func() bool { _, found, _ := gitx.Resolve(fx.RepoDir, "refs/remotes/origin/"+branch); return found }():
		run(t, fx.RepoDir, "checkout", "-b", branch, "origin/"+branch)
	default:
		run(t, fx.RepoDir, "checkout", "-b", branch, "origin/main")
	}
	commit := writeAndCommit(t, fx.RepoDir, "fix.txt", "jig's fix\n", ticket+" fix-1: jig's fix")
	journalBuilt(t, d.Store, ticket, "fix-1", commit, 1)
	return commit
}

// commitLeftover leaves what a build attempt that never verified leaves in a
// clone: one commit no journal line records. It returns the commit.
func commitLeftover(t *testing.T, dir, ticket string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "leftover.txt"), []byte("an attempt's leftover\n"), 0o644); err != nil {
		t.Fatalf("write leftover.txt: %v", err)
	}
	builder(t).Commit(dir, ticket+" attempt: never verified")
	return run(t, dir, "rev-parse", "HEAD")
}

// buildLeaseOf is the directory a ticket is built in on this machine: the
// operator's clone.
func buildLeaseOf(t *testing.T, fx *fixture.Fixture, ticket string) string {
	t.Helper()
	return fx.RepoDir
}

// TestGateAdoptsTheBranchItReviews is the adoption itself, and the chain of
// what a gate does with a branch it adopted until jig builds on it, on a ticket
// minted with a record and nothing else - no brief, no slices - as `jig ticket
// new` leaves it: the first `jig gate --branch X` records X in the ticket's
// record, keeping its title, and records the ticket's start sha as X's tip. Main
// has moved past X's fork point by then, so the tip is not the target's: what
// jig builds on X descends from the tip, and a start sha off main would fail
// verifyGreen for every fix. A start sha an earlier dispatch left is replaced.
// Both writes reach the store's remote, and every later round - with the flag or
// without it - works on the recorded branch.
//
// Until jig has built on an adopted branch it is the author's, so every round
// reviews origin's copy as it is now (the steps after the adoption):
//
//   - naming the recorded branch again is no adoption: the author pushes, the
//     flag is repeated, the round reviews what was pushed, and the start sha
//     stays where the adoption put it - it is the frontier that follows the
//     author, until jig builds, and not a gate round;
//   - the gate lease of a branch jig has built nothing on never commits, so a
//     local commit left behind by a killed reviewer is discarded - not reviewed
//     and passed clean - and origin advancing between two rounds makes the
//     second gate review the new head, not a stale local copy that pool.Acquire
//     would otherwise reuse as-is (it never resets an existing local branch), or
//     refuse over as diverged: the leftover and the author's new commit are each
//     ahead of the other (NM2 scenario B);
//   - the author rewrites the branch: drops its last commit for another;
//   - a branch that existed and gated, then got deleted on origin before the
//     next gate, fails BRANCH_NOT_FOUND rather than reviewing the stale tip that
//     pool.Acquire's own fetch (no --prune) leaves behind in
//     refs/remotes/origin/<branch>.
func TestGateAdoptsTheBranchItReviews(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)
	advanceTarget(t, fx) // main moves past the fork point
	movedMain := originRef(t, fx.RepoRemote, "refs/heads/main")
	if movedMain == run(t, fx.RepoRemote, "merge-base", "refs/heads/main", tip) {
		t.Fatal("test setup: main did not move past the branch's fork point")
	}
	// What an earlier dispatch that built nothing leaves: the target's tip.
	if err := d.Store.WriteStartSHA(ticket, "fixture-repo", movedMain); err != nil {
		t.Fatalf("WriteStartSHA: %v", err)
	}
	// reviewed is the head a round reviews now.
	reviewed := func(t *testing.T) string {
		t.Helper()
		return reviewedHead(t, d, ticket)
	}
	wantStartSHA := func(t *testing.T, want string) {
		t.Helper()
		if start, err := os.ReadFile(d.Store.StartSHAPath(ticket, "fixture-repo")); err != nil || strings.TrimSpace(string(start)) != want {
			t.Fatalf("start sha = %q (err %v), want it kept at the adoption's %s", start, err, want)
		}
	}

	step(t, "the first round adopts the branch and records its tip as the start sha", func(t *testing.T) {
		report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch})
		if err != nil {
			t.Fatalf("Gate --branch on a ticket with no slices: %v", err)
		}
		if report.Round != 1 || report.Verdict != "clean" || report.Branch != branch {
			t.Fatalf("report = round %d %q on %q, want round 1 clean on %q", report.Round, report.Verdict, report.Branch, branch)
		}
		if slices, err := d.Store.ReadSlices(ticket); err != nil || len(slices) != 0 {
			t.Fatalf("slices = %+v (err %v), want none: the ticket was adopted with no slices", slices, err)
		}

		rec, err := d.Store.ReadTicket(ticket)
		if err != nil || rec.Branch != branch || rec.Title != "Add retry" {
			t.Fatalf("record = %+v (err %v), want branch %q with the title kept", rec, err, branch)
		}
		start, err := os.ReadFile(d.Store.StartSHAPath(ticket, "fixture-repo"))
		if err != nil || strings.TrimSpace(string(start)) != tip {
			t.Fatalf("start sha = %q (err %v), want the branch's tip %s, not main's %s", start, err, tip, movedMain)
		}
		if got := run(t, fx.StoreRemote, "show", "main:tickets/"+ticket+"/ticket.yaml"); !strings.Contains(got, "branch: "+branch) {
			t.Fatalf("the store's remote holds ticket.yaml %q, want the adoption pushed", got)
		}
		if got := run(t, fx.StoreRemote, "show", "main:tickets/"+ticket+"/start.fixture-repo.sha"); got != tip {
			t.Fatalf("the store's remote holds the start sha %q, want %s", got, tip)
		}
		if head := reviewed(t); head != tip {
			t.Fatalf("the round reviewed %s, want the branch's tip %s", head, tip)
		}
	})

	step(t, "round 2 names no branch, round 3 names the same one: both work on it", func(t *testing.T) {
		for _, opts := range []GateOpts{{Ticket: ticket}, {Ticket: ticket, Branch: branch}} {
			r, err := Gate(d, alwaysCleanSource{}, opts)
			if err != nil {
				t.Fatalf("Gate %+v: %v", opts, err)
			}
			if r.Branch != branch {
				t.Fatalf("Gate %+v reviewed %q, want the adopted %q", opts, r.Branch, branch)
			}
		}
		if got, err := d.Store.TicketBranch(ticket, "main"); err != nil || got != branch {
			t.Fatalf("TicketBranch = %q (err %v), want the adopted %q", got, err, branch)
		}
	})

	var pushed string
	step(t, "naming the recorded branch again is no adoption: the author's push is reviewed and the start sha stays", func(t *testing.T) {
		pushed = authorPush(t, fx, branch, "more.txt")
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
			t.Fatalf("Gate naming the recorded branch again: %v", err)
		}
		if head := reviewed(t); head != pushed {
			t.Fatalf("the round reviewed %s, want what the author pushed, %s", head, pushed)
		}
		wantStartSHA(t, tip)
	})

	step(t, "the author's next push is what is reviewed", func(t *testing.T) {
		// The author pushes again.
		advanced := authorPush(t, fx, branch, "advance.txt")
		if advanced == pushed {
			t.Fatal("test setup: origin did not advance")
		}

		// This round names no branch: the ticket recorded it in round 1.
		report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		if err != nil {
			t.Fatalf("Gate after the author pushed over a leftover: %v", err)
		}
		if report.Branch != branch {
			t.Fatalf("report on %q, want the adopted %q", report.Branch, branch)
		}
		if head := reviewed(t); head != advanced {
			t.Fatalf("the round reviewed %s, want the advanced origin/%s = %s", head, branch, advanced)
		}
		pushed = advanced
	})

	step(t, "a branch the author rewrote is reviewed as rewritten", func(t *testing.T) {
		// The author rewrites the branch: drops its last commit for another.
		dir := t.TempDir()
		run(t, dir, "clone", fx.RepoRemote, ".")
		run(t, dir, "checkout", branch)
		run(t, dir, "reset", "--hard", "HEAD~1")
		if err := os.WriteFile(filepath.Join(dir, "rewritten.txt"), []byte("rewritten\n"), 0o644); err != nil {
			t.Fatalf("write rewritten.txt: %v", err)
		}
		builder(t).Commit(dir, "author: rewritten")
		run(t, dir, "push", "--force", "origin", branch)
		rewritten := run(t, dir, "rev-parse", "HEAD")

		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket}); err != nil {
			t.Fatalf("Gate over a rewritten branch: %v", err)
		}
		if head := reviewed(t); head != rewritten {
			t.Fatalf("the round reviewed %s, want the rewritten branch %s", head, rewritten)
		}
		wantStartSHA(t, tip)
	})

	step(t, "a branch deleted on origin is refused BRANCH_NOT_FOUND", func(t *testing.T) {
		run(t, fx.RepoRemote, "branch", "-D", branch)

		err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "BRANCH_NOT_FOUND" {
			t.Fatalf("Gate after origin deleted the branch: err = %v, want *axi.Error BRANCH_NOT_FOUND", err)
		}
		if len(ae.Help) == 0 {
			t.Fatal("BRANCH_NOT_FOUND has no Help line")
		}
	})
}

// TestGateRefusesAnAdoptionThatCannotStand covers each refusal, and that
// each leaves nothing behind: no journal line (gateRefused), no adopted
// branch, no start sha, and - for the ones decided before the lease - no gate
// lease, so a plain rerun is the whole recovery.
//
// One fixture serves every case, each case a step on a ticket of its own, so
// what one journaled does not reach another's. The ticket the fixture built (the
// one build of the test) carries the cases that read a built journal, in an
// order in which each leaves it as the next one needs: the build and its lease,
// then the lease gone, then the verified lines gone, then every slice requeued,
// then the slices' green states with no journal line to say so.
func TestGateRefusesAnAdoptionThatCannotStand(t *testing.T) {
	t.Parallel()

	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	authorBranch(t, fx, "add-retry")
	authorBranch(t, fx, "add-backoff")

	for _, tc := range []struct {
		name string
		// setup returns the options that must be refused and the branch the
		// ticket is expected to have recorded afterwards.
		setup func(t *testing.T) (GateOpts, string)
		want  string
		// leaseless: the refusal comes before any gate lease is acquired.
		leaseless bool
	}{
		{
			name: "a different branch than the one recorded",
			setup: func(t *testing.T) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-2")
				if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: "JIG-2", Branch: "add-retry"}); err != nil {
					t.Fatalf("Gate --branch add-retry: %v", err)
				}
				return GateOpts{Ticket: "JIG-2", Branch: "add-backoff"}, "add-retry"
			},
			want: "BRANCH_MISMATCH",
		},
		{
			name: "the target branch",
			setup: func(t *testing.T) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-3")
				return GateOpts{Ticket: "JIG-3", Branch: "main"}, ""
			},
			want: "TICKET_BRANCH_INVALID",
		},
		{
			name: "a name git rejects",
			setup: func(t *testing.T) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-4")
				return GateOpts{Ticket: "JIG-4", Branch: "bad..name"}, ""
			},
			want: "TICKET_BRANCH_INVALID",
		},
		{
			name: "a ticket the journal says jig built on",
			setup: func(t *testing.T) (GateOpts, string) {
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want: "TICKET_ALREADY_BUILT",
		},
		{
			// One commit is enough to strand: the rule is any, not several,
			// and driveBuild's whole scenario would journal several.
			name: "a ticket the journal records exactly one built commit on",
			setup: func(t *testing.T) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-5")
				journalBuilt(t, d.Store, "JIG-5", "a", "1111111111111111111111111111111111111111", 1)
				return GateOpts{Ticket: "JIG-5", Branch: "add-retry"}, ""
			},
			want: "TICKET_ALREADY_BUILT",
		},
		{
			// A jig that never journaled verified lines (v0.1.x, whose tickets
			// can be carried across an upgrade) leaves green result lines and
			// green slices, and nothing else, of a build. Adopting a branch
			// would strand what it built as surely as for a journal with
			// verified lines, so the refusal rests on what such a journal does hold.
			name: "a ticket built by a jig that journaled no verified lines",
			setup: func(t *testing.T) (GateOpts, string) {
				dropVerifiedLines(t, d.Store, fx.Ticket)
				lines, err := journal.Read(d.Store, fx.Ticket)
				if err != nil {
					t.Fatalf("journal.Read: %v", err)
				}
				greens := 0
				for _, l := range lines {
					if l.Event == "result" && l.Outcome == "green" && l.Commit != "" {
						greens++
					}
				}
				if got := journal.BuiltCommits(lines); len(got) != 0 || greens == 0 {
					t.Fatalf("test setup: journal has %d verified commit(s) and %d green result(s), want none and some", len(got), greens)
				}
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want: "TICKET_ALREADY_BUILT",
		},
		{
			// The same journal after `jig requeue --from-brief-diff`, which
			// sets every slice whose brief section changed back to queued,
			// green ones included: no slice is green and no verified line
			// exists, so the green result lines naming commits are all that is
			// left of the build. Adopting would strand it just the same, and
			// `--early` is what lets the gate get as far as adoption over the
			// requeued slices.
			name: "a ticket built by a jig that journaled no verified lines, then requeued",
			setup: func(t *testing.T) (GateOpts, string) {
				if touched := amendBriefAndRequeue(t, d, fx.Ticket); len(touched) != 4 {
					t.Fatalf("test setup: the requeue touched %v, want all four slices", touched)
				}
				slices, err := d.Store.ReadSlices(fx.Ticket)
				if err != nil {
					t.Fatalf("ReadSlices: %v", err)
				}
				for _, sl := range slices {
					if st, err := d.Store.ReadSliceState(fx.Ticket, sl.ID); err != nil || st.State != "queued" {
						t.Fatalf("test setup: slice %s is %q (err %v), want queued", sl.ID, st.State, err)
					}
				}
				lines, err := journal.Read(d.Store, fx.Ticket)
				if err != nil {
					t.Fatalf("journal.Read: %v", err)
				}
				if built, claims := journal.BuiltCommits(lines), journal.GreenClaims(lines); len(built) != 0 || len(claims) == 0 {
					t.Fatalf("test setup: journal has %d verified commit(s) and %d green claim(s), want none and some", len(built), len(claims))
				}
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want: "TICKET_ALREADY_BUILT",
		},
		{
			// The claims are the only read that answers for a journal with no
			// verified line, and a claim is not proof: a ticket of this
			// version whose green claims all failed verification has no
			// verified line either. It is refused as well, loudly and
			// recoverably (mint a ticket for the branch), where adopting it
			// would be silent and permanent.
			name: "a ticket whose only green claim failed verification",
			setup: func(t *testing.T) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-6")
				if err := journal.Append(d.Store, "JIG-6", journal.Line{Slice: "a", Event: "result", Outcome: "green", Commit: "1111111111111111111111111111111111111111", Attempt: 1}); err != nil {
					t.Fatalf("journal the claim: %v", err)
				}
				return GateOpts{Ticket: "JIG-6", Branch: "add-retry"}, ""
			},
			want: "TICKET_ALREADY_BUILT",
		},
		{
			// A verified line is a fact of its own too: the frontier journals
			// it after the result line it verifies, so a journal never holds
			// one alone, but the line is what says a commit is jig's, and
			// nothing else the store keeps is needed to read it.
			name: "a ticket whose only trace of a build is a verified line",
			setup: func(t *testing.T) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-7")
				if err := journal.Append(d.Store, "JIG-7", journal.Line{Slice: "a", Event: "verified", Commit: "1111111111111111111111111111111111111111", Attempt: 1}); err != nil {
					t.Fatalf("journal the verified commit: %v", err)
				}
				return GateOpts{Ticket: "JIG-7", Branch: "add-retry"}, ""
			},
			want: "TICKET_ALREADY_BUILT",
		},
		{
			// A slice in state green is a fact of its own: the journal that
			// recorded it may be gone from the store, and the state is still
			// what says the slice's commit verified. The build's journal here
			// has lost its lines, and a slice is green again.
			name: "a ticket with a green slice and no journal line to say so",
			setup: func(t *testing.T) (GateOpts, string) {
				dropJournalLines(t, d.Store, fx.Ticket, "journal without its build lines", func(l journal.Line) bool {
					return l.Event == "verified" || l.Event == "result"
				})
				if err := d.Store.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
					t.Fatalf("write slice state a: %v", err)
				}
				if err := d.Store.Push(fx.Ticket + ": slice a green"); err != nil {
					t.Fatalf("push slice a green: %v", err)
				}
				lines, err := journal.Read(d.Store, fx.Ticket)
				if err != nil {
					t.Fatalf("journal.Read: %v", err)
				}
				if built, claims := journal.BuiltCommits(lines), journal.GreenClaims(lines); len(built) != 0 || len(claims) != 0 {
					t.Fatalf("test setup: journal has %d verified commit(s) and %d green claim(s), want none", len(built), len(claims))
				}
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want: "TICKET_ALREADY_BUILT",
		},
	} {
		step(t, tc.name, func(t *testing.T) {
			opts, recorded := tc.setup(t)
			opts.Early = true // JIG-1's slices are queued; not what this test is about
			startBefore, startErr := os.ReadFile(d.Store.StartSHAPath(opts.Ticket, "fixture-repo"))

			err := gateRefused(t, d, alwaysCleanSource{}, opts)
			var ae *axi.Error
			if !errors.As(err, &ae) || ae.Code != tc.want {
				t.Fatalf("Gate %+v: err = %v, want an *axi.Error %s", opts, err, tc.want)
			}
			if len(ae.Help) == 0 {
				t.Errorf("%s carries no help", tc.want)
			}

			rec, rerr := d.Store.ReadTicket(opts.Ticket)
			if rerr != nil || rec.Branch != recorded {
				t.Fatalf("record after the refusal = %+v (err %v), want branch %q", rec, rerr, recorded)
			}
			startAfter, afterErr := os.ReadFile(d.Store.StartSHAPath(opts.Ticket, "fixture-repo"))
			if (startErr == nil) != (afterErr == nil) || string(startBefore) != string(startAfter) {
				t.Fatalf("the start sha changed over a refusal: %q (%v) -> %q (%v)", startBefore, startErr, startAfter, afterErr)
			}
			if _, serr := os.Stat(filepath.Join(fx.Home, "pool")); !os.IsNotExist(serr) {
				t.Fatalf("the refusal left a pool directory behind (stat err %v)", serr)
			}
		})
	}
}

// TestGateIgnoresACommitJigReportedButNeverVerified: a builder's claim that did
// not verify - a green result naming a sha that is not a commit, or the start
// sha itself - is a result line and no more. It put nothing on the branch, so
// it is not a commit jig built (journal.BuiltCommits), and every arm of the
// round's choice of copy, with a build lease here or without, sees the same
// journal and reviews the same thing. The gate used to refuse the machine
// without a lease over it forever, and ignore it on the machine with one.
// Adoption is stricter, and says so (TestGateRefusesAnAdoptionThatCannotStand):
// a ticket with such a claim and no verified line cannot be told from one built
// by a jig that never journaled them, so it is not adopted. The two arms are
// steps of one ticket: the claim is journaled once, and the round with the branch
// in no clone comes first.
func TestGateIgnoresACommitJigReportedButNeverVerified(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	claim := journal.Line{Slice: "fix-1", Event: "result", Outcome: "green", Commit: "0123456789abcdef0123456789abcdef01234567", Attempt: 1}
	if err := journal.Append(d.Store, ticket, claim); err != nil {
		t.Fatalf("journal the claim: %v", err)
	}
	wantClean := func(t *testing.T) {
		t.Helper()
		r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		if err != nil || r.Verdict != "clean" {
			t.Fatalf("Gate = %q (err %v), want a clean round: the claim built nothing", r.Verdict, err)
		}
	}

	step(t, "with the branch in no clone", wantClean)
	step(t, "with the branch in the clone", func(t *testing.T) {
		run(t, fx.RepoDir, "fetch", "origin")
		run(t, fx.RepoDir, "checkout", "-b", branch, "origin/"+branch)
		wantClean(t)
	})
}

// TestGatePostPublishRoundOnAnOrdinaryTicket: once a publish has pushed the
// ticket's own jig/<ticket> to origin, the branch is judged as any branch on
// origin is (chooseBuiltCopy). The first publish pushes a squash of the commits
// the build lease holds, so the two stand diverged, and the lease holds
// commits jig built that origin lacks: a round over either copy would call a
// head clean that is not the branch's, so the round is refused with
// BRANCH_DIVERGED, naming the build lease to integrate in. The refusal is the
// judgment of the two copies, not the acquire's own over the gate lease's stale
// copy of the branch: the gate drops its own copy before acquiring, since it
// re-points the lease at the round's source, and the refusal names the build
// lease, not the gate's. Once origin's branch is merged into the build lease, as
// the refusal says, the lease is ahead of origin's and the round reviews it.
func TestGatePostPublishRoundOnAnOrdinaryTicket(t *testing.T) {
	t.Parallel()

	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}

	// What a publish leaves on origin: the ticket's branch, holding a squash
	// that the build lease's commits are not under.
	dir := t.TempDir()
	run(t, dir, "clone", fx.RepoRemote, ".")
	run(t, dir, "checkout", "-b", ticketBranch(fx.Ticket))
	if err := os.WriteFile(filepath.Join(dir, "squash.txt"), []byte("squash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	builder(t).Commit(dir, "squash")
	builder(t).Push(dir, "origin", ticketBranch(fx.Ticket))

	err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "BRANCH_DIVERGED")
	if !strings.Contains(strings.ReplaceAll(err.Error(), `\\`, `\`), "the clone at "+fx.RepoDir) {
		t.Fatalf("the refusal %q does not name the clone to integrate in", err)
	}

	buildDir := fx.RepoDir
	run(t, buildDir, "fetch", "origin")
	run(t, buildDir, "merge", "--no-edit", "origin/"+ticketBranch(fx.Ticket))
	r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2 after the ticket's branch reached origin and was integrated: %v", err)
	}
	if r.Round != 2 || r.Verdict != "clean" || r.Branch != "" {
		t.Fatalf("report = round %d %q on %q, want a clean round 2 on the ticket's own branch", r.Round, r.Verdict, r.Branch)
	}
	if head, want := reviewedHead(t, d, fx.Ticket), run(t, fx.RepoDir, "rev-parse", "HEAD"); head != want {
		t.Fatalf("the round reviewed %s, want the clone's integrated copy %s", head, want)
	}
}

// advanceTargetEditing moves origin/main on by appending a line to rel, a file
// that already exists at the fork point, the way advanceTarget does with a new
// file.
func advanceTargetEditing(t *testing.T, fx *fixture.Fixture, rel string) {
	t.Helper()
	clone := t.TempDir()
	if _, err := gitx.Run(filepath.Dir(clone), "clone", fx.RepoRemote, clone); err != nil {
		t.Fatalf("clone remote: %v", err)
	}
	path := filepath.Join(clone, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if err := os.WriteFile(path, append(data, []byte("// upstream moved on\n")...), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	builder(t).Commit(clone, "upstream: unrelated change")
	if _, err := gitx.Run(clone, "push", "origin", "main"); err != nil {
		t.Fatalf("push: %v", err)
	}
}
