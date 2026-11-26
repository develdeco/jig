package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestARunIsNamedByItsRunnersRoleAndWitnessedByJig: a session that drafted the
// brief and then ran the oracle is the builder in a run's words, which never say
// who started the run in the words of the line's source, and the command is
// printed only when it is not the slice's own.
func TestARunIsNamedByItsRunnersRoleAndWitnessedByJig(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(
		jl(at(0, 1), "JIG-1", "", journal.EventApproval, with("s-both", "", "", "", "", "")),
		jl(at(0, 5), "JIG-1", "a", journal.EventOracle, with("s-both", sha('a'), "pass", "", "", ""), func(l *journal.Line) {
			l.Command, l.Seconds, l.Source = "go test ./...", 15, "jig"
		}),
		jl(at(0, 9), "JIG-1", "a", journal.EventOracle, with("s-both", sha('b'), "fail", "", "", ""), func(l *journal.Line) {
			l.Command, l.Source, l.Uncovered = "go vet ./...", "session", journal.UncoveredHeadMoved
		}),
	)
	tk := g.m.w.tickets["JIG-1"]
	runs := tk.commits("a")
	if len(runs) != 2 {
		t.Fatalf("commits = %+v, want two", runs)
	}
	own := g.m.runWords(tk, runs[0].Runs[0], "go test ./...")
	if own != "oracle passed in 15s · run by builder, witnessed by jig" {
		t.Errorf("a run of the slice's own oracle reads %q", own)
	}
	other := g.m.runWords(tk, runs[1].Runs[0], "go test ./...")
	for _, want := range []string{"`go vet ./...` failed", "covers nothing: head moved", "run by builder, witnessed by jig"} {
		if !strings.Contains(other, want) {
			t.Errorf("a run of another command reads %q, want it to say %q", other, want)
		}
	}
	for _, text := range []string{own, other} {
		if strings.Contains(text, "jig ran it") || strings.Contains(text, "the session ran it") || strings.Contains(text, "planner") {
			t.Errorf("a run is worded by who started it or by its runner's first role: %q", text)
		}
	}
}

// TestARunShowsItsFiguresWhenMeasured: what a run's process tree used is said
// in the words `jig status` uses, and a run whose line measured none says
// nothing of them.
func TestARunShowsItsFiguresWhenMeasured(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(
		jl(at(0, 5), "JIG-1", "a", journal.EventOracle, with("s-b", sha('a'), "pass", "", "", ""), func(l *journal.Line) {
			l.CPUMs, l.PeakBytes, l.PeakBasis = 63200, 1536<<20, journal.PeakBasisSampled
		}),
		jl(at(0, 6), "JIG-1", "a", journal.EventOracle, with("s-b", sha('b'), "pass", "", "", "")),
	)
	tk := g.m.w.tickets["JIG-1"]
	recs := tk.commits("a")
	if got := g.m.runWords(tk, recs[0].Runs[0], ""); !strings.Contains(got, "cpu 63.2s · peak 1.5 GiB (sampled peak)") {
		t.Errorf("a measured run reads %q", got)
	}
	if got := g.m.runWords(tk, recs[1].Runs[0], ""); strings.Contains(got, "cpu") || strings.Contains(got, "peak") || strings.Contains(got, "not measured") {
		t.Errorf("a run that was not measured reads %q", got)
	}
}

// TestACommitsTimeIsItsLatestRunOrVerification: a commit is as late as its latest
// run or verified line, and a verify-pending line gives it no time, no subject
// and no commit at all.
func TestACommitsTimeIsItsLatestRunOrVerification(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(
		jl(at(0, 1), "JIG-1", "a", journal.EventVerifyPending, with("s-b", sha('a'), "", "asked", "", "")),
		jl(at(0, 2), "JIG-1", "a", journal.EventVerifyPending, with("s-b", sha('b'), "", "asked", "", "")),
		jl(at(0, 10), "JIG-1", "a", journal.EventOracle, with("s-b", sha('b'), "pass", "", "", "")),
		jl(at(0, 30), "JIG-1", "a", journal.EventVerified, with("", sha('b'), "", "", "", "")),
		jl(at(0, 59), "JIG-1", "a", journal.EventVerifyPending, with("s-b", sha('b'), "", "asked again", "", "")),
	)
	tk := g.m.w.tickets["JIG-1"]
	recs := tk.commits("a")
	if len(recs) != 1 || recs[0].SHA != sha('b') {
		t.Fatalf("commits = %+v, want the one commit a run was of", recs)
	}
	if recs[0].Subject != "" {
		t.Errorf("a verify-pending line gave the commit the subject %q", recs[0].Subject)
	}
	if got := g.m.commitWhen(recs[0]); got != "09:00:30" {
		t.Errorf("the commit's time is %q, want its verified line's 09:00:30 and not the later pending line's", got)
	}
	if got := g.m.commitWhen(commitRecord{SHA: sha('c')}); got != "" {
		t.Errorf("a commit with no run and no verification has the time %q", got)
	}
}

// TestACommitCarriesTheRecordingsOfItsRun: the files a commit's recorded line
// names, with the scenario, flow, step, caption, file name and size, are the
// commit's; another commit's are not.
func TestACommitCarriesTheRecordingsOfItsRun(t *testing.T) {
	t.Parallel()
	want := journal.Recording{File: "greet-1.webm", Size: 2048, Scenario: "greet", Flow: "happy", Step: 2, Caption: "the greeting"}
	g := newRig(t, "JIG-1")
	g.feed(
		jl(at(0, 10), "JIG-1", "a", journal.EventOracle, with("s-b", sha('a'), "pass", "", "", "")),
		jl(at(0, 11), "JIG-1", "a", journal.EventRecorded, with("", sha('a'), "", "", "", ""), func(l *journal.Line) {
			l.Recordings = []journal.Recording{want}
		}),
		jl(at(0, 20), "JIG-1", "a", journal.EventOracle, with("s-b", sha('b'), "pass", "", "", "")),
	)
	recs := g.m.w.tickets["JIG-1"].commits("a")
	if len(recs) != 2 {
		t.Fatalf("commits = %+v, want two", recs)
	}
	if len(recs[0].Recordings) != 1 || recs[0].Recordings[0] != want {
		t.Errorf("the first commit's recordings are %+v, want %+v", recs[0].Recordings, want)
	}
	if len(recs[1].Recordings) != 0 {
		t.Errorf("the second commit has the recordings %+v of another", recs[1].Recordings)
	}
}
