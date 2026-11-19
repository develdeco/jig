package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// writeTicketYAML writes ticket's ticket.yaml in the fixture store, as a store
// of tickets from before the slug field would have it (a title, no slug).
func writeTicketYAML(t *testing.T, fx *fixture.Fixture, ticket, content string) {
	t.Helper()
	dir := filepath.Join(fx.StoreDir, "tickets", ticket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(dir, "ticket.yaml"), []byte(content))
}

// nextTicketOut is the part of `jig next <ticket> --json` these tests read.
type nextTicketOut struct {
	Ticket    string `json:"ticket"`
	State     string `json:"state"`
	Branch    string `json:"branch"`
	BlockedBy []struct {
		Ticket string `json:"ticket"`
		Kind   string `json:"kind"`
	} `json:"blocked_by"`
	Ready []struct {
		Repo   string   `json:"repo"`
		Slices []string `json:"slices"`
	} `json:"ready"`
	Topology []struct {
		ID        string   `json:"id"`
		Workspace string   `json:"workspace"`
		Repo      string   `json:"repo"`
		State     string   `json:"state"`
		Wave      int      `json:"wave"`
		BlockedBy []string `json:"blocked_by"`
	} `json:"topology"`
}

func nextJSON[T any](t *testing.T, env jigEnv, cwd string, args ...string) T {
	t.Helper()
	r := runJig(t, env, cwd, append([]string{"next", "--json"}, args...)...)
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("jig next --json %v: exit %d\nstdout:\n%s\nstderr:\n%s", args, r.Code, r.Stdout, r.Stderr)
	}
	var out T
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("jig next --json %v: not one JSON object: %v\n%s", args, err, r.Stdout)
	}
	return out
}

func setStates(t *testing.T, st *store.Store, ticket string, states map[string]string) {
	t.Helper()
	for slice, state := range states {
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: state}); err != nil {
			t.Fatalf("WriteSliceState(%s, %s): %v", slice, state, err)
		}
	}
}

// commitPinned commits what is staged in dir under the suite's pinned identity.
func commitPinned(t *testing.T, dir, msg string) {
	t.Helper()
	if _, err := gitx.RunEnv(dir, pinnedIdentityEnv, "commit", "-m", msg); err != nil {
		t.Fatalf("git commit in %s: %v", dir, err)
	}
}

// ghOnPath puts the fake gh first on PATH, logging every call it gets, and
// returns the environment and the log. The pull requests it holds are
// withPulls'.
func ghOnPath(t *testing.T, env jigEnv) (jigEnv, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "gh.log")
	return env.with(
		"PATH="+fixture.GhStub(t)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_LOG="+log,
	), log
}

// withPulls sets the pull requests the fake gh holds; the pull request numbered
// n is at pullURL(n).
func withPulls(t *testing.T, env jigEnv, pulls ...fixture.GhPull) jigEnv {
	t.Helper()
	return env.with("GH_STUB_PULLS=" + fixture.GhPulls(t, pulls...))
}

func pullURL(n int) string { return pullURLIn("repo", n) }

// pullURLIn is pullURL for a pull request on the repository repo
// (fixture.GhPull.Repo).
func pullURLIn(repo string, n int) string {
	return "https://github.example/owner/" + repo + "/pull/" + strconv.Itoa(n)
}

// publishedWith appends the lines of a ticket that published: a pr line for each
// of urls, then publish-done.
func publishedWith(t *testing.T, st *store.Store, ticket string, urls ...string) {
	t.Helper()
	for _, u := range urls {
		if err := journal.Append(st, ticket, journal.Line{Event: journal.EventPr, Outcome: "recorded", URL: u}); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Append(st, ticket, journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
}

// parentBranch is the branch of the parent ticket JIG-2, titled "Parent".
const parentBranch = "JIG-2-parent"

// stackOnAParent adds the parent ticket JIG-2 ("Parent", one slice in alpha)
// to the fixture's store and stacks the fixture's JIG-1 on it, then has the
// harness cut the parent's branch in the clone with one commit in alpha. The
// clone is left on the parent's branch; the parent's head is returned.
func stackOnAParent(t *testing.T, fx *fixture.Fixture, h *fakeHarness) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Parent"}); err != nil {
		t.Fatalf("CreateTicketRecord JIG-2: %v", err)
	}
	if err := st.AppendSlices("JIG-2", []store.Slice{{ID: "p", Workspace: "alpha", Goal: "Lay the ground.", Oracle: "test", BlockedBy: []string{}}}); err != nil {
		t.Fatalf("AppendSlices JIG-2: %v", err)
	}
	writeTicketYAML(t, fx, fx.Ticket, "schema_version: 1\nblocked_by:\n  - ticket: JIG-2\n    kind: stacked\n")
	h.Branch(parentBranch)
	head := h.Commit("p", "JIG-2 p: lay the ground", map[string]string{"alpha/PARENT.md": "the parent's ground\n"})
	return st, head
}

// repoNames is the names of repos, comma-joined, in order.
func repoNames(repos []project.MarkerRepo) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = project.RepoName(r)
	}
	return strings.Join(names, ",")
}
