package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// The screens are drawn from a stream and from what the verbs said, and
// compared with the golden files in testdata: go test ./internal/tui -update
// rewrites them, and the diff is the review of a change to a screen.

// mainRig is the project: JIG-1 mid-build, JIG-2 published, JIG-3 not started.
func mainRig(t *testing.T) *rig {
	g := newRig(t, "")
	g.feed(append(jig1(), jig2()...)...)
	return g
}

func TestMainScreen(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	if g.m.Screen() != "main" {
		t.Fatalf("opened on %s, want main", g.m.Screen())
	}
	golden(t, "main", g.view())
}

func TestTicketScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	golden(t, "ticket", g.view())
}

func TestTicketScreenWithAsks(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	golden(t, "ticket-asks", g.view())
}

// TestBuildScreenWithoutRecords: a status of a store that predates the sections
// has no workspace, oracle or goal, and the screen shows the journal's words.
func TestBuildScreenWithoutRecords(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	golden(t, "build-plain", g.view())
}

func TestBuildScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.feed(jig1Records()...).press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	golden(t, "build", g.view())
}

func TestSliceScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.feed(jig1Records()...).press("enter", "down", "down", "enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want slice", g.m.Screen())
	}
	golden(t, "slice", g.view())
}

func TestReviewScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...).press("enter") // the cursor follows the current step: the review
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	golden(t, "review", g.view())
}

func TestReviewScreenPublished(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.feed(jig2()...).press("up", "enter") // from the publish, the current step, up to the review
	golden(t, "review-published", g.view())
}

func TestPublishScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.feed(jig2()...).press("enter")
	if g.m.Screen() != "publish" {
		t.Fatalf("on %s, want publish", g.m.Screen())
	}
	golden(t, "publish", g.view())
}

func TestBriefScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.r.answer("brief show JIG-1 --text --store /s", briefTextJIG1(), 0)
	g.feed(append([]events.Envelope{
		jl(at(0, 0), "JIG-1", "", journal.EventApproval, with("s-planner", "", "", "Ada Lovelace <ada@example.invalid>", "0123456789ab", "agent")),
	}, jig1()...)...).press("up", "enter")
	if g.m.Screen() != "brief" {
		t.Fatalf("on %s, want brief", g.m.Screen())
	}
	golden(t, "brief", g.view())
}

func TestAnswerPromptScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("a", "Casual,", "space", "please")
	golden(t, "answer-prompt", g.view())
}

func TestPublishConfirmScreen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.feed(jig2()...).press("p")
	golden(t, "publish-confirm", g.view())
}

func TestTailPane(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("t")
	golden(t, "tail", g.view())
}

func TestCommandPanelShowsWhatTheVerbPrinted(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("triage JIG-2 r1-f2 dismiss --store /s", strings.Join([]string{
		"triage:", "  ticket: JIG-2", "  finding: r1-f2", "  decision: dismiss", "  provenance: person",
		"help[1]:", "  The next round applies it: run `jig gate JIG-2`",
	}, "\n")+"\n", 0)
	g.feed(jig2Mid()...).press("enter", "down", "d") // the review, then r1-f2
	golden(t, "panel-ok", g.view())

	g.r.answer("gate JIG-2 --store /s", "error: round 1 is not recorded\ncode: GATE_NOT_GREEN\nhelp[1]:\n  Run `jig next JIG-2`\n", 1)
	g.press("g")
	golden(t, "panel-fail", g.view())
}

// TestEveryStateHasItsOwnLookAndColor: a screen read without color tells
// states apart by their icons, and one read with color by both.
func TestEveryStateHasItsOwnLookAndColor(t *testing.T) {
	t.Parallel()
	states := []string{
		StateQueued, StateBuilding, StateVerifying, StateGreen, StateNeedsInput, StateStalled, StateEnvBlocked,
		StateReviewing, StateFindings, StateReady, StatePublishing, StatePublished, StateAsk, StateKept, StateDismissed,
		StateOK, StateFail,
	}
	plain := styles{r: newRig(t, "").m.st.r}
	paint := styles{r: colored()}

	icons := map[string][]string{}
	for _, s := range states {
		l := lookOf(s)
		if l.Icon == "" || l.Icon == lookOf("no such state").Icon {
			t.Errorf("state %q has no icon of its own", s)
		}
		icons[l.Icon] = append(icons[l.Icon], s)
		if got := plain.icon(s); got != l.Icon {
			t.Errorf("without color the icon of %q is %q, want the bare glyph %q", s, got, l.Icon)
		}
		if got := paint.icon(s); !strings.Contains(got, "\x1b[") || !strings.Contains(got, l.Icon) {
			t.Errorf("with color the icon of %q is %q, want the glyph in an escape sequence", s, got)
		}
	}
	// A glyph is shared only by states that are one thing seen in two places:
	// a round's findings and the ask in it, a published ticket and a command
	// that worked, a stalled slice and a command that failed. They never stand
	// side by side in a column.
	shared := map[string][]string{
		"!": {StateFindings, StateAsk},
		"✓": {StatePublished, StateOK},
		"✖": {StateStalled, StateFail},
	}
	for icon, who := range icons {
		if len(who) > 1 && !reflect.DeepEqual(who, shared[icon]) {
			t.Errorf("icon %q stands for %v: states must be told apart without color", icon, who)
		}
	}
	// The slice states, which sit side by side in one column, have distinct colors too.
	colors := map[string]string{}
	for _, s := range []string{StateQueued, StateBuilding, StateVerifying, StateGreen, StateNeedsInput, StateStalled} {
		c := lookOf(s).Color
		if other, dup := colors[c]; dup {
			t.Errorf("%q and %q share color %s", s, other, c)
		}
		colors[c] = s
	}
}

// TestColorIsOnlyDecoration: the same screen drawn with color has the same
// text as the one drawn without.
func TestColorIsOnlyDecoration(t *testing.T) {
	t.Parallel()
	plain := newRig(t, "JIG-1")
	plain.feed(jig1()...)
	paint := newRig(t, "JIG-1")
	paint.m.st = styles{r: colored()}
	paint.feed(jig1()...)

	got := paint.view()
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("the colored screen has no escape sequence:\n%s", got)
	}
	if stripped := stripANSI(got); stripped != plain.view() {
		t.Errorf("color changed the text\n--- plain\n%s\n--- colored, stripped\n%s", plain.view(), stripped)
	}
}

// stripANSI removes SGR sequences.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
