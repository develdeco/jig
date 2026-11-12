package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// deltaContext opens round 2 over a one-file change (beta.go) after round 1
// filed a note on that file and a person kept it (the gate queues the keep's
// slice, which is built, empty, beside the change), and prints round 2's
// review context.
func deltaContext(t *testing.T, change func(t *testing.T, fx *fixture.Fixture)) (*fixture.Fixture, string) {
	t.Helper()
	fx, e := floorNoteRound1(t)
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Worth doing now.", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "fix-2-beta-test") {
		t.Fatalf("jig gate: exit = %d, want 0 and the kept note's slice queued\n%s", code, out)
	}
	change(t, fx)
	gitMust(t, fx.RepoDir, "commit", "--allow-empty", "-m", fx.Ticket+" fix-2-beta-test: done with the change", "-m", "Jig-Slice: fix-2-beta-test")
	sha := gitMust(t, fx.RepoDir, "rev-parse", "HEAD")
	if out, code := runMain(t, cloneEnv(fx), "", "verify", fx.Ticket, sha, "--slice", "fix-2-beta-test", "--wait", "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "state: pass") {
		t.Fatalf("jig verify of the kept note's slice: exit = %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d, want 0 (round 2 opens)\n%s", code, out)
	}
	out, code := runMain(t, e, "", "review", "context", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("review context: exit = %d\n%s", code, out)
	}
	return fx, out
}

// TestADeltaRoundsContextHoldsOnlyItsDelta: a round 2 over one
// changed file prints that file's diff, the kept note it answers, the file in
// must_review and the read line, and none of the round 1 reading: no
// recordings, no whole-change hunks count.
func TestADeltaRoundsContextHoldsOnlyItsDelta(t *testing.T) {
	t.Parallel()
	_, out := deltaContext(t, func(t *testing.T, fx *fixture.Fixture) { changeBeta(t, fx) })
	for _, want := range []string{
		"scope: delta",
		"read: ",
		"delta:\n",
		"  files: 1\n",
		"  diff: |\n",
		"+// Greet trims nothing yet.",
		"diff --git a/beta/beta.go b/beta/beta.go",
		"answers[1]{id,file:line,action,risk,title,decision}:",
		"Greet does not trim surrounding whitespace",
		"decided: keep",
		"must_review[1]{path}:\n  beta/beta.go",
		"oracles_passed[",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a delta round's context lacks %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"recordings:", "hunks:", "open[", "delta.diff"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a delta round's context holds %q, which round 1 read\n%s", unwanted, out)
		}
	}
}

// TestAFullRoundsContextIsUnchanged: round 1 prints the whole change's hunks
// count and the open table, and no delta or answers block.
func TestAFullRoundsContextIsUnchanged(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home).atTerminal()
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}
	out, code := runMain(t, e, "", "review", "context", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("review context: exit = %d\n%s", code, out)
	}
	for _, want := range []string{"scope: full", "hunks: ", "unprobed: ", "open[0]{id,file:line,action,risk,title,decision}:", "must_review[", "probed[0]"} {
		if !strings.Contains(out, want) {
			t.Errorf("a full round's context lacks %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"delta:", "answers[", "read: "} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a full round's context holds %q\n%s", unwanted, out)
		}
	}
}

// TestALongDeltaIsHeldInAFileBesideTheRequest: over 400 lines the context
// prints the line count and the path of the diff file, not the lines.
func TestALongDeltaIsHeldInAFileBesideTheRequest(t *testing.T) {
	t.Parallel()
	_, out := deltaContext(t, func(t *testing.T, fx *fixture.Fixture) {
		path := filepath.Join(fx.RepoDir, "beta", "beta.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.Write(data)
		for i := 0; i < 450; i++ {
			fmt.Fprintf(&b, "// filler line %d\n", i)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		gitMust(t, fx.RepoDir, "add", "-A")
		gitMust(t, fx.RepoDir, "commit", "-m", fx.Ticket+" d: long change", "-m", "Jig-Slice: d")
		sha := gitMust(t, fx.RepoDir, "rev-parse", "HEAD")
		if o, code := runMain(t, cloneEnv(fx), "", "verify", fx.Ticket, sha, "--slice", "d", "--wait", "--store", fx.StoreDir); code != 0 || !strings.Contains(o, "state: pass") {
			t.Fatalf("jig verify: exit = %d\n%s", code, o)
		}
	})
	if strings.Contains(out, "filler line") || strings.Contains(out, "  diff: |") {
		t.Errorf("a long delta's context prints the diff\n%s", out)
	}
	i := strings.Index(out, "diff_path: ")
	if i < 0 {
		t.Fatalf("a long delta's context names no diff file\n%s", out)
	}
	path := strings.Trim(strings.SplitN(out[i+len("diff_path: "):], "\n", 2)[0], `"`)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the diff file: %v", err)
	}
	if !strings.Contains(string(data), "filler line 449") {
		t.Errorf("the diff file lacks the change")
	}
	if !strings.Contains(out, "  lines: 458\n") {
		t.Errorf("the context does not state the line count\n%s", out)
	}
}
