package land_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

func (w *world) readiness(container string) land.Readiness {
	w.t.Helper()
	w.d.Merges = &mergefacts.Reader{Store: w.st, Cfg: w.d.Cfg, PRs: w.h}
	if w.d.Clones == nil || w.noSuite != nil {
		// A repo with no clone is a suite jig cannot read: these tests are
		// about the children, so the repo is cloned with no suite declared, at
		// the container's branch.
		if w.noSuite == nil {
			w.noSuite = &suiteWorld{world: w, dir: w.t.TempDir()}
			w.noSuite.initClone()
			w.d.Clones = map[string]string{"app": w.noSuite.dir}
		}
		w.noSuite.branch = w.branch(container)
		w.noSuite.commit("oracles:\n  test: \"go test\"\n")
	}
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
			return []land.Unmet{{Code: "FAKE_TWO", Why: "second", Prompt: "Do the other"}}, nil
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

// TestContainerReadyHandsAPreconditionTheReposOfItsTasks: a precondition is
// asked for the repos the container's tasks build in (api and web, not docs) in
// the project order; a container with no task is in every repo.
func TestContainerReadyHandsAPreconditionTheReposOfItsTasks(t *testing.T) {
	t.Parallel()
	w := multiTaskWorld(t)
	w.d.Cfg = project.Config{Repos: []project.Repo{
		{Remote: "https://github.com/acme/api.git"}, {Remote: "https://github.com/acme/web.git"}, {Remote: "https://github.com/acme/docs.git"},
	}}
	for id, ws := range map[string]string{"T-1": "api/server", "T-2": "web/ui"} {
		path := filepath.Join(w.st.TicketDir(id), "slices.yaml")
		if err := store.AtomicWrite(path, []byte("slices:\n  - id: s\n    workspace: "+ws+"\n    goal: g\n    oracle: test\n    blocked_by: []\n    from_brief: []\n")); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"api", "web"}
	for _, id := range []string{"M-1", "I-1"} {
		c, err := land.ContainerOf(w.d, id)
		if err != nil {
			t.Fatalf("ContainerOf(%s): %v", id, err)
		}
		if !slices.Equal(c.Repos, want) {
			t.Errorf("%s's repos = %v, want %v: the tasks build in api and web", id, c.Repos, want)
		}
	}
	w.ticket("M-2", store.Ticket{Kind: store.KindMultiTask})
	if c, err := land.ContainerOf(w.d, "M-2"); err != nil || len(c.Repos) != 3 {
		t.Errorf("a container with no task = %+v, %v, want every repo of the project", c, err)
	}
}

// TestContainerReadyReadsASharedChildForItsIssue: a task two issues list counts
// for I-1 by its landed pull request into I-1 and waits I-2 on its open one.
func TestContainerReadyReadsASharedChildForItsIssue(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("I-2", store.Ticket{Kind: store.KindIssue})
	w.ticket("S-1", store.Ticket{})
	w.chart("a", "I-1", "S-1")
	w.chart("b", "I-2", "S-1")
	w.publish("S-1", "I-1", prURL("1"), ready.PROpen, w.branch("I-1"))
	w.publish("S-1", "I-2", prURL("2"), ready.PROpen, w.branch("I-2"))
	w.merged(prURL("1"), w.branch("I-1"))
	if r := w.readiness("I-1"); r.State != land.StateReady || r.Prompt != "Publish I-1" {
		t.Errorf("I-1 = %+v, want ready: the shared task landed into it", r)
	}
	if r := w.readiness("I-2"); r.State != land.StateWaiting || len(r.Waits) != 1 || r.Waits[0] != (land.Wait{Ticket: "S-1", Standing: "published"}) {
		t.Errorf("I-2 = %+v, want waiting on the shared task's open pull request into it", r)
	}
}

// TestContainerReadyAnEntryWithNoTicketHoldsItsContainer: an entry of the chart
// with no ticket yet, top-level or under a multi-task, is work not landed: it
// holds the issue or the multi-task as a row named by its position and title,
// whatever the graduated children did.
func TestContainerReadyAnEntryWithNoTicketHoldsItsContainer(t *testing.T) {
	t.Parallel()
	w := multiTaskWorld(t)
	chart := "issue: I-1\ntickets:\n  - id: M-1\n    title: Multi\n    kind: multi-task\n  - id: T-1\n    title: One\n    under: \"#1\"\n  - id: T-2\n    title: Two\n    under: M-1\n  - title: Sub\n    under: \"#1\"\n  - title: Later\n"
	if err := store.AtomicWrite(w.st.ChartFile("c"), []byte(chart)); err != nil {
		t.Fatal(err)
	}
	mb, ib := w.branch("M-1"), w.branch("I-1")
	for i, id := range []string{"T-1", "T-2"} {
		url := prURL(string(rune('1' + i)))
		w.publish(id, "M-1", url, ready.PROpen, mb)
		w.merged(url, mb)
	}
	if r := w.readiness("M-1"); r.State != land.StateWaiting || len(r.Waits) != 1 || r.Waits[0] != (land.Wait{Ticket: "#4 Sub", Standing: land.NotGraduated}) {
		t.Fatalf("M-1 = %+v, want waiting on its ungraduated entry only", r)
	}
	w.publish("M-1", "I-1", prURL("3"), ready.PROpen, ib)
	w.merged(prURL("3"), ib)
	if r := w.readiness("I-1"); r.State != land.StateWaiting || len(r.Waits) != 1 || r.Waits[0] != (land.Wait{Ticket: "#5 Later", Standing: land.NotGraduated}) {
		t.Fatalf("I-1 = %+v, want waiting on #5 Later alone: #4 is under the multi-task", r)
	}
	// Graduated, the entries hold nothing.
	if err := store.AtomicWrite(w.st.ChartFile("c"), []byte("issue: I-1\ntickets:\n  - id: M-1\n    title: Multi\n    kind: multi-task\n")); err != nil {
		t.Fatal(err)
	}
	if r := w.readiness("I-1"); r.State != land.StateReady {
		t.Errorf("I-1 with nothing ungraduated = %+v, want ready", r)
	}
}

// TestContainerReadyLandedContainerHasNothingToAsk: a multi-task whose own pull
// request merged on the host with no landed line is still published; with the
// line it is landed, with no prompt.
func TestContainerReadyLandedContainerHasNothingToAsk(t *testing.T) {
	t.Parallel()
	w := multiTaskWorld(t)
	ib := w.branch("I-1")
	w.publish("M-1", "I-1", prURL("4"), ready.PROpen, ib)
	w.merged(prURL("4"), ib)
	if r := w.readiness("M-1"); r.State != land.StatePublished || r.Prompt != "Land M-1" {
		t.Fatalf("merged, not recorded: %+v, want published, Land M-1", r)
	}
	if err := journal.Append(w.st, "M-1", journal.Line{Event: journal.EventLanded, Into: "I-1", URL: prURL("4")}); err != nil {
		t.Fatal(err)
	}
	if r := w.readiness("M-1"); r.State != land.StateLanded || r.Prompt != "" {
		t.Errorf("landed: %+v, want landed with no prompt", r)
	}
}
