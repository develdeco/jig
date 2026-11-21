package land_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

const remote = "https://github.com/acme/app"

func prURL(n string) string { return remote + "/pull/" + n }

// host is a repo host that holds pull requests: their states (what the merge
// fact reads) and their statuses (what a landing reads).
type host struct {
	status  map[string]repohost.Status
	methods repohost.MergeMethods
}

func (h *host) Read(url string) mergefacts.PRRead {
	return mergefacts.PRRead{State: h.status[url].State}
}

type world struct {
	t  *testing.T
	st *store.Store
	h  *host
	d  land.Deps
	// noSuite is the clone readiness gives a world that mapped none.
	noSuite *suiteWorld
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	h := &host{status: map[string]repohost.Status{}, methods: repohost.MergeMethods{Squash: true, Merge: true, Rebase: true}}
	cfg := project.Config{Repos: []project.Repo{{Remote: remote, Label: "app"}}}
	w := &world{t: t, st: st, h: h}
	w.d = land.Deps{
		Store: st, Cfg: cfg,
		Merges: &mergefacts.Reader{Store: st, Cfg: cfg, PRs: h},
		Status: func(url string) (repohost.Status, bool) {
			s, ok := h.status[url]
			return s, ok
		},
		Methods: func(string) (repohost.MergeMethods, bool) { return h.methods, true },
	}
	return w
}

func (w *world) ticket(id string, rec store.Ticket) {
	w.t.Helper()
	if rec.Title == "" {
		rec.Title = "Work of " + id
	}
	if err := w.st.CreateTicketRecord(id, rec); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) chart(name, issue string, entries ...string) {
	w.t.Helper()
	var b strings.Builder
	b.WriteString("issue: " + issue + "\ntickets:\n")
	for _, e := range entries {
		b.WriteString("  - id: " + e + "\n    title: " + e + "\n")
	}
	if err := store.AtomicWrite(w.st.ChartFile(name), []byte(b.String())); err != nil {
		w.t.Fatal(err)
	}
}

// publish records a published ticket: its pull request into container (the
// issue's id, "" for the target), in state, with base as the host has it.
func (w *world) publish(ticket, into, url, state, base string) {
	w.t.Helper()
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: into, URL: url},
		{Event: journal.EventPublishDone, Into: into},
	} {
		if err := journal.Append(w.st, ticket, l); err != nil {
			w.t.Fatal(err)
		}
	}
	w.h.status[url] = repohost.Status{State: state, Mergeable: repohost.MergeableYes, Base: base}
}

func (w *world) branch(id string) string {
	w.t.Helper()
	rec, err := w.st.ReadTicket(id)
	if err != nil {
		w.t.Fatal(err)
	}
	b, err := w.st.ResolveTicketBranch(id, rec, "main")
	if err != nil {
		w.t.Fatal(err)
	}
	return b
}

func (w *world) judge(ticket string, opts land.Options) land.Verdict {
	w.t.Helper()
	w.d.Merges = &mergefacts.Reader{Store: w.st, Cfg: w.d.Cfg, PRs: w.h}
	v, err := land.Judge(w.d, ticket, opts)
	if err != nil {
		w.t.Fatalf("Judge(%s): %v", ticket, err)
	}
	return v
}

func wantRefusal(t *testing.T, v land.Verdict, code string, first ...string) {
	t.Helper()
	if v.Refusal == nil || v.Refusal.Code != code {
		t.Fatalf("verdict = %+v, want refusal %s", v, code)
	}
	if strings.Join(v.Refusal.First, ",") != strings.Join(first, ",") {
		t.Errorf("%s: first = %v, want %v", code, v.Refusal.First, first)
	}
	if len(v.Merge) != 0 || len(v.Record) != 0 {
		t.Errorf("%s: a refusal names no merge: %+v", code, v)
	}
}

