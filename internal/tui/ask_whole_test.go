package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

const (
	longAskTitle  = "A failed write would pass silently through the retry loop in the config loader"
	longAskWhy    = "the fix budget is reached and a person has to say whether the loader keeps its retry loop"
	longAskReason = "The loop swallows the error the second time it fails, so a half-written file is read back as if it were whole by the next start of the program."
)

// askStatus is a status with the asks given as id, title and why, each with a
// rationale on its finding.
func askStatus(asks ...[3]string) string {
	var open, found [][]string
	for _, a := range asks {
		open = append(open, []string{a[0], "high", "alpha/a.go:3", a[1], a[2]})
		found = append(found, []string{a[0], "1", "ask", "high", "alpha/a.go:3", a[1], "asked", longAskReason, "", ""})
	}
	return strings.Join([]string{
		"ticket: JIG-2", "state: green", "fix_budget: 1 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "alpha", "unit", "-"},
		}),
		"questions: none",
		axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, open),
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "2", "1", "full", sha('0'), sha('c'), "4"},
		}),
		axi.Table("findings", findingColumns, found),
		axi.Help("Run `jig triage JIG-2 <id> keep|dismiss`"),
	}, "\n") + "\n"
}

func askRig(t *testing.T, w, h int, asks ...[3]string) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", askStatus(asks...), 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(jig2Mid()...)
	return g
}

var (
	askOne = [3]string{"r1-f1", longAskTitle, longAskWhy}
	askTwo = [3]string{"r1-f2", "Name shadows import", "-"}
)

// wholeAskText is what a person decides askOne from.
var wholeAskText = longAskTitle + " · " + longAskReason + " · " + longAskWhy

func TestAskWholeOnTicketAndReview(t *testing.T) {
	t.Parallel()
	for _, w := range []int{100, 40} {
		g := askRig(t, w, 100, askOne, askTwo)
		g.feed(jl(at(12, 0), "JIG-2", "", journal.EventTriage, with("s-person", "", "dismiss", "", "r1-f2", "person")))
		for _, screen := range []string{"ticket", "review"} {
			if screen == "review" {
				g.press("enter")
			}
			view := g.view()
			if g.m.Screen() != screen {
				t.Fatalf("at %d columns the screen is %s, want %s", w, g.m.Screen(), screen)
			}
			words := readWords(view)
			for _, want := range []string{wholeAskText, "Name shadows import"} {
				if !strings.Contains(words, readWords(want)) {
					t.Errorf("at %d columns the %s screen lacks the ask text %q:\n%s", w, screen, want, view)
				}
			}
			heading := map[string]string{"ticket": "\n  needs you\n", "review": "\n  asks\n"}[screen]
			for _, l := range strings.Split(between(view, heading, "ctivity"), "\n") {
				if strings.HasPrefix(l, "      ") && strings.Contains(l, "…") {
					t.Errorf("at %d columns the %s screen cuts the ask at %q:\n%s", w, screen, l, view)
				}
			}
		}
	}
}

// triaged is the triage commands the rig ran.
func triaged(g *rig) []string {
	var out []string
	for _, l := range g.r.lines() {
		if strings.HasPrefix(l, "triage ") {
			out = append(out, l)
		}
	}
	return out
}

