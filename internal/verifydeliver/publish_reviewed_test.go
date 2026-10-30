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
	report, err := Gate(d, newReviewerGateSource(reviewerBackend(t)), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict != "clean" || report.ReviewedSHA["fixture-repo"] == "" {
		t.Fatalf("reviewer round: verdict %q, reviewed head %q, want a clean round that recorded the head it reviewed", report.Verdict, report.ReviewedSHA["fixture-repo"])
	}
	return report
}

// TestPublishShipsOnlyTheHeadTheGateReviewed is the chain of the reviewed-head
// rule: publish refuses to ship anything the last clean round did not review,
// on a ticket built and reviewed once. A refusal comes with an axi code and help
// before anything is written: no journal line, no store commit, nothing on
// origin, and the stale-head failure is not swept into a "publish failed" store
// push, which only a publish that got as far as journaling makes. Once a round
// has reviewed the branch as it is, the same publish goes through. The steps
// run in order, each on what the one before left:
//
//   - a brief.md edited after the last clean round reviewed it is refused the
//     same way a head the round never saw is (checkReviewedHead), the intent
//     being what the round was handed (checkReviewedIntent). A round over the
//     branch with the brief as it now reads records the edited brief's own
//     sha256, and the publish is no longer stale;
//   - a commit lands on the ticket's branch after the round - here in the build
//     lease, the way another `jig run` or a hand edit would put it there - and a
//     publish would push work no reviewer saw under a clean verdict;
//   - the ticket's own jig/<ticket> was pushed as it is (by that publish), and a
//     commit was pushed on top of it after the clean round. The build lease is
//     behind origin's copy, which holds everything the lease does, so origin's is
//     the copy publish ships, exactly as the gate would review it: the commit no
//     round saw is not shipped (PUBLISH_UNREVIEWED_HEAD), where shipping the
//     build lease's copy would have passed the head check and met a push that is
//     not a fast-forward;
//   - the head publish holds to the review is the head it ships: the target
//     moves after a clean reviewer round, and publish merges nothing, so the
//     branch on origin stays exactly the reviewed head - the move of the target
//     is no refusal and no reason to rewrite anything (publish revalidates
//     nothing, so the tier is none).
func TestPublishShipsOnlyTheHeadTheGateReviewed(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	round := reviewedClean(t, fx, d)
	reviewed := round.ReviewedSHA["fixture-repo"]
	branch := ticketBranch(fx.Ticket)
	buildDir := buildLeaseDir(t, fx)
	if got := run(t, buildDir, "rev-parse", "HEAD"); got != reviewed {
		t.Fatalf("test setup: the round reviewed %s, but the build lease is at %s", reviewed, got)
	}
	// wantRefusedUntouched publishes, requires the refusal's code and that
	// nothing was written, and returns the refusal.
	wantRefusedUntouched := func(t *testing.T, code string) *axi.Error {
		t.Helper()
		journalBefore := journalLen(t, d, fx.Ticket)
		storeHead := run(t, d.Store.Root, "rev-parse", "HEAD")
		originBefore := originRef(t, fx.RepoRemote, "refs/heads/"+branch)

		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		wantAxiCode(t, err, code)
		var ae *axi.Error
		if !errors.As(err, &ae) {
			t.Fatalf("err = %v, want an *axi.Error", err)
		}
		if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != originBefore {
			t.Fatalf("origin has %s = %s after the refused publish, want %q untouched", branch, got, originBefore)
		}
		if got := journalLen(t, d, fx.Ticket); got != journalBefore {
			t.Fatalf("the journal grew from %d to %d lines over a refused publish", journalBefore, got)
		}
		if status := run(t, d.Store.Root, "status", "--porcelain"); status != "" {
			t.Fatalf("the store working copy is dirty after the refused publish:\n%s", status)
		}
		if got := run(t, d.Store.Root, "rev-parse", "HEAD"); got != storeHead {
			t.Fatalf("the store moved from %s to %s over a refused publish", storeHead, got)
		}
		return ae
	}

	step(t, "an intent edited since the reviewed round is refused", func(t *testing.T) {
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
		// commit this edit itself, and the "nothing moved" assertions would be
		// measuring that commit, not what Publish wrote once refused.
		if err := d.Store.Push(fx.Ticket + ": edit brief.md after the clean round"); err != nil {
			t.Fatalf("push the edited brief.md: %v", err)
		}

		ae := wantRefusedUntouched(t, "PUBLISH_UNREVIEWED_INTENT")
		if !strings.Contains(ae.Msg, briefPath) {
			t.Errorf("the refusal %q does not name %s", ae.Msg, briefPath)
		}

		// A round over the branch with the brief as it now reads records the
		// edited brief's own sha256, and the publish is no longer stale.
		if again := gateReviewedClean(t, fx, d); again.IntentSHA256 == round.IntentSHA256 {
			t.Fatalf("the second round recorded the same intent sha256 as the first, want the edited brief's")
		}
	})

	step(t, "a commit after the review is refused until a round has reviewed it", func(t *testing.T) {
		late := writeAndCommit(t, buildDir, "late.txt", "written after the review", "a change after the review")

		ae := wantRefusedUntouched(t, "PUBLISH_UNREVIEWED_HEAD")
		if !strings.Contains(ae.Msg, late) || !strings.Contains(ae.Msg, reviewed) {
			t.Errorf("the refusal %q does not name both the head it would ship (%s) and the reviewed one (%s)", ae.Msg, late, reviewed)
		}
		if want := []string{"Run `jig gate " + fx.Ticket + "` to review the branch as it is now, then publish again."}; !slices.Equal(ae.Help, want) {
			t.Errorf("the refusal's help = %q, want %q", ae.Help, want)
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
		if out := run(t, fx.RepoRemote, "ls-tree", "-r", "--name-only", branch); !strings.Contains(out, "late.txt") {
			t.Fatalf("origin's %s lacks late.txt: publish did not ship the head the second round reviewed (%s)", branch, report.Head["fixture-repo"])
		}
		reviewed = late
	})

	var pushed string
	step(t, "a commit pushed on top of the branch after the round is refused until a round has reviewed it", func(t *testing.T) {
		other := cloneFrom(t, fx.RepoRemote)
		run(t, other, "checkout", branch)
		pushed = writeAndCommit(t, other, "more.txt", "pushed after the round", "a commit pushed to the ticket's branch")
		run(t, other, "push", "origin", branch)

		wantRefusedUntouched(t, "PUBLISH_UNREVIEWED_HEAD")
		if got := gateReviewedClean(t, fx, d).ReviewedSHA["fixture-repo"]; got != pushed {
			t.Fatalf("Gate reviewed %s, want origin's copy %s", got, pushed)
		}
		reviewed = pushed
	})

	step(t, "the reviewed head is shipped as it is after the target moves", func(t *testing.T) {
		advanceTarget(t, fx)

		report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		if err != nil {
			t.Fatalf("Publish after a clean reviewer round and a moved target: %v", err)
		}
		if report.Tier != "none" {
			t.Errorf("revalidation tier = %q, want none: publish revalidates nothing", report.Tier)
		}
		if head := originRef(t, fx.RepoRemote, "refs/heads/"+branch); head != reviewed || report.Head["fixture-repo"] != reviewed {
			t.Fatalf("origin's %s = %q (report head %q), want the reviewed head %s: publish reconciles nothing, so the moved target changes nothing it ships", branch, head, report.Head["fixture-repo"], reviewed)
		}
	})
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
