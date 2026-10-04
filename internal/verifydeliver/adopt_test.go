package verifydeliver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/session"
)

// gateLeaseOf is the gate lease directory of ticket in fx's pool.
func gateLeaseOf(t *testing.T, fx *fixture.Fixture, ticket string) string {
	t.Helper()
	dir, err := pool.Dir(fx.Home, "fixture-repo", ticket, pool.Gate)
	if err != nil {
		t.Fatalf("resolve the gate lease: %v", err)
	}
	return dir
}

// jigBuildsOn plays a build session on branch for ticket on this machine: see
// jigBuildsOnAt.
func jigBuildsOn(t *testing.T, d Deps, fx *fixture.Fixture, ticket, branch string) string {
	t.Helper()
	return jigBuildsOnAt(t, d, fx, fx.Home, ticket, branch)
}

// jigBuildsOnAt plays a build session on branch for ticket in the pool under
// jigHome: the build lease is acquired (so it holds branch, on whatever
// Acquire's sync makes of it), one commit is made there, and the journal
// records it the way frontier does. It returns the commit. Nothing is pushed:
// jig's commits stay in the build lease until someone pushes them. A jigHome
// other than the fixture's is another machine's.
func jigBuildsOnAt(t *testing.T, d Deps, fx *fixture.Fixture, jigHome, ticket, branch string) string {
	t.Helper()
	lease, err := pool.Acquire(jigHome, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build)
	if err != nil {
		t.Fatalf("acquire the build lease: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lease.Dir, "fix.txt"), []byte("jig's fix\n"), 0o644); err != nil {
		t.Fatalf("write fix.txt: %v", err)
	}
	run(t, lease.Dir, "add", "-A")
	if _, err := gitx.RunEnv(lease.Dir, buildGitEnv, "commit", "-m", ticket+" fix-1: jig's fix"); err != nil {
		t.Fatalf("commit jig's fix: %v", err)
	}
	commit := run(t, lease.Dir, "rev-parse", "HEAD")
	journalBuilt(t, d.Store, ticket, "fix-1", commit, 1)
	return commit
}

// buildLeaseOf is the build lease directory of ticket on this machine.
func buildLeaseOf(t *testing.T, fx *fixture.Fixture, ticket string) string {
	t.Helper()
	dir, err := pool.Dir(fx.Home, "fixture-repo", ticket, pool.Build)
	if err != nil {
		t.Fatalf("resolve the build lease: %v", err)
	}
	return dir
}

// TestGateAdoptsTheBranchItReviews is the adoption itself, on a ticket minted
// with a record and nothing else - no brief, no slices - as `jig ticket new`
// leaves it: the first `jig gate --branch X` records X in the ticket's record,
// keeping its title, and records the ticket's start sha as X's tip. Main has
// moved past X's fork point by then, so the tip is not the target's: what jig
// builds on X descends from the tip, and a start sha off main would fail
// verifyGreen for every fix. A start sha an earlier dispatch left is
// replaced. Both writes reach the store's remote, and every later round -
// with the flag or without it - works on the recorded branch.
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
	if got := run(t, fx.StoreRemote, "show", "main:"+ticket+"/ticket.yaml"); !strings.Contains(got, "branch: "+branch) {
		t.Fatalf("the store's remote holds ticket.yaml %q, want the adoption pushed", got)
	}
	if got := run(t, fx.StoreRemote, "show", "main:"+ticket+"/start.fixture-repo.sha"); got != tip {
		t.Fatalf("the store's remote holds the start sha %q, want %s", got, tip)
	}
	if head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); head != tip {
		t.Fatalf("the round reviewed %s, want the branch's tip %s", head, tip)
	}

	// Round 2 names no branch, round 3 names the same one: both work on it.
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

	// Naming the recorded branch again is no adoption: the author pushes, the
	// flag is repeated, the round reviews what was pushed, and the start sha
	// stays where the adoption put it - it is the frontier that follows the
	// author, until jig builds, and not a gate round.
	pushed := authorPush(t, fx, branch, "more.txt")
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate naming the recorded branch again: %v", err)
	}
	if head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); head != pushed {
		t.Fatalf("the round reviewed %s, want what the author pushed, %s", head, pushed)
	}
	if start, err := os.ReadFile(d.Store.StartSHAPath(ticket, "fixture-repo")); err != nil || strings.TrimSpace(string(start)) != tip {
		t.Fatalf("start sha = %q (err %v) after the flag was repeated, want it kept at the adoption's %s", start, err, tip)
	}
}