// issueAndTwoTasks is the chart of the brief's critical path: an issue and two
// tasks, the second merged on the first, both published into the issue.
func issueAndTwoTasks(t *testing.T) *world {
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("T-1", store.Ticket{})
	w.ticket("T-2", store.Ticket{BlockedBy: []store.TicketBlockedBy{{Ticket: "T-1", Kind: "merged"}}})
	w.chart("c", "I-1", "T-1", "T-2")
	base := w.branch("I-1")
	w.publish("T-1", "I-1", prURL("1"), ready.PROpen, base)
	w.publish("T-2", "I-1", prURL("2"), ready.PROpen, base)
	return w
}

// TestLandJudgeOrdersByTheChart: the second task waits for the first (refused
// with the first named, nothing written); the first names its merge into the
// issue's branch; once the host says merged the first is recorded and the
// second may land.
func TestLandJudgeOrdersByTheChart(t *testing.T) {
	t.Parallel()
	w := issueAndTwoTasks(t)
	wantRefusal(t, w.judge("T-2", land.Options{}), land.CodeWaits, "T-1")

	v := w.judge("T-1", land.Options{})
	if v.Refusal != nil || len(v.Merge) != 1 {
		t.Fatalf("T-1 = %+v, want one merge", v)
	}
	row := v.Merge[0]
	if row.PR != prURL("1") || row.Base != w.branch("I-1") || row.Command != "gh pr merge "+prURL("1")+" --squash" {
		t.Errorf("row = %+v", row)
	}

	w.h.status[prURL("1")] = repohost.Status{State: repohost.StateMerged, Base: w.branch("I-1"), MergeCommit: "abc123"}
	v = w.judge("T-1", land.Options{})
	if v.Refusal != nil || len(v.Merge) != 0 || len(v.Record) != 1 {
		t.Fatalf("T-1 merged = %+v, want one line to record", v)
	}
	if got := v.Record[0]; got.PR != prURL("1") || got.Base != w.branch("I-1") || got.Into != "I-1" || got.Commit != "abc123" {
		t.Errorf("record = %+v", got)
	}
	if v := w.judge("T-2", land.Options{}); v.Refusal != nil || len(v.Merge) != 1 {
		t.Errorf("T-2 after T-1 merged = %+v, want its merge", v)
	}
}

// TestLandJudgeRefusals: not published, closed pull request, a base that is not
// the plan's.
func TestLandJudgeRefusals(t *testing.T) {
	t.Parallel()
	w := issueAndTwoTasks(t)
	w.ticket("T-3", store.Ticket{})
	wantRefusal(t, w.judge("T-3", land.Options{}), land.CodeNotPublished)

	w.h.status[prURL("1")] = repohost.Status{State: repohost.StateClosed, Base: w.branch("I-1")}
	wantRefusal(t, w.judge("T-1", land.Options{}), land.CodeClosed)

	w.h.status[prURL("1")] = repohost.Status{State: repohost.StateOpen, Base: "main"}
	v := w.judge("T-1", land.Options{})
	wantRefusal(t, v, land.CodeBaseDiffers)
	if !strings.Contains(strings.Join(v.Refusal.Help, " "), "jig publish T-1") {
		t.Errorf("help = %v, want jig publish", v.Refusal.Help)
	}
}

