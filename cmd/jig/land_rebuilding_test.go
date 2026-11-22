package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestLandSaysAMergeLandsWithoutTheWorkAfterThePublish: a ticket built again
// (a slice queued after its publish) still lands when the person says so, with a
// note that the merge lands the pull request without that work and that `jig
// next` says build; it is never refused. A dependent that is built again is not
// named as the next landing.
func TestLandSaysAMergeLandsWithoutTheWorkAfterThePublish(t *testing.T) {
	r := landRig(t)
	r.host(t, false, false)

	out, code := r.run(t, "land", "TK-2")
	if code != 0 || strings.Contains(out, "without it") {
		t.Fatalf("land TK-2 with nothing queued: exit %d, want a plain landing\n%s", code, out)
	}

	if err := r.st.WriteSliceState("TK-2", "a", store.SliceState{State: "queued"}); err != nil {
		t.Fatal(err)
	}
	out, code = r.run(t, "land", "TK-2")
	if code != 0 || !strings.Contains(out, "lands the pull request without it") || !strings.Contains(out, "jig next TK-2") || !strings.Contains(out, "gh pr merge") {
		t.Errorf("land TK-2 with a queued slice: exit %d, want the merge named with a note that it lands without the work\n%s", code, out)
	}

	// TK-2 merged on the host, TK-3 (which waits for it) built again: landing
	// TK-2 does not name TK-3 as the next landing.
	if err := r.st.WriteSliceState("TK-2", "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.WriteSliceState("TK-3", "a", store.SliceState{State: "queued"}); err != nil {
		t.Fatal(err)
	}
	r.host(t, true, false)
	out, code = r.run(t, "land", "TK-2")
	if code != 0 || strings.Contains(out, "jig land TK-3") {
		t.Errorf("land TK-2 names a dependent that is built again as the next landing: exit %d\n%s", code, out)
	}
}
