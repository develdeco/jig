package tui

import (
	"testing"
	"time"
)

// TestAKeptPullRequestStateIsWordedWithWhenItWasKept: a state the roster gives
// from what this machine kept says the instant it was kept; one read this run,
// and a state not read, say no instant.
func TestAKeptPullRequestStateIsWordedWithWhenItWasKept(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 23, 15, 4, 0, 0, time.UTC)
	for _, tc := range []struct {
		pr   digestPR
		want string
	}{
		{digestPR{Number: 7, State: "open", Target: "main", AsOf: at}, "PR #7 open into main (as of 15:04 UTC)"},
		{digestPR{Number: 7, State: "open", Target: "main"}, "PR #7 open into main"},
		{digestPR{Number: 7, State: "unknown", Target: "main"}, "PR #7 into main (state not read)"},
	} {
		if got := prWords([]digestPR{tc.pr}, true); got != tc.want {
			t.Errorf("prWords = %q, want %q", got, tc.want)
		}
	}
}