// TestLandJudgeMethodAndConflict: squash where the repo allows it, else merge,
// else rebase; the person's method overrides; a conflicting pull request is
// named with the merge that resolves it; a host that cannot be asked is a note.
func TestLandJudgeMethodAndConflict(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ticket("T-1", store.Ticket{})
	w.publish("T-1", "", prURL("1"), ready.PROpen, "main")
	for _, tc := range []struct {
		m      repohost.MergeMethods
		method string
		want   string
	}{
		{repohost.MergeMethods{Squash: true, Merge: true, Rebase: true}, "", "squash"},
		{repohost.MergeMethods{Merge: true, Rebase: true}, "", "merge"},
		{repohost.MergeMethods{Rebase: true}, "", "rebase"},
		{repohost.MergeMethods{Squash: true}, "merge", "merge"},
	} {
		w.h.methods = tc.m
		v := w.judge("T-1", land.Options{Method: tc.method})
		if len(v.Merge) != 1 || v.Merge[0].Method != tc.want || !strings.HasSuffix(v.Merge[0].Command, " --"+tc.want) {
			t.Errorf("methods %+v, asked %q: %+v, want %s", tc.m, tc.method, v.Merge, tc.want)
		}
		if wantNote := tc.method != "" && !tc.m.Allows(tc.method); wantNote != (len(v.Notes) == 1) {
			t.Errorf("methods %+v, asked %q: notes %v, want a note exactly when the repo does not allow it", tc.m, tc.method, v.Notes)
		}
	}
	s := w.h.status[prURL("1")]
	s.Mergeable = repohost.MergeableConflict
	w.h.status[prURL("1")] = s
	v := w.judge("T-1", land.Options{})
	if len(v.Merge) != 1 || !v.Merge[0].Conflicting || !strings.Contains(v.Merge[0].Resolve, "git merge origin/main") {
		t.Errorf("conflicting = %+v", v.Merge)
	}
	// A base that merely moved past the branch is no refusal: the host
	// answers OPEN and MERGEABLE for it.
	s.Mergeable = repohost.MergeableYes
	w.h.status[prURL("1")] = s

	w.d.Status = nil
	v = w.judge("T-1", land.Options{})
	if v.Refusal != nil || len(v.Merge) != 1 || len(v.Notes) != 1 {
		t.Errorf("host unknown = %+v, want a merge and a note", v)
	}
}

// TestLandJudgeStackedParentAndChildren: a stacked parent, a multi-task's tasks
// and an issue's tickets must land first, each through its own entry of Rules.
func TestLandJudgeStackedParentAndChildren(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("M-1", store.Ticket{Kind: store.KindMultiTask})
	w.ticket("T-1", store.Ticket{Parent: "M-1"})
	w.ticket("T-2", store.Ticket{BlockedBy: []store.TicketBlockedBy{{Ticket: "T-3", Kind: "stacked"}}})
	w.ticket("T-3", store.Ticket{})
	w.chart("c", "I-1", "M-1", "T-1", "T-2", "T-3")
	w.publish("I-1", "", prURL("20"), ready.PROpen, "main")
	w.publish("M-1", "I-1", prURL("10"), ready.PROpen, w.branch("I-1"))
	w.publish("T-1", "M-1", prURL("1"), ready.PROpen, w.branch("M-1"))
	w.publish("T-2", "I-1", prURL("2"), ready.PROpen, w.branch("I-1"))
	w.publish("T-3", "I-1", prURL("3"), ready.PROpen, w.branch("I-1"))

	wantRefusal(t, w.judge("T-2", land.Options{}), land.CodeWaits, "T-3")
	wantRefusal(t, w.judge("M-1", land.Options{}), land.CodeWaits, "T-1")
	wantRefusal(t, w.judge("I-1", land.Options{}), land.CodeWaits, "M-1", "T-2", "T-3")

	if got := len(land.Rules); got != 4 {
		t.Errorf("Rules has %d entries, want the four of the brief", got)
	}
}

