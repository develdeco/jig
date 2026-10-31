package observe

import (
	"strings"
	"testing"
)

// TestAPromptOnNoTicketHearsHowWorkGetsOne: a session in a project whose branch
// binds no ticket hears, with its first prompt, that a change asked for
// without a ticket is planned first with the intake skill, which decides brief
// or chart and mints a ticket for a brief; it hears it once. A session on a ticket's branch does not.
func TestAPromptOnNoTicketHearsHowWorkGetsOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))
	res := r.handle(r.ev(KindPrompt))
	for _, want := range []string{"works on no ticket", "planned first with the intake skill (`jig skill intake`), which decides brief or chart and mints a ticket for a brief", "change no code before the brief is approved"} {
		if !strings.Contains(res.Context, want) {
			t.Errorf("the first prompt's context lacks %q:\n%s", want, res.Context)
		}
	}
	if again := r.handle(r.ev(KindPrompt)); strings.Contains(again.Context, "works on no ticket") {
		t.Errorf("the note was told twice:\n%s", again.Context)
	}

	b := newRig(t)
	b.onTicketBranch()
	b.handle(b.ev(KindSessionStart))
	if res := b.handle(b.ev(KindPrompt)); strings.Contains(res.Context, "works on no ticket") {
		t.Errorf("a session on a ticket's branch heard the note:\n%s", res.Context)
	}
}
