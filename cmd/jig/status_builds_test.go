package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestStatusShowsTheBuildsATicketsRecordsNameWhenTheyVary: a ticket whose
// lines two builds wrote lists both with the first and last line each wrote,
// a build a round's report alone names shows with no times, and a ticket of
// one build shows nothing extra.
func TestStatusShowsTheBuildsATicketsRecordsNameWhenTheyVary(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)

	add := func(ts, jig string) {
		t.Helper()
		if err := journal.Append(st, fx.Ticket, journal.Line{TS: ts, Event: "oracle", Jig: jig}); err != nil {
			t.Fatal(err)
		}
	}
	add("2026-11-06T10:00:00Z", "v0.1.3+aaaaaaa")
	add("2026-11-06T11:00:00Z", "v0.1.3+aaaaaaa")
	// A line an older jig wrote names no build.
	old := filepath.Join(st.TicketDir(fx.Ticket), "journal.ndjson")
	f, err := os.OpenFile(old, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ts":"2026-11-06T09:00:00Z","ticket":"` + fx.Ticket + `","event":"claim"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := block(statusOf(t, fx, st), "builds["); got != nil {
		t.Fatalf("one build shows a builds block: %v", got)
	}

	add("2026-11-06T12:00:00Z", "v0.1.3+bbbbbbb-dirty")
	add("2026-11-06T13:00:00Z", "v0.1.3+bbbbbbb-dirty")
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{report: "round: 1\nverdict: fix-slices\njig: v0.1.2+ccccccc\ntarget_sha:\n  fixture-repo: " + sha40("1") + "\n"})
	got := strings.Join(block(statusOf(t, fx, st), "builds["), "\n")
	want := strings.Join([]string{
		"builds[3]{jig,first,last}:",
		`  v0.1.3+aaaaaaa,"2026-11-06T10:00:00Z","2026-11-06T11:00:00Z"`,
		`  v0.1.3+bbbbbbb-dirty,"2026-11-06T12:00:00Z","2026-11-06T13:00:00Z"`,
		"  v0.1.2+ccccccc,-,-",
	}, "\n")
	if got != want {
		t.Errorf("builds block:\n%s\nwant:\n%s", got, want)
	}
}

// TestStatusBuildsKeepsEachBuildsOwnSpan: a ticket whose lines two builds
// wrote lists both (the two-build critical path, before any report), a line
// that arrives later in the journal but is earlier in time lowers its build's
// first, and a build that wrote one line has that line as both first and last.
func TestStatusBuildsKeepsEachBuildsOwnSpan(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)

	add := func(ts, jig string) {
		t.Helper()
		if err := journal.Append(st, fx.Ticket, journal.Line{TS: ts, Event: "oracle", Jig: jig}); err != nil {
			t.Fatal(err)
		}
	}
	// Merged by git, a journal's lines are not in time order.
	add("2026-11-06T11:00:00Z", "v0.1.3+aaaaaaa")
	add("2026-11-06T10:00:00Z", "v0.1.3+aaaaaaa")
	add("2026-11-06T12:00:00Z", "v0.1.3+bbbbbbb")
	got := strings.Join(block(statusOf(t, fx, st), "builds["), "\n")
	want := strings.Join([]string{
		"builds[2]{jig,first,last}:",
		`  v0.1.3+aaaaaaa,"2026-11-06T10:00:00Z","2026-11-06T11:00:00Z"`,
		`  v0.1.3+bbbbbbb,"2026-11-06T12:00:00Z","2026-11-06T12:00:00Z"`,
	}, "\n")
	if got != want {
		t.Errorf("builds block:\n%s\nwant:\n%s", got, want)
	}
}

// TestStatusBuildsReportMergeGuards: a round's report naming no build is not
// counted (a one-build ticket with an older round shows nothing extra), and a
// report naming a build that also wrote lines keeps that build's times.
func TestStatusBuildsReportMergeGuards(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)

	add := func(ts, jig string) {
		t.Helper()
		if err := journal.Append(st, fx.Ticket, journal.Line{TS: ts, Event: "oracle", Jig: jig}); err != nil {
			t.Fatal(err)
		}
	}
	add("2026-11-06T10:00:00Z", "v0.1.3+aaaaaaa")
	add("2026-11-06T11:00:00Z", "v0.1.3+aaaaaaa")
	// A report an older jig wrote has no jig key.
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{report: "round: 1\nverdict: fix-slices\ntarget_sha:\n  fixture-repo: " + sha40("1") + "\n"})
	if got := block(statusOf(t, fx, st), "builds["); got != nil {
		t.Fatalf("a report naming no build shows a builds block: %v", got)
	}

	// The same build writes its lines and its round's report.
	writeRound(t, fx.StoreDir, fx.Ticket, 2, roundFiles{report: "round: 2\nverdict: fix-slices\njig: v0.1.3+aaaaaaa\ntarget_sha:\n  fixture-repo: " + sha40("1") + "\n"})
	if got := block(statusOf(t, fx, st), "builds["); got != nil {
		t.Fatalf("one build named by lines and a report shows a builds block: %v", got)
	}

	add("2026-11-06T12:00:00Z", "v0.1.3+bbbbbbb")
	got := strings.Join(block(statusOf(t, fx, st), "builds["), "\n")
	want := strings.Join([]string{
		"builds[2]{jig,first,last}:",
		`  v0.1.3+aaaaaaa,"2026-11-06T10:00:00Z","2026-11-06T11:00:00Z"`,
		`  v0.1.3+bbbbbbb,"2026-11-06T12:00:00Z","2026-11-06T12:00:00Z"`,
	}, "\n")
	if got != want {
		t.Errorf("builds block:\n%s\nwant:\n%s", got, want)
	}
}
