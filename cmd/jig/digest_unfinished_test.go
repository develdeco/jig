package main

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

// TestDigestUnfinished: phase.Derive marks every phase of a closed task done, so
// the digest names the phases it had not done when it closed: one closed before
// its brief was approved names them all, one that published and then closed
// names none (publishing stands for a review), and an open ticket names none.
func TestDigestUnfinished(t *testing.T) {
	t.Parallel()
	r, merges := landFirstRig(t)
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := nextForStore(r.st, cfg, project.MachineProject{}, merges)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, row := range fleet.Tickets {
		got[row.ID] = row.Digest.Unfinished
	}
	want := map[string][]string{
		"TK-13": {"brief", "build", "review", "publish"},
		"TK-15": nil,
		"TK-2":  nil, "TK-6": nil, "TK-14": nil, "TK-7": nil,
	}
	for id, w := range want {
		if !reflect.DeepEqual(got[id], w) {
			t.Errorf("%s: unfinished = %v, want %v", id, got[id], w)
		}
	}
}
