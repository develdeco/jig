package tui

import "testing"

// TestTheMergeRefreshIsOneBackgroundRead: a refresh is not asked for while one is
// out, and it waits behind a key's command like any background read.
func TestTheMergeRefreshIsOneBackgroundRead(t *testing.T) {
	t.Parallel()
	g := newGate()
	m := classModel(t, g)
	occupy(t, g, m.queue)
	first := m.refreshMergesThenRoster()
	if first == nil || m.refreshMergesThenRoster() != nil {
		t.Fatalf("a second refresh was asked for while one is out")
	}
	start(first)
	untilWaiting(t, m.queue, 1)
	start(m.exec("answer", "T-1", []string{"answer", "T-1", "q-1", "yes", "--store", "/s"}))
	untilWaiting(t, m.queue, 2)
	if got, want := order(t, g, 2), "answer T-1 q-1 yes | next --refresh-merges"; got != want {
		t.Errorf("the commands ran in the order\n  %s\nwant\n  %s", got, want)
	}
}
