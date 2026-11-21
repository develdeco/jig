package land_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

const suiteDeclared = "oracles:\n  suite: \"go test ./...\"\n"

// suiteWorld is an issue I-1 whose one task T-1 has landed, in a clone whose
// origin/<issue branch> holds the manifest. commit moves the tip.
type suiteWorld struct {
	*world
	dir    string
	branch string
}

func newSuiteWorld(t *testing.T, manifest string) *suiteWorld {
	t.Helper()
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("T-1", store.Ticket{})
	w.chart("c", "I-1", "T-1")
	base := w.branch("I-1")
	w.publish("T-1", "I-1", prURL("1"), ready.PROpen, base)
	w.merged(prURL("1"), base)

	sw := &suiteWorld{world: w, dir: t.TempDir(), branch: base}
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "fixture@example.invalid"}, {"config", "user.name", "fixture"}} {
		if _, err := gitx.Run(sw.dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	w.d.Clones = map[string]string{"app": sw.dir}
	if manifest != "" {
		sw.commit(manifest)
	}
	return sw
}

// commit commits a manifest and points origin/<branch> at it, returning the tip.
func (s *suiteWorld) commit(manifest string) string {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Join(s.dir, ".claude"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, ".claude", "jig.yaml"), []byte(manifest), 0o644); err != nil {
		s.t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--allow-empty", "-m", "m"}} {
		if _, err := gitx.Run(s.dir, args...); err != nil {
			s.t.Fatal(err)
		}
	}
	tip, err := gitx.RevParse(s.dir, "HEAD")
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := gitx.Run(s.dir, "update-ref", "refs/remotes/origin/"+s.branch, tip); err != nil {
		s.t.Fatal(err)
	}
	return tip
}

func (s *suiteWorld) journal(l journal.Line) {
	s.t.Helper()
	if err := journal.Append(s.st, "I-1", l); err != nil {
		s.t.Fatal(err)
	}
}

func unmetCodes(r land.Readiness) []string {
	var out []string
	for _, u := range r.Unmet {
		out = append(out, u.Code)
	}
	return out
}

// TestSuitePreconditionDeclaredAndNotRun: a declared suite with no covering run
// at the tip holds a ready issue as LAND_SUITE_NOT_RUN, naming the run; a pass
// at the tip releases it; a run at an earlier tip stands for nothing once the
// tip moves.
func TestSuitePreconditionDeclaredAndNotRun(t *testing.T) {
	t.Parallel()
	s := newSuiteWorld(t, suiteDeclared)
	tip := gitTip(t, s)

	r := s.readiness("I-1")
	if r.State != land.StateWaiting || len(r.Waits) != 0 || len(r.Unmet) != 1 || r.Unmet[0].Code != land.SuiteNotRun ||
		!strings.Contains(r.Prompt, "jig verify I-1 --final") || !strings.Contains(r.Prompt, "app") || !strings.Contains(r.Unmet[0].Why, tip[:12]) {
		t.Fatalf("readiness = %+v, want waiting on LAND_SUITE_NOT_RUN naming the run and the tip", r)
	}

	s.journal(journal.Line{Event: journal.EventFinalOracle, Commit: tip, Outcome: "pass", Seconds: 3})
	if r := s.readiness("I-1"); r.State != land.StateReady || len(r.Unmet) != 0 {
		t.Fatalf("readiness with a pass at the tip = %+v, want ready", r)
	}

	s.commit(suiteDeclared + "timeout: 1m\n")
	if r := s.readiness("I-1"); r.State != land.StateWaiting || len(r.Unmet) != 1 || r.Unmet[0].Code != land.SuiteNotRun {
		t.Fatalf("readiness after the tip moved = %+v, want the suite due again", r)
	}
}

func gitTip(t *testing.T, s *suiteWorld) string {
	t.Helper()
	tip, err := gitx.RevParse(s.dir, "origin/"+s.branch)
	if err != nil {
		t.Fatal(err)
	}
	return tip
}

