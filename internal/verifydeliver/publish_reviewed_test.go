package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// reviewerBackend is a reviewer that finds nothing: every round it is
// dispatched for is clean, and records the head it reviewed.
func reviewerBackend(t *testing.T) session.Backend {
	t.Helper()
	return stubBackend{run: func(sd session.Dispatch) error {
		writeMustReviewResult(t, sd)
		return nil
	}}
}

// reviewedClean builds the fixture's slices and runs one reviewer round over
// the result, which is clean: unlike the scripted rounds gateToClean plays, a
// reviewer round records the head it reviewed (reviewed_sha), which is what
// publish holds the head it ships to.
func reviewedClean(t *testing.T, fx *fixture.Fixture, d Deps) GateReport {
	t.Helper()
	driveBuild(t, fx, "rung-a")
	return gateReviewedClean(t, fx, d)
}

// gateReviewedClean runs one reviewer round, which must be clean.
func gateReviewedClean(t *testing.T, fx *fixture.Fixture, d Deps) GateReport {
	t.Helper()
	report, err := Gate(d, NewReviewerGateSource(reviewerBackend(t)), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "clean" || report.ReviewedSHA["fixture-repo"] == "" {
		t.Fatalf("reviewer round: verdict %q, reviewed head %q, want a clean round that recorded the head it reviewed", report.Verdict, report.ReviewedSHA["fixture-repo"])
	}
	return report
}

// TestPublishShipsOnlyTheHeadTheGateReviewed: publish refuses to ship a head
// other than the one the last clean round reviewed. A commit lands on the
// ticket's branch after the round - here in the build lease, the way another
// `jig run` or a hand edit would put it there - and a publish would push work
// no reviewer saw under a clean verdict. It is refused with an axi code and
// help before anything is written: no journal line, no store commit, nothing
// on origin, and the stale-head failure is not swept into a "publish failed"
// store push, which only a publish that got as far as journaling makes. Once
// a round has reviewed the branch as it is, the same publish goes through.
func TestPublishShipsOnlyTheHeadTheGateReviewed(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	reviewed := reviewedClean(t, fx, d).ReviewedSHA["fixture-repo"]

	buildDir := buildLeaseDir(t, fx)
	if got := run(t, buildDir, "rev-parse", "HEAD"); got != reviewed {
		t.Fatalf("test setup: the round reviewed %s, but the build lease is at %s", reviewed, got)
	}
	late := writeAndCommit(t, buildDir, "late.txt", "written after the review", "a change after the review")
	journalBefore, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an *axi.Error", err)
	}
	if !strings.Contains(ae.Msg, late) || !strings.Contains(ae.Msg, reviewed) {
		t.Errorf("the refusal %q does not name both the head it would ship (%s) and the reviewed one (%s)", ae.Msg, late, reviewed)
	}
	if want := []string{"Run `jig gate " + fx.Ticket + "` to review the branch as it is now, then publish again."}; !slices.Equal(ae.Help, want) {
		t.Errorf("the refusal's help = %q, want %q", ae.Help, want)
	}

	if got := originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(fx.Ticket)); got != "" {
		t.Fatalf("origin has %s = %s after the refused publish, want nothing pushed", ticketBranch(fx.Ticket), got)
	}
	journalAfter, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	if len(journalAfter) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish", len(journalBefore), len(journalAfter))
	}
	if status := run(t, d.Store.Root, "status", "--porcelain"); status != "" {
		t.Fatalf("the store working copy is dirty after the refused publish:\n%s", status)
	}
	if got := run(t, d.Store.Root, "rev-parse", "HEAD"); got != storeHead {
		t.Fatalf("the store moved from %s to %s over a refused publish", storeHead, got)
	}

	// A round over the branch as it is now reviews the late commit, and the
	// publish is no longer stale.
	if again := gateReviewedClean(t, fx, d).ReviewedSHA["fixture-repo"]; again != late {
		t.Fatalf("the second round reviewed %s, want the late commit %s", again, late)
	}
	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish after a round over the branch as it is: %v", err)
	}
	if out := run(t, fx.RepoRemote, "ls-tree", "-r", "--name-only", ticketBranch(fx.Ticket)); !strings.Contains(out, "late.txt") {
		t.Fatalf("origin's %s lacks late.txt: publish did not ship the head the second round reviewed (%s)", ticketBranch(fx.Ticket), report.Head["fixture-repo"])
	}
}

