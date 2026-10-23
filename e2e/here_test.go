package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
)

// hereOut is the part of `jig here --json` these tests read.
type hereOut struct {
	How     string `json:"how"`
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Mapped  bool   `json:"mapped"`
	Store   struct {
		Path   string `json:"path"`
		Remote string `json:"remote"`
	} `json:"store"`
	Repo struct {
		Name   string `json:"name"`
		Dir    string `json:"dir"`
		Branch string `json:"branch"`
	} `json:"repo"`
	Ticket struct {
		ID  string `json:"id"`
		How string `json:"how"`
	} `json:"ticket"`
	Slice struct {
		ID  string `json:"id"`
		How string `json:"how"`
	} `json:"slice"`
	Session struct {
		ID  string `json:"id"`
		How string `json:"how"`
	} `json:"session"`
	State struct {
		Slices []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"slices"`
		Questions []struct {
			ID string `json:"id"`
		} `json:"questions"`
	} `json:"state"`
	Explain []string `json:"explain"`
}

// hereJSON runs `jig here --json` plus args in cwd and decodes its answer.
func hereJSON(t *testing.T, env jigEnv, cwd string, args ...string) hereOut {
	t.Helper()
	r := runJig(t, env, cwd, append([]string{"here", "--json"}, args...)...)
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("jig here --json in %s: exit %d\nstdout:\n%s\nstderr:\n%s", cwd, r.Code, r.Stdout, r.Stderr)
	}
	var out hereOut
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("jig here --json in %s: not one JSON object: %v\n%s", cwd, err, r.Stdout)
	}
	return out
}

func sameDir(t *testing.T, what, got, want string) {
	t.Helper()
	if !gitx.SameDir(got, want) {
		t.Errorf("%s = %q, want the directory %q", what, got, want)
	}
}

// TestHereTwoRepoProject: a project with two repos, a mapped clone of the
// second on a ticket branch. `jig here` names the project, the store, the
// repo, the ticket the branch binds and the ticket's slices, from the clone's
// root, from a directory in it, and from a linked worktree of it.
func TestHereTwoRepoProject(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{SecondRepo: true})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	gitLog(t, fx.SecondRepoDir, "switch", "-c", "jig/JIG-1")

	r := runJig(t, env, fx.SecondRepoDir, "here")
	if r.Code != 0 {
		t.Fatalf("jig here exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{
		"here:\n",
		"  project: fixture\n",
		"  how: map-path\n",
		"  store: " + axi.Quote(fx.StoreDir) + "\n",
		"  repo: second-repo\n",
		"  repo_dir: " + axi.Quote(fx.SecondRepoDir) + "\n",
		"  branch: jig/JIG-1\n",
		"  ticket: JIG-1\n",
		"  ticket_how: branch\n",
		"slices[5]{id,state,attempts,blocked_by,question}:\n",
		"  a,queued,0,-,-\n",
		"  b,queued,0,a,-\n",
		"  e,queued,0,-,-\n",
		"questions: none\n",
		"help[1]:\n",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig here output lacks %q:\n%s", want, r.Stdout)
		}
	}

	got := hereJSON(t, env, fx.SecondRepoDir)
	if got.How != "map-path" || got.Project != "fixture" || got.Kind != "project" || got.Mapped {
		t.Errorf("how = %q, project = %q, kind = %q, mapped = %v, want map-path, fixture, project, false", got.How, got.Project, got.Kind, got.Mapped)
	}
	sameDir(t, "store.path", got.Store.Path, fx.StoreDir)
	if got.Repo.Name != "second-repo" || got.Repo.Branch != "jig/JIG-1" || got.Ticket.ID != "JIG-1" || got.Ticket.How != "branch" {
		t.Errorf("repo = %+v, ticket = %+v, want second-repo on jig/JIG-1 binding JIG-1 by branch", got.Repo, got.Ticket)
	}
	if len(got.State.Slices) != 5 || got.State.Slices[4].ID != "e" || got.State.Slices[4].State != "queued" {
		t.Errorf("slices = %+v, want a to e with e queued", got.State.Slices)
	}

	// From a directory inside the clone.
	sub := filepath.Join(fx.SecondRepoDir, "gamma")
	if got := hereJSON(t, env, sub); got.Repo.Name != "second-repo" || got.Ticket.ID != "JIG-1" || got.How != "map-path" {
		t.Errorf("from %s: repo = %+v, ticket = %+v, how = %q", sub, got.Repo, got.Ticket, got.How)
	}

	// From a linked worktree: it reads as its clone, on its own branch.
	wt := filepath.Join(t.TempDir(), "wt")
	gitLog(t, fx.SecondRepoDir, "worktree", "add", "-b", "jig/JIG-1-follow-up", wt)
	got = hereJSON(t, env, wt)
	if got.How != "map-path" || got.Repo.Name != "second-repo" || got.Repo.Branch != "jig/JIG-1-follow-up" || got.Ticket.ID != "JIG-1" {
		t.Errorf("from a worktree: how = %q, repo = %+v, ticket = %+v, want second-repo on jig/JIG-1-follow-up binding JIG-1", got.How, got.Repo, got.Ticket)
	}
	sameDir(t, "worktree repo.dir", got.Repo.Dir, fx.SecondRepoDir)

	// The first repo's clone is on main: no ticket by branch, and a positional
	// one names it.
	if got := hereJSON(t, env, fx.RepoDir); got.Repo.Name != "fixture-repo" || got.Repo.Branch != "main" || got.Ticket.ID != "" {
		t.Errorf("first repo: repo = %+v, ticket = %+v, want fixture-repo on main with no ticket", got.Repo, got.Ticket)
	}
	if got := hereJSON(t, env, fx.RepoDir, "JIG-1"); got.Ticket.ID != "JIG-1" || got.Ticket.How != "arg" || len(got.State.Slices) != 5 {
		t.Errorf("positional: ticket = %+v, %d slices, want JIG-1 by arg with 5 slices", got.Ticket, len(got.State.Slices))
	}
	if r := runJig(t, env, fx.RepoDir, "here", "JIG-99"); r.Code == 0 || !strings.Contains(r.Stdout, "JIG-99") {
		t.Errorf("jig here JIG-99: exit %d, want a refusal naming the ticket:\n%s", r.Code, r.Stdout)
	}

	// Slice and session come from the flags, then the environment.
	if got := hereJSON(t, env.with("JIG_SLICE=b", "JIG_SESSION=s-env"), fx.SecondRepoDir); got.Slice.ID != "b" || got.Slice.How != "env" || got.Session.ID != "s-env" || got.Session.How != "env" {
		t.Errorf("env: slice = %+v, session = %+v, want b and s-env by env", got.Slice, got.Session)
	}
	if got := hereJSON(t, env.with("JIG_SLICE=b"), fx.SecondRepoDir, "--slice", "e", "--session", "s-flag"); got.Slice.ID != "e" || got.Slice.How != "arg" || got.Session.ID != "s-flag" {
		t.Errorf("flags: slice = %+v, session = %+v, want e and s-flag by flag", got.Slice, got.Session)
	}

	// --explain says how, and the store was neither synced nor written to.
	r = runJig(t, env, fx.SecondRepoDir, "here", "--explain")
	if !strings.Contains(r.Stdout, "explain[") || !strings.Contains(r.Stdout, "map-path:") {
		t.Errorf("jig here --explain lacks the explain block:\n%s", r.Stdout)
	}
	if r := runJig(t, env, fx.SecondRepoDir, "here", "--json"); strings.Contains(r.Stdout, `"explain"`) {
		t.Errorf("jig here --json carries explain without --explain:\n%s", r.Stdout)
	}
	if got := hereJSON(t, env, fx.SecondRepoDir, "--explain"); len(got.Explain) == 0 {
		t.Error("jig here --json --explain carries no explain lines")
	}
	if status := gitLog(t, fx.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("jig here left the store dirty:\n%s", status)
	}
}

// TestHereSilentOutside: a directory in no project gets no output at all and
// exit 0, so a hook can run jig here everywhere.
func TestHereSilentOutside(t *testing.T) {
	t.Parallel()
	_, env := newFixture(t, fixture.Opts{})
	outside := t.TempDir()
	for _, args := range [][]string{{"here"}, {"here", "--json"}, {"here", "JIG-1"}} {
		r := runJig(t, env, outside, args...)
		if r.Code != 0 || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("jig %s outside a project: exit %d, stdout %q, stderr %q, want 0 and nothing", strings.Join(args, " "), r.Code, r.Stdout, r.Stderr)
		}
	}
	if r := runJig(t, env, outside, "here", "--explain"); r.Code != 0 || !strings.Contains(r.Stdout, "none:") {
		t.Errorf("jig here --explain outside a project: exit %d, stdout %q, want the explain block", r.Code, r.Stdout)
	}
}

// TestHereFreshCloneByRemote: a fresh clone of a repo a mapped project lists
// is recognized by its origin remote, and recorded in projects.yaml so the next
// call finds it by path.
func TestHereFreshCloneByRemote(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	// This machine knows the project and its store but not where the repo is.
	if err := project.UpdateMachine(env.home, func(m map[string]project.MachineProject) error {
		mp := m["fixture"]
		mp.Clones = map[string]string{}
		m["fixture"] = mp
		return nil
	}); err != nil {
		t.Fatalf("UpdateMachine: %v", err)
	}
	clone := filepath.Join(t.TempDir(), "fresh")
	gitLog(t, filepath.Dir(clone), "clone", fx.RepoRemote, clone)

	got := hereJSON(t, env, clone)
	if got.How != "map-remote" || got.Project != "fixture" || got.Repo.Name != "fixture-repo" || !got.Mapped || got.Repo.Branch != "main" {
		t.Errorf("fresh clone: how = %q, project = %q, repo = %+v, mapped = %v, want map-remote, fixture, fixture-repo on main, mapped", got.How, got.Project, got.Repo, got.Mapped)
	}
	sameDir(t, "store.path", got.Store.Path, fx.StoreDir)
	sameDir(t, "repo.dir", got.Repo.Dir, clone)

	machine, err := project.LoadMachine(env.home)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	sameDir(t, "projects.yaml clone", machine["fixture"].Clones["fixture-repo"], clone)

	if again := hereJSON(t, env, clone); again.How != "map-path" || again.Mapped {
		t.Errorf("second call: how = %q, mapped = %v, want map-path and not mapped again", again.How, again.Mapped)
	}

	// --no-map recognizes a clone without recording it.
	other := filepath.Join(t.TempDir(), "other")
	gitLog(t, filepath.Dir(other), "clone", fx.RepoRemote, other)
	if got := hereJSON(t, env, other, "--no-map"); got.How != "map-remote" || got.Mapped {
		t.Errorf("--no-map: how = %q, mapped = %v, want map-remote and not mapped", got.How, got.Mapped)
	}
	// And a repo whose mapped clone is there is never moved to another clone.
	if got := hereJSON(t, env, other); got.How != "map-remote" || got.Mapped {
		t.Errorf("a second clone of a mapped repo: how = %q, mapped = %v, want map-remote and the mapping left alone", got.How, got.Mapped)
	}
	machine, err = project.LoadMachine(env.home)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	sameDir(t, "projects.yaml clone after a second clone", machine["fixture"].Clones["fixture-repo"], clone)
}

// TestHereFreshCloneByMarker: a clone that carries a committed jig.yaml marker
// names its project and finds the store beside it, on a machine whose jig home
// is empty; and a marker whose store is not cloned says where to get it.
func TestHereFreshCloneByMarker(t *testing.T) {
	t.Parallel()
	fx, _ := newFixture(t, fixture.Opts{})
	env := jigEnv{home: t.TempDir(), userHome: t.TempDir()}
	work := t.TempDir()

	clone := filepath.Join(work, "fixture-repo")
	gitLog(t, work, "clone", fx.RepoRemote, clone)
	if err := project.WriteMarker(clone, project.Marker{Jig: "1", Project: "fixture", Store: fx.StoreRemote}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	gitLog(t, clone, "add", project.MarkerFile)
	if _, err := gitx.RunEnv(clone, pinnedIdentityEnv, "commit", "-m", "add the jig marker"); err != nil {
		t.Fatalf("commit the marker: %v", err)
	}

	// Without the store beside it the marker still names the project, and the
	// store's remote with the command to get it.
	got := hereJSON(t, env, clone)
	if got.How != "marker" || got.Project != "fixture" || got.Store.Path != "" || got.Store.Remote != fx.StoreRemote || got.Mapped {
		t.Errorf("no store yet: how = %q, project = %q, store = %+v, mapped = %v, want marker, fixture, only the remote, not mapped", got.How, got.Project, got.Store, got.Mapped)
	}
	r := runJig(t, env, clone, "here")
	if !strings.Contains(r.Stdout, "git clone "+fx.StoreRemote+" ") || !strings.Contains(r.Stdout, "fixture-tickets") {
		t.Errorf("jig here with no store lacks the clone command:\n%s", r.Stdout)
	}
	if r := runJig(t, env, clone, "status", "JIG-1"); r.Code == 0 || !strings.Contains(r.Stdout, "not cloned") {
		t.Errorf("jig status with no store: exit %d, want a refusal saying the store is not cloned:\n%s", r.Code, r.Stdout)
	}

	// With the sibling <project>-tickets cloned, the project resolves whole.
	sibling := filepath.Join(work, "fixture-tickets")
	gitLog(t, work, "clone", fx.StoreRemote, sibling)
	got = hereJSON(t, env, clone)
	if got.How != "marker" || got.Project != "fixture" || got.Kind != "project" || got.Repo.Name != "fixture-repo" || got.Repo.Branch != "main" || !got.Mapped {
		t.Errorf("with the store: how = %q, project = %q, kind = %q, repo = %+v, mapped = %v, want marker, fixture, project, fixture-repo on main, mapped", got.How, got.Project, got.Kind, got.Repo, got.Mapped)
	}
	sameDir(t, "store.path", got.Store.Path, sibling)
	sameDir(t, "repo.dir", got.Repo.Dir, clone)

	machine, err := project.LoadMachine(env.home)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	mp, ok := machine["fixture"]
	if !ok {
		t.Fatalf("projects.yaml has no fixture entry after jig here: %v", machine)
	}
	sameDir(t, "projects.yaml store", mp.Store, sibling)
	sameDir(t, "projects.yaml clone", mp.Clones["fixture-repo"], clone)
	sameDir(t, "projects.yaml root", mp.Root, clone)

	// Every verb now works from the marker-only clone.
	if r := runJig(t, env, filepath.Join(clone, "alpha"), "status", "JIG-1"); r.Code != 0 || !strings.Contains(r.Stdout, "ticket: JIG-1") {
		t.Errorf("jig status from the marker-only clone: exit %d\n%s", r.Code, r.Stdout)
	}
	if _, err := os.Stat(filepath.Join(sibling, "ledger.md")); err != nil {
		t.Errorf("the sibling store clone lost its ledger: %v", err)
	}
}
