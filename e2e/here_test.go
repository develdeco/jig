package e2e

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
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
		"slices[5]{id,state,covers,attempts,blocked_by,question}:\n",
		"  a,queued,-,0,-,-\n",
		"  b,queued,-,0,a,-\n",
		"  e,queued,-,0,-,-\n",
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

	// Only the second repo's branch is read from its clone. A run of slice a,
	// which builds in the first repo, says "not checked" (its branch was never
	// read), where one of slice e, in the second, says it has no commit there.
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	head := gitLog(t, fx.SecondRepoDir, "rev-parse", "HEAD")
	if err := st.WriteStartSHA("JIG-1", "second-repo", head); err != nil {
		t.Fatal(err)
	}
	for _, slice := range []string{"a", "e"} {
		if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventOracle, Slice: slice, Commit: head, Outcome: "pass", Attempt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	r = runJig(t, env, fx.SecondRepoDir, "here")
	for _, want := range []string{"  a,queued,not checked,0,-,-\n", "  e,queued,\"no: slice has no commit on the branch\",0,-,-\n"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig here lacks %q:\n%s", want, r.Stdout)
		}
	}

	// `jig status` reads the same way: it reads the ticket's branch in the
	// clones that have a start sha, so only the second repo's.
	next := runJig(t, env, fx.SecondRepoDir, "next", "JIG-1")
	b := regexp.MustCompile(`(?m)^\s*branch:\s*(\S+)`).FindStringSubmatch(next.Stdout)
	if b == nil {
		t.Fatalf("jig next names no branch:\n%s", next.Stdout)
	}
	gitLog(t, fx.SecondRepoDir, "branch", "-f", b[1], "HEAD")
	r = runJig(t, env, fx.SecondRepoDir, "status", "JIG-1")
	var lastRuns []string
	for _, l := range strings.Split(r.Stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "a,") || strings.HasPrefix(strings.TrimSpace(l), "e,") {
			lastRuns = append(lastRuns, l)
		}
	}
	text := strings.Join(lastRuns, "\n")
	if !strings.Contains(text, ",pass,not checked,") || !strings.Contains(text, "\"no: slice has no commit on the branch\"") {
		t.Errorf("jig status last_runs: a (first repo, no start sha) should say not checked and e (second repo) that it has no commit:\n%s", r.Stdout)
	}
}
