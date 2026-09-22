package main

import (
	"fmt"
	"testing"
)

// TestGateSourceForCompatibilityRule pins Q10's `jig gate` half (F18): the
// old scripted source (NewFakeGateSource) runs iff --scenario is set and
// --backend is not; any other combination, including --backend fake
// together with --scenario, dispatches the reviewer (NewReviewerGateSource).
// It asserts on the concrete type name via %T rather than reaching into
// verifydeliver's unexported types.
func TestGateSourceForCompatibilityRule(t *testing.T) {
	cases := []struct {
		name              string
		backend, scenario string
		want              string
	}{
		{"scenario alone uses the scripted source", "", "somedir", "*verifydeliver.fakeGateSource"},
		{"backend fake plus scenario uses the reviewer", "fake", "somedir", "*verifydeliver.reviewerGateSource"},
		{"neither flag uses the reviewer", "fake", "", "*verifydeliver.reviewerGateSource"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, err := gateSourceFor(c.backend, c.scenario)
			if err != nil {
				t.Fatalf("gateSourceFor(%q, %q): %v", c.backend, c.scenario, err)
			}
			if got := fmt.Sprintf("%T", src); got != c.want {
				t.Fatalf("gateSourceFor(%q, %q) = %s, want %s", c.backend, c.scenario, got, c.want)
			}
		})
	}
}

// TestGateSourceForSolveCompatibilityRule pins Q10's `jig solve` half
// (F18): unlike `jig gate`, solve's scripted-source decision never looks at
// --backend - the scripted source runs iff --scenario is set, whatever
// --backend says; the reviewer runs only without --scenario.
func TestGateSourceForSolveCompatibilityRule(t *testing.T) {
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
