package main

import (
	"fmt"
	"testing"
)

// TestGateSourceForSolveCompatibilityRule pins `jig solve`'s half of the
// compatibility rule: unlike `jig gate`, solve's scripted-source decision never looks at
// --backend - the scripted source runs iff --scenario is set, whatever
// --backend says; the reviewer runs only without --scenario.
func TestGateSourceForSolveCompatibilityRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		scenario string
		want     string
	}{
		{"scenario set uses the scripted source", "somedir", "*verifydeliver.fakeGateSource"},
		{"no scenario uses the reviewer", "", "*verifydeliver.reviewerGateSource"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := gateSourceForSolve(c.scenario, nil)
			if got := fmt.Sprintf("%T", src); got != c.want {
				t.Fatalf("gateSourceForSolve(%q, _) = %s, want %s", c.scenario, got, c.want)
			}
		})
	}
}
