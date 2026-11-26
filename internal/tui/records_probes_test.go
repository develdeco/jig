package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

var probeColumns = []string{"round", "outcome", "file:line", "seam", "mutation", "command"}

// probeStatus is reviewStatus with the tables given added before its help.
func probeStatus(rounds [][]string, tables ...string) string {
	out := reviewStatus(rounds, nil)
	i := strings.LastIndex(out, "help[")
	return out[:i] + strings.Join(tables, "\n") + "\n" + out[i:]
}

// probeRound is a recorded clean round row with the probe counts given.
func probeRound(n, probes, survived string) []string {
	return []string{n, "clean", "s-reviewer", "-", "-", "true", "0", "0", "0", "full", sha('0'), sha('c'), "4", "0", probes, survived}
}

// TestReviewScreenShowsEachRoundsProbes: the probes section says how many probes
// the rounds recorded, and the probes block has a
// row for each probe, a survivor first within its round, with a long mutation
// wrapped whole and the seam and command beneath it.
func TestReviewScreenShowsEachRoundsProbes(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	long := "replace the early return in the loader with a fall through so the config is read twice and the second read overwrites the first with whatever the environment holds at that point"
	g.r.answer("status JIG-2 --store /s", probeStatus([][]string{
		probeRound("1", "3", "1"),
		probeRound("2", "0", "0"),
		{"3", "findings", "-", "-", "-", "-", "0", "0", "0", "-", "-", sha('d'), "-", "-", "-", "-"},
	},
		axi.Table("probes", probeColumns, [][]string{
			{"1", "caught", "alpha/a.go:9", "the parser", "flip the guard", "go test ./alpha"},
			{"1", "caught", "alpha/a.go:20", "the loader", long, "go test ./alpha -run Load"},
			{"1", "survived", "beta/b.go:4", "the greeting", "drop the check", "go test ./beta"},
		}),
	), 0)
	g.feed(append(jig2Mid(),
		jl(at(12, 30), "JIG-2", "", journal.EventGateOpen),
		jl(at(12, 40), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
		jl(at(12, 50), "JIG-2", "", journal.EventGateOpen),
		jl(at(12, 55), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt = 3 }),
	)...).press("enter")
	text := pageText(g)
	for _, want := range []string{
		"3 probes · 2 caught · 1 survived",
		"as recorded: a file changed since a round is not marked",
		"round 1 beta/b.go:4 survived",
		"round 1 alpha/a.go:9 caught",
		"the greeting · go test ./beta",
	} {
		if !strings.Contains(readWords(text), want) {
			t.Errorf("the screen lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "beta/b.go:4") > strings.Index(text, "alpha/a.go:9") {
		t.Errorf("the survivor is not listed first in its round:\n%s", text)
	}
	// The long mutation wraps over several lines and none of it is cut.
	var wrapped []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "replace the early") || strings.Contains(long, strings.TrimSpace(l)) && strings.TrimSpace(l) != "" && len(strings.Fields(l)) > 3 {
			wrapped = append(wrapped, strings.TrimSpace(l))
		}
	}
	if len(wrapped) < 2 || strings.Join(wrapped, " ") != long {
		t.Errorf("the long mutation is not wrapped whole: %q", wrapped)
	}
}

// TestReviewScreenMarksASupersededProbe: a probe a later round probed on the
// same file and line is drawn back and says what that round found; one nobody
// probed again is not.
func TestReviewScreenMarksASupersededProbe(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", probeStatus([][]string{
		probeRound("1", "3", "2"),
		probeRound("2", "2", "1"),
	},
		axi.Table("probes", probeColumns, [][]string{
			{"1", "survived", "a.go:4", "s", "m1", "c"},
			{"1", "survived", "a.go:8", "s", "m2", "c"},
			{"1", "caught", "a.go:12", "s", "m3", "c"},
			{"2", "caught", "a.go:4", "s", "m4", "c"},
			{"2", "survived", "a.go:8", "s", "m5", "c"},
			{"2", "survived", "a.go:12", "s", "m6", "c"},
		}),
	), 0)
	g.feed(jig2Mid()...).press("enter")
	text := pageText(g)
	for _, want := range []string{
		"round 1 a.go:4 survived · caught in round 2",
		"round 1 a.go:8 survived · survived again in round 2",
		"round 1 a.go:12 caught · survived in round 2 (caught in round 1)",
	} {
		if !strings.Contains(readWords(text), want) {
			t.Errorf("the screen lacks %q:\n%s", want, text)
		}
	}
	for _, l := range strings.Split(text, "\n") {
		if f := strings.Fields(l); len(f) > 3 && f[1] == "round" && f[2] == "2" && strings.Contains(l, " in round ") {
			t.Errorf("the latest probe of a line is marked superseded: %q", l)
		}
	}
}

// TestParseStatusProbes: the probe columns and the probes table are read by
// name; a status without them parses as before, with the counts unknown, and
// the page of a ticket with no reviewer round shows no probes.
func TestParseStatusProbes(t *testing.T) {
	t.Parallel()
	info := parseStatus(probeStatus([][]string{probeRound("1", "2", "1")},
		axi.Table("probes", probeColumns, [][]string{{"1", "survived", "a.go:4", "seam", "mut", "cmd"}}),
	))
	r := info.Rounds[0]
	if r.Probes != 2 || r.Survived != 1 {
		t.Errorf("round counts = %+v, want 2 probes, 1 survived", r)
	}
	if len(info.Probes) != 1 || info.Probes[0] != (statusProbe{Round: 1, Outcome: "survived", Where: "a.go:4", Seam: "seam", Mutation: "mut", Command: "cmd"}) {
		t.Errorf("probes = %+v", info.Probes)
	}

	old := parseStatus(reviewStatus([][]string{{"1", "clean", "s-reviewer", "-", "-", "true", "0", "0", "0", "full", sha('0'), sha('c'), "4"}}, nil))
	if r := old.Rounds[0]; r.Probes != -1 || r.Survived != -1 || len(old.Probes) != 0 {
		t.Errorf("an older status has probe counts: %+v, probes %v", r, old.Probes)
	}
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewStatus([][]string{{"1", "clean", "s-reviewer", "-", "-", "true", "0", "0", "0", "full", sha('0'), sha('c'), "4"}}, nil), 0)
	g.feed(jig2Mid()...).press("enter")
	text := pageText(g)
	for _, not := range []string{"probe", "unprobed", "as recorded"} {
		if strings.Contains(text, not) {
			t.Errorf("an older status shows %q:\n%s", not, text)
		}
	}
	g2 := newRig(t, "JIG-2")
	g2.r.answer("status JIG-2 --store /s", reviewStatus(nil, nil), 0)
	g2.feed(jig2Mid()...).press("enter")
	if text := pageText(g2); strings.Contains(text, "probe") || strings.Contains(text, "unprobed") {
		t.Errorf("a ticket with no reviewer round shows a ledger:\n%s", text)
	}
}
