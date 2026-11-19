package main

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
	"github.com/develdeco/jig/internal/verifydeliver"
)

// claimantEnv is the env of a command run from the clone by the harness
// session id.
func claimantEnv(fx *fixture.Fixture, session string) env {
	userHome := fx.Home + "/operator-home"
	return envFrom(map[string]string{
		"JIG_HOME": fx.Home, "HOME": userHome, "USERPROFILE": userHome, "JIG_SESSION": session,
	}).inDir(fx.RepoDir)
}

// builderProbeArgs is `jig probe` of file and line with outcome, for a store.
func builderProbeArgs(fx *fixture.Fixture, file string, line int, outcome string) []string {
	return []string{"probe", fx.Ticket, "--seam", "the bound of a percentage", "--file", file, "--line", strconv.Itoa(line),
		"--mutation", "raise the bound", "--command", "go test ./alpha", "--outcome", outcome, "--store", fx.StoreDir}
}

// TestBuilderProbeRecordsForTheClaimedSlice: with no round open, `jig probe`
// by the session that claimed a slice journals the probe for that slice with
// the blob of its file in the worktree, takes a skip the same way, and holds
// both to the shape rules of a round's draft.
func TestBuilderProbeRecordsForTheClaimedSlice(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	if out, code := runMain(t, e, "", "claim", fx.Ticket, "--slice", "a", "--session", "s-1", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("claim: exit %d\n%s", code, out)
	}
	mine := claimantEnv(fx, "s-1")

	out, code := runMain(t, mine, "", builderProbeArgs(fx, "alpha/alpha.go", 5, verifydeliver.ProbeSurvived)...)
	if code != 0 || !strings.Contains(out, "slice: a") || !strings.Contains(out, "recorded: builder") {
		t.Fatalf("jig probe by the claimant: exit %d, want 0 recording it for slice a\n%s", code, out)
	}
	out, code = runMain(t, mine, "", "probe", fx.Ticket, "--skip", "alpha/alpha.go:1-3", "--why", "a doc comment", "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "kind: skip") {
		t.Fatalf("jig probe --skip by the claimant: exit %d\n%s", code, out)
	}
	lines := linesOf(t, st, fx.Ticket, journal.EventBuilderProbe)
	if len(lines) != 2 || lines[0].Slice != "a" || lines[0].Session != "s-1" || lines[0].Probe == nil || lines[1].Skip == nil || lines[1].Skip.To != 3 {
		t.Fatalf("builder-probe lines = %+v, want a probe then a skip, both of slice a", lines)
	}
	want := gitMust(t, fx.RepoDir, "hash-object", "alpha/alpha.go")
	if got := lines[0].Probe; got.Blob != want || got.Outcome != verifydeliver.ProbeSurvived || got.Line != 5 || lines[1].Skip.Blob != want {
		t.Errorf("probe %+v and skip %+v, want the blob %s and the survivor at line 5", got, lines[1].Skip, want)
	}

	// The same shape rules as a round's probe.
	out, code = runMain(t, mine, "", builderProbeArgs(fx, "alpha/alpha.go", 5, "killed")...)
	if code != 1 || !strings.Contains(out, "REVIEW_INVALID") {
		t.Errorf("a probe with an unknown outcome: exit %d, want 1 and REVIEW_INVALID\n%s", code, out)
	}
	out, code = runMain(t, mine, "", builderProbeArgs(fx, "alpha/missing.go", 5, verifydeliver.ProbeCaught)...)
	if code != 2 || !strings.Contains(out, "is not in this worktree") {
		t.Errorf("a probe of a file not in the worktree: exit %d, want 1 naming it\n%s", code, out)
	}
	if n := len(linesOf(t, st, fx.Ticket, journal.EventBuilderProbe)); n != 2 {
		t.Errorf("%d builder-probe lines, want the refusals to add none", n)
	}
}

