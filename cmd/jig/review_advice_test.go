package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestAdviceToSpawnNamesTheForegroundRule: the advice that tells a session to
// spawn a reviewer says to run it in the foreground, wait for its verdict in the
// same turn and not to end the turn while it runs, for a session that built the
// ticket and for one jig cannot tell. A fresh session is told to review itself,
// with no subagent to wait for.
func TestAdviceToSpawnNamesTheForegroundRule(t *testing.T) {
	t.Parallel()
	rule := []string{"run_in_background: false", "wait for its verdict in this same turn", "never end the turn while it runs"}
	cases := []struct {
		name  string
		fresh verifydeliver.Fresh
		state string
		spawn bool
	}{
		{"built it", verifydeliver.Fresh{Known: true, Why: []string{"claimed slice a"}}, freshNo, true},
		{"unknown", verifydeliver.Fresh{}, freshUnknown, true},
		{"fresh", verifydeliver.Fresh{Known: true, Fresh: true}, freshYes, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := adviceFor(tc.fresh, "SHOP-1", "/work/shop")
			if a.State != tc.state {
				t.Errorf("state = %q, want %q", a.State, tc.state)
			}
			text := strings.Join(a.Lines, "\n")
			for _, want := range rule {
				if got := strings.Contains(text, want); got != tc.spawn {
					t.Errorf("advice has %q = %v, want %v:\n%s", want, got, tc.spawn, text)
				}
			}
		})
	}
}
