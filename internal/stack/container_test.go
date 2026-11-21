package stack

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestContainerJudge: a ticket with a container goes into the container's
// branch; it starts from the target with no dependency and from the container
// behind a merged dependency or a settled stacked parent; an unmerged stacked
// parent is built on as before; the retarget test compares with the container.
func TestContainerJudge(t *testing.T) {
	t.Parallel()
	parent := Parent{Ticket: "JIG-1", Branch: "jig/JIG-1", Ref: "jig/JIG-1", Head: "p1"}
	ctr := func(f Facts) Facts { f.Container, f.ContainerBranch = "I-1", "I-1-issue"; return f }
	for _, c := range []struct {
		name string
		f    Facts
		want Repo
	}{
		{"no dependency", ctr(Facts{Repo: "app", Target: "main"}),
			Repo{Repo: "app", Target: "main", StartFrom: "origin/main", PRTarget: "I-1-issue", Container: "I-1", ContainerBranch: "I-1-issue"}},
		{"a merged dependency", ctr(Facts{Repo: "app", Target: "main", MergedDep: true}),
			Repo{Repo: "app", Target: "main", StartFrom: "origin/I-1-issue", PRTarget: "I-1-issue", Container: "I-1", ContainerBranch: "I-1-issue"}},
		{"a multi-task starts from its container", ctr(Facts{Repo: "app", Target: "main", FromContainer: true}),
			Repo{Repo: "app", Target: "main", StartFrom: "origin/I-1-issue", PRTarget: "I-1-issue", Container: "I-1", ContainerBranch: "I-1-issue"}},
		{"an unmerged stacked parent", ctr(Facts{Repo: "app", Target: "main", Parents: []Parent{parent}}),
			Repo{Repo: "app", Target: "main", StartFrom: "jig/JIG-1", PRTarget: "jig/JIG-1", Container: "I-1", ContainerBranch: "I-1-issue", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1"}},
		{"a settled stacked parent", ctr(Facts{Repo: "app", Target: "main", TargetHeld: true, Head: "c1", Parents: []Parent{withMerged(parent)}}),
			Repo{Repo: "app", Target: "main", StartFrom: "origin/I-1-issue", PRTarget: "I-1-issue", Container: "I-1", ContainerBranch: "I-1-issue", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1", Merged: true, Head: "c1"}},
		{"a settled parent, the container not held", ctr(Facts{Repo: "app", Target: "main", Head: "c1", Parents: []Parent{withMerged(parent)}}),
			Repo{Repo: "app", Target: "main", StartFrom: "origin/I-1-issue", PRTarget: "I-1-issue", Container: "I-1", ContainerBranch: "I-1-issue", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1", Merged: true, Head: "c1", Retarget: true}},
		{"a task that started from the target is not behind its container", ctr(Facts{Repo: "app", Target: "main", Head: "c1"}),
			Repo{Repo: "app", Target: "main", StartFrom: "origin/main", PRTarget: "I-1-issue", Container: "I-1", ContainerBranch: "I-1-issue", Head: "c1"}},
	} {
		if got := Judge(c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Judge = %+v,\nwant %+v", c.name, got, c.want)
		}
	}
}

// containerStore is a store with an issue I-1 whose chart lists T-1, T-2 (a
// merged dependent of T-1), a multi-task M-1 and T-3 under it; L-1 is listed by
// a legacy chart.
func containerStore(t *testing.T) *store.Store {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	add := func(id string, rec store.Ticket) {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
		slices := []store.Slice{{ID: "a", Workspace: "server", Goal: "Do it.", Oracle: "test", BlockedBy: []string{}}}
		if err := st.AppendSlices(id, slices); err != nil {
			t.Fatal(err)
		}
	}
	add("I-1", store.Ticket{Title: "Issue", Kind: store.KindIssue})
	add("T-1", store.Ticket{Title: "One"})
	add("T-2", store.Ticket{Title: "Two", BlockedBy: []store.TicketBlockedBy{{Ticket: "T-1"}}})
	add("M-1", store.Ticket{Title: "Multi", Kind: store.KindMultiTask})
	add("T-3", store.Ticket{Title: "Three", Parent: "M-1"})
	add("L-1", store.Ticket{Title: "Legacy"})
	files := map[string]string{
		"c":      "issue: I-1\ntickets:\n  - id: T-1\n    title: One\n  - id: T-2\n    title: Two\n  - id: M-1\n    title: Multi\n    kind: multi-task\n  - id: T-3\n    title: Three\n",
		"legacy": "tickets:\n  - id: L-1\n    title: Legacy\n",
	}
	for name, content := range files {
		if err := store.AtomicWrite(st.ChartFile(name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// TestContainerRead: in a chart with an issue, a task's pull request goes into
// the issue's branch and starts from the target, a task merged on it starts
// from the issue's branch, a task under a multi-task goes into the
// multi-task's branch, the multi-task and the issue have a plan of their own
// branch, and a ticket of a legacy chart has none.
func TestContainerRead(t *testing.T) {
	t.Parallel()
	api := newRepo(t, "workspaces:\n  - id: server\n    path: server\noracles:\n  test: go test ./{path}/...\n")
	st := containerStore(t)
	in := Input{
		Store:  st,
		Cfg:    project.Config{Repos: []project.Repo{{Remote: api.origin, Label: "api"}}},
		Clones: map[string]string{"api": api.clone},
		Merges: fakeMerges{},
	}
	branch := func(id string) string {
		rec, err := st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := st.ResolveTicketBranch(id, rec, "main")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	issue, multi := branch("I-1"), branch("M-1")
	for _, c := range []struct {
		ticket string
		want   Repo
	}{
		{"T-1", Repo{Repo: "api", Target: "main", StartFrom: "origin/main", PRTarget: issue, Container: "I-1", ContainerBranch: issue, ContainerFrom: "origin/main"}},
		{"T-2", Repo{Repo: "api", Target: "main", StartFrom: "origin/" + issue, PRTarget: issue, Container: "I-1", ContainerBranch: issue, ContainerFrom: "origin/main"}},
		{"T-3", Repo{Repo: "api", Target: "main", StartFrom: "origin/main", PRTarget: multi, Container: "M-1", ContainerBranch: multi, ContainerFrom: "origin/" + issue}},
		{"M-1", Repo{Repo: "api", Target: "main", StartFrom: "origin/" + issue, PRTarget: issue, Container: "I-1", ContainerBranch: issue, ContainerFrom: "origin/main"}},
		{"I-1", Repo{Repo: "api", Target: "main", StartFrom: "origin/main", PRTarget: "main"}},
	} {
		plan, err := Read(in, c.ticket)
		if err != nil {
			t.Fatalf("%s: %v", c.ticket, err)
		}
		if plan.Branch != branch(c.ticket) || !reflect.DeepEqual(plan.Repos, []Repo{c.want}) {
			t.Errorf("%s: plan = %+v,\nwant branch %q, repo %+v", c.ticket, plan, branch(c.ticket), c.want)
		}
		if plan.HasContainer() != (c.ticket != "I-1") {
			t.Errorf("%s: HasContainer = %v", c.ticket, plan.HasContainer())
		}
	}
	plan, err := Read(in, "L-1")
	if err != nil || len(plan.Repos) != 0 || plan.Branch != "" {
		t.Errorf("a ticket of a legacy chart: plan = %+v, %v; want none", plan, err)
	}
}

// TestContainerReadStartsFromTheContainerOnlyBehindADependencyOfIt: a merged
// dependency outside the task's container (no chart's, another issue's) leaves
// the task starting from the target; one of the same container, or of the same
// multi-task, starts it from the container's branch.
func TestContainerReadStartsFromTheContainerOnlyBehindADependencyOfIt(t *testing.T) {
	t.Parallel()
	api := newRepo(t, "workspaces:\n  - id: server\n    path: server\noracles:\n  test: go test ./{path}/...\n")
	st := containerStore(t)
	add := func(id string, rec store.Ticket) {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
		if err := st.AppendSlices(id, []store.Slice{{ID: "a", Workspace: "server", Goal: "Do it.", Oracle: "test", BlockedBy: []string{}}}); err != nil {
			t.Fatal(err)
		}
	}
	dep := func(on string) []store.TicketBlockedBy { return []store.TicketBlockedBy{{Ticket: on}} }
	add("I-2", store.Ticket{Title: "Other issue", Kind: store.KindIssue})
	add("X-1", store.Ticket{Title: "Other chart's"})
	add("U-1", store.Ticket{Title: "No chart's"})
	add("D-1", store.Ticket{Title: "Waits for another chart's", BlockedBy: dep("X-1")})
	add("D-2", store.Ticket{Title: "Waits for no chart's", BlockedBy: dep("U-1")})
	add("D-3", store.Ticket{Title: "Waits for one under a multi-task", Parent: "M-1", BlockedBy: dep("T-3")})
	add("D-4", store.Ticket{Title: "Waits for a task of the issue", Parent: "M-1", BlockedBy: dep("T-1")})
	add("M-2", store.Ticket{Title: "Sibling multi", Kind: store.KindMultiTask})
	add("T-4", store.Ticket{Title: "Under the sibling", Parent: "M-2"})
	add("D-5", store.Ticket{Title: "Waits for the sibling's task", Parent: "M-1", BlockedBy: dep("T-4")})
	chart := "issue: I-1\ntickets:\n  - id: T-1\n    title: One\n  - id: T-2\n    title: Two\n  - id: M-1\n    title: Multi\n    kind: multi-task\n  - id: T-3\n    title: Three\n  - id: D-1\n    title: d1\n  - id: D-2\n    title: d2\n  - id: D-3\n    title: d3\n  - id: D-4\n    title: d4\n"
	if err := store.AtomicWrite(st.ChartFile("c"), []byte(chart)); err != nil {
		t.Fatal(err)
	}
	if err := store.AtomicWrite(st.ChartFile("other"), []byte("issue: I-2\ntickets:\n  - id: X-1\n    title: x\n")); err != nil {
		t.Fatal(err)
	}
	in := Input{
		Store:  st,
		Cfg:    project.Config{Repos: []project.Repo{{Remote: api.origin, Label: "api"}}},
		Clones: map[string]string{"api": api.clone},
		Merges: fakeMerges{},
	}
	for _, c := range []struct {
		ticket    string
		container bool
	}{
		{"D-1", false}, {"D-2", false}, {"D-3", true}, {"D-4", true}, {"D-5", false}, {"T-2", true},
	} {
		plan, err := Read(in, c.ticket)
		if err != nil || len(plan.Repos) != 1 {
			t.Fatalf("%s: plan = %+v, %v", c.ticket, plan, err)
		}
		r := plan.Repos[0]
		if got := r.StartFrom == "origin/"+r.ContainerBranch; got != c.container {
			t.Errorf("%s: StartFrom = %q, want the container's branch (%q): %v", c.ticket, r.StartFrom, r.ContainerBranch, c.container)
		}
	}
}

// TestContainerReadRetargetsAgainstTheContainer: a task stacked on a parent that
// merged goes into the container's branch, and is to merge it while its branch
// does not hold the container's head.
func TestContainerReadRetargetsAgainstTheContainer(t *testing.T) {
	t.Parallel()
	api := newRepo(t, "workspaces:\n  - id: server\n    path: server\noracles:\n  test: go test ./{path}/...\n")
	st := containerStore(t)
	if err := st.CreateTicketRecord("S-1", store.Ticket{Title: "Stacked", BlockedBy: []store.TicketBlockedBy{{Ticket: "T-1", Kind: KindStacked}}}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSlices("S-1", []store.Slice{{ID: "a", Workspace: "server", Goal: "Do it.", Oracle: "test", BlockedBy: []string{}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AtomicWrite(st.ChartFile("c"), []byte("issue: I-1\ntickets:\n  - id: T-1\n    title: One\n  - id: S-1\n    title: Stacked\n")); err != nil {
		t.Fatal(err)
	}
	in := Input{
		Store:  st,
		Cfg:    project.Config{Repos: []project.Repo{{Remote: api.origin, Label: "api"}}},
		Clones: map[string]string{"api": api.clone},
		Merges: fakeMerges{"T-1": merged("api")},
	}
	rec, _ := st.ReadTicket("I-1")
	issue, err := st.ResolveTicketBranch("I-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	child := "S-1-stacked"
	mustGit(t, api.clone, "switch", "-c", child)
	api.commit(t, "server/child.txt")
	mustGit(t, api.clone, "switch", "main")
	mustGit(t, api.clone, "switch", "-c", issue)
	api.commit(t, "server/issue.txt")
	mustGit(t, api.clone, "push", "origin", issue)
	mustGit(t, api.clone, "fetch", "origin")

	plan, err := Read(in, "S-1")
	if err != nil || len(plan.Repos) != 1 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if got := plan.Repos[0]; got.PRTarget != issue || got.StartFrom != "origin/"+issue || !got.Retarget {
		t.Errorf("repo = %+v, want a retarget into %q", got, issue)
	}
}
