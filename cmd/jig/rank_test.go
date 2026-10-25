package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestParseRankArgs: a ticket and a whole number from 1 are a rank; --clear
// takes the ticket alone; everything else is a VALIDATION_ERROR that says how
// to call the verb.
func TestParseRankArgs(t *testing.T) {
	t.Parallel()
	ticket, rank, err := parseRankArgs([]string{"JIG-1", "3"}, false)
	if err != nil || ticket != "JIG-1" || rank == nil || *rank != 3 {
		t.Fatalf("parseRankArgs JIG-1 3 = %q, %v, %v, want JIG-1 with rank 3", ticket, rank, err)
	}
	ticket, rank, err = parseRankArgs([]string{"JIG-1"}, true)
	if err != nil || ticket != "JIG-1" || rank != nil {
		t.Fatalf("parseRankArgs JIG-1 --clear = %q, %v, %v, want JIG-1 with no rank", ticket, rank, err)
	}

	for _, tc := range []struct {
		name        string
		args        []string
		cleared     bool
		wantMessage string
	}{
		{"no ticket", nil, false, "missing required ticket"},
		{"no ticket to clear", nil, true, "missing required ticket"},
		{"no rank", []string{"JIG-1"}, false, "missing required rank"},
		{"a rank and --clear", []string{"JIG-1", "2"}, true, "--clear takes the ticket alone"},
		{"too many", []string{"JIG-1", "2", "3"}, false, "got 3 arguments"},
		{"zero", []string{"JIG-1", "0"}, false, "not a whole number from 1"},
		{"not a number", []string{"JIG-1", "soon"}, false, "not a whole number from 1"},
		{"a fraction", []string{"JIG-1", "1.5"}, false, "not a whole number from 1"},
	} {
		_, _, err := parseRankArgs(tc.args, tc.cleared)
		ae, ok := err.(*axi.Error)
		if !ok || ae.Code != "VALIDATION_ERROR" || !strings.Contains(ae.Msg, tc.wantMessage) || len(ae.Help) == 0 {
			t.Errorf("%s: parseRankArgs = %v, want a VALIDATION_ERROR containing %q with help", tc.name, err, tc.wantMessage)
		}
	}
}

// TestSameRank: both absent or equal.
func TestSameRank(t *testing.T) {
	t.Parallel()
	one, otherOne, two := 1, 1, 2
	for _, tc := range []struct {
		a, b *int
		want bool
	}{{nil, nil, true}, {&one, nil, false}, {nil, &one, false}, {&one, &otherOne, true}, {&one, &two, false}} {
		if got := sameRank(tc.a, tc.b); got != tc.want {
			t.Errorf("sameRank(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
