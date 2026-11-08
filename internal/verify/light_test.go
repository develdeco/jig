package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

const lightManifest = `workspaces:
  - id: alpha
    path: alpha
  - id: beta
    path: beta
oracles:
  test: "echo full {path}"
light:
  paths: ["*.md", "docs/**/*.md"]
  oracle: "echo lint"
`

// lightRig is a rig whose manifest declares the light rule, with a first
// commit of slice a that the full oracle has passed.
func lightRig(t *testing.T) (*rig, string) {
	t.Helper()
	r := newRig(t, fixture.Opts{})
	first := r.commit("a", "declare light", map[string]string{".claude/jig.yaml": lightManifest})
	res, err := Run(r.deps, r.request("a", first))
	if err != nil || res.State != journal.VerifyPass {
		t.Fatalf("first Run = %+v, %v", res, err)
	}
	if calls := r.oracle.seen(); len(calls) != 1 || calls[0].cmd != "echo full alpha" {
		t.Fatalf("first run commands = %+v, want the full oracle", calls)
	}
	return r, first
}

func lastOracle(t *testing.T, r *rig) journal.Line {
	t.Helper()
	lines := r.lines()
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Event == journal.EventOracle {
			return lines[i]
		}
	}
	t.Fatal("no oracle line")
	return journal.Line{}
}

type errString string

func (e errString) Error() string { return string(e) }

// TestLightDeltaRunsTheLightOracleAfterAPass: a Markdown-only commit after a
// pass runs the light oracle, journals the slice's command with ran and
// reused_from, and the slice is green; a second Markdown commit names the same
// origin, not the first light one.
func TestLightDeltaRunsTheLightOracleAfterAPass(t *testing.T) {
	t.Parallel()
	r, first := lightRig(t)
	second := r.commit("a", "notes", map[string]string{"README.md": "x", "docs/adr/0001.md": "y"})
	res, err := Run(r.deps, r.request("a", second))
	if err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	calls := r.oracle.seen()
	if len(calls) != 2 || calls[1].cmd != "echo lint" {
		t.Fatalf("commands = %+v, want the light oracle second", calls)
	}
	l := lastOracle(t, r)
	if l.Command != "echo full alpha" || l.Ran != "echo lint" || l.ReusedFrom != first || l.Outcome != "pass" || l.Commit != second {
		t.Errorf("oracle line = %+v", l)
	}
	if res.Ran != "echo lint" || res.ReusedFrom != first {
		t.Errorf("Result = %+v", res)
	}
	if st := r.state("a"); st.State != "green" {
		t.Errorf("state = %+v, want green", st)
	}

	third := r.commit("a", "more notes", map[string]string{"CONTEXT.md": "z"})
	if _, err := Run(r.deps, r.request("a", third)); err != nil {
		t.Fatal(err)
	}
	if l := lastOracle(t, r); l.Ran != "echo lint" || l.ReusedFrom != first {
		t.Errorf("a chained reuse = %+v, want the origin %s, not the light commit", l, first)
	}
}

// TestLightDeltaNeedsEveryFileLight: one non-light file runs the full oracle,
// and so does a Markdown file under skills.
func TestLightDeltaNeedsEveryFileLight(t *testing.T) {
	t.Parallel()
	r, _ := lightRig(t)
	mixed := r.commit("a", "mixed", map[string]string{"README.md": "x", "alpha/extra.txt": "y"})
	if _, err := Run(r.deps, r.request("a", mixed)); err != nil {
		t.Fatal(err)
	}
	if l := lastOracle(t, r); l.Ran != "" || l.ReusedFrom != "" {
		t.Errorf("mixed delta oracle line = %+v, want a full run", l)
	}
	skill := r.commit("a", "skill", map[string]string{"skills/x/SKILL.md": "x"})
	if _, err := Run(r.deps, r.request("a", skill)); err != nil {
		t.Fatal(err)
	}
	if calls := r.oracle.seen(); calls[len(calls)-1].cmd != "echo full alpha" {
		t.Errorf("a skills markdown delta ran %q, want the full oracle", calls[len(calls)-1].cmd)
	}
}

// TestLightDeltaWithoutAPassRunsTheFullOracle: nothing earlier passed, so the
// slice's own oracle runs even on a Markdown-only commit.
func TestLightDeltaWithoutAPassRunsTheFullOracle(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.commit("", "declare light", map[string]string{".claude/jig.yaml": lightManifest})
	md := r.commit("a", "notes", map[string]string{"README.md": "x"})
	if _, err := Run(r.deps, r.request("a", md)); err != nil {
		t.Fatal(err)
	}
	if calls := r.oracle.seen(); len(calls) != 1 || calls[0].cmd != "echo full alpha" {
		t.Errorf("commands = %+v, want the full oracle", calls)
	}
	if l := lastOracle(t, r); l.Ran != "" || l.ReusedFrom != "" {
		t.Errorf("oracle line = %+v", l)
	}
}

