package repohost

import (
	"path/filepath"
	"testing"
)

// TestParsePRState: gh's state is merged only for MERGED; an answer with no
// state, or no JSON, is not known - never "not merged".
func TestParsePRState(t *testing.T) {
	for _, tc := range []struct {
		out           string
		merged, known bool
	}{
		{`{"state":"MERGED"}`, true, true},
		{"{\"state\":\"merged\"}\n", true, true},
		{`{"state":"OPEN"}`, false, true},
		{`{"state":"CLOSED"}`, false, true},
		{`{"body":"text"}`, false, false},
		{`{"state":""}`, false, false},
		{``, false, false},
		{`not json`, false, false},
	} {
		if merged, known := parsePRState(tc.out); merged != tc.merged || known != tc.known {
			t.Errorf("parsePRState(%q) = merged %v, known %v, want %v, %v", tc.out, merged, known, tc.merged, tc.known)
		}
	}
}

// TestPRMergedCannotTellWithoutGh: no gh to ask, or a url that is no link,
// reads as unknown rather than as unmerged.
func TestPRMergedCannotTellWithoutGh(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-gh")
	if merged, known := PRMerged(missing, []string{}, "https://github.com/o/r/pull/1"); merged || known {
		t.Errorf("PRMerged with a gh that does not exist = %v, %v, want false, false", merged, known)
	}
	if merged, known := PRMerged("", nil, "--repo=x"); merged || known {
		t.Errorf("PRMerged of a flag-shaped url = %v, %v, want false, false", merged, known)
	}
}
