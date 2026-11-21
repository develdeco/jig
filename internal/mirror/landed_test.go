package mirror

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func appendLines(t *testing.T, st *store.Store, ticket string, lines ...journal.Line) {
	t.Helper()
	for _, l := range lines {
		if err := journal.Append(st, ticket, l); err != nil {
			t.Fatal(err)
		}
	}
}

func prLine(repo, url, into string) journal.Line {
	return journal.Line{Event: journal.EventPr, Repo: repo, URL: url, Into: into}
}

func landedLine(repo, url, into string) journal.Line {
	return journal.Line{Event: journal.EventLanded, Repo: repo, URL: url, Into: into}
}

// TestTicketStatusLandedReadsDoneWithNoPullRequestKnown: a journal whose pull
// requests all have landed lines reads Done with no pull request known to the
// host; one repo of two landed reads as before; a shared task needs each issue;
// a closure still wins.
func TestTicketStatusLandedReadsDoneWithNoPullRequestKnown(t *testing.T) {
	status := func(t *testing.T, st *store.Store, ticket string, prs []PRRef) string {
		t.Helper()
		got, err := TicketStatus(st, ticket, prs)
		if err != nil {
			t.Fatalf("TicketStatus: %v", err)
		}
		return got
	}
	newTicket := func(t *testing.T) *store.Store {
		t.Helper()
		st := &store.Store{Root: t.TempDir()}
		if err := st.CreateTicketRecord("DEMO-1", store.Ticket{}); err != nil {
			t.Fatal(err)
		}
		return st
	}

	t.Run("one repo landed", func(t *testing.T) {
		st := newTicket(t)
		appendLines(t, st, "DEMO-1", prLine("app", "https://github.com/o/app/pull/1", ""), landedLine("app", "https://github.com/o/app/pull/1", ""))
		if got := status(t, st, "DEMO-1", nil); got != "Done" {
			t.Fatalf("status = %q, want Done", got)
		}
	})

	t.Run("landed outranks an open pull request", func(t *testing.T) {
		st := newTicket(t)
		appendLines(t, st, "DEMO-1", prLine("app", "https://github.com/o/app/pull/1", ""), landedLine("app", "https://github.com/o/app/pull/1", ""))
		if got := status(t, st, "DEMO-1", []PRRef{{Owner: "o", Repo: "app", Number: 2, State: "OPEN"}}); got != "Done" {
			t.Fatalf("status = %q, want Done", got)
		}
	})

	t.Run("one repo of two landed reads as before", func(t *testing.T) {
		st := newTicket(t)
		appendLines(t, st, "DEMO-1",
			prLine("app", "https://github.com/o/app/pull/1", ""),
			prLine("lib", "https://github.com/o/lib/pull/1", ""),
			landedLine("app", "https://github.com/o/app/pull/1", ""))
		if got := status(t, st, "DEMO-1", nil); got != "In progress" {
			t.Fatalf("status = %q, want In progress", got)
		}
		if got := status(t, st, "DEMO-1", []PRRef{{Owner: "o", Repo: "lib", Number: 1, State: "OPEN"}}); got != "In review" {
			t.Fatalf("status = %q, want In review", got)
		}
		appendLines(t, st, "DEMO-1", landedLine("lib", "https://github.com/o/lib/pull/1", ""))
		if got := status(t, st, "DEMO-1", nil); got != "Done" {
			t.Fatalf("status = %q, want Done once both landed", got)
		}
	})

	t.Run("a shared task needs each issue", func(t *testing.T) {
		st := newTicket(t)
		appendLines(t, st, "DEMO-1",
			prLine("app", "https://github.com/o/app/pull/1", "I-1"),
			prLine("app", "https://github.com/o/app/pull/2", "I-2"),
			landedLine("app", "https://github.com/o/app/pull/1", "I-1"))
		if got := status(t, st, "DEMO-1", nil); got != "In progress" {
			t.Fatalf("status = %q, want In progress", got)
		}
		appendLines(t, st, "DEMO-1", landedLine("app", "https://github.com/o/app/pull/2", "I-2"))
		if got := status(t, st, "DEMO-1", nil); got != "Done" {
			t.Fatalf("status = %q, want Done", got)
		}
	})

	t.Run("no pull request is not landed", func(t *testing.T) {
		st := newTicket(t)
		appendLines(t, st, "DEMO-1", landedLine("app", "https://github.com/o/app/pull/1", ""))
		if got := status(t, st, "DEMO-1", nil); got != "In progress" {
			t.Fatalf("status = %q, want In progress", got)
		}
	})

	t.Run("a closure outranks landed", func(t *testing.T) {
		st := newTicket(t)
		appendLines(t, st, "DEMO-1", prLine("app", "https://github.com/o/app/pull/1", ""), landedLine("app", "https://github.com/o/app/pull/1", ""))
		if err := st.CloseTicket("DEMO-1", store.Closed{Reason: store.CloseNotPlanned, By: "me", At: "2026-01-01"}); err != nil {
			t.Fatal(err)
		}
		if got := status(t, st, "DEMO-1", nil); got != "Not planned" {
			t.Fatalf("status = %q, want Not planned", got)
		}
	})
}

// TestSyncLandedPlacesAnIssueAndItsTasksInDone: a sync of an issue with two
// landed tasks, no pull request known to the host, places all three in Done and
// closes the issue; a second sync places nothing.
func TestSyncLandedPlacesAnIssueAndItsTasksInDone(t *testing.T) {
	w := newTasksWorld(t)
	for _, id := range []string{"DEMO-1", "DEMO-2", "DEMO-3"} {
		url := "https://github.com/example/demo/pull/" + id
		appendLines(t, w.st, id, prLine("demo", url, ""), landedLine("demo", url, ""))
	}
	report, client := w.sync(true)
	for _, id := range []string{"DEMO-1", "DEMO-2", "DEMO-3"} {
		if got := placedStatus(client, id); got != "Done" {
			t.Errorf("%s Status = %q, want Done", id, got)
		}
	}
	for _, is := range client.issues {
		if (is.Title == "The need" || is.Title == "First task" || is.Title == "Second task") && is.Open {
			t.Errorf("issue %q is open, want closed", is.Title)
		}
	}
	again, _ := w.sync(true)
	if len(again.Placed) != 0 {
		t.Errorf("second sync placed %d items, want none (first placed %d)", len(again.Placed), len(report.Placed))
	}
}