// TestGateRefusesAnAdoptionThatCannotStand covers each refusal, and that
// each leaves nothing behind: no journal line (gateRefused), no adopted
// branch, no start sha, and - for the ones decided before the lease - no gate
// lease, so a plain rerun is the whole recovery.
func TestGateRefusesAnAdoptionThatCannotStand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// setup returns the ticket, the options that must be refused, and
		// the branch the ticket is expected to have recorded afterwards.
		setup func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string)
		want  string
		// leaseless: the refusal comes before any gate lease is acquired.
		leaseless bool
	}{
		{
			name: "a different branch than the one recorded",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-2")
				authorBranch(t, fx, "add-retry")
				authorBranch(t, fx, "add-backoff")
				if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: "JIG-2", Branch: "add-retry"}); err != nil {
					t.Fatalf("Gate --branch add-retry: %v", err)
				}
				return GateOpts{Ticket: "JIG-2", Branch: "add-backoff"}, "add-retry"
			},
			want: "BRANCH_MISMATCH",
		},
		{
			name: "the target branch",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-2")
				return GateOpts{Ticket: "JIG-2", Branch: "main"}, ""
			},
			want:      "TICKET_BRANCH_INVALID",
			leaseless: true,
		},
		{
			name: "a name git rejects",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				newAdoptTicket(t, d, "JIG-2")
				return GateOpts{Ticket: "JIG-2", Branch: "bad..name"}, ""
			},
			want:      "TICKET_BRANCH_INVALID",
			leaseless: true,
		},
		{
			name: "a ticket the journal says jig built on, with its build lease",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				driveBuild(t, fx, "rung-a")
				authorBranch(t, fx, "add-retry")
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
		},
		{
			// One commit is enough to strand: the rule is any, not several,
			// and driveBuild's whole scenario would journal several.
			name: "a ticket the journal records exactly one built commit on",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				authorBranch(t, fx, "add-retry")
				journalBuilt(t, d.Store, fx.Ticket, "a", "1111111111111111111111111111111111111111", 1)
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
		},
		{
			// A jig that never journaled verified lines (v0.1.x, whose tickets
			// can be carried across an upgrade) leaves green result lines and
			// green slices, and nothing else, of a build. Adopting a branch
			// would strand what it built as surely as for a journal with
			// verified lines, so the refusal rests on what such a journal does hold.
			name: "a ticket built by a jig that journaled no verified lines",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				driveBuild(t, fx, "rung-a")
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
				authorBranch(t, fx, "add-retry")
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
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
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				driveBuild(t, fx, "rung-a")
				dropVerifiedLines(t, d.Store, fx.Ticket)
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
				authorBranch(t, fx, "add-retry")
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
		},
		{
			// The claims are the only read that answers for a journal with no
			// verified line, and a claim is not proof: a ticket of this
			// version whose green claims all failed verification has no
			// verified line either. It is refused as well, loudly and
			// recoverably (mint a ticket for the branch), where adopting it
			// would be silent and permanent.
			name: "a ticket whose only green claim failed verification",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				authorBranch(t, fx, "add-retry")
				if err := journal.Append(d.Store, fx.Ticket, journal.Line{Slice: "a", Event: "result", Outcome: "green", Commit: "1111111111111111111111111111111111111111", Attempt: 1}); err != nil {
					t.Fatalf("journal the claim: %v", err)
				}
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
		},
		{
			// A slice in state green is a fact of its own: the journal that
			// recorded it may be gone from the store, and the state is still
			// what says the slice's commit verified.
			name: "a ticket with a green slice and no journal line to say so",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				driveBuild(t, fx, "rung-a")
				dropJournalLines(t, d.Store, fx.Ticket, "journal without its build lines", func(l journal.Line) bool {
					return l.Event == "verified" || l.Event == "result"
				})
				lines, err := journal.Read(d.Store, fx.Ticket)
				if err != nil {
					t.Fatalf("journal.Read: %v", err)
				}
				if built, claims := journal.BuiltCommits(lines), journal.GreenClaims(lines); len(built) != 0 || len(claims) != 0 {
					t.Fatalf("test setup: journal has %d verified commit(s) and %d green claim(s), want none", len(built), len(claims))
				}
				authorBranch(t, fx, "add-retry")
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
		},
		{
			// A verified line is a fact of its own too: the frontier journals
			// it after the result line it verifies, so a journal never holds
			// one alone, but the line is what says a commit is jig's, and
			// nothing else the store keeps is needed to read it.
			name: "a ticket whose only trace of a build is a verified line",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				authorBranch(t, fx, "add-retry")
				if err := journal.Append(d.Store, fx.Ticket, journal.Line{Slice: "a", Event: "verified", Commit: "1111111111111111111111111111111111111111", Attempt: 1}); err != nil {
					t.Fatalf("journal the verified commit: %v", err)
				}
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
		},
		{
			// The store is the judge, never this machine's lease: a lease
			// shows what one machine has, and the ticket may have been built
			// on another.
			name: "a ticket the journal says jig built on, with no build lease here",
			setup: func(t *testing.T, fx *fixture.Fixture, d Deps) (GateOpts, string) {
				driveBuild(t, fx, "rung-a")
				authorBranch(t, fx, "add-retry")
				if err := os.RemoveAll(buildLeaseDir(t, fx)); err != nil {
					t.Fatalf("remove the build lease: %v", err)
				}
				return GateOpts{Ticket: fx.Ticket, Branch: "add-retry"}, ""
			},
			want:      "TICKET_ALREADY_BUILT",
			leaseless: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			opts, recorded := tc.setup(t, fx, d)
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
			if tc.leaseless {
				if _, serr := os.Stat(gateLeaseOf(t, fx, opts.Ticket)); !os.IsNotExist(serr) {
					t.Fatalf("the refusal left a gate lease behind (stat err %v)", serr)
				}
			}
		})
	}
}

