package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// closedStore is a store of tickets T-1 and T-2, T-2 carrying the alias OLD-2.
func closedStore(t *testing.T) *store.Store {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	mustMkdirTicket(t, st, "T-2")
	if err := st.CreateTicketRecord("T-2", store.Ticket{Aliases: []string{"OLD-2"}}); err != nil {
		t.Fatal(err)
	}
	return st
}

// TestValidateTicketClosed: the closed entry of a ticket.yaml is checked like
// any other field: a known reason, a by where the reason needs one, an
// existing ticket for superseded, and nothing for a ticket that is open.
func TestValidateTicketClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		closed *store.Closed
		want   string // a substring of the one problem, "" for none
	}{
		{"open", nil, ""},
		{"covered by a reference", &store.Closed{Reason: store.CloseCovered, By: "develdeco/jig#82", At: "2026-10-25"}, ""},
		{"superseded by a ticket of the store", &store.Closed{Reason: store.CloseSuperseded, By: "T-2"}, ""},
		{"superseded by a ticket through its alias", &store.Closed{Reason: store.CloseSuperseded, By: "OLD-2"}, ""},
		{"not planned", &store.Closed{Reason: store.CloseNotPlanned, Note: "no"}, ""},
		{"an unknown reason", &store.Closed{Reason: "done"}, `reason "done" is none of covered, superseded, not-planned`},
		{"covered with no by", &store.Closed{Reason: store.CloseCovered}, "needs by"},
		{"not planned with a by", &store.Closed{Reason: store.CloseNotPlanned, By: "T-2"}, "has no by"},
		{"superseded by a ticket the store lacks", &store.Closed{Reason: store.CloseSuperseded, By: "T-404"}, `by "T-404" is not a ticket of the store`},
		{"superseded by itself", &store.Closed{Reason: store.CloseSuperseded, By: "T-1"}, "cannot be superseded by itself"},
		{"a date that is none", &store.Closed{Reason: store.CloseNotPlanned, At: "soon"}, "is not a date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := closedStore(t)
			if err := st.CreateTicketRecord("T-1", store.Ticket{Closed: tc.closed}); err != nil {
				t.Fatal(err)
			}
			got := validateTicketClosed(st, "T-1")
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("validateTicketClosed = %v, want none", got)
				}
				return
			}
			if len(got) != 1 || !strings.HasPrefix(got[0], "ticket.yaml: ") || !strings.Contains(got[0], tc.want) {
				t.Fatalf("validateTicketClosed = %v, want one ticket.yaml problem containing %q", got, tc.want)
			}
		})
	}
}

// TestCheckClosureResolvesTheSupersedingTicket: what jig close writes is
// checked first, and the ticket a superseded one names is written by its
// current id, whichever id was typed.
func TestCheckClosureResolvesTheSupersedingTicket(t *testing.T) {
	t.Parallel()
	st := closedStore(t)
	got, err := checkClosure(st, "T-1", store.Closed{Reason: store.CloseSuperseded, By: "OLD-2"})
	if err != nil || got.By != "T-2" {
		t.Fatalf("checkClosure by an alias = %+v, %v, want it to name T-2", got, err)
	}
	for name, c := range map[string]store.Closed{
		"covered with no by":      {Reason: store.CloseCovered},
		"an unknown reason":       {Reason: "done"},
		"not planned with a by":   {Reason: store.CloseNotPlanned, By: "T-2"},
		"superseded by no ticket": {Reason: store.CloseSuperseded, By: "T-404"},
		"superseded by itself":    {Reason: store.CloseSuperseded, By: "T-1"},
	} {
		_, err := checkClosure(st, "T-1", c)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
			t.Errorf("%s: checkClosure of %+v = %v, want a VALIDATION_ERROR", name, c, err)
		}
	}
}
