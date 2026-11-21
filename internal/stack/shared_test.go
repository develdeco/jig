package stack

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestSharedStack: a task two issues list is planned from the target with no
// PRTarget and each issue's branch in Containers; ReadFor gives the plan with
// that issue as the container, StartFrom unchanged; a task of one issue is
// unchanged by either.
func TestSharedStack(t *testing.T) {
	t.Parallel()
	api := newRepo(t, "workspaces:\n  - id: server\n    path: server\noracles:\n  test: go test ./{path}/...\n")
	st := &store.Store{Root: t.TempDir()}
	add := func(id string, rec store.Ticket) {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
		if err := st.AppendSlices(id, []store.Slice{{ID: "a", Workspace: "server", Goal: "Do it.", Oracle: "test", BlockedBy: []string{}}}); err != nil {
			t.Fatal(err)
		}
	}
	add("I-1", store.Ticket{Title: "One", Kind: store.KindIssue})
	add("I-2", store.Ticket{Title: "Two", Kind: store.KindIssue})
	add("T-S", store.Ticket{Title: "Shared"})
	add("T-1", store.Ticket{Title: "Alone"})
	for name, content := range map[string]string{
		"a": "issue: I-1\ntickets:\n  - id: T-S\n    title: Shared\n  - id: T-1\n    title: Alone\n",
		"b": "issue: I-2\ntickets:\n  - id: T-S\n    title: Shared\n",
	} {
		if err := store.AtomicWrite(st.ChartFile(name), []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	in := Input{
		Store:  st,
		Cfg:    project.Config{Repos: []project.Repo{{Remote: api.origin, Label: "api"}}},
		Clones: map[string]string{"api": api.clone},
		Merges: fakeMerges{},
	}
	branch := func(id string) string {
		rec, _ := st.ReadTicket(id)
		b, err := st.ResolveTicketBranch(id, rec, "main")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b1, b2 := branch("I-1"), branch("I-2")

	plan, err := Read(in, "T-S")
	if err != nil || len(plan.Repos) != 1 || plan.Branch != branch("T-S") {
		t.Fatalf("Read(T-S) = %+v, %v", plan, err)
	}
	want := Repo{Repo: "api", Target: "main", StartFrom: "origin/main", PRTarget: "",
		Containers: []ContainerBranch{{"I-1", b1}, {"I-2", b2}}}
	if !reflect.DeepEqual(plan.Repos[0], want) {
		t.Errorf("shared plan:\n%+v\nwant\n%+v", plan.Repos[0], want)
	}
	for issue, b := range map[string]string{"I-1": b1, "I-2": b2} {
		plan, err := ReadFor(in, "T-S", issue)
		want := Repo{Repo: "api", Target: "main", StartFrom: "origin/main", PRTarget: b, Container: issue, ContainerBranch: b, ContainerFrom: "origin/main"}
		if err != nil || len(plan.Repos) != 1 || !reflect.DeepEqual(plan.Repos[0], want) {
			t.Errorf("ReadFor(T-S, %s) = %+v, %v\nwant %+v", issue, plan.Repos, err, want)
		}
	}
	alone, err := Read(in, "T-1")
	if err != nil || len(alone.Repos) != 1 || alone.Repos[0].PRTarget != b1 || alone.Repos[0].Containers != nil {
		t.Errorf("Read(T-1) = %+v, %v, want the one container's plan", alone, err)
	}
}
