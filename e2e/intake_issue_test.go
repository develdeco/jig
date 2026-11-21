package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestIntakeIssueFlow drives intake's two verdicts with the real binary: the
// one step (`jig chart new --need`) makes an issue and its chart; a brief
// verdict writes one entry and graduates it to a task with a brief to approve,
// a chart verdict graduates several entries; both charts validate, the issue
// of each lists its tasks, and a refused name or an empty need mints nothing.
func TestIntakeIssueFlow(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	// Refusals mint nothing.
	before, _ := st.Tickets()
	for _, args := range [][]string{
		{"chart", "new", "Bad Name", "--title", "T", "--need", "n"},
		{"chart", "new", "ok", "--title", "T", "--need", " "},
	} {
		if r := runJig(t, env, fx.StoreDir, args...); r.Code == 0 {
			t.Fatalf("jig %s: exit 0, want a refusal\n%s", strings.Join(args, " "), r.Stdout)
		}
	}
	if ids, _ := st.Tickets(); len(ids) != len(before) {
		t.Fatalf("a refusal minted: tickets %v, were %v", ids, before)
	}

	step := func(name, title, need string) string {
		t.Helper()
		if r := runJig(t, env, fx.StoreDir, "chart", "new", name, "--title", title, "--need", need); r.Code != 0 {
			t.Fatalf("jig chart new %s --need: exit %d\n%s", name, r.Code, r.Stdout)
		}
		issue, err := st.ChartIssue(name)
		if err != nil || issue == "" {
			t.Fatalf("chart %s records issue %q, %v", name, issue, err)
		}
		rec, err := st.ReadTicket(issue)
		if err != nil || rec.KindOrDefault() != store.KindIssue || rec.Body != need {
			t.Fatalf("issue %s = %+v, %v, want an issue with the need as body", issue, rec, err)
		}
		if r := runJig(t, env, fx.StoreDir, "validate", issue); r.Code != 0 {
			t.Fatalf("jig validate %s: exit %d\n%s", issue, r.Code, r.Stdout)
		}
		return issue
	}
	write := func(name, issue, entries string) {
		t.Helper()
		if err := os.WriteFile(st.ChartFile(name), []byte("issue: "+issue+"\ntickets:\n"+entries), 0o644); err != nil {
			t.Fatal(err)
		}
		if r := runJig(t, env, fx.StoreDir, "validate", name); r.Code != 0 {
			t.Fatalf("jig validate %s: exit %d\n%s", name, r.Code, r.Stdout)
		}
		if r := runJig(t, env, fx.StoreDir, "graduate", name); r.Code != 0 {
			t.Fatalf("jig graduate %s: exit %d\n%s", name, r.Code, r.Stdout)
		}
	}

	// A brief verdict: one entry, one task, a brief to approve.
	issue := step("fix-login", "Fix login", "Logging in with a plus sign in the email fails.")
	write("fix-login", issue, "  - title: Fix login with a plus sign\n    body: Accept a plus sign in the email.\n")
	entries, err := st.ReadChart("fix-login")
	if err != nil || len(entries) != 1 || entries[0].ID == "" {
		t.Fatalf("fix-login entries = %+v, %v", entries, err)
	}
	task := nextJSON[kindNextOut](t, env, fx.RepoDir, entries[0].ID)
	if task.Ticket != entries[0].ID || task.Phase == nil {
		t.Errorf("jig next %s = %+v, want a task in its brief phase", entries[0].ID, task)
	}
	// An issue that has a chart answers its run: the chart's standing.
	root := nextJSON[issueStanding](t, env, fx.RepoDir, issue)
	if root.Chart != "fix-login" || len(root.Tickets) != 1 || root.Issue == nil || root.Issue.Ticket != issue {
		t.Errorf("jig next %s = %+v, want the run of chart fix-login with 1 task", issue, root)
	}

	// A chart verdict: several entries, graduated under the issue.
	issue2 := step("checkout", "Checkout rework", "Checkout is slow and confusing.")
	write("checkout", issue2, "  - title: Speed up totals\n  - title: Clearer steps\n  - title: Tidy the receipt\n    blocked_by:\n      - ref: \"#2\"\n")
	root2 := nextJSON[issueStanding](t, env, fx.RepoDir, issue2)
	if root2.Chart != "checkout" || len(root2.Tickets) != 3 || root2.Issue == nil || root2.Issue.Ticket != issue2 {
		t.Errorf("jig next %s = %+v, want the run of chart checkout with 3 tasks", issue2, root2)
	}
}

// TestIntakeIssueExistingTicket: intake on a backlog ticket makes the issue
// and its chart, and the chart's one entry carries the ticket's id, so
// graduate keeps it and mints no second task.
func TestIntakeIssueExistingTicket(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if r := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "Backlog item"); r.Code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", r.Code, r.Stdout)
	}
	all, _ := st.Tickets()
	backlog := all[len(all)-1]
	if r := runJig(t, env, fx.StoreDir, "chart", "new", "backlog", "--title", "Backlog item", "--need", "Do the backlog item."); r.Code != 0 {
		t.Fatalf("jig chart new: exit %d\n%s", r.Code, r.Stdout)
	}
	issue, _ := st.ChartIssue("backlog")
	before, _ := st.Tickets()
	if err := os.WriteFile(st.ChartFile("backlog"), []byte("issue: "+issue+"\ntickets:\n  - id: "+backlog+"\n    title: Backlog item\n    body: Its part.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := runJig(t, env, fx.StoreDir, "graduate", "backlog"); r.Code != 0 {
		t.Fatalf("jig graduate: exit %d\n%s", r.Code, r.Stdout)
	}
	if after, _ := st.Tickets(); len(after) != len(before) {
		t.Errorf("graduate minted a second task: %v, were %v", after, before)
	}
	entries, err := st.ReadChart("backlog")
	if err != nil || len(entries) != 1 || entries[0].ID != backlog {
		t.Errorf("entries = %+v, %v, want the one entry keeping %s", entries, err, backlog)
	}
}
