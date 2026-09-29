package verifydeliver

import (
	"errors"
	"fmt"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// TestFailureCode pins failureCode's own branches directly, as a pure
// function, rather than by paying for another full fixture and gate
// rounds: Publish's own push_error_axi_code_only subtest already shows
// Gate and Publish call failureCode rather than err.Error() (its stubbed
// axi.Error's Msg and Code deliberately differ, so a fallback to the raw
// message would fail that subtest too), leaving failureCode's remaining
// branches - a wrapped *axi.Error, one with no code, and a plain error -
// to pin here instead.
func TestFailureCode(t *testing.T) {
	plain := errors.New("exit status 1: /host/secret/abs/path: permission denied")
	coded := &axi.Error{Msg: "push rejected: /host/secret/abs/path", Code: "PUBLISH_PUSH_REJECTED"}
	uncoded := &axi.Error{Msg: "no code here"}
	wrapped := fmt.Errorf("verifydeliver: publish: guarded push: %w", coded)

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"axi error with a code", coded, "PUBLISH_PUSH_REJECTED"},
		{"axi error wrapped by fmt.Errorf", wrapped, "PUBLISH_PUSH_REJECTED"},
		{"axi error with an empty code", uncoded, "INTERNAL"},
		{"plain error", plain, "INTERNAL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failureCode(c.err); got != c.want {
				t.Fatalf("failureCode(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// TestConsolidatedTitlePrefersSliceGoal covers the top precedence arm: a
// slice's own goal wins even when the ticket has a recorded title.
func TestConsolidatedTitlePrefersSliceGoal(t *testing.T) {
	t.Parallel()

	got := consolidatedTitle("Recorded title", "T-1", []store.Slice{{ID: "s-1", Goal: "Slice goal"}})
	if got != "Slice goal" {
		t.Fatalf("consolidatedTitle = %q, want %q", got, "Slice goal")
	}
}

// TestConsolidatedTitleFallsBackToRecordedTitle covers a ticket with no
// usable slice goal - no slices yet, or a first slice whose goal is empty:
// it falls back to ticket.yaml's own title rather than the bare ticket id.
func TestConsolidatedTitleFallsBackToRecordedTitle(t *testing.T) {
	t.Parallel()

	for name, slices := range map[string][]store.Slice{
		"no slices":        nil,
		"empty first goal": {{ID: "s-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := consolidatedTitle("Recorded title", "T-1", slices)
			if got != "Recorded title" {
				t.Fatalf("consolidatedTitle = %q, want %q", got, "Recorded title")
			}
		})
	}
}

// TestConsolidatedTitleFallsBackToTicketID covers a ticket with no slices
// and no recorded title: the last resort is still the bare ticket id, the
// only fallback consolidatedTitle had before it could read a recorded
// title at all.
func TestConsolidatedTitleFallsBackToTicketID(t *testing.T) {
	t.Parallel()

	got := consolidatedTitle("", "T-1", nil)
	if got != "T-1" {
		t.Fatalf("consolidatedTitle = %q, want %q", got, "T-1")
	}
}