// TestPublishOfAScriptedRoundChecksNoHead: a scripted round records no
// reviewed head, and publish keeps its behavior there: whatever the branch
// holds is shipped, as before. (TestPublishFullChain and the tests around it
// publish after scripted rounds; this one pins the reason.)
func TestPublishOfAScriptedRoundChecksNoHead(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	rep, err := latestGateReport(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("latestGateReport: %v", err)
	}
	if len(rep.ReviewedSHA) != 0 {
		t.Fatalf("test setup: a scripted round recorded reviewed_sha %v", rep.ReviewedSHA)
	}
	writeAndCommit(t, buildLeaseDir(t, fx), "late.txt", "written after the last round", "a change after the last round")

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish after a scripted round: %v", err)
	}
}

// TestPublishShipsTheReviewedHeadAfterTheTargetMoves: the head publish holds
// to the review is the one it would ship before reconcile, not the one it ends
// with. The target moves after a clean reviewer round, so reconcile rebases the
// ticket's branch onto it and the head publish pushes is not the head the round
// reviewed - and that is not a refusal: it is what publish does to every
// branch, the revalidation being what stands behind the moved target. Reading
// the head after reconcile would refuse every publish once the target moved.
func TestPublishShipsTheReviewedHeadAfterTheTargetMoves(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	reviewed := reviewedClean(t, fx, d).ReviewedSHA["fixture-repo"]
	advanceTarget(t, fx)
	moved := originRef(t, fx.RepoRemote, "refs/heads/main")

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish after a clean reviewer round and a moved target: %v", err)
	}
	if report.Tier != "oracles-only" {
		t.Errorf("revalidation tier = %q, want oracles-only: the target moved after the round", report.Tier)
	}
	branch := ticketBranch(fx.Ticket)
	head := originRef(t, fx.RepoRemote, "refs/heads/"+branch)
	if head == "" || head == reviewed {
		t.Fatalf("origin's %s = %q, want the head publish pushed, which is not the reviewed %s: reconcile put the branch on the moved target", branch, head, reviewed)
	}
	run(t, fx.RepoRemote, "merge-base", "--is-ancestor", moved, head)
}

// TestCheckReviewedHead: the rule on its own. A head equal to the recorded one
// passes; a different one is refused; a round that recorded no head at all - a
// scripted round - has nothing to hold the head to. A round that recorded
// heads, but none for this repo, is another thing: it reviewed something, and
// not this. A repo that changed name between the round and the publish must not
// ship a head no reviewer saw under a clean verdict with nothing said, so that
// is refused too.
func TestCheckReviewedHead(t *testing.T) {
	t.Parallel()

	recorded := reportYAML{ReviewedSHA: map[string]string{"api": "aaa"}}
	if err := checkReviewedHead(recorded, "api", "aaa", "T-1"); err != nil {
		t.Errorf("the reviewed head: %v", err)
	}
	wantAxiCode(t, checkReviewedHead(recorded, "api", "bbb", "T-1"), "PUBLISH_UNREVIEWED_HEAD")
	if err := checkReviewedHead(reportYAML{}, "api", "bbb", "T-1"); err != nil {
		t.Errorf("a round that recorded no head: %v", err)
	}
	if err := checkReviewedHead(reportYAML{ReviewedSHA: map[string]string{}}, "api", "bbb", "T-1"); err != nil {
		t.Errorf("a round that recorded an empty set of heads: %v", err)
	}

	err := checkReviewedHead(recorded, "web", "bbb", "T-1")
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an *axi.Error", err)
	}
	if !strings.Contains(ae.Msg, "web") || !strings.Contains(ae.Msg, "api") {
		t.Errorf("the refusal %q does not name the repo publish ships, web, and the one the round reviewed, api", ae.Msg)
	}
	if want := []string{"Run `jig gate T-1` to review the branch as it is now, then publish again."}; !slices.Equal(ae.Help, want) {
		t.Errorf("the refusal's help = %q, want %q", ae.Help, want)
	}
	// A repo recorded with an empty sha is no record of a head either.
	wantAxiCode(t, checkReviewedHead(reportYAML{ReviewedSHA: map[string]string{"api": ""}}, "api", "bbb", "T-1"), "PUBLISH_UNREVIEWED_HEAD")
}

