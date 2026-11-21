package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// issueStanding is the part of `jig next <issue> --json` that says which chart
// answered and which tickets and issue block it carries.
type issueStanding struct {
	Chart   string `json:"chart"`
	Tickets []struct {
		ID string `json:"id"`
	} `json:"tickets"`
	Issue *struct {
		Ticket string `json:"ticket"`
	} `json:"issue"`
}

// issueRunOut is the part of `jig next <issue> --json` the run of an issue
// reads.
type issueRunOut struct {
	Chart  string `json:"chart"`
	Ready  bool   `json:"ready"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
	Prompt string `json:"prompt"`
	Run    [][]string
	Lands  []struct {
		Ticket string   `json:"ticket"`
		Prompt string   `json:"prompt"`
		After  []string `json:"after"`
		State  string   `json:"state"`
	} `json:"lands"`
	Ends []struct {
		Ticket  string `json:"ticket"`
		Blocker string `json:"blocker"`
		Fact    string `json:"fact"`
	} `json:"ends"`
	Issue *struct {
		Ticket string `json:"ticket"`
		Stage  string `json:"stage"`
		Branch string `json:"branch"`
	} `json:"issue"`
}

// TestIssueRunFlow drives the run of an issue with the real binary and a fake
// host: an issue of two tasks, the second waiting for the first to merge. The
// run stops at Land of the first with the second an end behind it, in the
// issue's own id; a merge made on the host is a record to make (`jig land`),
// after which the second runs.
func TestIssueRunFlow(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env, _ = ghOnPath(t, env.without("JIG_SLICE", "JIG_SESSION"))
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if r := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "An issue", "--kind", "issue"); r.Code != 0 {
		t.Fatalf("jig ticket new --kind issue: exit %d\n%s", r.Code, r.Stdout)
	}
	ids, _ := st.Tickets()
	issue := ids[len(ids)-1]
	if rec, err := st.ReadTicket(issue); err != nil || rec.KindOrDefault() != store.KindIssue {
		t.Fatalf("tickets = %v, want the issue last", ids)
	}
	chartDir := filepath.Join(fx.StoreDir, "charts", "plan")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(chartDir, "tickets.yaml"), []byte("issue: "+issue+"\ntickets:\n  - title: First\n  - title: Second\n    blocked_by:\n      - ref: \"#1\"\n"))
	if r := runJig(t, env, fx.StoreDir, "graduate", "plan"); r.Code != 0 {
		t.Fatalf("jig graduate plan: exit %d\n%s", r.Code, r.Stdout)
	}
	entries, err := st.ReadChart("plan")
	if err != nil || len(entries) != 2 {
		t.Fatalf("chart plan = %v, %v", entries, err)
	}
	first, second := entries[0].ID, entries[1].ID
	for _, id := range []string{first, second} {
		briefed(t, st, id)
	}

	read := func() issueRunOut {
		return nextJSON[issueRunOut](t, env, fx.RepoDir, issue)
	}
	// Before anything published: the run is the first task, asked for in the
	// issue's id.
	if c := read(); !c.Ready || c.Chart != "plan" || c.Prompt != "Continue "+issue || c.Issue == nil || c.Issue.Ticket != issue {
		t.Fatalf("started: %+v, want the run of plan asked as Continue %s", c, issue)
	}
	branch := read().Issue.Branch

	// The first published, its pull request open: the run stops at its landing.
	pr := pullURL(7)
	publishedWith(t, st, first, pr)
	env = withPulls(t, env, fixture.GhPull{Number: 7, Base: branch})
	c := read()
	if c.Ready || c.Code != "CHART_WAITS_LAND" || c.Prompt != "Continue "+issue || len(c.Lands) != 1 || c.Lands[0].Ticket != first || c.Lands[0].Prompt != "Land "+first {
		t.Fatalf("stop: ready %v code %q prompt %q lands %+v, want CHART_WAITS_LAND at Land %s", c.Ready, c.Code, c.Prompt, c.Lands, first)
	}
	if len(c.Ends) != 1 || c.Ends[0].Ticket != second || c.Ends[0].Blocker != first || !strings.Contains(c.Ends[0].Fact, "to land into "+branch) {
		t.Errorf("ends = %+v, want %s behind the landing into %s", c.Ends, second, branch)
	}
	text := runJig(t, env, fx.RepoDir, "next", issue).Stdout
	if want := "Land " + first + " (or merge it on the host), then ask: Continue " + issue; !strings.Contains(text, want) {
		t.Errorf("jig next %s lacks %q:\n%s", issue, want, text)
	}

	// Merged on the host: a record to make, and the second runs.
	env = withPulls(t, env, fixture.GhPull{Number: 7, Base: branch, State: "closed", Merged: true})
	c = read()
	if !c.Ready || len(c.Run) != 1 || c.Run[0][0] != second || len(c.Lands) != 1 || c.Lands[0].State != "merged on the host, not recorded" {
		t.Fatalf("merged on the host: ready %v run %v lands %+v", c.Ready, c.Run, c.Lands)
	}
	if r := runJig(t, env, fx.RepoDir, "land", first); r.Code != 0 {
		t.Fatalf("jig land %s: exit %d\n%s", first, r.Code, r.Stdout)
	}
	if c = read(); !c.Ready || len(c.Lands) != 0 || len(c.Run) != 1 || c.Run[0][0] != second {
		t.Errorf("after Land %s: ready %v lands %+v run %v, want %s to run", first, c.Ready, c.Lands, c.Run, second)
	}
}
