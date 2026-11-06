package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// promptContext sends a UserPromptSubmit hook for prompt, from the fixture
// clone on its default branch (no ticket bound), and returns the context the
// session reads.
func promptContext(t *testing.T, fx *fixture.Fixture, e env, prompt string) string {
	t.Helper()
	out, code := runMain(t, e, hookPayload(t, fx, "UserPromptSubmit", map[string]any{"prompt": prompt}), "observe", "--harness", "claude")
	if code != 0 {
		t.Fatalf("UserPromptSubmit exit %d, output %q", code, out)
	}
	if out == "" {
		return ""
	}
	var got struct {
		Specific struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := jsonUnmarshal(out, &got); err != nil {
		t.Fatalf("stdout is not the one JSON object Claude Code reads: %q: %v", out, err)
	}
	return got.Specific.Context
}

// TestObservePromptNamingATicketHearsItsPhaseAndSkill: a prompt that names a store
// ticket, from a directory working on no ticket, hears the ticket's phase and
// the skill for it once, and not the fresh-work note.
func TestObservePromptNamingATicketHearsItsPhaseAndSkill(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	e := testEnv(fx.Home).inDir(fx.RepoDir).withExecutable(filepath.Join(fx.Dir, "bin", "jig"))

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := derivePhase(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	skill := map[phase.Name]string{phase.Brief: "intake", phase.Build: "build", phase.Review: "review", phase.Publish: "publish"}[tk.Current]

	got := promptContext(t, fx, e, "please carry on with "+fx.Ticket+" now")
	for _, want := range []string{fx.Ticket, string(tk.Current), "`jig skill " + skill + "`", "`jig next " + fx.Ticket + "`"} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt's context lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "works on no ticket") {
		t.Errorf("a prompt naming a ticket heard the fresh-work note:\n%s", got)
	}
}

// TestObservePromptHearsTheSameNotesForEitherSlashOfTheCwd: a harness on
// Windows may send the directory with backslashes or with slashes (as valid
// JSON, a backslash is two characters on the wire); either way the prompt
// is placed in the project and hears the fresh-work note.
func TestObservePromptHearsTheSameNotesForEitherSlashOfTheCwd(t *testing.T) {
	t.Parallel()
	for _, form := range []string{"slash", "backslash"} {
		if form == "backslash" && filepath.Separator == '/' {
			continue // a backslash is a filename character here, not a separator
		}
		fx := newFixture(t, fixture.Opts{})
		e := testEnv(fx.Home).inDir(fx.RepoDir).withExecutable(filepath.Join(fx.Dir, "bin", "jig"))
		cwd := filepath.ToSlash(fx.RepoDir)
		if form == "backslash" {
			cwd = strings.ReplaceAll(cwd, "/", `\`)
		}
		out, code := runMain(t, e, hookPayload(t, fx, "UserPromptSubmit", map[string]any{"prompt": "add a flag", "cwd": cwd}), "observe", "--harness", "claude")
		if code != 0 || !strings.Contains(out, "works on no ticket") {
			t.Errorf("%s cwd %s: exit %d, output %q, want the fresh-work note", form, cwd, code, out)
		}
	}
}
