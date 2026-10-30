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
	"github.com/develdeco/jig/internal/pool"
)

// whenPublishFetchesOrigin runs move just before publish's fetch of origin, the
// one made after its lease is pointed at the copy it ships: a push that lands
// after the lease compared that copy with origin's, which is the one push no
// state of the leases reaches.
func whenPublishFetchesOrigin(d *Deps, move func()) {
	orig := d.FetchOrigin
	d.FetchOrigin = func(dir string) error {
		move()
		if orig != nil {
			return orig(dir)
		}
		// Use package-level default if not set
		_, err := gitx.Run(dir, "fetch", "origin")
		return err
	}
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
// The target has moved too, with a change to the place the branch changed, so
// that publish doing anything with the moved target could only conflict: the
// refusal is still the fast-forward one, and the conflict never hides the real
// problem, that someone else pushed to the branch. The commit counts the
// refusal gives (origin's one the copy lacks, and the copy's own that origin
// lacks) are pinned on their own by TestRequireFastForward.
//
// The second step is the same state met earlier: once someone else's commit is
// on origin, a publish that finds it before it points its lease at a copy is
// refused as the build and the gate are, with BRANCH_DIVERGED naming the build
// lease to integrate in, before its first store write.
func TestPublishRefusesAPushThatIsNotAFastForward(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)
	// wantNothingWritten checks what a refusal before the first store write
	// leaves: the other commit on origin untouched (publish must not force), the
	// journal as it was, and the store clean and where it was.
	wantNothingWritten := func(t *testing.T, theirs string, journalBefore int, storeHead string) {
		t.Helper()
		if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != theirs {
			t.Fatalf("origin's %s = %s after the refused publish, want the other commit %s untouched: publish must not force", branch, got, theirs)
		}
		if got := journalLen(t, d, fx.Ticket); got != journalBefore {
			t.Fatalf("the journal grew from %d to %d lines over a refused publish: the refusal came after the first store write", journalBefore, got)
		}
		if status := run(t, d.Store.Root, "status", "--porcelain"); status != "" {
			t.Fatalf("the store working copy is dirty after the refused publish:\n%s", status)
		}
		if got := run(t, d.Store.Root, "rev-parse", "HEAD"); got != storeHead {
			t.Fatalf("the store moved from %s to %s over a refused publish", storeHead, got)
		}
	}

	var theirs string
	step(t, "a branch someone else pushed to after the lease was pointed is refused", func(t *testing.T) {
		// The branch changes Clamp's last lines (slice a); main moves on at the same
		// place, so merging main into the branch cannot succeed.
		oldMain := originRef(t, fx.RepoRemote, "refs/heads/main")
		advanceTargetReplacing(t, fx, "alpha/alpha.go", "\treturn v\n}", "\treturn v + 0\n}")
		mainBefore := originRef(t, fx.RepoRemote, "refs/heads/main")

		// Someone else's commit on origin's copy of the ticket's branch, cut from
		// the main the branch was.
		dd := d
		whenPublishFetchesOrigin(&dd, func() {
			foreign := t.TempDir()
			run(t, foreign, "clone", fx.RepoRemote, ".")
			run(t, foreign, "checkout", "-b", branch, oldMain)
			writeAndCommit(t, foreign, "theirs.txt", "not in the build lease", "someone else's commit")
			run(t, foreign, "push", "origin", branch)
			theirs = originRef(t, fx.RepoRemote, "refs/heads/"+branch)
		})
		journalBefore := journalLen(t, d, fx.Ticket)
		storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

		_, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: true})
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
		if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
			t.Fatalf("origin's main = %s, want %s unchanged", got, mainBefore)
		}
		wantNothingWritten(t, theirs, journalBefore, storeHead)
	})

	step(t, "a branch someone else pushed to before the publish is refused as diverged", func(t *testing.T) {
		journalBefore := journalLen(t, d, fx.Ticket)
		storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		wantAxiCode(t, err, "BRANCH_DIVERGED")
		if !strings.Contains(err.Error(), "the build lease at "+buildLeaseOf(t, fx, fx.Ticket)) {
			t.Errorf("the refusal %q does not name the build lease to integrate in", err)
		}
		wantNothingWritten(t, theirs, journalBefore, storeHead)
	})
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
	// The message counts the commits each side lacks: origin's one the copy lacks,
	// and none of the copy's that origin lacks.
	if !strings.Contains(fromBuild.Msg, "1 commit(s) the branch lacks") || !strings.Contains(fromBuild.Msg, "0 commit(s) origin lacks") {
		t.Errorf("the refusal %q does not count origin's one commit the copy lacks and none of the copy's", fromBuild.Msg)
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
