package e2e

// End-to-end test of what the detail screens read from the real binary: the
// slices' workspace and oracle, the brief's text, the commits and their
// verifications, the rounds and what they found. The TUI is the real `jig tui`
// on a pipe over a ticket that was built, had one round close with an ask, and
// has a brief on disk.

import "testing"

// TestTUIShowsTheRecordsOfTheSteps: from the review step, which is the current
// one, up through the build to the brief, each screen shows what `jig status`
// and `jig brief show --text` printed for it.
func TestTUIShowsTheRecordsOfTheSteps(t *testing.T) {
	t.Parallel()
	b, _ := askedRound(t)
	T := b.fx.Ticket
	tui := startJig(t, b.env, b.builder.Clone, "tui", T)
	screens := tuiScreens{t: t, p: tui}
	// The cursor follows the current step once the status is read, so the keys
	// wait for the Steps panel to say where the ticket stands.
	screens.until("› "+T, "round 1 left 1 ask")

	// The round: who reviewed it, whether they stood apart from the builders,
	// what they read, what they raised, and the ask with the reviewer's reason.
	screens.press("\r")
	screens.until("round 1  findings", "raised 1 ask", "independent", "files read", "fix budget 0 of 3 rounds used",
		"Which greeting does the product want?", "undecided", "a user-visible string")

	// The build: each slice's workspace and oracle from slices.yaml, and the
	// commit that was verified, with its subject.
	screens.press("\x7f")
	screens.press("\x1b[A")
	screens.press("\r")
	screens.until("workspace alpha · oracle test", "workspace beta · oracle test", "“JIG-1 a: ", "fix budget 0 of 3 rounds used")

	// The brief: its sections with their first lines, read by the screen from
	// the verb, and the slices that cite them.
	screens.press("\x7f")
	screens.press("\x1b[A")
	screens.press("\r")
	screens.until("Slice A - failing clamp", "`alpha.Clamp` is committed broken", "workspace alpha · oracle test · after a")
	screens.press("q")
	if res := tui.Stop(); res.Code != 0 {
		t.Fatalf("jig tui exit = %d, want 0 after q\nstderr:\n%s", res.Code, res.Stderr)
	}
}