// TestCheckReviewedIntent: the rule on its own, for both binding sources.
// Bytes unchanged since the sha256 was recorded pass; bytes edited since are
// refused. An inferred or absent intent has nothing pinned to hold the
// bytes to - renderIntentSection never reads either into a pull request -
// so neither source is even read: the ticket dir here has no intent.md at
// all, and both still pass.
func TestCheckReviewedIntent(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	const ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}

	briefPath := filepath.Join(st.TicketDir(ticket), "brief.md")
	if err := os.WriteFile(briefPath, []byte("# T-1\n\n## Intent\n\nWhy this matters.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	briefData, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	briefRep := reportYAML{Intent: reportIntentYAML{Source: IntentSourceBrief, SHA256: intentSHA256(IntentSourceBrief, string(briefData))}}
	if err := checkReviewedIntent(st, ticket, briefRep); err != nil {
		t.Errorf("unedited brief.md: %v", err)
	}
	if err := os.WriteFile(briefPath, []byte("# T-1\n\n## Intent\n\nEdited after the round.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = checkReviewedIntent(st, ticket, briefRep)
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_INTENT")
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an *axi.Error", err)
	}
	if !strings.Contains(ae.Msg, briefPath) {
		t.Errorf("the refusal %q does not name the file that changed, %s", ae.Msg, briefPath)
	}
	if want := []string{"Run `jig gate " + ticket + "` to review the intent as it is now, then publish again."}; !slices.Equal(ae.Help, want) {
		t.Errorf("the refusal's help = %q, want %q", ae.Help, want)
	}

	if err := st.WriteIntent(ticket, store.Intent{Source: IntentSourceExplicit, Text: "an explicit statement of intent"}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}
	intentData, err := os.ReadFile(st.IntentPath(ticket))
	if err != nil {
		t.Fatal(err)
	}
	explicitRep := reportYAML{Intent: reportIntentYAML{Source: IntentSourceExplicit, SHA256: intentSHA256(IntentSourceExplicit, string(intentData))}}
	if err := checkReviewedIntent(st, ticket, explicitRep); err != nil {
		t.Errorf("unedited intent.md: %v", err)
	}
	if err := st.WriteIntent(ticket, store.Intent{Source: IntentSourceExplicit, Text: "a different statement, typed after the round"}); err != nil {
		t.Fatalf("WriteIntent (edit): %v", err)
	}
	wantAxiCode(t, checkReviewedIntent(st, ticket, explicitRep), "PUBLISH_UNREVIEWED_INTENT")

	for _, source := range []string{IntentSourceInferred, IntentSourceNone} {
		if err := checkReviewedIntent(st, ticket, reportYAML{Intent: reportIntentYAML{Source: source}}); err != nil {
			t.Errorf("source %s has nothing pinned: %v", source, err)
		}
	}
}

// TestPublishRefusesAnIntentEditedSinceTheReviewedRound: a brief.md edited
// after the last clean round reviewed it is refused before anything is
// written, the same way a head the round never saw is (checkReviewedHead):
// no journal line, no store commit, nothing on origin. A round over the
// branch with the brief as it now reads clears the refusal.
func TestPublishRefusesAnIntentEditedSinceTheReviewedRound(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	round := reviewedClean(t, fx, d)
	if round.Intent.Source != IntentSourceBrief || round.IntentSHA256 == "" {
		t.Fatalf("test setup: round intent = %+v, want a clean round that recorded a brief intent sha256", round)
	}

	briefPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")
	original, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatalf("read brief.md: %v", err)
	}
	if err := os.WriteFile(briefPath, append(original, []byte("\nEdited after the clean round.\n")...), 0o644); err != nil {
		t.Fatalf("edit brief.md: %v", err)
	}
	// Committed before Publish runs, the way an edit to any other store
	// file is: otherwise Publish's own unconditional Sync at the top would
	// commit this edit itself, and the "nothing moved" assertions below
	// would be measuring that commit, not what Publish wrote once refused.
	if err := d.Store.Push(fx.Ticket + ": edit brief.md after the clean round"); err != nil {
		t.Fatalf("push the edited brief.md: %v", err)
	}

	journalBefore, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_INTENT")
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an *axi.Error", err)
	}
	if !strings.Contains(ae.Msg, briefPath) {
		t.Errorf("the refusal %q does not name %s", ae.Msg, briefPath)
	}

	if got := originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(fx.Ticket)); got != "" {
		t.Fatalf("origin has %s = %s after the refused publish, want nothing pushed", ticketBranch(fx.Ticket), got)
	}
	journalAfter, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	if len(journalAfter) != len(journalBefore) {
		t.Fatalf("the journal grew from %d to %d lines over a refused publish", len(journalBefore), len(journalAfter))
	}
	if got := run(t, d.Store.Root, "rev-parse", "HEAD"); got != storeHead {
		t.Fatalf("the store moved from %s to %s over a refused publish", storeHead, got)
	}

	// A round over the branch with the brief as it now reads records the
	// edited brief's own sha256, and the publish is no longer stale.
	if again := gateReviewedClean(t, fx, d); again.IntentSHA256 == round.IntentSHA256 {
		t.Fatalf("the second round recorded the same intent sha256 as the first, want the edited brief's")
	}
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish after a round over the brief as it is: %v", err)
	}
}
