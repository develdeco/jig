package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
)

// whenPublishFetchesOrigin runs move just before publish's fetch of origin, the
// one made after its lease is pointed at the copy it ships: a push that lands
// after the lease compared that copy with origin's, which is the one push no
// state of the leases reaches. The test must stay serial: it replaces the
// fetch.
func whenPublishFetchesOrigin(t *testing.T, move func()) {
	t.Helper()
	orig := fetchOrigin
	fetchOrigin = func(dir string) error {
		move()
		return orig(dir)
	}
	t.Cleanup(func() { fetchOrigin = orig })
}

// advanceTargetReplacing moves origin/main on by replacing from with to in
// rel, a file that already exists at the fork point, so the change can collide
// with one a branch made at the same place.
func advanceTargetReplacing(t *testing.T, fx *fixture.Fixture, rel, from, to string) {
	t.Helper()
	clone := cloneFrom(t, fx.RepoRemote)
	path := filepath.Join(clone, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("%s does not contain %q", rel, from)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), from, to, 1)), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	run(t, clone, "add", "-A")
	run(t, clone, "commit", "-m", "upstream: a change in the same place")
	run(t, clone, "push", "origin", "main")
}

// TestPublishRefusesAPushThatIsNotAFastForward: publish never rewrites what is
// on origin, and never forces. When the ticket's branch is on origin and the
// copy publish would ship is not a descendant of it - here someone else pushes
// a commit to jig/<ticket> after the lease was pointed at the copy, which the
// build lease lacks - the push would be refused by git after publish had
// journaled, written its changelogs and ledger, and made its commits. Publish
// refuses it up front instead, with the commit counts on both sides and where
// to integrate them, before its first store write: nothing in the journal, no
// store commit, nothing on origin.
//
// This test must stay serial: it replaces publish's fetch of origin.
func TestPublishRefusesAPushThatIsNotAFastForward(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)

	// Someone else's commit on origin's jig/<ticket>, cut from main.
	var theirs string
	whenPublishFetchesOrigin(t, func() {
		foreign := t.TempDir()
		run(t, foreign, "clone", fx.RepoRemote, ".")
		run(t, foreign, "checkout", "-b", branch)
		writeAndCommit(t, foreign, "theirs.txt", "not in the build lease", "someone else's commit")
		run(t, foreign, "push", "origin", branch)
		theirs = originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	})
	mainBefore := originRef(t, fx.RepoRemote, "refs/heads/main")

	journalBefore, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_NOT_FAST_FORWARD")
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an *axi.Error", err)
	}
	buildDir, perr := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Build)
	if perr != nil {
		t.Fatal(perr)
	}
	if !strings.Contains(ae.Msg, branch) || !strings.Contains(ae.Msg, "1 commit(s) the branch lacks") {
		t.Errorf("the refusal %q does not name the branch and origin's one commit the copy lacks", ae.Msg)
	}
	help := strings.Join(ae.Help, "\n")
	if !strings.Contains(help, "git -C "+buildDir+" fetch origin && git -C "+buildDir+" merge origin/"+branch) || !strings.Contains(help, "never forces") {
		t.Errorf("the refusal's help = %q, want the merge in the build lease at %s and that publish never forces", help, buildDir)
	}

	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != theirs {
		t.Fatalf("origin's %s = %s after the refused publish, want the other commit %s untouched: publish must not force", branch, got, theirs)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
		t.Fatalf("origin's main = %s, want %s unchanged", got, mainBefore)
	}
	journalAfter, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	if len(journalAfter) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish: the refusal came after the first store write", len(journalBefore), len(journalAfter))
	}
	if status := run(t, d.Store.Root, "status", "--porcelain"); status != "" {
		t.Fatalf("the store working copy is dirty after the refused publish:\n%s", status)
	}
	if got := run(t, d.Store.Root, "rev-parse", "HEAD"); got != storeHead {
		t.Fatalf("the store moved from %s to %s over a refused publish", storeHead, got)
	}
}