// TestGateFollowsTheAuthorOnAnAdoptedBranch: until jig has built on an
// adopted branch it is the author's, so every round reviews origin's copy as
// it is now - the author pushing between rounds is reviewed, and so is a
// branch the author rewrote.
func TestGateFollowsTheAuthorOnAnAdoptedBranch(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	lease := gateLeaseOf(t, fx, ticket)

	pushed := authorPush(t, fx, branch, "more.txt")
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket}); err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if head := run(t, lease, "rev-parse", "HEAD"); head != pushed {
		t.Fatalf("round 2 reviewed %s, want what the author pushed, %s", head, pushed)
	}

	// The author rewrites the branch: drops its last commit for another.
	dir := t.TempDir()
	run(t, dir, "clone", fx.RepoRemote, ".")
	run(t, dir, "checkout", branch)
	run(t, dir, "reset", "--hard", "HEAD~1")
	if err := os.WriteFile(filepath.Join(dir, "rewritten.txt"), []byte("rewritten\n"), 0o644); err != nil {
		t.Fatalf("write rewritten.txt: %v", err)
	}
	run(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", "author: rewritten"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	run(t, dir, "push", "--force", "origin", branch)
	rewritten := run(t, dir, "rev-parse", "HEAD")

	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket}); err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if head := run(t, lease, "rev-parse", "HEAD"); head != rewritten {
		t.Fatalf("round 3 reviewed %s, want the rewritten branch %s", head, rewritten)
	}
}

// TestGateReviewsTheBuildLeaseCopyOnceJigBuilt: jig's commits on an adopted
// branch stay in the build lease until someone pushes them, so while origin
// lacks them the round reviews the build lease's copy, which holds them. The
// author pushing after that leaves the two diverged: neither copy holds both
// jig's commits and the author's, so the round refuses with BRANCH_DIVERGED, as
// the next build does, instead of saying "clean" over a head that is not the
// branch's. The refusal leaves everything as it was: no journal line, the
// build lease and origin untouched, and its help - fetch, then merge, in the
// build lease - is a way forward: the round then reviews the merged lease.
func TestGateReviewsTheBuildLeaseCopyOnceJigBuilt(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	tip := authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}

	built := jigBuildsOn(t, d, fx, ticket, branch)
	if built == tip {
		t.Fatal("test setup: jig's commit did not move the branch")
	}
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket}); err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); head != built {
		t.Fatalf("round 2 reviewed %s, want the build lease's copy with jig's commit, %s", head, built)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != tip {
		t.Fatalf("origin's %s = %s, want the author's %s untouched: gating pushes nothing", branch, got, tip)
	}

	// The author pushes while jig's commit is still unpushed.
	pushed := authorPush(t, fx, branch, "more.txt")
	err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BRANCH_DIVERGED" {
		t.Fatalf("Gate over a diverged branch: err = %v, want an *axi.Error BRANCH_DIVERGED", err)
	}
	buildLease := buildLeaseOf(t, fx, ticket)
	for _, want := range []string{"the build lease at " + buildLease, "1 commit(s) origin/" + branch + " lacks", "has 1 the lease lacks"} {
		if !strings.Contains(ae.Msg, want) {
			t.Errorf("BRANCH_DIVERGED message %q does not name %q", ae.Msg, want)
		}
	}
	// The round compared the two copies in its own lease and never fetched
	// into the build lease, whose view of origin still predates the author's
	// push: the help fetches before it merges, or its merge would integrate
	// nothing.
	if _, err := gitx.Run(buildLease, "cat-file", "-e", pushed+"^{commit}"); err == nil {
		t.Fatal("test setup: the build lease already has the author's commit")
	}
	if help, want := strings.Join(ae.Help, "\n"), "git -C "+buildLease+" fetch origin && git -C "+buildLease+" merge origin/"+branch; !strings.Contains(help, want) {
		t.Errorf("BRANCH_DIVERGED help %q does not give %q", help, want)
	}
	if got := run(t, buildLease, "rev-parse", "HEAD"); got != built {
		t.Fatalf("the build lease is at %s after the refusal, want jig's %s untouched", got, built)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != pushed {
		t.Fatalf("origin's %s = %s, want the author's %s untouched", branch, got, pushed)
	}
	// The build would refuse the same way.
	_, err = pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build)
	wantAxiCode(t, err, "BRANCH_DIVERGED")

	// Following the help integrates them in the build lease, which is then
	// ahead of origin: the round reviews the merged lease's copy.
	run(t, buildLease, "fetch", "origin")
	if _, err := gitx.RunEnv(buildLease, buildGitEnv, "merge", "--no-edit", "origin/"+branch); err != nil {
		t.Fatalf("merge origin/%s in the build lease: %v", branch, err)
	}
	merged := run(t, buildLease, "rev-parse", "HEAD")
	r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
	if err != nil {
		t.Fatalf("Gate after integrating: %v", err)
	}
	if r.Verdict != "clean" {
		t.Fatalf("verdict = %q after integrating, want clean", r.Verdict)
	}
	if head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); head != merged {
		t.Fatalf("the round reviewed %s, want the merged lease's copy, %s", head, merged)
	}
	if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build); err != nil {
		t.Fatalf("build Acquire after integrating: %v", err)
	}
}