// TestLightDeltaAfterAFailedFullRunRunsTheFullOracle: a failed full run of the
// command since the pass stops the reuse; a failed light run does not.
func TestLightDeltaAfterAFailedFullRunRunsTheFullOracle(t *testing.T) {
	t.Parallel()
	r, first := lightRig(t)

	// A failed light run first: the next light delta still reuses the pass.
	r.oracle.err = errString("lint broke")
	bad := r.commit("a", "bad notes", map[string]string{"README.md": "x"})
	if res, err := Run(r.deps, r.request("a", bad)); err != nil || res.State != journal.VerifyFail {
		t.Fatalf("light run = %+v, %v, want a fail", res, err)
	}
	r.oracle.err = nil
	fixed := r.commit("a", "good notes", map[string]string{"README.md": "y"})
	if _, err := Run(r.deps, r.request("a", fixed)); err != nil {
		t.Fatal(err)
	}
	if l := lastOracle(t, r); l.Ran != "echo lint" || l.ReusedFrom != first {
		t.Errorf("after a failed light run: %+v, want the light oracle on %s", l, first)
	}

	// A failed full run since the pass: the next Markdown delta runs in full.
	r.oracle.err = errString("tests broke")
	code := r.commit("a", "code", map[string]string{"alpha/extra.txt": "x"})
	if res, err := Run(r.deps, r.request("a", code)); err != nil || res.State != journal.VerifyFail {
		t.Fatalf("full run = %+v, %v, want a fail", res, err)
	}
	r.oracle.err = nil
	md := r.commit("a", "notes again", map[string]string{"README.md": "z"})
	if _, err := Run(r.deps, r.request("a", md)); err != nil {
		t.Fatal(err)
	}
	if l := lastOracle(t, r); l.Ran != "" || l.ReusedFrom != "" || l.Commit != md {
		t.Errorf("after a failed full run: %+v, want a full run", l)
	}
}

// TestLightNeedsTheRuleAndAnAncestor: no light key never reuses, and a pass on
// a commit that is not an ancestor, or of another env, is not a base.
func TestLightNeedsTheRuleAndAnAncestor(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	first := r.commit("a", "no light key", map[string]string{"x.txt": "x"})
	if _, err := Run(r.deps, r.request("a", first)); err != nil {
		t.Fatal(err)
	}
	md := r.commit("a", "notes", map[string]string{"README.md": "x"})
	if _, err := Run(r.deps, r.request("a", md)); err != nil {
		t.Fatal(err)
	}
	if l := lastOracle(t, r); l.Ran != "" {
		t.Errorf("no light key: %+v, want a full run", l)
	}

	cmd := "echo full alpha"
	isAnc := func(a, d string) bool { return a == "anc" }
	lines := []journal.Line{
		{Event: journal.EventOracle, Outcome: "pass", Commit: "other", Command: cmd},
		{Event: journal.EventOracle, Outcome: "pass", Commit: "anc", Command: cmd, Env: "db"},
	}
	if _, ok := lightBase(lines, cmd, "", "head", isAnc); ok {
		t.Error("a pass on a non-ancestor, and one of another env, were taken as a base")
	}
	if b, ok := lightBase(lines, cmd, "db", "head", isAnc); !ok || b.Commit != "anc" {
		t.Errorf("lightBase = %+v, %v, want the env's pass on the ancestor", b, ok)
	}
}

// TestLightDeltaAfterAFailedRerunOfThePassRunsTheFullOracle: the next Markdown
// delta stays light from the passing commit, so only the failed full run of the
// command since the pass stops the reuse.
func TestLightDeltaAfterAFailedRerunOfThePassRunsTheFullOracle(t *testing.T) {
	t.Parallel()
	r, first := lightRig(t)
	r.oracle.err = errString("flaky suite")
	req := r.request("a", first)
	req.Rerun = true
	if res, err := Run(r.deps, req); err != nil || res.State != journal.VerifyFail {
		t.Fatalf("rerun = %+v, %v, want a fail", res, err)
	}
	r.oracle.err = nil
	md := r.commit("a", "notes", map[string]string{"README.md": "x"})
	if _, err := Run(r.deps, r.request("a", md)); err != nil {
		t.Fatal(err)
	}
	if l := lastOracle(t, r); l.Ran != "" || l.ReusedFrom != "" || l.Commit != md {
		t.Errorf("oracle line = %+v, want a full run after the failed rerun", l)
	}
}

// TestLightDeltaCountsADeletedNonLightFile: a commit that deletes a code file
// beside a Markdown edit runs the slice's own oracle.
func TestLightDeltaCountsADeletedNonLightFile(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	first := r.commit("a", "declare light", map[string]string{".claude/jig.yaml": lightManifest, "alpha/doomed.go": "package alpha\n"})
	if res, err := Run(r.deps, r.request("a", first)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("first Run = %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"rm", "-q", "alpha/doomed.go"}, {"add", "README.md"}, {"commit", "-q", "-m", "drop a file", "-m", "Jig-Slice: a"}} {
		if _, err := gitx.Run(r.fx.RepoDir, args...); err != nil {
			t.Fatal(err)
		}
	}
	sha, err := gitx.RevParse(r.fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(r.deps, r.request("a", sha)); err != nil {
		t.Fatal(err)
	}
	if l := lastOracle(t, r); l.Ran != "" || l.ReusedFrom != "" || l.Commit != sha {
		t.Errorf("oracle line = %+v, want a full run for a delta that deletes code", l)
	}
}

// TestLightBaseTakesOnlyThePassOfTheSameCommand: a pass of another command
// does not stand for the slice's.
func TestLightBaseTakesOnlyThePassOfTheSameCommand(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{{Event: journal.EventOracle, Outcome: "pass", Commit: "anc", Command: "go vet ./..."}}
	if _, ok := lightBase(lines, "go test ./...", "", "head", func(string, string) bool { return true }); ok {
		t.Error("a pass of another command was taken as a base")
	}
}
