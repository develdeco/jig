package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
)

// finalRequest is a final run of the fixture's ticket.
func (r *rig) finalRequest() FinalRequest {
	return FinalRequest{Ticket: r.fx.Ticket, Session: "s-1", Agent: "agent-1", Flags: "--final"}
}

// oracleFor is the command the manifest gives the fixture's manifest oracle
// `test` in a workspace.
func (r *rig) oracleFor(workspace string) string {
	r.t.Helper()
	man, err := manifest.Resolve(r.fx.RepoDir)
	if err != nil {
		r.t.Fatal(err)
	}
	ws, ok := man.Workspace(workspace)
	if !ok {
		r.t.Fatalf("test setup: the manifest has no workspace %s", workspace)
	}
	return strings.TrimSpace(man.OracleCmd("test", ws))
}

// writeSlices replaces the ticket's slices.yaml with the slices given as
// (id, workspace, oracle, env) rows.
func (r *rig) writeSlices(rows ...[4]string) {
	r.t.Helper()
	var b strings.Builder
	b.WriteString("slices:\n")
	for _, row := range rows {
		b.WriteString("  - id: " + row[0] + "\n    workspace: " + row[1] + "\n    goal: g\n    oracle: " + `"` + row[2] + `"` + "\n")
		if row[3] != "" {
			b.WriteString("    env: " + row[3] + "\n")
		}
		b.WriteString("    blocked_by: []\n    from_brief: []\n")
	}
	if err := os.WriteFile(filepath.Join(r.st.TicketDir(r.fx.Ticket), "slices.yaml"), []byte(b.String()), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// TestFinalRunsTheUnionOfTheSliceCommands: a final run is one command, the
// commands of the ticket's slices each in parentheses joined by &&, run once
// in the worktree at HEAD, with the env classes the slices name up around it.
func TestFinalRunsTheUnionOfTheSliceCommands(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	want := "(" + r.oracleFor("alpha") + ") && (" + r.oracleFor("beta") + ")"
	calls := r.oracle.seen()
	if len(calls) != 1 || calls[0].cmd != want {
		t.Fatalf("the oracle ran %+v, want one call of %q", calls, want)
	}
	if filepath.Clean(calls[0].dir) != filepath.Clean(r.fx.RepoDir) || calls[0].head != sha || calls[0].limit != 0 {
		t.Errorf("the union ran in %s at %s within %s, want the worktree at %s with no limit", calls[0].dir, calls[0].head, calls[0].limit, sha)
	}
	if rd := envValue(calls[0].env, recordDirEnv); rd != "" {
		t.Errorf("the union was given %s: a final run records no scenarios", recordDirEnv)
	}
	if res.State != journal.VerifyPass || res.Command != want || res.Code != 0 || !res.Ran {
		t.Errorf("Result = %+v, want a pass of the union", res.Result)
	}
	var ids []string
	for _, s := range res.Slices {
		ids = append(ids, s.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c", "d"}) {
		t.Errorf("Slices = %v, want every slice of the ticket in slices.yaml order", ids)
	}
	if !reflect.DeepEqual(res.Classes, []string{"rig"}) {
		t.Errorf("Classes = %v, want the one env class slice d names", res.Classes)
	}
	if got, want := events(r.lines()), []string{"env-up", "final-oracle:pass", "env-down"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal events = %v, want the env up once around the one run: %v", got, want)
	}
}

// TestFinalResolvesAManifestNameForEachSlicesWorkspace: slices naming one
// manifest oracle in two workspaces contribute two commands, each with its own
// workspace's path.
func TestFinalResolvesAManifestNameForEachSlicesWorkspace(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.fixClamp()

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	alpha, beta := r.oracleFor("alpha"), r.oracleFor("beta")
	if alpha == beta {
		t.Fatalf("test setup: both workspaces give %q", alpha)
	}
	got := map[string]string{}
	for _, s := range res.Slices {
		got[s.ID] = s.Command
	}
	if got["a"] != alpha || got["b"] != alpha || got["c"] != beta || got["d"] != beta {
		t.Errorf("the slices' commands = %v, want alpha's %q for a and b and beta's %q for c and d", got, alpha, beta)
	}
	if !strings.Contains(res.Command, alpha) || !strings.Contains(res.Command, beta) {
		t.Errorf("the union %q lacks one workspace's command", res.Command)
	}
}

// TestFinalListsIdenticalCommandsOnce: the same resolved text appears once, at
// its first position, however many slices contribute it.
func TestFinalListsIdenticalCommandsOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.fixClamp()
	r.writeSlices([4]string{"a", "alpha", "echo one", ""}, [4]string{"b", "alpha", "echo two", ""}, [4]string{"c", "alpha", "echo one", ""})

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	if want := "(echo one) && (echo two)"; res.Command != want {
		t.Errorf("the union = %q, want %q: the repeat of `echo one` is dropped", res.Command, want)
	}
	if len(res.Slices) != 3 {
		t.Errorf("Slices = %+v, want all three listed with their commands", res.Slices)
	}
}

// TestFinalStopsAtTheFirstFailingCommand: the commands run in order in one sh,
// the first failure ends the run and its code is the run's, and the line says
// the run failed.
func TestFinalStopsAtTheFirstFailingCommand(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.fixClamp()
	r.writeSlices([4]string{"a", "alpha", "echo first", ""}, [4]string{"b", "alpha", "echo second; exit 7", ""}, [4]string{"c", "alpha", "echo third", ""})
	r.deps.Oracle = nil
	var out strings.Builder
	r.deps.Out = &out

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	if res.State != journal.VerifyFail || res.Code != 7 {
		t.Errorf("Result = %+v, want a failure ending with the failing command's code 7", res.Result)
	}
	if !strings.Contains(out.String(), "first") || !strings.Contains(out.String(), "second") || strings.Contains(out.String(), "third") {
		t.Errorf("output = %q, want the first two commands to have run and not the third", out.String())
	}
	line, ok := journal.FinalRunAt(r.lines(), "", res.SHA)
	if !ok || line.Outcome != "fail" {
		t.Errorf("FinalRunAt = %+v, %v, want the failed run", line, ok)
	}
}

// TestAFinalRunCoversTheHeadItRanAt: a clean run at an unmoved head is the
// final run at that commit, and the line holds what is needed to run it again;
// a dirty tree or a moved head records the run and covers nothing.
func TestAFinalRunCoversTheHeadItRanAt(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	if !res.Covered || res.Uncovered != "" {
		t.Fatalf("Result = %+v, want a run that covers HEAD", res.Result)
	}
	line, ok := journal.FinalRunAt(r.lines(), "", sha)
	if !ok || line.Outcome != "pass" {
		t.Fatalf("FinalRunAt = %+v, %v, want the pass at %s", line, ok, sha)
	}
	if line.Commit != sha || line.HeadEnd != sha || line.TreeBefore != journal.TreeClean || line.TreeAfter != journal.TreeClean ||
		line.Flags != "--final" || line.Source != "jig" || line.Session != "s-1" || line.Agent != "agent-1" || line.Jig == "" ||
		line.Seconds < 1 || line.Command != res.Command || line.Slice != "" || line.Env != "rig" ||
		!reflect.DeepEqual(line.Slices, []string{"a", "b", "c", "d"}) || filepath.Clean(line.Dir) != filepath.Clean(r.fx.RepoDir) {
		t.Errorf("final-oracle line = %+v, want every input and fact of the run", line)
	}

	for _, c := range []struct {
		name   string
		during func(r *rig)
		setup  func(r *rig)
		want   string
	}{
		{name: "a dirty tree before", setup: func(r *rig) { writeIn(t, r, "scratch.txt", "x") }, want: journal.UncoveredDirtyBefore},
		{name: "a file left behind", during: func(r *rig) { writeIn(t, r, "left.txt", "x") }, want: journal.UncoveredDirtyAfter},
		{name: "a moved HEAD", during: func(r *rig) { r.commit("", "moved", map[string]string{"m.txt": "1"}) }, want: journal.UncoveredHeadMoved},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			sha := r.fixClamp()
			if c.setup != nil {
				c.setup(r)
			}
			if c.during != nil {
				r.oracle.during = func(string) { c.during(r) }
			}
			res, err := RunFinal(r.deps, r.finalRequest())
			if err != nil {
				t.Fatalf("RunFinal: %v", err)
			}
			if res.Covered || res.Uncovered != c.want || !res.Ran {
				t.Fatalf("Result = %+v, want a run that covers nothing: %s", res.Result, c.want)
			}
			head, _ := gitx.RevParse(r.fx.RepoDir, "HEAD")
			for _, at := range []string{sha, head} {
				if line, ok := journal.FinalRunAt(r.lines(), "", at); ok {
					t.Errorf("FinalRunAt(%s) = %+v, want none: an uncovered run counts for nothing", at, line)
				}
			}
		})
	}
}

