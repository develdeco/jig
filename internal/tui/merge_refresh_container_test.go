package tui

import "testing"

// TestMainWaitsOnTheMergeOfAnIssuesOwnPullRequest: an issue or a multi-task has
// no phases, so its row waits on a merge when its digest says its own pull
// request is published; one that landed, is closed or is only ready does not.
func TestMainWaitsOnTheMergeOfAnIssuesOwnPullRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		digest digestInfo
		want   bool
	}{
		{"published", digestInfo{Kind: "issue", State: "published"}, true},
		{"landed", digestInfo{Kind: "issue", State: "landed"}, false},
		{"ready", digestInfo{Kind: "issue", State: "ready"}, false},
		{"closed", digestInfo{Kind: "multi-task", State: "published", Closed: "not planned"}, false},
	} {
		m := New(quietConfig())
		row := listed("I-1")
		d := tc.digest
		row.Digest = &d
		_, _ = m.Update(rosterMsg{rows: []rosterRow{row}, seq: 1})
		if got := m.mainWaitsOnMerge(); got != tc.want {
			t.Errorf("%s: mainWaitsOnMerge = %v, want %v", tc.name, got, tc.want)
		}
	}
}
