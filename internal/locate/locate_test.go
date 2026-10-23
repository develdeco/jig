package locate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

func explained(w Where, sub string) bool {
	for _, l := range w.Explain {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// TestLocateNoneAndIgnored: outside every project the answer is none, with no
// error and no write; an ignored directory is none whatever holds there, except
// that --store still names a store.
func TestLocateNoneAndIgnored(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	outside := mkdir(t, w.path("elsewhere", "deep"))

	got := w.locate(outside, Opts{})
	if got.How != HowNone || got.Project != "" || got.StorePath != "" || got.Mapped {
		t.Errorf("outside a project: %+v, want how none and nothing named", got)
	}
	if !explained(got, "none:") {
		t.Errorf("explain = %v, want a closing none line", got.Explain)
	}
	if _, err := os.Stat(filepath.Join(w.home, "projects.yaml")); err == nil {
		t.Error("Locate wrote the machine map for a directory in no project")
	}

	// A mapped clone that is then ignored is none.
	st := w.store("alpha-tickets", "alpha")
	clone := w.repo("app", "")
	w.mapProject("alpha", project.MachineProject{Store: st, Clones: map[string]string{"app": clone}})
	if got := w.locate(clone, Opts{}); got.How != HowMapPath {
		t.Fatalf("before ignoring: how = %q, want map-path", got.How)
	}
	if _, err := project.Ignore(w.home, clone); err != nil {
		t.Fatal(err)
	}
	got = w.locate(filepath.Join(clone, "sub"), Opts{})
	if got.How != HowNone || !explained(got, "ignored:") {
		t.Errorf("an ignored clone: how = %q, explain = %v, want none and an ignored line", got.How, got.Explain)
	}
	// --store is explicit and wins.
	if got := w.locate(clone, Opts{StoreFlag: st}); got.How != HowFlag {
		t.Errorf("ignored clone with --store: how = %q, want flag", got.How)
	}
}

func TestLocateFlag(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	st := w.store("alpha-tickets", "alpha")
	other := w.store("beta-tickets", "beta")
	betaClone := mkdir(t, w.path("beta-app"))
	w.mapProject("beta", project.MachineProject{Store: other, Clones: map[string]string{"app": betaClone}})

	got := w.locate(w.root, Opts{StoreFlag: "alpha-tickets"})
	if got.How != HowFlag || got.Project != "alpha" || got.Kind != project.KindProject {
		t.Errorf("relative --store: %+v, want flag, alpha, project", got)
	}
	same(t, "StorePath", got.StorePath, st)

	// The flag beats the project the directory belongs to, and the repo is
	// that project's, not the directory's.
	got = w.locate(betaClone, Opts{StoreFlag: st})
	if got.How != HowFlag || got.Project != "alpha" || got.Repo != "" {
		t.Errorf("--store from another project's clone: %+v, want alpha and no repo", got)
	}
	got = w.locate(filepath.Join(betaClone, "x"), Opts{StoreFlag: other})
	if got.Project != "beta" || got.Repo != "app" {
		t.Errorf("--store naming the clone's own project: project %q, repo %q, want beta, app", got.Project, got.Repo)
	}

	if _, err := Locate(w.home, w.root, Opts{StoreFlag: w.path("not-a-store")}); err == nil {
		t.Error("--store at a directory with no project.yaml: no error")
	}
}

// TestLocateMarkerFindsTheStore: a jig.yaml names the project; the store is
// the machine map's, or the sibling <project>-tickets, or only a remote.
func TestLocateMarkerFindsTheStore(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	app := w.repo("app", "")
	if err := project.WriteMarker(app, project.Marker{Jig: "1", Project: "alpha", Store: "git@example.com:o/alpha-tickets.git"}); err != nil {
		t.Fatal(err)
	}
	sub := mkdir(t, filepath.Join(app, "pkg", "deep"))

	// No store here: the marker still names the project, and says where the
	// store lives. Nothing is mapped without a store.
	got := w.locate(sub, Opts{})
	if got.How != HowMarker || got.Project != "alpha" || got.Kind != project.KindProject || got.StorePath != "" || got.StoreRemote != "git@example.com:o/alpha-tickets.git" || got.Mapped {
		t.Errorf("marker without a store: %+v", got)
	}
	same(t, "Root", got.Root, app)
	same(t, "RepoDir", got.RepoDir, app)
	if len(w.machine()) != 0 {
		t.Errorf("machine map = %v, want it untouched", w.machine())
	}

	// The sibling store appears: found, and the clone mapped on the spot.
	st := w.store("alpha-tickets", "alpha", "https://example.com/o/app.git")
	got = w.locate(sub, Opts{})
	if got.How != HowMarker || got.Project != "alpha" || !got.Mapped || got.Repo != "app" || got.Branch != "main" {
		t.Errorf("marker with a sibling store: %+v", got)
	}
	same(t, "StorePath", got.StorePath, st)
	mp := w.machine()["alpha"]
	same(t, "mapped store", mp.Store, st)
	same(t, "mapped clone", mp.Clones["app"], app)
	same(t, "mapped root", mp.Root, app)

	// Mapped now: the next call changes nothing.
	if again := w.locate(app, Opts{}); again.How != HowMarker || again.Mapped {
		t.Errorf("second call: how %q, mapped %v, want marker and no new mapping", again.How, again.Mapped)
	}

	// The machine map's store wins over the sibling.
	elsewhere := w.store("kept-elsewhere", "alpha")
	w.mapProject("alpha", project.MachineProject{Store: elsewhere, Clones: map[string]string{"app": app}, Root: app})
	same(t, "machine-mapped store", w.locate(app, Opts{}).StorePath, elsewhere)

	// A mapped store that vanished falls back to the sibling, and the map is
	// repaired.
	if err := os.RemoveAll(elsewhere); err != nil {
		t.Fatal(err)
	}
	got = w.locate(app, Opts{})
	same(t, "StorePath after the mapped store vanished", got.StorePath, st)
	if !got.Mapped {
		t.Error("the map was not repaired")
	}
	same(t, "repaired store", w.machine()["alpha"].Store, st)
}

// TestLocateMarkerBeatsTheMapAndNoMapKeepsItOut: the marker is read before the
// machine map, and --no-map leaves the map alone.
func TestLocateMarkerBeatsTheMapAndNoMapKeepsItOut(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	app := w.repo("app", "")
	w.store("alpha-tickets", "alpha")
	if err := project.WriteMarker(app, project.Marker{Project: "alpha"}); err != nil {
		t.Fatal(err)
	}
	beta := w.store("beta-tickets", "beta")
	w.mapProject("beta", project.MachineProject{Store: beta, Clones: map[string]string{"app": app}})

	got := w.locate(app, Opts{NoMap: true})
	if got.How != HowMarker || got.Project != "alpha" || got.Mapped {
		t.Errorf("marker over a mapped clone: %+v, want marker, alpha, not mapped", got)
	}
	if _, ok := w.machine()["alpha"]; ok {
		t.Error("--no-map still wrote the project")
	}
}

// TestLocateIgnoresAJigYamlThatIsNoMarker: a jig.yaml that says nothing a
// marker says (another tool's file) is passed over.
func TestLocateIgnoresAJigYamlThatIsNoMarker(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dir := mkdir(t, w.path("tool"))
	if err := os.WriteFile(filepath.Join(dir, project.MarkerFile), []byte("workspaces:\n  - id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := w.locate(dir, Opts{})
	if got.How != HowNone || !explained(got, "not read as a jig marker") {
		t.Errorf("a foreign jig.yaml: how %q, explain %v, want none and a note", got.How, got.Explain)
	}
	// The per-repo manifest is never a marker.
	manifest := mkdir(t, w.path("repo", ".claude"))
	if err := os.WriteFile(filepath.Join(manifest, project.MarkerFile), []byte("jig: 1\nproject: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := w.locate(manifest, Opts{}); got.How != HowNone {
		t.Errorf("inside .claude: how %q, want none", got.How)
	}
	// A marker that is not YAML is an error, not silence.
	bad := mkdir(t, w.path("bad"))
	if err := os.WriteFile(filepath.Join(bad, project.MarkerFile), []byte("a: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Locate(w.home, bad, Opts{}); err == nil {
		t.Error("a jig.yaml that is not YAML: no error")
	}
}

// TestLocateOrchestratorAndMember: an orchestrator marker lists nested repos
// and the one cwd is in is the repo; a member marker is a repo that does not
// make the orchestrator its root.
func TestLocateOrchestratorAndMember(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	orch := mkdir(t, w.path("orch"))
	api := mkdir(t, orch, "api")
	web := mkdir(t, orch, "web")
	if err := project.WriteMarker(orch, project.Marker{
		Project: "suite", Kind: project.KindOrchestrator,
		Repos: []project.MarkerRepo{{Name: "api", Path: "api"}, {Name: "web", Path: "web"}, {Name: "gone", Path: "gone"}},
	}); err != nil {
		t.Fatal(err)
	}
	st := w.store("suite-tickets", "suite")
	_ = st

	got := w.locate(mkdir(t, web, "src"), Opts{})
	if got.How != HowMarker || got.Kind != project.KindOrchestrator || got.Repo != "web" || got.Project != "suite" {
		t.Errorf("in a nested repo: %+v", got)
	}
	same(t, "RepoDir", got.RepoDir, web)
	same(t, "Root", got.Root, orch)
	if len(got.Nested) != 2 {
		t.Errorf("Nested = %v, want the two repos that exist", got.Nested)
	}
	mp := w.machine()["suite"]
	same(t, "mapped api", mp.Clones["api"], api)
	same(t, "mapped web", mp.Clones["web"], web)
	same(t, "mapped root", mp.Root, orch)

	got = w.locate(orch, Opts{})
	if got.Repo != "" || got.RepoDir != "" || got.Kind != project.KindOrchestrator || len(got.Nested) != 2 {
		t.Errorf("in the orchestrator itself: %+v, want no repo and two nested", got)
	}

	// A member's own marker is nearest, so the member is the root; its
	// orchestrator is not recorded as the root.
	if err := project.WriteMarker(api, project.Marker{Project: "suite", Kind: project.KindMember}); err != nil {
		t.Fatal(err)
	}
	if err := project.UpdateMachine(w.home, func(m map[string]project.MachineProject) error {
		delete(m, "suite")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got = w.locate(api, Opts{})
	if got.Kind != project.KindMember || got.Repo == "" {
		t.Errorf("a member: %+v", got)
	}
	same(t, "member Root", got.Root, api)
	if root := w.machine()["suite"].Root; root != "" {
		t.Errorf("a member recorded %q as the project's root", root)
	}
}

// TestLocateMapPath: the longest mapped clone containing cwd wins, a linked
// worktree reads as its clone, and a project root with no clone under it still
// answers.
func TestLocateMapPath(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	st := w.store("alpha-tickets", "alpha")
	app := w.repo("app", "")
	lib := w.repo(filepath.Join("app", "vendor", "lib"), "")
	w.mapProject("alpha", project.MachineProject{Store: st, Clones: map[string]string{"app": app, "lib": lib}})

	got := w.locate(mkdir(t, lib, "x"), Opts{})
	if got.How != HowMapPath || got.Repo != "lib" || got.Project != "alpha" {
		t.Errorf("in the nested clone: %+v, want the longest clone, lib", got)
	}
	same(t, "StorePath", got.StorePath, st)
	if got := w.locate(mkdir(t, app, "y"), Opts{}); got.Repo != "app" {
		t.Errorf("in the outer clone: repo %q, want app", got.Repo)
	}

	// A linked worktree is the clone it was added from, on its own branch.
	wt := w.path("wt")
	git(t, app, "worktree", "add", "-b", "feature", wt)
	got = w.locate(wt, Opts{})
	if got.How != HowMapPath || got.Repo != "app" || got.Branch != "feature" {
		t.Errorf("in a worktree: %+v, want map-path, app, feature", got)
	}
	same(t, "worktree RepoDir", got.RepoDir, app)
	if !explained(got, "linked worktree") {
		t.Errorf("explain = %v, want the worktree named", got.Explain)
	}

	// A project root with no clone under it.
	orch := mkdir(t, w.path("orch"))
	w.mapProject("beta", project.MachineProject{Store: st, Root: orch})
	got = w.locate(mkdir(t, orch, "docs"), Opts{})
	if got.How != HowMapPath || got.Project != "beta" || got.Repo != "" || got.RepoDir != "" {
		t.Errorf("under a project root: %+v, want beta with no repo", got)
	}
	same(t, "Root", got.Root, orch)
}

// TestLocateMapRemote: a clone whose origin is a repo of a mapped project is
// that project's, and is recorded when the repo has no live clone.
func TestLocateMapRemote(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	st := w.store("alpha-tickets", "alpha", "https://example.com/o/app.git", "https://example.com/o/lib.git")
	w.mapProject("alpha", project.MachineProject{Store: st, Clones: map[string]string{}})

	fresh := w.repo("fresh", "git@example.com:o/lib")
	got := w.locate(mkdir(t, fresh, "src"), Opts{})
	if got.How != HowMapRemote || got.Project != "alpha" || got.Repo != "lib" || !got.Mapped {
		t.Errorf("a fresh clone: %+v, want map-remote, alpha, lib, mapped", got)
	}
	same(t, "RepoDir", got.RepoDir, fresh)
	same(t, "StorePath", got.StorePath, st)
	same(t, "recorded clone", w.machine()["alpha"].Clones["lib"], fresh)
	if again := w.locate(fresh, Opts{}); again.How != HowMapPath {
		t.Errorf("the recorded clone: how %q, want map-path", again.How)
	}

	// A second clone of the same repo does not take the mapping over.
	second := w.repo("second", "https://example.com/o/lib.git")
	got = w.locate(second, Opts{})
	if got.How != HowMapRemote || got.Mapped {
		t.Errorf("a second clone: how %q, mapped %v, want map-remote and the mapping left alone", got.How, got.Mapped)
	}
	same(t, "RepoDir of the second clone", got.RepoDir, second)
	same(t, "clone after a second clone", w.machine()["alpha"].Clones["lib"], fresh)

	// ... until the mapped clone is gone.
	if err := os.RemoveAll(fresh); err != nil {
		t.Fatal(err)
	}
	if got := w.locate(second, Opts{}); !got.Mapped {
		t.Error("a vanished clone was not replaced")
	}
	same(t, "clone after the old one vanished", w.machine()["alpha"].Clones["lib"], second)

	// --no-map, an origin nobody lists, and no origin at all.
	third := w.repo("third", "https://example.com/o/app")
	if got := w.locate(third, Opts{NoMap: true}); got.How != HowMapRemote || got.Mapped {
		t.Errorf("--no-map: %+v", got)
	}
	if _, ok := w.machine()["alpha"].Clones["app"]; ok {
		t.Error("--no-map recorded the clone")
	}
	if got := w.locate(w.repo("stranger", "https://example.com/o/other.git"), Opts{}); got.How != HowNone {
		t.Errorf("an unlisted origin: how %q, want none", got.How)
	}
	if got := w.locate(w.repo("no-origin", ""), Opts{}); got.How != HowNone || !explained(got, "no origin") {
		t.Errorf("no origin: how %q, explain %v", got.How, got.Explain)
	}
}

// TestLocateStoreDir: a store, or a directory in a store's repo, is the store;
// the standalone store next to a repo is that repo's; a project.yaml in some
// other repo's root is not a store.
func TestLocateStoreDir(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	st := w.gitStore("alpha-tickets", "alpha")
	got := w.locate(st, Opts{})
	if got.How != HowStore || got.Project != "alpha" || got.Repo != "" || got.Branch != "" {
		t.Errorf("in the store: %+v, want store and alpha, no repo or branch", got)
	}
	same(t, "StorePath", got.StorePath, st)
	deep := mkdir(t, st, "tickets", "JIG-1", "questions")
	got = w.locate(deep, Opts{})
	if got.How != HowStore || got.Project != "alpha" {
		t.Errorf("inside the store: %+v", got)
	}
	same(t, "StorePath from inside", got.StorePath, st)

	// Some other tool's project.yaml at a repo's root: not a store from below.
	foreign := w.repo("foreign", "")
	if err := os.WriteFile(filepath.Join(foreign, "project.yaml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := w.locate(mkdir(t, foreign, "src"), Opts{}); got.How != HowNone {
		t.Errorf("below a foreign project.yaml: how %q, want none", got.How)
	}

	// jig init --standalone's sibling store, with nothing in the machine map.
	app := w.repo("app", "")
	w.store("app-tickets", "app", app)
	got = w.locate(mkdir(t, app, "pkg"), Opts{})
	if got.How != HowStore || got.Project != "app" || got.Repo != "app" || got.Branch != "main" {
		t.Errorf("a standalone store beside the repo: %+v, want store, app, repo app on main", got)
	}
	same(t, "RepoDir", got.RepoDir, app)
}

// TestLocateTicketSliceSession: the ticket is the argument or the branch's;
// the slice is the flag, JIG_SLICE or the session's claim; the session is the
// flag or JIG_SESSION.
func TestLocateTicketSliceSession(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	stPath := w.store("alpha-tickets", "alpha")
	app := w.repo("app", "")
	w.mapProject("alpha", project.MachineProject{Store: stPath, Clones: map[string]string{"app": app}})
	st := openStore(t, stPath)
	if err := st.CreateTicketRecord("JIG-1", store.Ticket{Title: "One", Aliases: []string{"OLD-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Two"}); err != nil {
		t.Fatal(err)
	}
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}

	// The branch.
	if got := w.locate(app, Opts{}); got.Ticket != "" || got.Branch != "main" {
		t.Errorf("on main: ticket %q, branch %q, want no ticket", got.Ticket, got.Branch)
	}
	git(t, app, "switch", "-c", "jig/JIG-1")
	got := w.locate(app, Opts{})
	if got.Ticket != "JIG-1" || got.TicketHow != HowBranch {
		t.Errorf("on jig/JIG-1: ticket %q (%s), want JIG-1 by branch", got.Ticket, got.TicketHow)
	}
	git(t, app, "switch", "-c", "OLD-1-fix")
	if got := w.locate(app, Opts{}); got.Ticket != "JIG-1" || got.TicketHow != HowBranchAlias {
		t.Errorf("on OLD-1-fix: ticket %q (%s), want JIG-1 by an alias in the branch", got.Ticket, got.TicketHow)
	}
	git(t, app, "switch", "-c", "JIG-9-nothing")
	if got := w.locate(app, Opts{}); got.Ticket != "" {
		t.Errorf("on a branch leading with an unknown id: ticket %q, want none", got.Ticket)
	}

	// An explicit ticket beats the branch, and resolves an old id.
	git(t, app, "switch", "jig/JIG-1")
	if got := w.locate(app, Opts{Ticket: "JIG-2"}); got.Ticket != "JIG-2" || got.TicketHow != HowArg {
		t.Errorf("explicit ticket: %q (%s), want JIG-2 by arg", got.Ticket, got.TicketHow)
	}
	if got := w.locate(app, Opts{Ticket: "OLD-1"}); got.Ticket != "JIG-1" || got.TicketHow != HowArgAlias {
		t.Errorf("explicit old id: %q (%s), want JIG-1 by an alias", got.Ticket, got.TicketHow)
	}

	// Session: the argument, then JIG_SESSION.
	if got := w.locate(app, Opts{Getenv: env("JIG_SESSION", "s-env")}); got.Session != "s-env" || got.SessionHow != HowEnv {
		t.Errorf("session from env: %q (%s)", got.Session, got.SessionHow)
	}
	if got := w.locate(app, Opts{Session: "s-arg", Getenv: env("JIG_SESSION", "s-env")}); got.Session != "s-arg" || got.SessionHow != HowArg {
		t.Errorf("session from the argument: %q (%s)", got.Session, got.SessionHow)
	}

	// Slice: the claim line of the session, until it is released.
	claim := func(session, slice string) {
		t.Helper()
		if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventClaim, Session: session, Slice: slice}); err != nil {
			t.Fatal(err)
		}
	}
	claim("s1", "a")
	claim("s2", "z")
	claim("s1", "b")
	if got := w.locate(app, Opts{Session: "s1"}); got.Slice != "b" || got.SliceHow != HowClaim {
		t.Errorf("claim: slice %q (%s), want b by the latest claim", got.Slice, got.SliceHow)
	}
	if got := w.locate(app, Opts{Session: "s3"}); got.Slice != "" {
		t.Errorf("a session with no claim: slice %q", got.Slice)
	}
	if got := w.locate(app, Opts{Getenv: env("JIG_SLICE", "c")}); got.Slice != "c" || got.SliceHow != HowEnv {
		t.Errorf("slice from env: %q (%s)", got.Slice, got.SliceHow)
	}
	if got := w.locate(app, Opts{Session: "s1", Slice: "d", Getenv: env("JIG_SLICE", "c")}); got.Slice != "d" || got.SliceHow != HowArg {
		t.Errorf("slice from the argument: %q (%s)", got.Slice, got.SliceHow)
	}
	if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventRelease, Session: "s1", Slice: "b"}); err != nil {
		t.Fatal(err)
	}
	if got := w.locate(app, Opts{Session: "s1"}); got.Slice != "" {
		t.Errorf("after the release: slice %q, want none", got.Slice)
	}

	// StoreOnly asks for the store and nothing else.
	got = w.locate(app, Opts{StoreOnly: true, Ticket: "JIG-2", Session: "s1"})
	if got.StorePath == "" || got.Branch != "" || got.Ticket != "" || got.Session != "" {
		t.Errorf("StoreOnly: %+v, want the store and no branch, ticket or session", got)
	}
}

// TestLocateAnAliasCollisionOnABranchBindsNothing: a branch whose id two
// tickets claim must not stop every verb run on it.
func TestLocateAnAliasCollisionOnABranchBindsNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	stPath := w.store("alpha-tickets", "alpha")
	app := w.repo("app", "")
	w.mapProject("alpha", project.MachineProject{Store: stPath, Clones: map[string]string{"app": app}})
	st := openStore(t, stPath)
	for _, id := range []string{"JIG-1", "JIG-2"} {
		if err := st.CreateTicketRecord(id, store.Ticket{Aliases: []string{"OLD-7"}}); err != nil {
			t.Fatal(err)
		}
	}
	git(t, app, "switch", "-c", "OLD-7-x")
	got := w.locate(app, Opts{})
	if got.Ticket != "" || !explained(got, "binds nothing") {
		t.Errorf("a colliding alias: ticket %q, explain %v, want none and a note", got.Ticket, got.Explain)
	}
	if _, err := Locate(w.home, app, Opts{Ticket: "OLD-7"}); err == nil {
		t.Error("an explicit ticket that two tickets claim: no error")
	}
}
