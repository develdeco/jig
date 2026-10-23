package e2e

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// skillCommand returns the first line of skills/<skill>/SKILL.md that starts
// with prefix, trimmed: the command the skill tells a session to run.
func skillCommand(t *testing.T, skill, prefix string) string {
	t.Helper()
	data := readFileOrFatal(t, filepath.Join(repoRoot, "skills", skill, "SKILL.md"))
	for _, line := range strings.Split(normalizeCRLF(string(data)), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("skills/%s/SKILL.md has no line starting %q", skill, prefix)
	return ""
}

var (
	shaRE    = regexp.MustCompile(`\b[0-9a-f]{12}\b`)
	oracleRE = regexp.MustCompile(`(?m)^  oracle_command: .*$`)
	quotedRE = regexp.MustCompile(`"(<(?:store|repo)>[^"]*)"`)
)

// normalizeContext makes the output of jig context the same on every machine:
// the directories it names become <store> and <repo>, the shas <sha>, and the
// oracle command, which names the Go toolchain of the host, is checked apart
// (the return value) and left out.
func normalizeContext(t *testing.T, out string, dirs map[string]string) (normalized, oracle string) {
	t.Helper()
	out = strings.ReplaceAll(normalizeCRLF(out), `\`, `/`)
	out = strings.ReplaceAll(out, `\`, `/`)
	for token, dir := range dirs {
		out = strings.ReplaceAll(out, filepath.ToSlash(dir), token)
		// A temp directory may be a symlink (macOS), and jig names it either way.
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			out = strings.ReplaceAll(out, filepath.ToSlash(resolved), token)
		}
	}
	out = quotedRE.ReplaceAllString(out, "$1")
	if m := oracleRE.FindString(out); m != "" {
		oracle = m
		out = oracleRE.ReplaceAllString(out, "  oracle_command: <oracle>")
	}
	return shaRE.ReplaceAllString(out, "<sha>"), oracle
}

// TestContextGoldenMatchesBuildSkill: the command skills/build tells a session
// to run to get the context of a slice, filled in for ticket JIG-1 and slice
// b, is exactly `jig context JIG-1 --slice b`, and what it prints for a slice
// whose neighbour a was built and verified in the clone, whose own first
// verification failed, and with an answered question, is the golden.
func TestContextGoldenMatchesBuildSkill(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	h := newHarness(t, fx, env, "s-build")
	h.Branch("jig/" + fx.Ticket)
	shaA := h.CommitScenario("a", 1)

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	for _, l := range []journal.Line{
		{Slice: "a", Event: journal.EventVerified, Commit: shaA, Attempt: 1},
		{Slice: "b", Event: journal.EventOracle, Outcome: "fail", Commit: shaA, Attempt: 1, Seconds: 4,
			Summary: "alpha/caller_test.go:9: Percent(5, 0, 10) = 0, want 50"},
	} {
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddQuestion(fx.Ticket, "b", "Should Percent clamp its input?"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AnswerAs(fx.Ticket, "q-001", "Yes, through Clamp.", "person"); err != nil {
		t.Fatal(err)
	}

	cmd := skillCommand(t, "build", "jig context ")
	cmd = strings.NewReplacer("<ticket>", fx.Ticket, "<slice>", "b").Replace(cmd)
	if cmd != "jig context JIG-1 --slice b" {
		t.Fatalf("skills/build tells a session to run %q, want %q", cmd, "jig context JIG-1 --slice b")
	}

	r := runJig(t, env, fx.RepoDir, strings.Fields(cmd)[1:]...)
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", cmd, r.Code, r.Stdout, r.Stderr)
	}
	got, oracle := normalizeContext(t, r.Stdout, map[string]string{"<store>": fx.StoreDir, "<repo>": fx.RepoDir})
	if !strings.Contains(oracle, "test ./alpha/...") {
		t.Errorf("the oracle line %q does not run the tests of workspace alpha", oracle)
	}
	assertGolden(t, "context-b.txt", got)
}
