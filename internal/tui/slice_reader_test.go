package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// squeezed is a screen's words with the frame and the spaces gone.
func squeezed(view string) string { return strings.ReplaceAll(readWords(view), " ", "") }

// readsAs says whether the words of want are on the screen in order: a field that
// wraps under its column has the other columns' cells between its lines, so the
// words are read as a run with others allowed between them.
func readsAs(view, want string) bool {
	if strings.Contains(squeezed(view), strings.ReplaceAll(want, " ", "")) {
		return true
	}
	wanted := strings.Fields(want)
	next := 0
	for _, w := range strings.Fields(readWords(view)) {
		if next < len(wanted) && w == wanted[next] {
			next++
		}
	}
	return next == len(wanted)
}

// reaches says whether want is drawn in the window of the screen, or comes into
// it by the keys: down in the focused pane, then each pane after it. It moves
// the cursor, and says how many keys it took.
func reaches(g *rig, want string, keys int) (found bool, moves int) {
	if readsAs(g.view(), want) {
		return true, 0
	}
	for pane := 0; pane < 8; pane++ {
		for i := 0; i < keys; i++ {
			g.press("down")
			moves++
			if readsAs(g.view(), want) {
				return true, moves
			}
		}
		g.press("tab")
		moves++
	}
	return false, moves
}

// TestEverySliceFactIsReachableAtEverySize: a slice with a long goal and a long
// oracle draws its facts before them, so each fact is in the window the screen
// opens on at 60x20 and at 100x30, and the long text is reached by the keys.
func TestEverySliceFactIsReachableAtEverySize(t *testing.T) {
	t.Parallel()
	goal := strings.Repeat("Greeting rewrites the welcome copy so every visitor is named by first name. ", 6)
	oracle := "go test -count=1 ./internal/greeting -run '" + strings.Repeat("TestNamesTheVisitorByFirstName|", 8) + "TestLast'"
	status := strings.Join([]string{
		"ticket: JIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate", "findings"}, [][]string{
			{"a", "green", "1", "-", "-", "Say hello", "app", "unit", "-", "-"},
			{"b", "building", "2", "a", "-", goal, "app", oracle, "-", "-"},
			{"c", "queued", "0", "b", "-", "Say bye", "app", "unit", "-", "-"},
		}),
		"questions: none",
	}, "\n") + "\n"
	lines := []events.Envelope{jl(at(0, 5), "JIG-1", "b", journal.EventClaim, with("s-builder", "", "", "", "", ""))}
	for _, size := range [][2]int{{60, 20}, {100, 30}} {
		w, h := size[0], size[1]
		g := openSlice(t, status, lines, "b", w, h)
		for _, fact := range []string{"state", "tries", "held by builder", "after", "needed by", "workspace app"} {
			if found, moves := reaches(g, fact, 0); !found || moves != 0 {
				t.Errorf("at %dx%d the fact %q is not in the window the screen opens on:\n%s", w, h, fact, g.view())
			}
		}
		for _, long := range []string{goal, oracle} {
			if found, _ := reaches(g, long[len(long)-30:], 40); !found {
				t.Errorf("at %dx%d the end of %q is reached by no key:\n%s", w, h, long[:30], g.view())
			}
		}
	}
}

// TestSliceScreenKeepsTheColorOfEveryWrappedLine: the text under a commit is
// colored after it is wrapped, so each line it came to is whole in its color.
func TestSliceScreenKeepsTheColorOfEveryWrappedLine(t *testing.T) {
	t.Parallel()
	g := openSlice(t, fixRoundStatus(), storyLines(), "b", 60, 40)
	g.m.st = styles{r: colored()}
	g.m.w.tickets["JIG-1"].memo = nil // the body was built plain
	var commits []string
	in := false
	for _, l := range g.m.page().lines {
		switch {
		case l.box == boxTop && strings.Contains(l.text, "Commits"):
			in = true
		case l.box == boxBottom:
			in = false
		case in && strings.TrimSpace(l.text) != "":
			commits = append(commits, l.text)
		}
	}
	if len(commits) < 8 {
		t.Fatalf("the commits panel has %d lines:\n%s", len(commits), strings.Join(commits, "\n"))
	}
	wrapped := 0
	for _, l := range commits {
		if !strings.Contains(l, "\x1b[") {
			t.Errorf("a line of the commits panel has no color: %q", l)
		}
		if strings.Contains(l, "(sampled peak)") && !strings.Contains(l, "oracle passed") {
			wrapped++
		}
	}
	if wrapped == 0 {
		t.Errorf("no run line wrapped at 60 columns, so the test proves nothing:\n%s", strings.Join(commits, "\n"))
	}
}

