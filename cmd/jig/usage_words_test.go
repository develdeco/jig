package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestUsageIsToldInWords: CPU in seconds with one decimal, memory in MiB or
// GiB, the basis in words, and nothing for a figure that is not there.
func TestUsageIsToldInWords(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		got, want string
	}{
		{cpuWords(63200), "63.2"},
		{cpuWords(1), "0.0"},
		{cpuWords(0), ""},
		{memoryWords(300 << 20), "300 MiB"},
		{memoryWords(1), "1 MiB"},
		{memoryWords(1536 << 20), "1.5 GiB"},
		{memoryWords(0), ""},
		{basisWords("job"), "job"},
		{basisWords("sampled"), "sampled peak"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// TestHereVerifyShowsWhatTheLatestRunUsed: the verification block and its JSON
// carry the CPU time, peak memory and basis of the commit's latest oracle line
// beside the seconds, and a line with none shows none.
func TestHereVerifyShowsWhatTheLatestRunUsed(t *testing.T) {
	t.Parallel()
	section := verifySection(t)
	r := newVerifyRig(t)
	sha := r.fixClamp()
	add := func(l journal.Line) {
		t.Helper()
		if err := journal.Append(r.st, r.fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	add(journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "pass", Commit: sha, Attempt: 1, Seconds: 41})
	blocks, err := section.Blocks(hereVerifyCtx(r, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(blocks, "\n"); strings.Contains(text, "cpu_seconds") || strings.Contains(text, "peak_") {
		t.Errorf("a line with no figures shows some:\n%s", text)
	}

	add(journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "pass", Commit: sha, Attempt: 2, Seconds: 41,
		CPUMs: 63200, PeakBytes: 3 << 29, PeakBasis: journal.PeakBasisSampled})
	blocks, err = section.Blocks(hereVerifyCtx(r, "a"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(blocks, "\n")
	for _, want := range []string{"seconds: 41", "cpu_seconds: 63.2", "peak_memory: 1.5 GiB", "peak_basis: sampled peak"} {
		if !strings.Contains(text, want) {
			t.Errorf("verification block lacks %q:\n%s", want, text)
		}
	}
	v, err := section.JSON(hereVerifyCtx(r, "a"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(v)
	for _, want := range []string{`"cpu_seconds":"63.2"`, `"peak_memory":"1.5 GiB"`, `"peak_basis":"sampled peak"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON lacks %s: %s", want, data)
		}
	}
}

// TestStatusListsTheLastRunOfEachSliceWithWhatItUsed: one row per slice with an
// oracle line, its latest; a run with no figures reads "not measured".
func TestStatusListsTheLastRunOfEachSliceWithWhatItUsed(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	add := func(l journal.Line) {
		t.Helper()
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	if got := block(statusOf(t, fx, st), "last_runs["); got != nil {
		t.Fatalf("a ticket with no oracle line shows a last_runs block: %v", got)
	}
	a := "0123456789abcdef0123456789abcdef01234567"
	b := "fedcba9876543210fedcba9876543210fedcba98"
	add(journal.Line{Slice: "one", Event: journal.EventOracle, Outcome: "fail", Commit: a, Attempt: 1, Seconds: 9, CPUMs: 1000, PeakBytes: 1 << 20, PeakBasis: "job"})
	add(journal.Line{Slice: "one", Event: journal.EventOracle, Outcome: "pass", Commit: b, Attempt: 2, Seconds: 41, CPUMs: 63200, PeakBytes: 3 << 29, PeakBasis: "sampled"})
	add(journal.Line{Slice: "two", Event: journal.EventOracle, Outcome: "pass", Commit: a, Attempt: 1, Seconds: 5})
	got := strings.Join(block(statusOf(t, fx, st), "last_runs["), "\n")
	want := strings.Join([]string{
		"last_runs[2]{slice,commit,seconds,cpu_seconds,peak_memory,basis}:",
		"  one,fedcba987654,41,63.2,1.5 GiB,sampled peak",
		"  two,0123456789ab,5,not measured,not measured,not measured",
	}, "\n")
	if got != want {
		t.Errorf("last_runs block:\n%s\nwant:\n%s", got, want)
	}
}
