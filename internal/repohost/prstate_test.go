package repohost_test

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/repohost"
)

// TestPRStateReadsThePullRequestOfTheUrl: the state is read for the pull
// request the URL ends in, from the host: merged for a closed one with a merge
// time, closed for one closed without, open for the rest. A pull request the
// host does not hold is not known, and so is a host that cannot be run.
func TestPRStateReadsThePullRequestOfTheUrl(t *testing.T) {
	t.Parallel()
	bin := ghStubBinary(t)
	pulls := fixture.GhPulls(t,
		fixture.GhPull{Number: 1},
		fixture.GhPull{Number: 2, State: "closed", Merged: true},
		fixture.GhPull{Number: 3, State: "closed"},
	)
	env := append(os.Environ(), "GH_STUB_PULLS="+pulls)
	const base = "https://github.example/owner/repo/pull/"
	for _, tc := range []struct {
		url   string
		state string
		known bool
	}{
		{base + "1", repohost.StateOpen, true},
		{base + "2", repohost.StateMerged, true},
		{base + "3", repohost.StateClosed, true},
		{base + "2/", repohost.StateMerged, true},
		{base + "4", "", false},
		{base + "12", "", false},
	} {
		if state, known := repohost.PRState(bin, env, tc.url); state != tc.state || known != tc.known {
			t.Errorf("PRState(%s) = %q, %v, want %q, %v", tc.url, state, known, tc.state, tc.known)
		}
	}
	if state, known := repohost.PRState(bin, os.Environ(), base+"1"); state != "" || known {
		t.Errorf("PRState with no pull requests held = %q, %v, want not known", state, known)
	}
}