// TestSuitePreconditionFailedNamesAFixTask: a covering fail at the tip is
// LAND_SUITE_FAILED with the run's seconds and signature and the prompt to add
// a fix task; a stopped or uncovered run is no result at all.
func TestSuitePreconditionFailedNamesAFixTask(t *testing.T) {
	t.Parallel()
	s := newSuiteWorld(t, suiteDeclared)
	tip := gitTip(t, s)

	s.journal(journal.Line{Event: journal.EventFinalOracle, Commit: tip, Outcome: "fail", Seconds: 41, Signature: "final:boom", Uncovered: "tree-dirty"})
	if got := unmetCodes(s.readiness("I-1")); len(got) != 1 || got[0] != land.SuiteNotRun {
		t.Fatalf("an uncovered fail gave %v, want the suite not run", got)
	}
	s.journal(journal.Line{Event: journal.EventFinalOracle, Commit: tip, Outcome: "fail", Seconds: 41, Signature: "final:boom"})
	r := s.readiness("I-1")
	if len(r.Unmet) != 1 || r.Unmet[0].Code != land.SuiteFailed || r.Prompt != "Add a fix task for I-1" ||
		!strings.Contains(r.Unmet[0].Why, "41 s") || !strings.Contains(r.Unmet[0].Why, "final:boom") {
		t.Fatalf("readiness = %+v, want LAND_SUITE_FAILED with seconds, signature and the fix-task prompt", r)
	}
}

// TestSuitePreconditionUndeclaredNeedsNothing: a repo with no suite declared
// is met, and so is a multi-task, whatever the manifest says.
func TestSuitePreconditionUndeclaredNeedsNothing(t *testing.T) {
	t.Parallel()
	s := newSuiteWorld(t, "oracles:\n  test: \"go test\"\n")
	if r := s.readiness("I-1"); r.State != land.StateReady || len(r.Unmet) != 0 {
		t.Fatalf("readiness without a declared suite = %+v, want ready", r)
	}

	m := multiTaskWorld(t)
	base := m.branch("M-1")
	m.publish("T-1", "M-1", prURL("1"), ready.PROpen, base)
	m.publish("T-2", "M-1", prURL("2"), ready.PROpen, base)
	m.merged(prURL("1"), base)
	m.merged(prURL("2"), base)
	if r := m.readiness("M-1"); r.State != land.StateReady {
		t.Fatalf("a multi-task = %+v, want ready: it has no suite", r)
	}
}

// TestSuitePreconditionNoCopyOfTheBranchSaysFetch: a clone with no
// origin/<branch> cannot show a tip, so the suite is unmet and says git fetch.
func TestSuitePreconditionNoCopyOfTheBranchSaysFetch(t *testing.T) {
	t.Parallel()
	s := newSuiteWorld(t, "")
	if _, err := gitx.Run(s.dir, "commit", "--allow-empty", "-m", "m"); err != nil {
		t.Fatal(err)
	}
	r := s.readiness("I-1")
	if len(r.Unmet) != 1 || r.Unmet[0].Code != land.SuiteNotRun || !strings.Contains(r.Unmet[0].Why, "git fetch") {
		t.Fatalf("readiness = %+v, want the suite not run, saying git fetch", r)
	}
}

// TestSuitePreconditionAWaiverAtTheTipMeetsIt: a suite-waived line at the tip
// meets the entry and is shown with its reason and who said it; a waiver at an
// earlier tip asks again.
func TestSuitePreconditionAWaiverAtTheTipMeetsIt(t *testing.T) {
	t.Parallel()
	s := newSuiteWorld(t, suiteDeclared)
	tip := gitTip(t, s)
	s.journal(journal.Line{Event: journal.EventSuiteWaived, Commit: tip, Summary: "ci runs it", Provenance: journal.ProvenancePerson})

	r := s.readiness("I-1")
	if r.State != land.StateReady || len(r.Unmet) != 0 || len(r.Waived) != 1 ||
		!strings.Contains(r.Waived[0], "ci runs it") || !strings.Contains(r.Waived[0], "person") {
		t.Fatalf("readiness = %+v, want ready with the waiver shown", r)
	}
	s.commit(suiteDeclared + "timeout: 1m\n")
	if r := s.readiness("I-1"); r.State != land.StateWaiting || len(r.Waived) != 0 {
		t.Fatalf("readiness after the tip moved = %+v, want the waiver spent", r)
	}
}
