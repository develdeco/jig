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

// TestAPromptNamingATicketHearsItsPhaseAndSkill: a prompt that names a store
// ticket, from a directory working on no ticket, hears the ticket's phase and
// the skill for it once, and not the fresh-work note.
func TestAPromptNamingATicketHearsItsPhaseAndSkill(t *testing.T) {
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
