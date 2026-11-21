package mergefacts

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// containerStore is an issue I-1 with a multi-task M-1 of two tasks, one built
// in api and one in web (docs is built in by none); M-1 published its own pull
// requests, one in each of its tasks' repos.
func containerStore(t *testing.T, apiPR, webPR string) (*store.Store, project.Config) {
	t.Helper()
	cfg := project.Config{Repos: []project.Repo{
		{Remote: "https://github.com/o/api.git"},
		{Remote: "https://github.com/o/web.git"},
		{Remote: "https://github.com/o/docs.git"},
	}}
	st := &store.Store{Root: t.TempDir()}
	for id, rec := range map[string]store.Ticket{
		"I-1": {Title: "Issue", Kind: store.KindIssue}, "M-1": {Title: "Multi", Kind: store.KindMultiTask},
		"T-1": {Title: "One", Parent: "M-1"}, "T-2": {Title: "Two", Parent: "M-1"},
	} {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	chart := "issue: I-1\ntickets:\n  - id: M-1\n    title: Multi\n  - id: T-1\n    title: One\n  - id: T-2\n    title: Two\n"
	if err := store.AtomicWrite(st.ChartFile("c"), []byte(chart)); err != nil {
		t.Fatal(err)
	}
	for id, ws := range map[string]string{"T-1": "api/server", "T-2": "web/ui"} {
		write(t, filepath.Join(st.TicketDir(id), "slices.yaml"),
			"slices:\n  - id: s\n    workspace: "+ws+"\n    goal: g\n    oracle: test\n    blocked_by: []\n    from_brief: []\n")
	}
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "I-1", URL: apiPR}, {Event: journal.EventPr, Into: "I-1", URL: webPR},
		{Event: journal.EventPublishDone, Into: "I-1"},
	} {
		if err := journal.Append(st, "M-1", l); err != nil {
			t.Fatal(err)
		}
	}
	return st, cfg
}

// TestContainerPublishMergeFactReadsItsOwnPullRequests: a multi-task is judged
// by its own recorded pull requests, one in each repo its tasks build in (not
// docs, which none does), merged only when every one is; its tasks' pull
// requests are not its own.
func TestContainerPublishMergeFactReadsItsOwnPullRequests(t *testing.T) {
	t.Parallel()
	const (
		apiPR = "https://github.com/o/api/pull/1"
		webPR = "https://github.com/o/web/pull/2"
	)
	st, cfg := containerStore(t, apiPR, webPR)
	prs := &fakePRs{reads: map[string]PRRead{apiPR: {State: ready.PRMerged}, webPR: {State: ready.PROpen}}}
	m, err := (&Reader{Store: st, Cfg: cfg, PRs: prs}).Of("M-1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Met() || !m.Published || len(m.Repos) != 2 || m.Repos[0].Repo != "api" || m.Repos[1].Repo != "web" || m.Why() != "web: its PR is open ("+webPR+")" {
		t.Fatalf("api merged, web open: %+v (why %q), want published, api and web, only web missing", m, m.Why())
	}
	prs.reads[webPR] = PRRead{State: ready.PRMerged}
	if m, err := (&Reader{Store: st, Cfg: cfg, PRs: prs}).Of("M-1"); err != nil || !m.Met() || m.Via() != ready.ByPR {
		t.Errorf("both merged: %+v, %v, want met by pull request", m, err)
	}
}

// TestContainerPublishMergeFactOfATaskIsUnchanged: a task under the multi-task
// is read as before, through its own journal and slices, and a container with
// no task builds in every repo of the project.
func TestContainerPublishMergeFactOfATaskIsUnchanged(t *testing.T) {
	t.Parallel()
	const (
		apiPR = "https://github.com/o/api/pull/1"
		webPR = "https://github.com/o/web/pull/2"
	)
	st, cfg := containerStore(t, apiPR, webPR)
	if err := journal.Append(st, "T-1", journal.Line{Event: journal.EventPr, Into: "M-1", URL: apiPR}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, "T-1", journal.Line{Event: journal.EventPublishDone, Into: "M-1"}); err != nil {
		t.Fatal(err)
	}
	prs := &fakePRs{reads: map[string]PRRead{apiPR: {State: ready.PRMerged}}}
	if m, err := (&Reader{Store: st, Cfg: cfg, PRs: prs}).Of("T-1"); err != nil || !m.Met() || len(m.Repos) != 1 {
		t.Errorf("T-1 = %+v, %v, want met in the one repo it builds in", m, err)
	}

	if err := st.CreateTicketRecord("M-2", store.Ticket{Title: "Empty", Kind: store.KindMultiTask}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, "M-2", journal.Line{Event: journal.EventPublishDone, Into: "I-1"}); err != nil {
		t.Fatal(err)
	}
	if m, err := (&Reader{Store: st, Cfg: cfg, PRs: prs}).Of("M-2"); err != nil || len(m.Repos) != 3 {
		t.Errorf("a container with no task = %+v, %v, want every repo of the project", m, err)
	}
}
