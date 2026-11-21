package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// numberedHost is a pull-request host that numbers the pull requests it opens,
// one URL each, and holds the open ones it is told of (by head and base).
type numberedHost struct {
	repohost.Host
	creates [][2]string // head, base of each pull request opened
	updates []string    // url of each pull request edited
	open    map[[2]string]string
}

func (h *numberedHost) CreatePR(head, base, title, bodyFile string) (string, error) {
	h.creates = append(h.creates, [2]string{head, base})
	return fmt.Sprintf("https://github.com/owner/fixture-repo/pull/%d", 100+len(h.creates)), nil
}

func (h *numberedHost) FindOpenPRInto(head, base string) (repohost.OpenPR, error) {
	if url := h.open[[2]string{head, base}]; url != "" {
		return repohost.OpenPR{URL: url, Base: base}, nil
	}
	return repohost.OpenPR{}, nil
}

func (h *numberedHost) UpdatePR(url, bodyFile, base string) error {
	h.updates = append(h.updates, url)
	return nil
}

// TestContainerPublishAgainUpdatesItsOpenPullRequest: a second publish of a
// published multi-task finds its open pull request into its issue's branch and
// edits it, and opens none.
func TestContainerPublishAgainUpdatesItsOpenPullRequest(t *testing.T) {
	t.Parallel()
	d, _, _ := containerDeps(t)
	const open = "https://github.com/owner/fixture-repo/pull/50"
	host := &numberedHost{open: map[[2]string]string{{multiBranch, issueBranch}: open}}
	d.Host = host
	report, err := PublishContainer(*d, PublishOpts{Ticket: "M-1", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.creates) != 0 || !slices.Equal(host.updates, []string{open}) || !report.PRUpdated["fixture-repo"] || report.PRURL["fixture-repo"] != open {
		t.Errorf("creates %v, updates %v, report %+v, want the open pull request edited and none opened", host.creates, host.updates, report)
	}
}

// TestContainerPublishBodyNamesChildrenInChartOrder: the children are listed in
// the chart's order, a child that has no landed line is named by the pull
// request it opened, a multi-task's own record body is not in its pull request
// (only an issue's is its need), and a task two issues share is named by its
// pull request into this issue.
func TestContainerPublishBodyNamesChildrenInChartOrder(t *testing.T) {
	t.Parallel()
	d, _, _ := containerDeps(t)
	if err := d.Store.CreateTicketRecord("T-4", store.Ticket{Title: "Fourth task", Parent: "M-1"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(d.Store, "T-4", journal.Line{Event: journal.EventPr, Into: "M-1", URL: "https://github.com/owner/fixture-repo/pull/44"}); err != nil {
		t.Fatal(err)
	}
	chart := "issue: I-1\ntickets:\n  - id: M-1\n    title: The multi-task\n  - id: T-4\n    title: Fourth task\n  - id: T-2\n    title: Second task\n  - id: T-1\n    title: First task\n  - id: T-3\n    title: Third task\n"
	if err := store.AtomicWrite(d.Store.ChartFile("c"), []byte(chart)); err != nil {
		t.Fatal(err)
	}
	d.Host = &numberedHost{}
	report, err := PublishContainer(*d, PublishOpts{Ticket: "M-1", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"])))
	want := "# The multi-task\n\n## Children\n\n" +
		"- Fourth task (T-4): https://github.com/owner/fixture-repo/pull/44\n" +
		"- Second task (T-2): https://github.com/owner/fixture-repo/pull/2\n" +
		"- First task (T-1): https://github.com/owner/fixture-repo/pull/1\n"
	if string(body) != want {
		t.Errorf("body:\n%s\nwant:\n%s", body, want)
	}

	// A task two issues list: the issue's body names the pull request into it.
	if err := d.Store.CreateTicketRecord("I-2", store.Ticket{Title: "Other issue", Kind: store.KindIssue, Slug: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AtomicWrite(d.Store.ChartFile("d"), []byte("issue: I-2\ntickets:\n  - id: T-3\n    title: Third task\n")); err != nil {
		t.Fatal(err)
	}
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "I-2", URL: "https://github.com/owner/fixture-repo/pull/33"},
		{Event: journal.EventLanded, Into: "I-2", URL: "https://github.com/owner/fixture-repo/pull/33"},
	} {
		if err := journal.Append(d.Store, "T-3", l); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []journal.Line{{Event: journal.EventPr, Into: "I-1", URL: "https://github.com/owner/fixture-repo/pull/9"}, {Event: journal.EventLanded, Into: "I-1", URL: "https://github.com/owner/fixture-repo/pull/9"}} {
		if err := journal.Append(d.Store, "M-1", l); err != nil {
			t.Fatal(err)
		}
	}
	report, err = PublishContainer(*d, PublishOpts{Ticket: "I-1", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody["fixture-repo"])))
	if !strings.Contains(string(body), "- Third task (T-3): https://github.com/owner/fixture-repo/pull/3\n") || strings.Contains(string(body), "pull/33") {
		t.Errorf("the issue's body must name the shared task's pull request into it:\n%s", body)
	}
}

// addIssue records an issue I-1 with a need, tasks and the chart that lists
// them; each task has landed into I-1 with the pull requests urls names by repo.
func addIssue(t *testing.T, st *store.Store, tasks map[string]map[string]string) {
	t.Helper()
	if err := st.CreateTicketRecord("I-1", store.Ticket{Title: "The issue", Body: "People need the thing.", Kind: store.KindIssue, Slug: "the-issue"}); err != nil {
		t.Fatal(err)
	}
	chart := "issue: I-1\ntickets:\n"
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: "Task " + id}); err != nil {
			t.Fatal(err)
		}
		chart += "  - id: " + id + "\n    title: Task " + id + "\n"
		for repo, url := range tasks[id] {
			for _, l := range []journal.Line{
				{Event: journal.EventPr, Into: "I-1", Repo: repo, URL: url},
				{Event: journal.EventPublishDone, Into: "I-1"},
				{Event: journal.EventLanded, Into: "I-1", Repo: repo, URL: url},
			} {
				if err := journal.Append(st, id, l); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := store.AtomicWrite(st.ChartFile("c"), []byte(chart)); err != nil {
		t.Fatal(err)
	}
}

// TestContainerPublishInSeveralRepos: an issue whose tasks built in two repos
// opens one pull request in each, into the target, each body naming the other
// repo's pull request (not as pending) and listing only its own repo's pull
// request of each child, each pr line naming its repo, and one publish-done after
// both.
func TestContainerPublishInSeveralRepos(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "api", "web")
	d := o.d
	const apiPR, webPR = "https://github.com/owner/api/pull/1", "https://github.com/owner/web/pull/2"
	addIssue(t, d.Store, map[string]map[string]string{"T-1": {"api": apiPR}, "T-2": {"web": webPR}})
	d.Stack = func(ticket string) (stack.Plan, error) {
		plan := stack.Plan{Ticket: ticket}
		for _, name := range []string{"api", "web"} {
			plan.Repos = append(plan.Repos, stack.Repo{Repo: name, Target: "main", StartFrom: "origin/main", PRTarget: "main"})
		}
		return plan, nil
	}
	d.Container = func(string) (ContainerStanding, error) { return ContainerStanding{State: ContainerReady}, nil }
	host := &numberedHost{}
	d.Host = host

	report, err := PublishContainer(d, PublishOpts{Ticket: "I-1", Yes: true})
	if err != nil {
		t.Fatalf("PublishContainer: %v", err)
	}
	if len(host.creates) != 2 {
		t.Fatalf("pull requests opened: %v, want one per repo", host.creates)
	}
	for _, c := range host.creates {
		if c[1] != "main" {
			t.Errorf("a pull request was opened from %s into %s, want main", c[0], c[1])
		}
	}
	apiURL, webURL := report.PRURL["api"], report.PRURL["web"]
	if apiURL == "" || webURL == "" || apiURL == webURL || len(report.Pending) != 0 {
		t.Fatalf("report = %+v, want a distinct pull request in each repo and nothing pending", report)
	}
	read := func(repo string) string {
		b, err := os.ReadFile(filepath.Join(d.Store.Root, filepath.FromSlash(report.PRBody[repo])))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	api, web := read("api"), read("web")
	if !strings.Contains(api, "## Part of I-1\n\n- also web: "+webURL+"\n") || !strings.Contains(web, "## Part of I-1\n\n- also api: "+apiURL+"\n") {
		t.Errorf("each body must name the other repo's pull request:\napi:\n%s\nweb:\n%s", api, web)
	}
	if !strings.Contains(api, "- Task T-1 (T-1): "+apiPR+"\n") || strings.Contains(api, webPR) || !strings.Contains(web, "- Task T-2 (T-2): "+webPR+"\n") || strings.Contains(web, apiPR) {
		t.Errorf("a body lists another repo's pull request of a child:\napi:\n%s\nweb:\n%s", api, web)
	}
	var prRepos []string
	done := 0
	for _, l := range journalEvents(t, &d, "I-1") {
		switch l.Event {
		case journal.EventPr:
			prRepos = append(prRepos, l.Repo)
		case journal.EventPublishDone:
			done++
		}
	}
	if !slices.Equal(prRepos, []string{"api", "web"}) || done != 1 {
		t.Errorf("pr lines for %v and %d publish-done, want api and web and one", prRepos, done)
	}
}
