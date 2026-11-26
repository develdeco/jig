package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// inOrder fails the test unless each of wants is in text, each below the one
// before it.
func inOrder(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	last := -1
	for _, want := range wants {
		at := strings.Index(text, want)
		if at < 0 || at < last {
			t.Errorf("%s: %q is not below what precedes it (at %d after %d):\n%s", what, want, at, last, text)
			return
		}
		last = at
	}
}

// TestRoundsAndTheirProbesReadNewestFirst: the review screen lists the newest
// round on top, and the probes block the newest round's probes first, each
// round's in the order recorded with a survivor first.
func TestRoundsAndTheirProbesReadNewestFirst(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", probeStatus([][]string{
		probeRound("1", "3", "1"),
		probeRound("2", "2", "0"),
		probeRound("3", "1", "0"),
	},
		axi.Table("probes", probeColumns, [][]string{
			{"1", "caught", "alpha/a.go:9", "the parser", "flip the guard", "go test ./alpha"},
			{"1", "survived", "beta/b.go:4", "the greeting", "drop the check", "go test ./beta"},
			{"1", "caught", "alpha/a.go:20", "the loader", "swap the branches", "go test ./alpha"},
			{"2", "caught", "gamma/c.go:5", "the writer", "skip the flush", "go test ./gamma"},
			{"2", "caught", "gamma/c.go:7", "the reader", "skip the read", "go test ./gamma"},
			{"3", "caught", "delta/d.go:1", "the cache", "never evict", "go test ./delta"},
		}),
	), 0)
	g.feed(append(jig2Mid(),
		jl(at(12, 30), "JIG-2", "", journal.EventGateOpen),
		jl(at(12, 40), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
		jl(at(12, 50), "JIG-2", "", journal.EventGateOpen),
		jl(at(12, 55), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 3, "s-reviewer" }),
	)...).press("enter")
	text := pageText(g)
	rounds, probes, _ := strings.Cut(text, "Probes\n")
	inOrder(t, "rounds", rounds, "round 3 ", "round 2 ", "round 1 ")
	inOrder(t, "probes", readWords(probes),
		"round 3 delta/d.go:1", "round 2 gamma/c.go:5", "round 2 gamma/c.go:7",
		"round 1 beta/b.go:4", "round 1 alpha/a.go:9", "round 1 alpha/a.go:20")
}

// TestTheRoundUnderWayReadsAboveTheClosedOnes: a round that is open and has no
// closing line yet is the newest, whatever number its row takes.
func TestTheRoundUnderWayReadsAboveTheClosedOnes(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(backwards(append(jig2Mid(), jl(at(13, 0), "JIG-2", "", journal.EventGateOpen)))...).press("enter").sized(100, 60)
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want review", g.m.Screen())
	}
	rounds, _, _ := strings.Cut(pageText(g), "\nAsks\n")
	inOrder(t, "rounds", readWords(rounds), "round 2 open", "round 1 findings")
}

// TestFixSlicesSentBackAreRowsOfTheBuildWhateverTheOrderOfTheLines: the fix
// slices a round sent back are rows of the build screen's list, each reading its
// own round whatever order the lines came in, and each row still opens its slice;
// the review screen has no panel of them.
func TestFixSlicesSentBackAreRowsOfTheBuildWhateverTheOrderOfTheLines(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	g.r.answer("status JIG-6 --store /s", fixSliceStatus(true), 0)
	g.feed(backwards(fixSliceLines())...)
	g.press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	inOrder(t, "fix slices", readWords(pageText(g)), "round 1 · Fix round one", "round 2 · Fix round two")
	var slices []string
	for _, r := range g.m.page().refs() {
		if r.kind == rowSlice {
			slices = append(slices, r.id)
		}
	}
	if strings.Join(slices, " ") != "a fix-1 fix-2" {
		t.Errorf("the rows that open a slice are %v, want a, fix-1 and fix-2", slices)
	}
	g.press("down", "enter")
	if g.m.Screen() != "slice" || g.m.top().slice != "fix-1" {
		t.Errorf("a fix slice's row opens %s %q, want the slice screen of fix-1", g.m.Screen(), g.m.top().slice)
	}
	g.press("esc", "esc", "down", "enter")
	if g.m.Screen() != "review" || strings.Contains(pageText(g), "Sent back") {
		t.Errorf("the review screen has a Sent back panel:\n%s", pageText(g))
	}
}

// TestLandingsReadNewestFirst: a ticket that landed in two repos in turn says
// the later landing first, in the landings and in the words of its end state.
func TestLandingsReadNewestFirst(t *testing.T) {
	t.Parallel()
	g := chartRig(t)
	land := func(ts, repo, base string) {
		g.feed(jl(ts, "JIG-4", "", journal.EventLanded, func(l *journal.Line) { l.Repo, l.Base = repo, base }))
	}
	land(at(15, 10), "web", "main")
	land(at(15, 0), "api", "main") // written after web, stamped before it
	land(at(15, 20), "docs", "main")
	landed := g.m.landedLines(g.m.w.tickets["JIG-4"])
	var repos []string
	for _, l := range landed {
		repos = append(repos, l.Repo)
	}
	if got := strings.Join(repos, " "); got != "docs web api" {
		t.Errorf("landings = %s, want docs web api", got)
	}
	if got, want := landedWords("landed", landed), "landed · docs into main at 09:15:20; web into main at 09:15:10; api into main at 09:15:00"; got != want {
		t.Errorf("landed words = %q, want %q", got, want)
	}
}