func TestTriageNeedsTheAskDrawnWhole(t *testing.T) {
	t.Parallel()
	// Drawn whole, k and d decide the only ask left undecided.
	g := askRig(t, 80, 60, askOne)
	g.press("k")
	if got := triaged(g); len(got) != 1 || !strings.Contains(got[0], "triage JIG-2 r1-f1 keep") {
		t.Errorf("k on an ask drawn whole ran %q", got)
	}
	g = askRig(t, 80, 60, askOne)
	g.press("d")
	if got := triaged(g); len(got) != 1 || !strings.Contains(got[0], "triage JIG-2 r1-f1 dismiss") {
		t.Errorf("d on an ask drawn whole ran %q", got)
	}

	// On a screen too short for its text they run nothing and say what to do.
	for _, key := range []string{"k", "d"} {
		g = askRig(t, 80, 14, askOne)
		if g.m.drawn["r1-f1"] == askWhole {
			t.Fatalf("the test needs the ask cut by the window:\n%s", g.view())
		}
		g.press(key)
		if got := triaged(g); len(got) != 0 {
			t.Errorf("%s decided an ask the screen did not draw whole: %q", key, got)
		}
		if want := "ask r1-f1 is not drawn whole on this screen: make the screen taller, or open JIG-2 to read it"; g.m.notice != want {
			t.Errorf("notice = %q, want %q", g.m.notice, want)
		}
	}

	// The same screen made taller draws it, and the keys act.
	g.send(tea.WindowSizeMsg{Width: 80, Height: 60})
	g.press("d")
	if got := triaged(g); len(got) != 1 {
		t.Errorf("d after the screen was made taller ran %q", got)
	}

	// The target is chosen as before: several undecided asks and the cursor
	// not on one, nothing is decided and the notice says so.
	g = askRig(t, 80, 60, askOne, askTwo)
	g.press("k")
	if got := triaged(g); len(got) != 0 || !strings.Contains(g.m.notice, "several asks are waiting") {
		t.Errorf("with two asks open and the cursor elsewhere k ran %q, notice %q", got, g.m.notice)
	}
	// The cursor on an ask decides that one when it is drawn whole.
	g.press("end", "up")
	g.press("d")
	if got := triaged(g); len(got) != 1 || !strings.Contains(got[0], "triage JIG-2 r1-f1 dismiss") {
		t.Errorf("d on the first ask ran %q", got)
	}
}

// TestTriageActsExactlyWhenTheViewHoldsTheWholeAsk: at every height the keys act
// when the screen holds every word of the ask's text and not otherwise, so a
// window that cuts the text in the middle refuses too.
func TestTriageActsExactlyWhenTheViewHoldsTheWholeAsk(t *testing.T) {
	t.Parallel()
	// A notice on screen takes a row of the body, and a key clears it: the key
	// is judged against the screen the person read, notice and all. "a" with no
	// question open leaves one.
	// The tail pane takes half the body and cuts the page to what is left: with
	// it open the cursor stands on the ask, and the keys are judged against
	// what the page's share of the body drew.
	for _, variant := range []string{"plain", "notice", "tail"} {
		acted, refused := 0, 0
		for h := 10; h <= 60; h++ {
			g := askRig(t, 60, h, askOne)
			switch variant {
			case "notice":
				g.press("a")
				if g.m.notice == "" {
					t.Fatal("a with no question open left no notice")
				}
			case "tail":
				g.press("t", "end")
				if !g.m.tail {
					t.Fatal("t did not open the tail")
				}
			}
			whole := strings.Contains(readWords(g.view()), readWords(wholeAskText))
			g.press("d")
			got := len(triaged(g)) == 1
			if got != whole {
				t.Errorf("at height %d (%s) the screen holds the whole ask: %v, d acted: %v:\n%s", h, variant, whole, got, g.view())
			}
			if got {
				acted++
			} else {
				refused++
			}
		}
		if acted == 0 || refused == 0 {
			t.Errorf("the heights tried did not cross the edge (%s): acted %d, refused %d", variant, acted, refused)
		}
	}
}

// TestTriageIgnoresKeysOfOneReadAfterTheFirst: a read that brings several keys
// is taken a key at a time with no screen drawn between them, so k and d after
// another key of the same read are judged against nothing the person saw: they
// run nothing and say to press again, at every height, with a notice on screen
// and with the tail open.
func TestTriageIgnoresKeysOfOneReadAfterTheFirst(t *testing.T) {
	t.Parallel()
	read := func(g *rig, keys string) {
		g.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keys)})
	}
	for h := 10; h <= 60; h++ {
		g := askRig(t, 60, h, askOne)
		g.press("a") // a notice on screen
		read(g, "xd")
		if got := triaged(g); len(got) != 0 {
			t.Fatalf("at height %d d after another key of one read ran %q", h, got)
		}
		if want := "the screen changed under the key before it was drawn: press it again"; g.m.notice != want {
			t.Fatalf("at height %d notice = %q, want %q", h, g.m.notice, want)
		}

		// The tail left open, and closed by a key that comes before the d.
		g = askRig(t, 60, h, askOne)
		g.press("t", "end")
		read(g, "xd")
		if got := triaged(g); len(got) != 0 {
			t.Fatalf("at height %d d after another key of one read, the tail open, ran %q", h, got)
		}
		g = askRig(t, 60, h, askOne)
		g.press("t")
		read(g, "td")
		if got := triaged(g); len(got) != 0 {
			t.Fatalf("at height %d d after t in one read ran %q", h, got)
		}

		// A key of its own, after the screen is drawn, is judged as before.
		whole := strings.Contains(readWords(g.view()), readWords(wholeAskText))
		g.press("d")
		if got := len(triaged(g)) == 1; got != whole {
			t.Fatalf("at height %d the screen holds the whole ask: %v, d on its own acted: %v", h, whole, got)
		}

		// The first key of a read is judged against the screen drawn, as a key
		// of its own is: it acts when the screen holds the whole ask.
		g = askRig(t, 60, h, askOne)
		whole = strings.Contains(readWords(g.view()), readWords(wholeAskText))
		read(g, "dx")
		if got := len(triaged(g)) == 1; got != whole {
			t.Fatalf("at height %d the screen holds the whole ask: %v, the first d of a read acted: %v", h, whole, got)
		}
	}
}

