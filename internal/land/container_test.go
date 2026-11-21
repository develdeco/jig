package land_test

import (
	"errors"
	"testing"

	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

func (w *world) readiness(container string) land.Readiness {
	w.t.Helper()
	w.d.Merges = &mergefacts.Reader{Store: w.st, Cfg: w.d.Cfg, PRs: w.h}
	r, err := land.ReadinessOf(w.d, container)
	if err != nil {
		w.t.Fatalf("ReadinessOf(%s): %v", container, err)
	}
	return r
}

func (w *world) merged(url, base string) {
	w.h.status[url] = repohost.Status{State: repohost.StateMerged, Base: base}
}

// multiTaskWorld is an issue with a multi-task of two tasks (M-1: T-1, T-2).
func multiTaskWorld(t *testing.T) *world {
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("M-1", store.Ticket{Kind: store.KindMultiTask})
	w.ticket("T-1", store.Ticket{Parent: "M-1"})
	w.ticket("T-2", store.Ticket{Parent: "M-1"})
	w.chart("c", "I-1", "M-1", "T-1", "T-2")
	return w
}

// TestContainerReadyWaitsForEveryChild: a multi-task waits, one row per child
// not landed with its standing, until both tasks have landed; then it is ready
// with the prompt Publish M-1; once it has a pull request it is published.
func TestContainerReadyWaitsForEveryChild(t *testing.T) {
	t.Parallel()
	w := multiTaskWorld(t)
	base := w.branch("M-1")
	w.publish("T-1", "M-1", prURL("1"), ready.PROpen, base)

	r := w.readiness("M-1")
	if r.State != land.StateWaiting || r.Prompt != "" || len(r.Waits) != 2 ||
		r.Waits[0] != (land.Wait{Ticket: "T-1", Standing: "published"}) ||
		r.Waits[1] != (land.Wait{Ticket: "T-2", Standing: "building"}) {
		t.Fatalf("readiness = %+v, want waiting on T-1 (published) and T-2 (building)", r)
	}

	w.merged(prURL("1"), base)
	w.publish("T-2", "M-1", prURL("2"), ready.PROpen, base)
	if r := w.readiness("M-1"); r.State != land.StateWaiting || len(r.Waits) != 1 || r.Waits[0].Ticket != "T-2" {
		t.Fatalf("readiness with T-2 open = %+v, want waiting on T-2 alone", r)
	}

	w.merged(prURL("2"), base)
	r = w.readiness("M-1")
	if r.State != land.StateReady || r.Prompt != "Publish M-1" || len(r.Waits) != 0 {
		t.Fatalf("readiness once both landed = %+v, want ready, Publish M-1", r)
	}

	w.publish("M-1", "I-1", prURL("3"), ready.PROpen, w.branch("I-1"))
	if r := w.readiness("M-1"); r.State != land.StatePublished || r.Prompt != "Land M-1" {
		t.Fatalf("readiness once published = %+v, want published, Land M-1", r)
	}
}

// TestContainerReadyAnIssueCountsAMultiTaskByItsOwnPullRequest: an issue whose
// only child is a multi-task waits until the multi-task's own pull request has
// landed, whatever its tasks did; a container with no child is not ready.
func TestContainerReadyAnIssueCountsAMultiTaskByItsOwnPullRequest(t *testing.T) {
	t.Parallel()
	w := multiTaskWorld(t)
	mb, ib := w.branch("M-1"), w.branch("I-1")
	w.publish("T-1", "M-1", prURL("1"), ready.PROpen, mb)
	w.publish("T-2", "M-1", prURL("2"), ready.PROpen, mb)
	w.merged(prURL("1"), mb)
	w.merged(prURL("2"), mb)
	if r := w.readiness("I-1"); r.State != land.StateWaiting || len(r.Waits) != 1 || r.Waits[0] != (land.Wait{Ticket: "M-1", Standing: "building"}) {
		t.Fatalf("issue readiness = %+v, want waiting on M-1 (the multi-task is not published)", r)
	}
	w.publish("M-1", "I-1", prURL("3"), ready.PROpen, ib)
	if r := w.readiness("I-1"); len(r.Waits) != 1 || r.Waits[0].Standing != "published" {
		t.Fatalf("issue readiness = %+v, want M-1 published and waited on", r)
	}
	w.merged(prURL("3"), ib)
	if r := w.readiness("I-1"); r.State != land.StateReady || r.Prompt != "Publish I-1" {
		t.Fatalf("issue readiness = %+v, want ready", r)
	}

	empty := newWorld(t)
	empty.ticket("I-9", store.Ticket{Kind: store.KindIssue})
	if r := empty.readiness("I-9"); r.State != land.StateWaiting || len(r.Waits) != 0 || r.Prompt != "" {
		t.Fatalf("an issue with no child = %+v, want waiting with no row and no prompt", r)
	}
}

// TestContainerReadyAClosedChildIsNamed: a child closed as not planned holds
// the container, named with why; one closed as covered releases it.
func TestContainerReadyAClosedChildIsNamed(t *testing.T) {
	t.Parallel()
	w := multiTaskWorld(t)
	base := w.branch("M-1")
	w.publish("T-1", "M-1", prURL("1"), ready.PROpen, base)
	w.merged(prURL("1"), base)
	if err := w.st.CloseTicket("T-2", store.Closed{Reason: store.CloseNotPlanned}); err != nil {
		t.Fatal(err)
	}
	r := w.readiness("M-1")
	if r.State != land.StateWaiting || len(r.Waits) != 1 || r.Waits[0] != (land.Wait{Ticket: "T-2", Standing: ready.NotPlanned}) {
		t.Fatalf("readiness = %+v, want T-2 named as not planned", r)
	}
	w = multiTaskWorld(t)
	w.publish("T-1", "M-1", prURL("1"), ready.PROpen, base)
	w.merged(prURL("1"), base)
	if err := w.st.CloseTicket("T-2", store.Closed{Reason: store.CloseCovered, By: "x/y#1"}); err != nil {
		t.Fatal(err)
	}
	if r := w.readiness("M-1"); r.State != land.StateReady {
		t.Fatalf("readiness with T-2 covered = %+v, want ready", r)
	}
}

// TestContainerReadyCheckRunsTheRegistry: a registered precondition that is
// unmet holds an otherwise ready container with its Why and Prompt, and Check
// reports every unmet one; the empty registry holds nothing.
func TestContainerReadyCheckRunsTheRegistry(t *testing.T) {
	// Not parallel: it swaps the package's registry.
	w := multiTaskWorld(t)
	base := w.branch("M-1")
	for i, id := range []string{"T-1", "T-2"} {
		url := prURL(string(rune('1' + i)))
		w.publish(id, "M-1", url, ready.PROpen, base)
		w.merged(url, base)
	}
	if unmet, err := land.Check(land.Container{ID: "M-1"}); err != nil || len(unmet) != 0 {
		t.Fatalf("Check with the empty registry = %v, %v, want none", unmet, err)
	}

	saved := land.Preconditions
	defer func() { land.Preconditions = saved }()
	var seen land.Container
	land.Preconditions = []land.Precondition{
		func(c land.Container) ([]land.Unmet, error) {
			seen = c
			return []land.Unmet{{Code: "FAKE_NOT_MET", Why: "the fake suite has not run", Prompt: "Run the fake suite"}}, nil
		},
		func(land.Container) ([]land.Unmet, error) {
			return []land.Unmet{{Code: "FAKE_TWO", Why: "second"}}, nil
		},
	}
	r := w.readiness("M-1")
	if r.State != land.StateWaiting || len(r.Unmet) != 2 || r.Unmet[0].Code != "FAKE_NOT_MET" || r.Prompt != "Run the fake suite" || r.Unmet[0].Why != "the fake suite has not run" {
		t.Fatalf("readiness = %+v, want waiting on the fake precondition with its prompt", r)
	}
	if seen.ID != "M-1" || seen.Kind != store.KindMultiTask || seen.Branch != base || len(seen.Children) != 2 || len(seen.Repos) != 1 {
		t.Errorf("the precondition was asked about %+v", seen)
	}

	land.Preconditions = []land.Precondition{func(land.Container) ([]land.Unmet, error) { return nil, errors.New("boom") }}
	if _, err := land.ReadinessOf(w.d, "M-1"); err == nil {
		t.Error("a precondition's error was swallowed")
	}
}
