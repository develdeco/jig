package main

import (
	"strings"
	"testing"
)

// TestKeepsARefusedClaimTakesNoDriver: a claim refused unattended for its
// conditions (a brief nobody approved) leaves no driver behind, and the
// slice stays queued.
func TestKeepsARefusedClaimTakesNoDriver(t *testing.T) {
	t.Parallel()
	T, st, e := driverFixture(t)

	out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--unattended", "--session", "s-1")
	if code != 1 || !strings.Contains(out, "CLAIM_NOT_APPROVED") {
		t.Fatalf("unattended claim of an unapproved brief: exit %d, want CLAIM_NOT_APPROVED\n%s", code, out)
	}
	if out, _ := runMain(t, e, "", "status", T); strings.Contains(out, "driver:") {
		t.Errorf("a refused claim took the driver:\n%s", out)
	}
	if d, ok, err := st.Driver(T); err != nil || ok {
		t.Errorf("Driver = %+v, %v, %v after a refused claim, want none", d, ok, err)
	}
	if ss, _ := st.ReadSliceState(T, "a"); ss.State != "queued" {
		t.Errorf("a refused claim left slice a %s, want queued", ss.State)
	}
}