// TestAFinalRunChangesNoSliceState: a final run writes one final-oracle line
// and nothing a slice's own run writes: no state, no verified line, no
// recorded line, no recording, and no verification of any commit.
func TestAFinalRunChangesNoSliceState(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	before := map[string]string{}
	for _, id := range []string{"a", "b", "c", "d"} {
		before[id] = r.state(id).State
	}
	r.oracle.files = map[string]string{"login.svg": "<svg xmlns='http://www.w3.org/2000/svg'/>"}

	if _, err := RunFinal(r.deps, r.finalRequest()); err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	for id, was := range before {
		if got := r.state(id).State; got != was {
			t.Errorf("slice %s is %q after a final run, was %q", id, got, was)
		}
	}
	for _, l := range r.lines() {
		switch l.Event {
		case journal.EventFinalOracle, journal.EventEnvUp, journal.EventEnvDown:
		default:
			t.Errorf("a final run wrote a %s line: %+v", l.Event, l)
		}
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if v := journal.VerificationState(r.lines(), sha, id); v.State != journal.VerifyNone {
			t.Errorf("VerificationState(%s) = %+v, want none: a final run verifies no slice", id, v)
		}
	}
	if got := journal.BuiltCommits(r.lines()); len(got) != 0 {
		t.Errorf("BuiltCommits = %v, want none", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(r.fx.Home, "recordings")); len(entries) != 0 {
		t.Errorf("a final run left recordings: %v", entries)
	}
}

// TestAFinalWithAWorkspaceTheManifestLacksCannotBeReached: a slice whose
// workspace the manifest at HEAD does not declare (and an env class it does
// not declare) is a reason the union cannot be reached: nothing runs, nothing
// is written, and the run is an error outcome carrying the reason.
func TestAFinalWithAWorkspaceTheManifestLacksCannotBeReached(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		row  [4]string
		want string
	}{
		{"a workspace the manifest lacks", [4]string{"z", "nowhere", "echo z", ""}, `"nowhere"`},
		{"an empty oracle", [4]string{"z", "alpha", "", ""}, "has no oracle"},
		{"an env class the manifest lacks", [4]string{"z", "alpha", "echo z", "ghost"}, `"ghost"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			r.fixClamp()
			r.writeSlices([4]string{"a", "alpha", "echo a", ""}, c.row)

			res, err := RunFinal(r.deps, r.finalRequest())
			if err != nil {
				t.Fatalf("RunFinal: %v", err)
			}
			if res.State != journal.VerifyError || res.Code != ExitUnrun || res.Ran || !strings.Contains(res.Summary, c.want) {
				t.Errorf("Result = %+v, want an error outcome (exit 125) saying %s", res.Result, c.want)
			}
			if n := len(r.oracle.seen()); n != 0 {
				t.Errorf("the oracle ran %d times, want none", n)
			}
			if lines := r.lines(); len(lines) != 0 {
				t.Errorf("journal = %v, want nothing written for a run that could not start", events(lines))
			}
			if _, err := PlanFinal(r.deps, r.finalRequest()); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("PlanFinal error = %v, want the same reason", err)
			}
		})
	}
}