// TestGateReviewsOriginOnceItHoldsWhatJigBuilt: once jig's commits are pushed
// - by a publish, or by the human opening the pull request by hand -
// origin holds everything the build lease does, so it is origin's copy the
// round reviews, with whatever the author added on top since; the build lease's
// older copy is not the branch's head, and a clean round over it would say
// nothing of the author's newer commit. The same when this machine has no
// build lease at all: the refusal for a missing lease is only for commits
// origin lacks.
func TestGateReviewsOriginOnceItHoldsWhatJigBuilt(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	for _, tc := range []struct {
		name string
		// push is what happens after jig built, and returns the head the
		// next round must review.
		push func(t *testing.T, d Deps, fx *fixture.Fixture, built string) string
	}{
		{"the human pushed jig's commits", func(t *testing.T, d Deps, fx *fixture.Fixture, built string) string {
			run(t, buildLeaseOf(t, fx, ticket), "push", "origin", branch)
			return built
		}},
		{"and the author pushed on top", func(t *testing.T, d Deps, fx *fixture.Fixture, built string) string {
			run(t, buildLeaseOf(t, fx, ticket), "push", "origin", branch)
			return authorPush(t, fx, branch, "more.txt")
		}},
		{"and this machine has no build lease", func(t *testing.T, d Deps, fx *fixture.Fixture, built string) string {
			leaseDir := buildLeaseOf(t, fx, ticket)
			run(t, leaseDir, "push", "origin", branch)
			if err := os.RemoveAll(leaseDir); err != nil {
				t.Fatalf("remove the build lease: %v", err)
			}
			return built
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			newAdoptTicket(t, d, ticket)
			authorBranch(t, fx, branch)
			if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
				t.Fatalf("Gate round 1: %v", err)
			}
			built := jigBuildsOn(t, d, fx, ticket, branch)
			want := tc.push(t, d, fx, built)

			r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
			if err != nil {
				t.Fatalf("Gate over a branch origin holds jig's commits of: %v", err)
			}
			if r.Verdict != "clean" || r.Branch != branch {
				t.Fatalf("report = %q on %q, want a clean round on %q", r.Verdict, r.Branch, branch)
			}
			head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD")
			if head != want {
				t.Fatalf("the round reviewed %s, want origin's head %s (jig built %s)", head, want, built)
			}
			if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != want {
				t.Fatalf("origin's %s = %s after the round, want %s untouched: gating pushes nothing", branch, got, want)
			}
		})
	}
}

// TestGateRefusesAnAdoptedBranchDeletedOnOriginOnceJigBuilt: an adopted branch
// is on origin by definition, whether or not jig has built on it. When it is
// gone the round refuses with BRANCH_NOT_FOUND instead of reviewing the build
// lease's copy of a branch nobody can open a pull request from.
func TestGateRefusesAnAdoptedBranchDeletedOnOriginOnceJigBuilt(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	newAdoptTicket(t, d, ticket)
	authorBranch(t, fx, branch)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	jigBuildsOn(t, d, fx, ticket, branch)
	run(t, fx.RepoRemote, "branch", "-D", branch)

	err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
	wantAxiCode(t, err, "BRANCH_NOT_FOUND")
}

