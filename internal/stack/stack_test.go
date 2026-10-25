package stack

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// TestJudge: a repo with no parent is the target's; one shared with a parent
// that has not merged starts from the parent's branch and goes into it, and is
// behind when the parent's head is not in the ticket's branch; once the parent
// merged it is the target's again, and asks for a retarget while the ticket's
// branch lacks the target's head. The first parent that has not merged is the
// one built on.
func TestJudge(t *testing.T) {
	t.Parallel()
	parent := Parent{Ticket: "JIG-1", Branch: "jig/JIG-1", Ref: "jig/JIG-1", Head: "p1"}
	for _, c := range []struct {
		name string
		f    Facts
		want Repo
	}{
		{"no parent", Facts{Repo: "app", Target: "main", Head: "c1"},
			Repo{Repo: "app", Target: "main", StartFrom: "origin/main", PRTarget: "main", Head: "c1"}},
		{"shared, not started", Facts{Repo: "app", Target: "main", Parents: []Parent{parent}},
			Repo{Repo: "app", Target: "main", StartFrom: "jig/JIG-1", PRTarget: "jig/JIG-1", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1"}},
		{"shared, up to date", Facts{Repo: "app", Target: "main", Head: "c1", Parents: []Parent{withInHead(parent, true)}},
			Repo{Repo: "app", Target: "main", StartFrom: "jig/JIG-1", PRTarget: "jig/JIG-1", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1", Head: "c1"}},
		{"shared, the parent moved", Facts{Repo: "app", Target: "main", Head: "c1", Parents: []Parent{parent}},
			Repo{Repo: "app", Target: "main", StartFrom: "jig/JIG-1", PRTarget: "jig/JIG-1", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1", Head: "c1", Behind: true}},
		{"shared, no copy of the parent's branch", Facts{Repo: "app", Target: "main", Head: "c1", Parents: []Parent{{Ticket: "JIG-1", Branch: "jig/JIG-1"}}},
			Repo{Repo: "app", Target: "main", StartFrom: "origin/jig/JIG-1", PRTarget: "jig/JIG-1", Parent: "JIG-1", ParentBranch: "jig/JIG-1", Head: "c1"}},
		{"the parent merged, the target not merged in", Facts{Repo: "app", Target: "main", Head: "c1", Parents: []Parent{withMerged(parent)}},
			Repo{Repo: "app", Target: "main", StartFrom: "origin/main", PRTarget: "main", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1", Merged: true, Head: "c1", Retarget: true}},
		{"the parent merged, the target merged in", Facts{Repo: "app", Target: "main", Head: "c1", TargetHeld: true, Parents: []Parent{withMerged(parent)}},
			Repo{Repo: "app", Target: "main", StartFrom: "origin/main", PRTarget: "main", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1", Merged: true, Head: "c1"}},
		{"the parent merged before the ticket started", Facts{Repo: "app", Target: "main", Parents: []Parent{withMerged(parent)}},
			Repo{Repo: "app", Target: "main", StartFrom: "origin/main", PRTarget: "main", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1", ParentHead: "p1", Merged: true}},
		{"the first parent merged, the second not", Facts{Repo: "app", Target: "main", Parents: []Parent{withMerged(parent), {Ticket: "JIG-2", Branch: "jig/JIG-2", Ref: "origin/jig/JIG-2", Head: "p2"}}},
			Repo{Repo: "app", Target: "main", StartFrom: "origin/jig/JIG-2", PRTarget: "jig/JIG-2", Parent: "JIG-2", ParentBranch: "jig/JIG-2", ParentRef: "origin/jig/JIG-2", ParentHead: "p2"}},
	} {
		if got := Judge(c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Judge = %+v,\nwant %+v", c.name, got, c.want)
		}
	}
}

func withInHead(p Parent, in bool) Parent { p.InHead = in; return p }
func withMerged(p Parent) Parent          { p.Merged = true; return p }

// TestPlanStackedAndIn: a plan is stacked when a repo of it has a parent, and
// In finds a repo it holds by name; a repo is shared while its parent has not
// merged.
func TestPlanStackedAndIn(t *testing.T) {
	t.Parallel()
	p := Plan{Repos: []Repo{{Repo: "api", Target: "main", PRTarget: "jig/JIG-1", Parent: "JIG-1"}, {Repo: "web", Target: "main", PRTarget: "main"}}}
	if !p.Stacked() {
		t.Errorf("Stacked() = false, want true")
	}
	if r, ok := p.In("api"); !ok || r.PRTarget != "jig/JIG-1" || !r.Shared() {
		t.Errorf("In(api) = %+v, %v, want the shared api", r, ok)
	}
	if r, ok := p.In("web"); !ok || r.Shared() {
		t.Errorf("In(web) = %+v, %v, want web, not shared", r, ok)
	}
	if _, ok := p.In("docs"); ok {
		t.Errorf("In(docs) found a repo the plan does not hold")
	}
	if (Repo{Parent: "JIG-1", Merged: true}).Shared() {
		t.Errorf("a repo whose parent merged is shared")
	}
	if (Plan{Repos: []Repo{{Repo: "web", Target: "main"}}}).Stacked() {
		t.Errorf("a plan with no parent in any repo is stacked")
	}
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo is a clone with an origin: main has one commit, pushed.
type repo struct{ origin, clone string }

func newRepo(t *testing.T, manifest string) repo {
	t.Helper()
	root := t.TempDir()
	r := repo{origin: filepath.Join(root, "origin.git"), clone: filepath.Join(root, "clone")}
	mustGit(t, root, "init", "--bare", "-b", "main", r.origin)
	mustGit(t, root, "init", "-b", "main", r.clone)
	mustGit(t, r.clone, "config", "user.email", "fixture@example.invalid")
	mustGit(t, r.clone, "config", "user.name", "fixture")
	mustGit(t, r.clone, "remote", "add", "origin", r.origin)
	if manifest != "" {
		write(t, filepath.Join(r.clone, ".claude", "jig.yaml"), manifest)
	}
	r.commit(t, "base.txt")
	mustGit(t, r.clone, "push", "origin", "main")
	mustGit(t, r.clone, "fetch", "origin")
	return r
}

// commit adds a file named name to the clone's current branch and returns
// the new head.
func (r repo) commit(t *testing.T, name string) string {
	t.Helper()
	write(t, filepath.Join(r.clone, name), name+"\n")
	mustGit(t, r.clone, "add", "-A")
	mustGit(t, r.clone, "commit", "-m", "add "+name)
	return mustGit(t, r.clone, "rev-parse", "HEAD")
}

// fakeMerges says which tickets have merged.
type fakeMerges map[string]ready.Merge

func (f fakeMerges) Of(ticket string) (ready.Merge, error) { return f[ticket], nil }

// merged is the merge fact of a ticket that published and merged in each of
// repos.
func merged(repos ...string) ready.Merge {
	m := ready.Merge{Published: true}
	for _, r := range repos {
		m.Repos = append(m.Repos, ready.RepoMerge{Repo: r, Met: true, By: ready.ByAncestry})
	}
	return m
}

// stackedStore is a store with JIG-1 ("Parent", branch JIG-1-parent) and
// JIG-2 ("Child", branch JIG-2-child) stacked on it, each with the slices
// given as workspaces.
func stackedStore(t *testing.T, parentWS, childWS []string) *store.Store {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	add := func(id, title string, blockers []store.TicketBlockedBy, ws []string) {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: title, BlockedBy: blockers}); err != nil {
			t.Fatal(err)
		}
		var slices []store.Slice
		for i, w := range ws {
			slices = append(slices, store.Slice{ID: string(rune('a' + i)), Workspace: w, Goal: "Do it.", Oracle: "test", BlockedBy: []string{}})
		}
		if err := st.AppendSlices(id, slices); err != nil {
			t.Fatal(err)
		}
	}
	add("JIG-1", "Parent", nil, parentWS)
	add("JIG-2", "Child", []store.TicketBlockedBy{{Ticket: "JIG-1", Kind: KindStacked}}, childWS)
	return st
}

// TestReadStartsFromTheParentsBranchInASharedRepo: in a project of two repos, a
// ticket stacked on a parent that builds in one of them starts from the
// parent's branch there and goes into it, and from its target in the other;
// the parent's branch moving past the child's makes it behind, and merging it
// in makes it level; the parent merging makes the shared repo the target's,
// asking for a retarget until the child's branch holds the target's head.
func TestReadStartsFromTheParentsBranchInASharedRepo(t *testing.T) {
	t.Parallel()
	api := newRepo(t, "workspaces:\n  - id: server\n    path: server\noracles:\n  test: go test ./{path}/...\n")
	web := newRepo(t, "workspaces:\n  - id: ui\n    path: ui\noracles:\n  test: go test ./{path}/...\n")
	st := stackedStore(t, []string{"server"}, []string{"server", "ui"})
	in := Input{
		Store:  st,
		Cfg:    project.Config{Repos: []project.Repo{{Remote: api.origin, Label: "api"}, {Remote: web.origin, Label: "web"}}},
		Clones: map[string]string{"api": api.clone, "web": web.clone},
		Merges: fakeMerges{},
	}

	// The parent's branch is in the api clone, local and pushed; the child has
	// not started.
	mustGit(t, api.clone, "switch", "-c", "JIG-1-parent")
	parentHead := api.commit(t, "server/parent.txt")
	mustGit(t, api.clone, "push", "origin", "JIG-1-parent")
	plan, err := Read(in, "JIG-2")
	if err != nil {
		t.Fatal(err)
	}
	want := []Repo{
		{Repo: "api", Target: "main", StartFrom: "JIG-1-parent", PRTarget: "JIG-1-parent", Parent: "JIG-1", ParentBranch: "JIG-1-parent", ParentRef: "JIG-1-parent", ParentHead: parentHead},
		{Repo: "web", Target: "main", StartFrom: "origin/main", PRTarget: "main"},
	}
	if plan.Branch != "JIG-2-child" || !reflect.DeepEqual(plan.Repos, want) {
		t.Fatalf("Read = %+v,\nwant branch JIG-2-child and %+v", plan, want)
	}

	// The child branches from the parent and builds: level with it.
	mustGit(t, api.clone, "switch", "-c", "JIG-2-child")
	childHead := api.commit(t, "server/child.txt")
	plan, _ = Read(in, "JIG-2")
	if r, _ := plan.In("api"); r.Head != childHead || r.Behind {
		t.Errorf("after the child built on the parent: api = %+v, want head %s and not behind", r, childHead)
	}

	// The parent gets a commit: the child is behind it, until it merges it in.
	mustGit(t, api.clone, "switch", "JIG-1-parent")
	moved := api.commit(t, "server/fix.txt")
	mustGit(t, api.clone, "switch", "JIG-2-child")
	plan, _ = Read(in, "JIG-2")
	if r, _ := plan.In("api"); !r.Behind || r.ParentHead != moved || r.StartFrom != "JIG-1-parent" {
		t.Errorf("after the parent moved: api = %+v, want behind the parent's head %s", r, moved)
	}
	mustGit(t, api.clone, "merge", "--no-edit", "JIG-1-parent")
	plan, _ = Read(in, "JIG-2")
	if r, _ := plan.In("api"); r.Behind {
		t.Errorf("after the child merged the parent: api = %+v, want level", r)
	}

	// The parent merges, with a merge commit as a host makes one: the api repo is
	// the target's again, and asks for a retarget until the child holds origin/main's head.
	in.Merges = fakeMerges{"JIG-1": merged("api")}
	mustGit(t, api.clone, "switch", "main")
	mustGit(t, api.clone, "merge", "--no-ff", "--no-edit", "JIG-1-parent")
	mustGit(t, api.clone, "push", "origin", "main")
	mustGit(t, api.clone, "fetch", "origin")
	mustGit(t, api.clone, "switch", "JIG-2-child")
	plan, _ = Read(in, "JIG-2")
	r, _ := plan.In("api")
	if !r.Merged || r.StartFrom != "origin/main" || r.PRTarget != "main" || !r.Retarget || r.Behind {
		t.Errorf("after the parent merged: api = %+v, want merged, the target's, asking for a retarget", r)
	}
	mustGit(t, api.clone, "merge", "--no-edit", "origin/main")
	plan, _ = Read(in, "JIG-2")
	if r, _ := plan.In("api"); r.Retarget {
		t.Errorf("after the child merged the target: api = %+v, want no retarget", r)
	}
}

// TestReadTakesTheNewerCopyOfTheParentsBranch: a parent's branch the clone
// holds only on its origin, or whose origin copy is ahead of the local one, is
// read from the origin; a local branch ahead of the origin's is read locally.
func TestReadTakesTheNewerCopyOfTheParentsBranch(t *testing.T) {
	t.Parallel()
	app := newRepo(t, "")
	st := stackedStore(t, []string{"root"}, []string{"root"})
	in := Input{Store: st, Cfg: project.Config{Repos: []project.Repo{{Remote: app.origin, Label: "app"}}}, Clones: map[string]string{"app": app.clone}}

	mustGit(t, app.clone, "switch", "-c", "JIG-1-parent")
	first := app.commit(t, "parent.txt")
	mustGit(t, app.clone, "push", "origin", "JIG-1-parent")
	mustGit(t, app.clone, "switch", "main")
	mustGit(t, app.clone, "branch", "-D", "JIG-1-parent")
	plan, err := Read(in, "JIG-2")
	if err != nil {
		t.Fatal(err)
	}
	if r := plan.Repos[0]; r.StartFrom != "origin/JIG-1-parent" || r.ParentHead != first || r.PRTarget != "JIG-1-parent" {
		t.Errorf("origin only: %+v, want origin/JIG-1-parent at %s", r, first)
	}

	// A local branch behind the origin's: the origin's is newer.
	mustGit(t, app.clone, "branch", "JIG-1-parent", "main")
	plan, _ = Read(in, "JIG-2")
	if r := plan.Repos[0]; r.StartFrom != "origin/JIG-1-parent" || r.ParentHead != first {
		t.Errorf("local behind origin: %+v, want origin/JIG-1-parent at %s", r, first)
	}

	// A local branch ahead of the origin's: the local one.
	mustGit(t, app.clone, "switch", "JIG-1-parent")
	mustGit(t, app.clone, "merge", "--ff-only", "origin/JIG-1-parent")
	ahead := app.commit(t, "more.txt")
	plan, _ = Read(in, "JIG-2")
	if r := plan.Repos[0]; r.StartFrom != "JIG-1-parent" || r.ParentHead != ahead {
		t.Errorf("local ahead of origin: %+v, want JIG-1-parent at %s", r, ahead)
	}
}

// TestReadOfATicketStackedOnNothing: a ticket whose blockers are all merged
// ones, or whose stacked parent is closed, has an empty plan; so does one with
// no blockers.
func TestReadOfATicketStackedOnNothing(t *testing.T) {
	t.Parallel()
	app := newRepo(t, "")
	st := &store.Store{Root: t.TempDir()}
	for id, rec := range map[string]store.Ticket{
		"JIG-1": {Title: "Parent"},
		"JIG-2": {Title: "Waits", BlockedBy: []store.TicketBlockedBy{{Ticket: "JIG-1", Kind: "merged"}}},
		"JIG-3": {Title: "Closed parent", Closed: &store.Closed{Reason: store.CloseNotPlanned}},
		"JIG-4": {Title: "On a closed one", BlockedBy: []store.TicketBlockedBy{{Ticket: "JIG-3", Kind: KindStacked}}},
		"JIG-5": {Title: "Free"},
	} {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	in := Input{Store: st, Cfg: project.Config{Repos: []project.Repo{{Remote: app.origin, Label: "app"}}}, Clones: map[string]string{"app": app.clone}}
	for _, id := range []string{"JIG-2", "JIG-4", "JIG-5"} {
		plan, err := Read(in, id)
		if err != nil {
			t.Fatalf("Read(%s): %v", id, err)
		}
		if plan.Stacked() || len(plan.Repos) != 0 {
			t.Errorf("Read(%s) = %+v, want an empty plan", id, plan)
		}
	}
}