// TestADownOnTheSliceScreenBuildsNoRow: a slice with a thousand recordings is
// laid out when the store or the stream changes, and a key that moves the cursor
// builds and lays out nothing.
func TestADownOnTheSliceScreenBuildsNoRow(t *testing.T) {
	t.Parallel()
	var recs []journal.Recording
	for i := 0; i < 1200; i++ {
		recs = append(recs, journal.Recording{File: "r" + strings.Repeat("x", i%7) + ".webm", Size: int64(1000 + i), Scenario: "scenario", Step: i})
	}
	lines := append(jig1Records(),
		jl(at(3, 0), "JIG-1", "b", journal.EventOracle, with("s-builder", sha('f'), "pass", "", "", "")),
		jl(at(3, 1), "JIG-1", "b", journal.EventRecorded, with("", sha('f'), "", "", "", ""), func(l *journal.Line) { l.Recordings = recs }),
	)
	g := openSlice(t, fixRoundStatus(), lines, "b", 100, 30)
	built, laid := g.m.sliceBuilds, g.m.laidBuilds
	if built == 0 {
		t.Fatal("the slice screen built no body")
	}
	for i := 0; i < 5; i++ {
		g.press("down")
		g.view()
	}
	g.press("up")
	g.view()
	if g.m.sliceBuilds != built || g.m.laidBuilds != laid {
		t.Errorf("keys built %d bodies and laid out %d lists, want none", g.m.sliceBuilds-built, g.m.laidBuilds-laid)
	}
	// What arrives builds it again, and then the frames that follow build nothing.
	g.feed(jl(at(4, 0), "JIG-1", "b", journal.EventNote, with("s-builder", "", "", "a note", "", "")))
	g.view()
	built = g.m.sliceBuilds
	g.view()
	g.press("down")
	g.view()
	if g.m.sliceBuilds != built {
		t.Errorf("frames after a line came in built %d bodies, want none", g.m.sliceBuilds-built)
	}
}

// TestAReleasedSliceKeepsItsBuilder: the builder is the slice's last claimant,
// whether it holds the slice or has let go of it.
func TestAReleasedSliceKeepsItsBuilder(t *testing.T) {
	t.Parallel()
	lines := []events.Envelope{
		jl(at(0, 5), "JIG-1", "a", journal.EventClaim, with("s-both", "", "", "", "", "")),
		jl(at(0, 6), "JIG-1", "", journal.EventApproval, with("s-both", "", "", "", "", "")),
		jl(at(0, 9), "JIG-1", "a", journal.EventRelease, with("s-both", "", "", "", "", "")),
	}
	status := slicesStatus([]string{"a", "queued", "1", "-", "-", "Greet", "app", "unit", "-"})
	g := openSlice(t, status, lines, "a", 100, 30)
	if page := readWords(pageText(g)); !strings.Contains(page, "built by builder") {
		t.Errorf("a released slice draws no builder:\n%s", pageText(g))
	}
}

// TestARecordedLinesOutcomeIsDrawn: what a recorded line says of the files it
// left out is drawn with its commit, and a commit nobody wrote a subject for has
// no subject header.
func TestARecordedLinesOutcomeIsDrawn(t *testing.T) {
	t.Parallel()
	lines := []events.Envelope{
		jl(at(3, 0), "JIG-1", "a", journal.EventOracle, with("s-builder", sha('f'), "pass", "", "", "")),
		jl(at(3, 1), "JIG-1", "a", journal.EventRecorded, with("", sha('f'), "", "", "", ""), func(l *journal.Line) {
			l.Outcome = "1034 recordings beyond the first 200 left out"
		}),
	}
	status := slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"})
	g := openSlice(t, status, lines, "a", 100, 30)
	page := readWords(pageText(g))
	if !strings.Contains(page, "1034 recordings beyond the first 200 left out") {
		t.Errorf("the outcome of the recorded line is not drawn:\n%s", pageText(g))
	}
	if commits := between(pageText(g), "Commits", "Timeline"); strings.Contains(commits, "subject") {
		t.Errorf("a column with no value has a header:\n%s", commits)
	}
}