// TestGateRefusesWhenJigBuiltOnAnAdoptedBranchAndThisMachineHasNoCopy: the
// journal records commits jig built on the adopted branch, but this machine's
// build lease does not hold them - it is missing, or holds another branch - and
// origin lacks them too: they are on another machine, unpushed. The round
// refuses instead of silently reviewing less than the ticket built.
func TestGateRefusesWhenJigBuiltOnAnAdoptedBranchAndThisMachineHasNoCopy(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	for _, tc := range []struct {
		name string
		// setup runs after another machine's build, whose lease is otherLease.
		setup func(t *testing.T, fx *fixture.Fixture, otherLease string)
	}{
		{"no build lease", func(t *testing.T, fx *fixture.Fixture, otherLease string) {}},
		{"a build lease on another branch", func(t *testing.T, fx *fixture.Fixture, otherLease string) {
			if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", ticketBranch(ticket), ticket, pool.Build); err != nil {
				t.Fatalf("acquire the build lease: %v", err)
			}
		}},
		{"origin holds the commit on another branch only", func(t *testing.T, fx *fixture.Fixture, otherLease string) {
			// The commit is in this machine's repository once it fetches, but
			// the adopted branch does not hold it.
			run(t, otherLease, "push", "origin", "HEAD:refs/heads/somewhere-else")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d := newDeps(t, fx)
			newAdoptTicket(t, d, ticket)
			authorBranch(t, fx, branch)
			if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
				t.Fatalf("Gate round 1: %v", err)
			}
			// Another machine's build, still unpushed there: the journal has
			// the commit, this machine has nothing, origin's branch has nothing.
			other := t.TempDir()
			elsewhere := jigBuildsOnAt(t, d, fx, other, ticket, branch)
			otherLease, err := pool.Dir(other, "fixture-repo", ticket, pool.Build)
			if err != nil {
				t.Fatal(err)
			}
			tc.setup(t, fx, otherLease)

			err = gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
			var ae *axi.Error
			if !errors.As(err, &ae) || ae.Code != "BUILD_LEASE_MISSING" {
				t.Fatalf("Gate: err = %v, want an *axi.Error BUILD_LEASE_MISSING", err)
			}
			if len(ae.Help) == 0 || !strings.Contains(ae.Msg, branch) {
				t.Fatalf("BUILD_LEASE_MISSING = %+v, want it to name the branch and carry help", ae)
			}
			if _, err := gitx.Run(fx.RepoRemote, "merge-base", "--is-ancestor", elsewhere, "refs/heads/"+branch); err == nil {
				t.Fatalf("test setup: origin's %s holds the commit %s, so nothing is missing", branch, elsewhere)
			}
		})
	}
}

// TestGateRefusesABuildLeaseThatLacksWhatJigBuilt: the journal records a commit
// jig built on the adopted branch on another machine, and it is still in that
// machine's build lease. A build lease on this machine - cut afterwards, as a
// `jig run` whose attempt parked does - holds the branch as origin has it, so it
// lacks the commit too, and so may one that has built commits of its own on top:
// a round over either says clean over less than the ticket built. The round is
// refused (BUILD_LEASE_MISSING) whatever the lease is like, as it is when this
// machine has none, and the way forward the help gives - push the commit from
// the machine that built it - works.
func TestGateRefusesABuildLeaseThatLacksWhatJigBuilt(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	setup := func(t *testing.T) (fx *fixture.Fixture, d Deps, elsewhere, otherLease string) {
		t.Helper()
		fx = fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d = newDeps(t, fx)
		newAdoptTicket(t, d, ticket)
		authorBranch(t, fx, branch)
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
			t.Fatalf("Gate round 1: %v", err)
		}
		other := t.TempDir()
		elsewhere = jigBuildsOnAt(t, d, fx, other, ticket, branch)
		otherLease, err := pool.Dir(other, "fixture-repo", ticket, pool.Build)
		if err != nil {
			t.Fatal(err)
		}
		return fx, d, elsewhere, otherLease
	}
	wantMissing := func(t *testing.T, err error, elsewhere string) {
		t.Helper()
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "BUILD_LEASE_MISSING" {
			t.Fatalf("Gate: err = %v, want an *axi.Error BUILD_LEASE_MISSING", err)
		}
		if len(ae.Help) == 0 || !strings.Contains(ae.Msg, branch) || !strings.Contains(ae.Msg, elsewhere[:7]) {
			t.Fatalf("BUILD_LEASE_MISSING = %+v, want it to name the branch and the missing commit %s, and carry help", ae, elsewhere[:7])
		}
	}

	t.Run("a build lease cut afterwards", func(t *testing.T) {
		t.Parallel()
		fx, d, elsewhere, otherLease := setup(t)
		// What `jig run` does before it dispatches: acquire the build lease,
		// cut from origin's copy, which lacks the other machine's commit.
		if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build, pool.MustExistOnOrigin()); err != nil {
			t.Fatalf("acquire the build lease: %v", err)
		}
		wantMissing(t, gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: ticket}), elsewhere)

		// The help: push it from the machine that built it. This machine's
		// lease then follows origin, which holds the commit.
		run(t, otherLease, "push", "origin", branch)
		r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		if err != nil || r.Verdict != "clean" {
			t.Fatalf("Gate after the commit was pushed = %q (err %v), want a clean round", r.Verdict, err)
		}
		if head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); head != elsewhere {
			t.Fatalf("the round reviewed %s, want origin's head with the other machine's commit, %s", head, elsewhere)
		}
	})

	t.Run("and built its own commit on it", func(t *testing.T) {
		t.Parallel()
		fx, d, elsewhere, _ := setup(t)
		lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build, pool.MustExistOnOrigin())
		if err != nil {
			t.Fatalf("acquire the build lease: %v", err)
		}
		if err := os.WriteFile(filepath.Join(lease.Dir, "fix2.txt"), []byte("this machine's fix\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, lease.Dir, "add", "-A")
		if _, err := gitx.RunEnv(lease.Dir, buildGitEnv, "commit", "-m", ticket+" fix-2"); err != nil {
			t.Fatalf("commit: %v", err)
		}
		own := run(t, lease.Dir, "rev-parse", "HEAD")
		journalBuilt(t, d.Store, ticket, "fix-2", own, 1)

		err = gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		wantMissing(t, err, elsewhere)
		var ae *axi.Error
		if errors.As(err, &ae) && strings.Contains(ae.Msg, own[:7]) {
			t.Errorf("BUILD_LEASE_MISSING %q names this machine's own commit %s as missing", ae.Msg, own[:7])
		}
	})
}

