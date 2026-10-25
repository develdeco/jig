package repohost

import (
	"path/filepath"
	"testing"
)

// TestParsePRState: gh's state is one of the three states of a pull request,
// upper-cased; an answer with no state, or no JSON, is not known - never "not
// merged".
func TestParsePRState(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want string
	}{
		{`{"state":"MERGED"}`, StateMerged},
		{"{\"state\":\"merged\"}\n", StateMerged},
		{`{"state":"OPEN"}`, StateOpen},
		{`{"state":"CLOSED"}`, StateClosed},
		{`{"state":"DRAFT"}`, ""},
		{`{"body":"text"}`, ""},
		{`{"state":""}`, ""},
		{``, ""},
		{`not json`, ""},
	} {
		if got := parsePRState(tc.out); got != tc.want {
			t.Errorf("parsePRState(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
}

// TestPRStateCannotTellWithoutGh: no gh to ask, or a url that is no link,
// reads as unknown rather than as unmerged.
func TestPRStateCannotTellWithoutGh(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-gh")
	if state, known := PRState(missing, []string{}, "https://github.com/o/r/pull/1"); state != "" || known {
		t.Errorf("PRState with a gh that does not exist = %q, %v, want \"\", false", state, known)
	}
	if state, known := PRState("", nil, "--repo=x"); state != "" || known {
		t.Errorf("PRState of a flag-shaped url = %q, %v, want \"\", false", state, known)
	}
}