// TestLandCompleteNeedsChildrenAndItsOwnLanding: a multi-task is complete when
// its tasks have merged into its branch and its own pull request has landed.
func TestLandCompleteNeedsChildrenAndItsOwnLanding(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("M-1", store.Ticket{Kind: store.KindMultiTask})
	w.ticket("T-1", store.Ticket{Parent: "M-1"})
	w.chart("c", "I-1", "M-1", "T-1")
	w.publish("M-1", "I-1", prURL("10"), ready.PROpen, w.branch("I-1"))
	w.publish("T-1", "M-1", prURL("1"), ready.PROpen, w.branch("M-1"))

	w.d.Merges = &mergefacts.Reader{Store: w.st, Cfg: w.d.Cfg, PRs: w.h}
	done, waiting, err := land.Complete(w.d, "M-1")
	if err != nil || done || strings.Join(waiting, ",") != "T-1" {
		t.Fatalf("Complete = %v, %v, %v, want waiting on T-1", done, waiting, err)
	}
	w.h.status[prURL("1")] = repohost.Status{State: repohost.StateMerged}
	w.d.Merges = &mergefacts.Reader{Store: w.st, Cfg: w.d.Cfg, PRs: w.h}
	if done, waiting, err = land.Complete(w.d, "M-1"); err != nil || done || len(waiting) != 0 {
		t.Fatalf("Complete with the task merged and no landing = %v, %v, %v, want not done", done, waiting, err)
	}
	w.h.status[prURL("10")] = repohost.Status{State: repohost.StateMerged}
	w.d.Merges = &mergefacts.Reader{Store: w.st, Cfg: w.d.Cfg, PRs: w.h}
	if err := journal.Append(w.st, "M-1", land.Landing{PR: prURL("10"), Base: "main", Into: "I-1", Provenance: journal.ProvenanceAgent}.Line("M-1")); err != nil {
		t.Fatal(err)
	}
	if done, _, err = land.Complete(w.d, "M-1"); err != nil || !done {
		t.Fatalf("Complete after its own landing = %v, %v, want done", done, err)
	}
}

// TestLandLandedIsPerIssue: a shared task needs --issue, one issue's landing
// says nothing of the other's, and a landed ticket is done, not landed again.
func TestLandLandedIsPerIssue(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, id := range []string{"I-1", "I-2"} {
		w.ticket(id, store.Ticket{Kind: store.KindIssue})
	}
	w.ticket("T-1", store.Ticket{})
	w.chart("a", "I-1", "T-1")
	w.chart("b", "I-2", "T-1")
	w.publish("T-1", "I-1", prURL("1"), ready.PRMerged, w.branch("I-1"))
	w.publish("T-1", "I-2", prURL("2"), ready.PROpen, w.branch("I-2"))

	var ae *axi.Error
	if _, err := land.Landed(w.st, "T-1", ""); !errors.As(err, &ae) || ae.Code != "SHARED_TASK_ISSUE_NEEDED" {
		t.Fatalf("Landed with no issue = %v, want SHARED_TASK_ISSUE_NEEDED", err)
	}
	if _, err := land.Judge(w.d, "T-1", land.Options{}); !errors.As(err, &ae) || ae.Code != "SHARED_TASK_ISSUE_NEEDED" {
		t.Fatalf("Judge with no issue = %v, want SHARED_TASK_ISSUE_NEEDED", err)
	}
	v := w.judge("T-1", land.Options{Issue: "I-1"})
	if len(v.Record) != 1 || v.Record[0].Into != "I-1" {
		t.Fatalf("I-1 = %+v, want its merged pull request to record", v)
	}
	line := v.Record[0]
	line.Provenance = journal.ProvenanceHost
	if err := journal.Append(w.st, "T-1", line.Line("T-1")); err != nil {
		t.Fatal(err)
	}
	got, err := land.Landed(w.st, "T-1", "I-1")
	if err != nil || len(got) != 1 || got[0].Into != "I-1" || got[0].Provenance != journal.ProvenanceHost {
		t.Fatalf("Landed(I-1) = %+v, %v", got, err)
	}
	if got, err = land.Landed(w.st, "T-1", "I-2"); err != nil || len(got) != 0 {
		t.Fatalf("Landed(I-2) = %+v, %v, want none", got, err)
	}
	if v := w.judge("T-1", land.Options{Issue: "I-1"}); !v.Done || v.Refusal != nil {
		t.Errorf("I-1 landed = %+v, want done", v)
	}
	if v := w.judge("T-1", land.Options{Issue: "I-2"}); v.Done || len(v.Merge) != 1 {
		t.Errorf("I-2 not landed = %+v, want its merge", v)
	}
}