// TestPublishRefusesABranchOriginIsAheadOf: the other way a push is not a
// fast-forward is origin's copy being ahead of the one publish would ship
// (here the build lease's copy, pushed, is an ancestor of the commit someone
// else pushes on top of it after the lease was pointed at it). It is refused
// the same way, and the message counts nothing on the copy's side.
//
// This test must stay serial: it replaces publish's fetch of origin.
func TestPublishRefusesABranchOriginIsAheadOf(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)

	// The build lease's branch pushed, then origin's copy moved past it.
	buildDir := buildLeaseDir(t, fx)
	run(t, buildDir, "push", "origin", branch)
	var moved string
	whenPublishFetchesOrigin(t, func() {
		ahead := t.TempDir()
		run(t, ahead, "clone", fx.RepoRemote, ".")
		run(t, ahead, "checkout", branch)
		writeAndCommit(t, ahead, "more.txt", "pushed after", "a commit pushed to the ticket's branch")
		run(t, ahead, "push", "origin", branch)
		moved = originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	})

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_NOT_FAST_FORWARD")
	var ae *axi.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Msg, "0 commit(s) origin lacks") || !strings.Contains(ae.Msg, "1 commit(s) the branch lacks") {
		t.Fatalf("err = %v, want the refusal to count origin's one commit and none of the copy's", err)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != moved {
		t.Fatalf("origin's %s = %s, want %s unchanged", branch, got, moved)
	}
}

// TestPublishChecksTheFastForwardBeforeItReconciles: the check that the push is
// a fast-forward comes before reconcile. Reconcile would merge the target into
// a branch that cannot be pushed, and here the target moved with a change to the
// place the branch changed: the merge conflicts, and the conflict would hide the
// real problem, that someone else pushed to the branch, behind CONFLICT. The
// refusal is the fast-forward one, and nothing was written.
//
// This test must stay serial: it replaces publish's fetch of origin.
func TestPublishChecksTheFastForwardBeforeItReconciles(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)

	// The branch changes Clamp's last lines (slice a); main moves on at the same
	// place, so merging main into the branch cannot succeed.
	advanceTargetReplacing(t, fx, "alpha/alpha.go", "\treturn v\n}", "\treturn v + 0\n}")
	whenPublishFetchesOrigin(t, func() {
		foreign := t.TempDir()
		run(t, foreign, "clone", fx.RepoRemote, ".")
		run(t, foreign, "checkout", "-b", branch)
		writeAndCommit(t, foreign, "theirs.txt", "not in the build lease", "someone else's commit")
		run(t, foreign, "push", "origin", branch)
	})
	journalBefore, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_NOT_FAST_FORWARD")
	if journalAfter, _ := journal.Read(d.Store, fx.Ticket); len(journalAfter) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish", len(journalBefore), len(journalAfter))
	}
}

// TestPublishRefusesADivergedTicketBranchBeforeWriting: a push that would not be
// a fast-forward is caught before the push wherever it can be. Here someone
// else pushed a commit to jig/<ticket> that the build lease lacks, before the
// publish: the build lease's copy holds commits jig built that origin lacks, and
// origin's holds one the lease lacks, so neither is the branch and the publish
// is refused as the build and the gate are, with BRANCH_DIVERGED naming the
// build lease to integrate in, before its first store write: nothing in the
// journal, no store commit, nothing on origin.
func TestPublishRefusesADivergedTicketBranchBeforeWriting(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)

	foreign := t.TempDir()
	run(t, foreign, "clone", fx.RepoRemote, ".")
	run(t, foreign, "checkout", "-b", branch)
	writeAndCommit(t, foreign, "theirs.txt", "not in the build lease", "someone else's commit")
	run(t, foreign, "push", "origin", branch)
	theirs := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	journalBefore, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "BRANCH_DIVERGED")
	if !strings.Contains(err.Error(), "the build lease at "+buildLeaseOf(t, fx, fx.Ticket)) {
		t.Errorf("the refusal %q does not name the build lease to integrate in", err)
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != theirs {
		t.Fatalf("origin's %s = %s after the refused publish, want the other commit %s untouched: publish must not force", branch, got, theirs)
	}
	journalAfter, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	if len(journalAfter) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish: the refusal came after the first store write", len(journalBefore), len(journalAfter))
	}
	if status := run(t, d.Store.Root, "status", "--porcelain"); status != "" {
		t.Fatalf("the store working copy is dirty after the refused publish:\n%s", status)
	}
	if got := run(t, d.Store.Root, "rev-parse", "HEAD"); got != storeHead {
		t.Fatalf("the store moved from %s to %s over a refused publish", storeHead, got)
	}
}

