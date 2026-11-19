package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// recordFinalRun journals a final-oracle line the way `jig verify --final`
// writes one: a run of command at commit that covers it, with the outcome and
// seconds given.
func recordFinalRun(t *testing.T, d Deps, ticket, commit, outcome string, seconds int, command string) {
	t.Helper()
	l := journal.Line{Event: journal.EventFinalOracle, Outcome: outcome, Commit: commit, HeadEnd: commit, Command: command, Seconds: seconds,
		TreeBefore: journal.TreeClean, TreeAfter: journal.TreeClean, Source: "jig"}
	if err := journal.Append(d.Store, ticket, l); err != nil {
		t.Fatalf("journal.Append: %v", err)
	}
}

// TestPublishReportsTheFinalRunAndRefusesNothing: every form that renders or
// pushes reports what the reviewed head has, and a head with none, or whose
// final run failed, is published all the same.
func TestPublishReportsTheFinalRunAndRefusesNothing(t *testing.T) {
	t.Parallel()
	fx, d, head := reviewedInTheClone(t)

	body, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if len(body.Repos) != 1 || body.Repos[0].FinalRun.Outcome != FinalRunNone || body.Repos[0].FinalRun.Head != head {
		t.Errorf("PublishBody repos = %+v, want the reviewed head %s with no final run", body.Repos, head)
	}
	approved, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "person")
	if err != nil {
		t.Fatalf("PublishApprove: %v", err)
	}
	if len(approved.Repos) != 1 || approved.Repos[0].FinalRun.Outcome != FinalRunNone {
		t.Errorf("PublishApprove repos = %+v, want no final run", approved.Repos)
	}

	recordFinalRun(t, d, fx.Ticket, head, "fail", 4, "the-failing-command")
	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish with a failed final run: %v, want it published all the same", err)
	}
	got := report.FinalRun["fixture-repo"]
	if got.Outcome != FinalRunFail || got.Command != "the-failing-command" || got.Head != head {
		t.Errorf("Publish's final run = %+v, want the failure of the-failing-command at %s", got, head)
	}
	if report.Head["fixture-repo"] != head {
		t.Errorf("Publish left %q on the branch, want the reviewed head %s", report.Head["fixture-repo"], head)
	}
}

// TestPublishStartsNoCommand: a manifest whose oracle would leave a marker and
// fail is read by publish and run by no one, whatever form is used: nothing
// revalidates the reviewed head.
func TestPublishStartsNoCommand(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	marker := filepath.Join(t.TempDir(), "ran")
	failingOracleManifest(t, fx, marker)

	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("PublishBody: %v", err)
	}
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "person"); err != nil {
		// A scripted round records no reviewed head to approve: only the bare
		// form below is then held to the marker.
		t.Logf("PublishApprove: %v", err)
	}
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("a command ran during publish: %s exists", marker)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		switch l.Event {
		case journal.EventOracle, journal.EventFinalOracle, "gate-oracle", journal.EventRevalidate:
			t.Errorf("publish wrote a %s line: %+v", l.Event, l)
		}
	}
}