// commitLeftover leaves what a build attempt that never verified leaves in a
// lease: one commit no journal line records. It returns the commit.
func commitLeftover(t *testing.T, dir, ticket string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "leftover.txt"), []byte("an attempt's leftover\n"), 0o644); err != nil {
		t.Fatalf("write leftover.txt: %v", err)
	}
	run(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", ticket+" attempt: never verified"); err != nil {
		t.Fatalf("commit the leftover: %v", err)
	}
	return run(t, dir, "rev-parse", "HEAD")
}

// TestGateAndBuildJudgeADivergedLeaseAlike: whether a diverged build lease is
// jig's to keep is one rule for the build's re-cut and the gate's choice of
// copy: it is when it holds a commit jig built that origin lacks
// (pool.HoldsUnpushedBuilt), and not when it holds only an attempt's unverified
// leftover, whatever else it holds. The build re-cuts such a lease from origin,
// and the gate counts it as a lease that does not hold the branch: the round
// reviews origin's copy, instead of refusing BRANCH_DIVERGED and sending the
// human to merge the leftover into the branch. Two states give it: jig's
// verified commit was built on another machine and pushed, and the lease holds
// only the leftover; or jig's commit was built here and pushed, and the
// leftover sits on top of it. The requirement that origin holds what jig built
// then applies as it does to any copy: without the push, the round is refused
// for the missing commit, not for the divergence.
func TestGateAndBuildJudgeADivergedLeaseAlike(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	// setup adopts the branch, leaves an attempt's unverified commit in this
	// machine's build lease, and has another machine build and verify jig's
	// commit. It returns the fixture, the leftover, jig's commit and the other
	// machine's build lease.
	setup := func(t *testing.T) (fx *fixture.Fixture, d Deps, leftover, built, otherLease string) {
		t.Helper()
		fx = fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d = newDeps(t, fx)
		newAdoptTicket(t, d, ticket)
		authorBranch(t, fx, branch)
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
			t.Fatalf("Gate round 1: %v", err)
		}
		lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build, pool.MustExistOnOrigin())
		if err != nil {
			t.Fatalf("acquire the build lease: %v", err)
		}
		leftover = commitLeftover(t, lease.Dir, ticket)
		other := t.TempDir()
		built = jigBuildsOnAt(t, d, fx, other, ticket, branch)
		otherLease, err = pool.Dir(other, "fixture-repo", ticket, pool.Build)
		if err != nil {
			t.Fatal(err)
		}
		return fx, d, leftover, built, otherLease
	}

	t.Run("jig's commit was pushed", func(t *testing.T) {
		t.Parallel()
		fx, d, leftover, built, otherLease := setup(t)
		run(t, otherLease, "push", "origin", branch)

		r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		if err != nil || r.Verdict != "clean" {
			t.Fatalf("Gate = %q (err %v), want a clean round over origin's copy: the lease holds none of jig's commits", r.Verdict, err)
		}
		if head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); head != built {
			t.Fatalf("the round reviewed %s, want origin's head with jig's commit, %s", head, built)
		}
		buildLease := buildLeaseOf(t, fx, ticket)
		if got := run(t, buildLease, "rev-parse", "HEAD"); got != leftover {
			t.Fatalf("the build lease is at %s after the round, want the leftover %s untouched: gating changes no lease of the build", got, leftover)
		}

		// The build, on the same state, follows origin the same way.
		lines, err := journal.Read(d.Store, ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build, pool.MustExistOnOrigin(), pool.RecutUnlessBuilt(journal.BuiltCommits(lines))); err != nil {
			t.Fatalf("build Acquire on the state the gate reviewed: %v", err)
		}
		if got := run(t, buildLease, "rev-parse", "HEAD"); got != built {
			t.Fatalf("the build lease is at %s, want origin's head with jig's commit, %s", got, built)
		}
	})

	t.Run("jig's commit was not pushed", func(t *testing.T) {
		t.Parallel()
		fx, d, _, built, _ := setup(t)
		authorPush(t, fx, branch, "more.txt") // the lease and origin diverge

		err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "BUILD_LEASE_MISSING" {
			t.Fatalf("Gate: err = %v, want an *axi.Error BUILD_LEASE_MISSING: origin lacks the commit jig built", err)
		}
		if !strings.Contains(ae.Msg, built[:7]) {
			t.Errorf("BUILD_LEASE_MISSING %q does not name the missing commit %s", ae.Msg, built[:7])
		}
	})

	// jig's commit was built here and pushed, an attempt then left an
	// unverified commit on top of it in the lease, and the author pushed on.
	// The lease has diverged, but the only commit jig built is on origin too,
	// so nothing the lease holds needs keeping, and refusing would name work
	// that is safe on origin.
	t.Run("jig's commit was pushed and a leftover sits on top of it", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d := newDeps(t, fx)
		newAdoptTicket(t, d, ticket)
		authorBranch(t, fx, branch)
		if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket, Branch: branch}); err != nil {
			t.Fatalf("Gate round 1: %v", err)
		}
		built := jigBuildsOn(t, d, fx, ticket, branch)
		buildLease := buildLeaseOf(t, fx, ticket)
		run(t, buildLease, "push", "origin", branch)
		leftover := commitLeftover(t, buildLease, ticket)
		pushed := authorPush(t, fx, branch, "more.txt") // on top of jig's commit

		r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
		if err != nil || r.Verdict != "clean" {
			t.Fatalf("Gate = %q (err %v), want a clean round over origin's copy: every commit jig built is there", r.Verdict, err)
		}
		if head := run(t, gateLeaseOf(t, fx, ticket), "rev-parse", "HEAD"); head != pushed {
			t.Fatalf("the round reviewed %s, want origin's head %s, which holds jig's commit %s and the author's", head, pushed, built)
		}
		if got := run(t, buildLease, "rev-parse", "HEAD"); got != leftover {
			t.Fatalf("the build lease is at %s after the round, want the leftover %s untouched: gating changes no lease of the build", got, leftover)
		}

		lines, err := journal.Read(d.Store, ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build, pool.MustExistOnOrigin(), pool.RecutUnlessBuilt(journal.BuiltCommits(lines))); err != nil {
			t.Fatalf("build Acquire on the state the gate reviewed: %v", err)
		}
		if got := run(t, buildLease, "rev-parse", "HEAD"); got != pushed {
			t.Fatalf("the build lease is at %s, want origin's head %s: the leftover is not jig's to keep", got, pushed)
		}
	})
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
// by a jig that never journaled them, so it is not adopted.
func TestGateIgnoresACommitJigReportedButNeverVerified(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	for _, tc := range []struct {
		name string
		// lease says whether this machine has a build lease on the branch.
		lease bool
	}{{"with no build lease here", false}, {"with a build lease here", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
			if tc.lease {
				if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, ticket, pool.Build, pool.MustExistOnOrigin()); err != nil {
					t.Fatalf("acquire the build lease: %v", err)
				}
			}
			r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: ticket})
			if err != nil || r.Verdict != "clean" {
				t.Fatalf("Gate = %q (err %v), want a clean round: the claim built nothing", r.Verdict, err)
			}
		})
	}
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

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	driveBuild(t, fx, "rung-a")
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
	run(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", "squash"); err != nil {
		t.Fatalf("commit the squash: %v", err)
	}
	run(t, dir, "push", "origin", ticketBranch(fx.Ticket))

	err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "BRANCH_DIVERGED")
	if !strings.Contains(err.Error(), "the build lease at "+buildLeaseOf(t, fx, fx.Ticket)) {
		t.Fatalf("the refusal %q does not name the build lease to integrate in", err)
	}

	buildDir := buildLeaseOf(t, fx, fx.Ticket)
	run(t, buildDir, "fetch", "origin")
	run(t, buildDir, "merge", "--no-edit", "origin/"+ticketBranch(fx.Ticket))
	r, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2 after the ticket's branch reached origin and was integrated: %v", err)
	}
	if r.Round != 2 || r.Verdict != "clean" || r.Branch != "" {
		t.Fatalf("report = round %d %q on %q, want a clean round 2 on the ticket's own branch", r.Round, r.Verdict, r.Branch)
	}
	if head, want := run(t, gateLeaseOf(t, fx, fx.Ticket), "rev-parse", "HEAD"), run(t, buildLeaseOf(t, fx, fx.Ticket), "rev-parse", "HEAD"); head != want {
		t.Fatalf("the round reviewed %s, want the build lease's integrated copy %s", head, want)
	}
}