// TestAskDrawnReadsTheWindowAsItIsDrawn: at every scroll position of the review
// page the check agrees with the screen, an ask the page lacks is neither whole
// nor on the page, and the check moves nothing.
func TestAskDrawnReadsTheWindowAsItIsDrawn(t *testing.T) {
	t.Parallel()
	whole, cut := 0, 0
	for height := 9; height <= 22; height++ {
		w, c := scrollAskDrawn(t, height)
		whole, cut = whole+w, cut+c
	}
	if whole == 0 || cut == 0 {
		t.Errorf("the scrolls did not cross the edge both ways: whole %d, cut %d", whole, cut)
	}
}

// scrollAskDrawn scrolls the review page of an ask a line at a time at the
// height given, and says how often the check found the ask whole and how often
// cut.
func scrollAskDrawn(t *testing.T, height int) (whole, cut int) {
	t.Helper()
	g := askRig(t, 60, height, askOne)
	g.press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want the review screen", g.m.Screen())
	}
	for i := 0; i < 80; i++ {
		view := g.view()
		want := strings.Contains(readWords(view), readWords(wholeAskText))
		state, onPage := g.m.drawn["r1-f1"]
		if (state == askWhole) != want || !onPage {
			t.Fatalf("scrolled %d lines: the record says %d (on the page %v), the screen holds the ask whole: %v:\n%s", i, state, onPage, want, view)
		}
		if want {
			whole++
		} else {
			cut++
		}
		g.press("down")
	}
	if state, onPage := g.m.drawn["r1-f9"]; state != 0 || onPage {
		t.Errorf("an ask the page lacks is on record as %d", state)
	}
	return whole, cut
}

// TestAskOnAScreenThatDoesNotDrawItIsNotShown: k and d on a screen with no line
// of the ask say to open the ticket, not to make the screen taller.
func TestAskOnAScreenThatDoesNotDrawItIsNotShown(t *testing.T) {
	t.Parallel()
	g := askRig(t, 80, 60, askOne)
	g.press("esc", "down") // the project's list, the cursor moved to JIG-2
	if got := g.m.targetTicket(); got != "JIG-2" {
		t.Fatalf("the cursor is on %q:\n%s", got, g.view())
	}
	g.press("d")
	if got := triaged(g); len(got) != 0 {
		t.Errorf("d on a screen that draws no ask ran %q", got)
	}
	if want := "ask r1-f1 is not shown on this screen: open JIG-2 to read it"; g.m.notice != want {
		t.Errorf("notice = %q, want %q", g.m.notice, want)
	}
}

func TestAskTextJoinsWhatIsThereToJoin(t *testing.T) {
	t.Parallel()
	tk := &ticket{status: &statusInfo{Findings: []statusFinding{{ID: "r1-f1", Rationale: "why it matters"}}}}
	for _, c := range []struct {
		ask  askRow
		want string
	}{
		{askRow{ID: "r1-f1", Title: "T", Why: "W"}, "T · why it matters · W"},
		{askRow{ID: "r1-f1", Title: "T", Why: "-"}, "T · why it matters"},
		{askRow{ID: "r1-f1", Title: "T"}, "T · why it matters"},
		{askRow{ID: "r1-f9", Title: "T", Why: "W"}, "T · W"},
	} {
		if got := tk.askText(c.ask); got != c.want {
			t.Errorf("askText(%+v) = %q, want %q", c.ask, got, c.want)
		}
	}
}