// TestPublishOfAnOrdinaryTicketHoldsTheHeadToTheReview: the ticket's own
// jig/<ticket> was pushed as it is, and a commit was pushed on top of it after
// the clean round. The build lease is behind origin's copy, which holds
// everything the lease does, so origin's is the copy publish ships, exactly as
// the gate would review it: the commit no round saw is not shipped
// (PUBLISH_UNREVIEWED_HEAD), where shipping the build lease's copy would have
// passed the head check and met a push that is not a fast-forward. Once a round
// has reviewed the branch as it is, the same publish is a fast-forward.
func TestPublishOfAnOrdinaryTicketHoldsTheHeadToTheReview(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	reviewedClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)
	run(t, buildLeaseOf(t, fx, fx.Ticket), "push", "origin", branch)
	other := cloneFrom(t, fx.RepoRemote)
	run(t, other, "checkout", branch)
	pushed := writeAndCommit(t, other, "more.txt", "pushed after the round", "a commit pushed to the ticket's branch")
	run(t, other, "push", "origin", branch)

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != pushed {
		t.Fatalf("origin's %s = %s after the refused publish, want %s untouched", branch, got, pushed)
	}

	if got := gateReviewedClean(t, fx, d).ReviewedSHA["fixture-repo"]; got != pushed {
		t.Fatalf("Gate reviewed %s, want origin's copy %s", got, pushed)
	}
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish after a round over the branch as it is: %v", err)
	}
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", pushed, originRef(t, fx.RepoRemote, "refs/heads/"+branch))
}

// TestRequireFastForward: the rule on its own, over a branch the lease holds
// and origin's copy of it, on the four ways they can stand.
func TestRequireFastForward(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "feature")
	writeAndCommit(t, clone, "f1.txt", "one", "one")
	run(t, clone, "push", "origin", "feature")
	run(t, clone, "fetch", "origin")

	// Equal, and ahead: a fast-forward.
	if err := requireFastForward(clone, "feature", "/build", "T-1", buildLeasesCopy); err != nil {
		t.Errorf("a copy equal to origin's: %v", err)
	}
	writeAndCommit(t, clone, "f1.txt", "two", "two")
	if err := requireFastForward(clone, "feature", "/build", "T-1", buildLeasesCopy); err != nil {
		t.Errorf("a copy ahead of origin's: %v", err)
	}

	// Behind: origin's copy has a commit the local one lacks.
	run(t, clone, "reset", "--hard", "HEAD~1")
	other := cloneFrom(t, remote)
	run(t, other, "checkout", "feature")
	writeAndCommit(t, other, "g.txt", "theirs", "theirs")
	run(t, other, "push", "origin", "feature")
	run(t, clone, "fetch", "origin")
	wantAxiCode(t, requireFastForward(clone, "feature", "/build", "T-1", buildLeasesCopy), "PUBLISH_NOT_FAST_FORWARD")

	// What the refusal says to do depends on whose copy publish would ship. The
	// build lease's is where jig's commits wait, so that is where origin's is
	// merged in. Origin's own copy has nothing to merge into anywhere: origin
	// moved since the round (a push between publish's two fetches), and the way
	// on is a round over the branch as it is.
	var fromBuild, fromOrigin *axi.Error
	if !errors.As(requireFastForward(clone, "feature", "/build", "T-1", buildLeasesCopy), &fromBuild) ||
		!errors.As(requireFastForward(clone, "feature", "/build", "T-1", originsCopy), &fromOrigin) {
		t.Fatal("a copy behind origin's was not refused with an *axi.Error")
	}
	if help := strings.Join(fromBuild.Help, "\n"); !strings.Contains(help, "git -C /build fetch origin && git -C /build merge origin/feature") || !strings.Contains(help, "jig gate") {
		t.Errorf("help for the build lease's copy = %q, want the merge in the build lease, then the gate", help)
	}
	if help := strings.Join(fromOrigin.Help, "\n"); !strings.Contains(help, "run `jig gate T-1`") || strings.Contains(help, "/build") || !strings.Contains(help, "never forces") {
		t.Errorf("help for origin's own copy = %q, want a round over the branch as it is, no build lease named, and that publish never forces", help)
	}

	// Diverged: each has a commit the other lacks.
	writeAndCommit(t, clone, "h.txt", "mine", "mine")
	wantAxiCode(t, requireFastForward(clone, "feature", "/build", "T-1", buildLeasesCopy), "PUBLISH_NOT_FAST_FORWARD")

	// A branch origin does not have has nothing to fast-forward.
	run(t, clone, "checkout", "-b", "never-pushed")
	if err := requireFastForward(clone, "never-pushed", "/build", "T-1", buildLeasesCopy); err != nil {
		t.Errorf("a branch that is not on origin: %v", err)
	}
}