// TestGateIntentRefusalLeavesNoAdoption: an adoption is recorded after
// --intent/--doc has been written and the intent resolved, so a refusal by
// either leaves no adopted branch behind. Recorded first, the refused round
// would leave the ticket on the branch, and a later --branch naming another one
// would be refused over an adoption that never happened.
func TestGateIntentRefusalLeavesNoAdoption(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()}) // brief.md present by default
	d := newDeps(t, fx)
	authorBranch(t, fx, "add-retry")

	err := gateRefused(t, d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: "add-retry", Early: true, Intent: "explicit text"})
	wantAxiCode(t, err, "INTENT_CONFLICT")
	wantNoAdoption(t, d, fx.Ticket)

	// A later round may adopt any other branch.
	other := pushedBranch(t, fx)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: other, Early: true}); err != nil {
		t.Fatalf("Gate --branch %s after the refusal: %v", other, err)
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
	if _, err := gitx.Run(clone, "add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := gitx.RunEnv(clone, buildGitEnv, "commit", "-m", "upstream: unrelated change"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := gitx.Run(clone, "push", "origin", "main"); err != nil {
		t.Fatalf("push: %v", err)
	}
}

// TestGateInfersAnAdoptedTicketsIntent: an adopted ticket's intent follows the
// precedence every ticket's does - brief, intent.md, inferred, none - and
// inference needs nothing special for it. A branch built outside jig is the
// case inference exists for: the scope diff is the adopted branch against its
// merge base with the target (not the target's tip: main has moved on since the
// fork), and the session that matches is the author's own. The round records
// the adoption and the inferred intent.md, and later rounds read it.
func TestGateInfersAnAdoptedTicketsIntent(t *testing.T) {
	t.Parallel()

	const ticket, branch = "JIG-2", "add-retry"
	tmpHome := t.TempDir()
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "inferred-intent", Home: t.TempDir()})
	d := newDeps(t, fx)
	d.UserHome = tmpHome
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	newAdoptTicket(t, d, ticket)
	authorBranch(t, fx, branch) // touches alpha/alpha.go only
	// main moves on by editing a file that exists at the fork point, so a scope
	// diff taken against main's tip instead of the merge base would show it.
	advanceTargetEditing(t, fx, "beta/beta.go")
	writeSyntheticClaudeSession(t, tmpHome, "session-author", fx.RepoDir,
		[]string{"alpha/alpha.go"}, time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC))

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	var (
		req        IntentInferRequest
		summarized int
	)
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		if sd.Slice == "intent" {
			summarized++
			data, rerr := os.ReadFile(sd.SliceJSON)
			if rerr != nil {
				t.Errorf("read the summarizer's request: %v", rerr)
			} else if jerr := json.Unmarshal(data, &req); jerr != nil {
				t.Errorf("parse the summarizer's request: %v", jerr)
			}
		}
		return backend.Run(sd)
	}}

	report, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: ticket, Branch: branch})
	if err != nil {
		t.Fatalf("Gate --branch on a brief-less ticket: %v", err)
	}
	if report.Branch != branch {
		t.Fatalf("report.Branch = %q, want the adopted %q", report.Branch, branch)
	}
	if report.Intent.Source != IntentSourceInferred || report.IntentNote != "" {
		t.Fatalf("intent = %q (note %q), want %q: the author's own session matches the adopted branch's diff", report.Intent.Source, report.IntentNote, IntentSourceInferred)
	}
	if want := []string{"alpha/alpha.go"}; !slices.Equal(req.DiffFiles, want) {
		t.Fatalf("the summarizer was given diff files %v, want %v: the adopted branch against its merge base, without main's own change", req.DiffFiles, want)
	}
	in, ok, err := d.Store.ReadIntent(ticket)
	if err != nil || !ok {
		t.Fatalf("ReadIntent: ok=%v err=%v", ok, err)
	}
	if in.Source != IntentSourceInferred || in.Session != "session-author" || strings.TrimSpace(in.Text) == "" {
		t.Fatalf("intent.md = %+v, want the summary inferred from session-author", in)
	}
	if got, err := d.Store.TicketBranch(ticket, "main"); err != nil || got != branch {
		t.Fatalf("TicketBranch = %q (err %v), want the adopted %q", got, err, branch)
	}

	// A later round finds intent.md and infers nothing again.
	again, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if again.Intent.Source != IntentSourceInferred || summarized != 1 {
		t.Fatalf("round 2 intent = %q after %d summarizer dispatch(es), want the recorded inferred intent read, not inferred again", again.Intent.Source, summarized)
	}
}
