package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

var (
	probeColumns = []string{"round", "outcome", "file:line", "seam", "mutation", "command"}
	skipColumns  = []string{"round", "file", "range", "why"}
	hunkColumns  = []string{"round", "file", "from", "to"}
)

// ledgerStatus is reviewStatus with the ledger tables added before its help.
func ledgerStatus(rounds [][]string, tables ...string) string {
	out := reviewStatus(rounds, nil)
	i := strings.LastIndex(out, "help[")
	return out[:i] + strings.Join(tables, "\n") + "\n" + out[i:]
}

// round is a recorded clean round row with the ledger counts given.
func ledgerRound(n, probes, survived, skipped, unprobed string) []string {
	return []string{n, "clean", "s-reviewer", "-", "-", "true", "0", "0", "0", "full", sha('0'), sha('c'), "4", "0", probes, survived, skipped, unprobed}
}

// TestReviewScreenShowsEachRoundsProbes: a reviewer round says what it probed in
// the ledger's words, a round that probed nothing says so, and the probes block
// has a row for each probe, a survivor first within its round, with a long
// mutation wrapped whole and the seam and command beneath it.
func TestReviewScreenShowsEachRoundsProbes(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	long := "replace the early return in the loader with a fall through so the config is read twice and the second read overwrites the first with whatever the environment holds at that point"
	g.r.answer("status JIG-2 --store /s", ledgerStatus([][]string{
		ledgerRound("1", "3", "1", "1", "2"),
		ledgerRound("2", "0", "0", "0", "0"),
		{"3", "findings", "-", "-", "-", "-", "0", "0", "0", "-", "-", sha('d'), "-", "-", "-", "-", "-"},
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
		"3 probes · 2 caught · 1 survived · 2 hunks unprobed · 1 skipped",
		"no probes · 0 hunks unprobed",
		"as recorded: a file changed since a round is not marked",
		"round 1 beta/b.go:4 survived",
		"round 1 alpha/a.go:9 caught",
		"the greeting · go test ./beta",
	} {
		if !strings.Contains(readWords(text), want) {
			t.Errorf("the screen lacks %q:\n%s", want, text)
		}
	}
	if rounds, _, _ := strings.Cut(text, "Probes\n"); strings.Count(rounds, "unprobed") != 2 {
		t.Errorf("a round no reviewer ran has a counts line:\n%s", text)
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
	g.r.answer("status JIG-2 --store /s", ledgerStatus([][]string{
		ledgerRound("1", "3", "2", "0", "0"),
		ledgerRound("2", "2", "1", "0", "0"),
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

// TestReviewScreenUnprobedHunks: the latest recording round's hunks are listed
// as file:from-to with its skips and their reasons; a round that left none says
// so; a status whose rounds recorded none shows no block.
func TestReviewScreenUnprobedHunks(t *testing.T) {
	t.Parallel()
	t.Run("listed with a skip's reason", func(t *testing.T) {
		t.Parallel()
		g := newRig(t, "JIG-2")
		g.r.answer("status JIG-2 --store /s", ledgerStatus([][]string{
			ledgerRound("1", "0", "0", "0", "5"),
			ledgerRound("2", "1", "0", "2", "2"),
		},
			axi.Table("probes", probeColumns, [][]string{{"2", "caught", "a.go:4", "s", "m", "c"}}),
			axi.Table("probe_skips", skipColumns, [][]string{
				{"2", "a_test.go", "whole file", "a test file"},
				{"2", "gen.go", "3-8", "generated"},
			}),
			axi.Table("unprobed", hunkColumns, [][]string{{"2", "web/c.go", "1", "5"}, {"2", "web/d.go", "10", "12"}}),
		), 0)
		g.feed(jig2Mid()...).press("enter")
		text := pageText(g)
		if got := summaryOf(g.m.page(), "Unprobed"); got != "round 2 left 2 hunks" {
			t.Errorf("the unprobed panel says %q", got)
		}
		for _, want := range []string{"web/c.go:1-5", "web/d.go:10-12", "a_test.go set aside: a test file", "gen.go:3-8 set aside: generated"} {
			if !strings.Contains(readWords(text), want) {
				t.Errorf("the screen lacks %q:\n%s", want, text)
			}
		}
	})
	t.Run("none left", func(t *testing.T) {
		t.Parallel()
		g := newRig(t, "JIG-2")
		g.r.answer("status JIG-2 --store /s", ledgerStatus([][]string{ledgerRound("1", "0", "0", "0", "0")}), 0)
		g.feed(jig2Mid()...).press("enter")
		if text := pageText(g); summaryOf(g.m.page(), "Unprobed") != "round 1 left no hunk unprobed" || !strings.Contains(text, "no hunk left unprobed") {
			t.Errorf("the screen does not say the round left none:\n%s", text)
		}
	})
	t.Run("not recorded", func(t *testing.T) {
		t.Parallel()
		g := newRig(t, "JIG-2")
		g.r.answer("status JIG-2 --store /s", ledgerStatus([][]string{ledgerRound("1", "0", "0", "0", "-")}), 0)
		g.feed(jig2Mid()...).press("enter")
		text := pageText(g)
		if strings.Contains(text, "unprobed") {
			t.Errorf("a round that did not record its hunks says something of them:\n%s", text)
		}
		if !strings.Contains(text, "no probes") {
			t.Errorf("the round's counts line is missing:\n%s", text)
		}
	})
}

// TestParseStatusProbes: the ledger columns and tables are read by name; a status
// without them parses as before, with the counts unknown, and the page of a ticket
// with no reviewer round shows no ledger.
func TestParseStatusProbes(t *testing.T) {
	t.Parallel()
	info := parseStatus(ledgerStatus([][]string{ledgerRound("1", "2", "1", "1", "-")},
		axi.Table("probes", probeColumns, [][]string{{"1", "survived", "a.go:4", "seam", "mut", "cmd"}}),
		axi.Table("probe_skips", skipColumns, [][]string{{"1", "t_test.go", "whole file", "tests"}}),
		axi.Table("unprobed", hunkColumns, [][]string{{"1", "b.go", "2", "3"}}),
	))
	r := info.Rounds[0]
	if r.Probes != 2 || r.Survived != 1 || r.Skipped != 1 || r.Unprobed != -1 {
		t.Errorf("round counts = %+v, want 2 probes, 1 survived, 1 skipped, unprobed unknown", r)
	}
	if len(info.Probes) != 1 || info.Probes[0] != (statusProbe{Round: 1, Outcome: "survived", Where: "a.go:4", Seam: "seam", Mutation: "mut", Command: "cmd"}) {
		t.Errorf("probes = %+v", info.Probes)
	}
	if len(info.ProbeSkips) != 1 || info.ProbeSkips[0] != (statusSkip{Round: 1, File: "t_test.go", Range: "whole file", Why: "tests"}) {
		t.Errorf("probe skips = %+v", info.ProbeSkips)
	}
	if len(info.Unprobed) != 1 || info.Unprobed[0] != (statusHunk{Round: 1, File: "b.go", From: 2, To: 3}) {
		t.Errorf("unprobed = %+v", info.Unprobed)
	}

	old := parseStatus(reviewStatus([][]string{{"1", "clean", "s-reviewer", "-", "-", "true", "0", "0", "0", "full", sha('0'), sha('c'), "4"}}, nil))
	if r := old.Rounds[0]; r.Probes != -1 || r.Survived != -1 || r.Skipped != -1 || r.Unprobed != -1 || len(old.Probes) != 0 {
		t.Errorf("an older status has ledger counts: %+v, probes %v", r, old.Probes)
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
