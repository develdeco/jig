package main

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/project"
)

const (
	landFirstPR14 = "https://github.com/acme/app/pull/34"
	landFirstPR15 = "https://github.com/acme/app/pull/35"
	landFirstPR16 = "https://github.com/acme/app/pull/36"
)

// landFirstRig is digestRig with two more tasks stacked on TK-2, which
// published and has not landed: TK-14 published, TK-15 published and then
// closed.
func landFirstRig(t *testing.T) (chartRig, issueMerges) {
	t.Helper()
	r, merges := digestRig(t)
	stacked := "blocked_by:\n  - ticket: TK-2\n    kind: stacked\n"
	r.ticket(t, "TK-14", stacked, true)
	r.ticket(t, "TK-15", stacked, true)
	r.ticket(t, "TK-16", stacked, true)
	for id, url := range map[string]string{"TK-14": landFirstPR14, "TK-15": landFirstPR15, "TK-16": landFirstPR16} {
		r.green(t, id)
		for _, l := range []journal.Line{{Event: journal.EventPr, Outcome: "opened", URL: url}, {Event: journal.EventPublishDone}} {
			if err := journal.Append(r.st, id, l); err != nil {
				t.Fatal(err)
			}
		}
		merges[id] = openInto(url)
	}
	if err := journal.Append(r.st, "TK-16", journal.Line{Event: journal.EventLanded}); err != nil {
		t.Fatal(err)
	}
	r.put(t, "tickets/TK-15/ticket.yaml", "schema_version: 1\ntitle: Ticket TK-15\n"+stacked+"closed:\n  reason: not-planned\n  at: 2026-11-02\n")
	return r, merges
}

// landFirstOfRoster reads the land_first of each ticket of the roster and, for
// each, what `jig next <ticket>` says of it.
func landFirstOfRoster(t *testing.T, r chartRig, merges mergeSource) (roster, one map[string][]string) {
	t.Helper()
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	fleet, err := nextForStore(r.st, cfg, project.MachineProject{}, merges)
	if err != nil {
		t.Fatal(err)
	}
	roster, one = map[string][]string{}, map[string][]string{}
	for _, row := range fleet.Tickets {
		roster[row.ID] = row.Digest.LandFirst
		n, err := nextForTicket(r.st, cfg, project.MachineProject{}, row.ID, "", false, "", merges)
		if err != nil {
			t.Fatal(err)
		}
		one[row.ID] = n.LandFirst
	}
	return roster, one
}

// TestDigestLandFirst: a published task behind a stacked parent that has not
// landed lists it; once the parent has landed the field is empty; a published
// task with nothing before it, a landed one, a closed one, an unpublished one
// and a container carry none; and each row equals `jig next <ticket>`'s.
func TestDigestLandFirst(t *testing.T) {
	t.Parallel()
	r, merges := landFirstRig(t)
	roster, one := landFirstOfRoster(t, r, merges)
	if !reflect.DeepEqual(roster, one) {
		t.Errorf("the roster says %v, jig next <ticket> says %v", roster, one)
	}
	if got, want := roster["TK-14"], []string{"TK-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TK-14 is published behind TK-2: land_first = %v, want %v", got, want)
	}
	for _, id := range []string{"TK-1", "TK-2", "TK-3", "TK-4", "TK-6", "TK-7", "TK-11", "TK-12", "TK-13", "TK-15", "TK-16"} {
		if got := roster[id]; len(got) != 0 {
			t.Errorf("%s: land_first = %v, want none", id, got)
		}
	}

	// TK-2 lands: nothing is left for TK-14 to wait for.
	if err := journal.Append(r.st, "TK-2", journal.Line{Event: journal.EventLanded}); err != nil {
		t.Fatal(err)
	}
	merges["TK-2"] = mergedInto(digestPR2)
	roster, one = landFirstOfRoster(t, r, merges)
	if len(roster["TK-14"]) != 0 || len(one["TK-14"]) != 0 {
		t.Errorf("TK-2 landed: TK-14's land_first = %v and %v, want none", roster["TK-14"], one["TK-14"])
	}
}

// TestDigestLandFirstAsksTheHostNoMore: the listing's digests are read over the
// reader's cache, so a predecessor whose pull request has no cached answer
// stays in land_first and no row asks the host.
func TestDigestLandFirstAsksTheHostNoMore(t *testing.T) {
	t.Parallel()
	r, _ := landFirstRig(t)
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	asked := 0
	reader := &mergefacts.Reader{Store: r.st, Cfg: cfg, PRs: &mergefacts.PRCache{
		Ask: func(string) (string, bool) { asked++; return "merged", true },
	}}
	fleet, err := nextForStore(r.st, cfg, project.MachineProject{}, digestMerges(reader))
	if err != nil {
		t.Fatal(err)
	}
	if asked != 0 {
		t.Errorf("the listing asked the host %d times, want none", asked)
	}
	for _, row := range fleet.Tickets {
		if row.ID == "TK-14" && !reflect.DeepEqual(row.Digest.LandFirst, []string{"TK-2"}) {
			t.Errorf("TK-14: land_first = %v, want TK-2 to stay with no cached answer", row.Digest.LandFirst)
		}
	}
}