// TestBuilderProbeNeedsAClaimedSlice: a session that holds no slice, or whose
// claim a release undid, is refused with both ways out.
func TestBuilderProbeNeedsAClaimedSlice(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	args := builderProbeArgs(fx, "alpha/alpha.go", 5, verifydeliver.ProbeCaught)

	out, code := runMain(t, claimantEnv(fx, "s-1"), "", args...)
	if code != 1 || !strings.Contains(out, "REVIEW_NOT_OPEN") || !strings.Contains(out, "jig claim "+fx.Ticket) || !strings.Contains(out, "jig gate "+fx.Ticket) {
		t.Fatalf("jig probe with no claim and no round: exit %d, want REVIEW_NOT_OPEN naming claim and gate\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "claim", fx.Ticket, "--slice", "a", "--session", "s-1", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("claim: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "claim", fx.Ticket, "--slice", "a", "--session", "s-1", "--release", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("release: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, claimantEnv(fx, "s-1"), "", args...); code != 1 || !strings.Contains(out, "REVIEW_NOT_OPEN") {
		t.Errorf("jig probe after a release: exit %d, want it refused\n%s", code, out)
	}
	if n := len(linesOf(t, st, fx.Ticket, journal.EventBuilderProbe)); n != 0 {
		t.Errorf("%d builder-probe lines, want none", n)
	}
}

// TestBuilderProbeJoinsTheNextRoundsLedger: a current caught probe a builder
// recorded leaves its hunk off the next round's unprobed list, a survivor does
// not, and the round's view names the slice whose probes they are.
func TestBuilderProbeJoinsTheNextRoundsLedger(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	base := gitMust(t, fx.RepoDir, "rev-parse", "HEAD")
	if out, code := runMain(t, cloneEnv(fx), "", "claim", fx.Ticket, "--slice", "a", "--session", "s-1", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("claim: exit %d\n%s", code, out)
	}
	buildFixtureTicket(t, fx)
	hunks, err := gitx.DiffHunks(fx.RepoDir, base, "HEAD")
	if err != nil || len(hunks) < 2 {
		t.Fatalf("hunks of the change = %+v, %v; want at least two", hunks, err)
	}
	caught, survived := hunks[0], hunks[1]
	mine := claimantEnv(fx, "s-1")
	for _, p := range []struct {
		h       gitx.Hunk
		outcome string
	}{{caught, verifydeliver.ProbeCaught}, {survived, verifydeliver.ProbeSurvived}} {
		if out, code := runMain(t, mine, "", builderProbeArgs(fx, p.h.Path, p.h.From, p.outcome)...); code != 0 {
			t.Fatalf("jig probe of %s:%d: exit %d\n%s", p.h.Path, p.h.From, code, out)
		}
	}

	e := cloneEnv(fx)
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit %d\n%s", code, out)
	}
	ctx, code := runMain(t, e, "", "review", "context", fx.Ticket, "--json", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("review context: exit %d\n%s", code, ctx)
	}
	var req verifydeliver.ReviewRequest
	if err := json.Unmarshal([]byte(ctx), &req); err != nil {
		t.Fatal(err)
	}
	states := map[int]string{}
	for _, p := range req.Probed {
		if p.Slice != "a" || p.Round != 0 {
			t.Errorf("probed entry %+v, want a builder's, of slice a", p)
		}
		states[p.Line] = p.State + "/" + p.Outcome
	}
	if len(req.Probed) != 2 || states[caught.From] != "current/caught" || states[survived.From] != "current/survived" {
		t.Errorf("probed = %+v, want the caught and the survivor, both current", req.Probed)
	}
	for _, h := range req.Hunks {
		switch {
		case h.File == caught.Path && h.From == caught.From && h.Unprobed():
			t.Errorf("hunk %+v is on the work list, want it covered by the builder's caught probe", h)
		case h.File == survived.Path && h.From == survived.From && !h.Unprobed():
			t.Errorf("hunk %+v is covered, want the survivor to leave it on the work list", h)
		}
	}
	out, code := runMain(t, e, "", "review", "context", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "builder_probes: 2 (slice a)") || !strings.Contains(out, "probed[2]{round,file:line,outcome,state,seam,mutation,slice}:") {
		t.Errorf("review context: exit %d, want the builder's probes named by slice\n%s", code, out)
	}
}

// TestBuilderProbeRecordedBeforeTheCommitIsCurrentAfterIt: the build skill has
// a builder probe before committing, so the probe is recorded over uncommitted
// edits. It records the blob of the file as it is in the worktree, which the
// commit then makes the blob at HEAD: the probe is current after the commit,
// and stale once the file is edited again.
func TestBuilderProbeRecordedBeforeTheCommitIsCurrentAfterIt(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	if out, code := r.run("claim", r.fx.Ticket, "--slice", "a", "--session", "s-1", "--store", r.fx.StoreDir); code != 0 {
		t.Fatalf("claim: exit %d\n%s", code, out)
	}
	path := filepath.Join(r.fx.RepoDir, "alpha", "alpha.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(data) + "\n// the fix\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, claimantEnv(r.fx, "s-1"), "", builderProbeArgs(r.fx, "alpha/alpha.go", 5, verifydeliver.ProbeCaught)...)
	if code != 0 {
		t.Fatalf("jig probe over an uncommitted edit: exit %d\n%s", code, out)
	}
	r.commit("a", "the fix", map[string]string{"alpha/alpha.go": edited})
	state := func() verifydeliver.SliceProbes {
		got, err := verifydeliver.BuilderProbesAt(r.st, r.fx.Ticket, "a", "fixture-repo", r.fx.RepoDir, "HEAD", false)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := state(); got.Current != 1 || got.Stale != 0 {
		t.Errorf("after the commit: %+v, want the probe current", got)
	}
	r.commit("a", "edit again", map[string]string{"alpha/alpha.go": edited + "// more\n"})
	if got := state(); got.Current != 0 || got.Stale != 1 {
		t.Errorf("after another edit: %+v, want the probe stale", got)
	}
}
