package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

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

// greenSlices leaves every slice of ticket green, as a ticket that published has
// them: a published ticket with a slice that is not green is built again.
func greenSlices(t *testing.T, st *store.Store, ticket string) {
	t.Helper()
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slices {
		if err := st.WriteSliceState(ticket, s.ID, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatal(err)
		}
	}
}

// publishedWith appends the lines of a ticket that published: a pr line for each
// of urls, then publish-done, with its slices green.
func publishedWith(t *testing.T, st *store.Store, ticket string, urls ...string) {
	t.Helper()
	greenSlices(t, st, ticket)
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
