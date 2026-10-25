package ready

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/store"
)

// TestJudgeRepo: a recorded pull request the host answered for decides, merged
// or not; with none, or none the host could be asked about, the branch tip
// being in the target is met by ancestry; otherwise the reason names the
// command that records the pull request. Nothing but a merged pull request or
// the branch stands for a merge.
func TestJudgeRepo(t *testing.T) {
	t.Parallel()
	const pr = "https://github.com/o/r/pull/7"
	at := time.Date(2026, 10, 25, 14, 5, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		e       Evidence
		met     bool
		by      string
		why     string
		wantAt  time.Time
		hasNote string
	}{
		{name: "its pull request is merged", e: Evidence{PR: pr, State: PRMerged}, met: true, by: ByPR},
		{name: "merged beats a branch that is not in the target", e: Evidence{PR: pr, State: PRMerged, Ancestor: false}, met: true, by: ByPR},
		{name: "open", e: Evidence{PR: pr, State: PROpen}, why: "its PR is open (" + pr + ")"},
		{name: "closed without merging", e: Evidence{PR: pr, State: PRClosed}, why: "its PR was closed without merging (" + pr + ")"},
		{name: "open stays open though the branch is in the target", e: Evidence{PR: pr, State: PROpen, Ancestor: true}, why: "its PR is open (" + pr + ")"},
		{name: "closed stays closed though the branch is in the target", e: Evidence{PR: pr, State: PRClosed, Ancestor: true}, why: "its PR was closed without merging (" + pr + ")"},
		{name: "no pull request, the branch is in the target", e: Evidence{Ancestor: true}, met: true, by: ByAncestry},
		{name: "a pull request the host could not be asked about, the branch is in the target", e: Evidence{PR: pr, Ancestor: true}, met: true, by: ByAncestry},
		{name: "a pull request the host could not be asked about", e: Evidence{PR: pr}, why: "its PR could not be read from the host (" + pr + ")"},
		{name: "nothing on record", e: Evidence{Ticket: "SHOP-1"}, why: "no merge on record: record its PR with `jig publish SHOP-1 --pr <url>`"},
		{name: "an open answer from earlier is dated", e: Evidence{PR: pr, State: PROpen, AsOf: at},
			why: "its PR is open (" + pr + ") (as of 2026-10-25 14:05 UTC)", wantAt: at},
		{name: "an answer the host could not refresh says so", e: Evidence{PR: pr, State: PROpen, AsOf: at, Offline: true},
			why: "its PR is open (" + pr + ") (the host did not answer; as of 2026-10-25 14:05 UTC)", wantAt: at},
		{name: "a merged answer from earlier needs no date", e: Evidence{PR: pr, State: PRMerged, AsOf: at}, met: true, by: ByPR},
	} {
		got := JudgeRepo(tc.e)
		if got.Met != tc.met || got.By != tc.by || got.Why != tc.why || !got.AsOf.Equal(tc.wantAt) {
			t.Errorf("%s: JudgeRepo = {met %v by %q why %q as of %v}, want {met %v by %q why %q as of %v}",
				tc.name, got.Met, got.By, got.Why, got.AsOf, tc.met, tc.by, tc.why, tc.wantAt)
		}
	}
}

// TestMerge: a ticket is met when it published and every repo it builds in is;
// what is missing names the repo when there are several, and a ticket that did
// not publish is never met.
func TestMerge(t *testing.T) {
	t.Parallel()
	merged := RepoMerge{Repo: "api", Met: true, By: ByPR}
	byBranch := RepoMerge{Repo: "web", Met: true, By: ByAncestry}
	open := RepoMerge{Repo: "web", Why: "its PR is open (https://github.com/o/web/pull/2)"}
	older := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		m    Merge
		met  bool
		why  string
		via  string
	}{
		{"not published", Merge{Repos: []RepoMerge{merged}}, false, NotPublished, ""},
		{"no repo", Merge{Published: true}, false, "it builds in no repo that is known", ""},
		{"one repo, merged", Merge{Published: true, Repos: []RepoMerge{{Met: true, By: ByPR}}}, true, "", ByPR},
		{"one repo, not met", Merge{Published: true, Repos: []RepoMerge{{Why: "its PR is open (u)"}}}, false, "its PR is open (u)", ""},
		{"two repos, both", Merge{Published: true, Repos: []RepoMerge{merged, byBranch}}, true, "", ByPR + "+" + ByAncestry},
		{"two repos, one missing", Merge{Published: true, Repos: []RepoMerge{merged, open}}, false, "web: its PR is open (https://github.com/o/web/pull/2)", ""},
	} {
		if got := tc.m.Met(); got != tc.met {
			t.Errorf("%s: Met = %v, want %v", tc.name, got, tc.met)
		}
		if got := tc.m.Why(); got != tc.why {
			t.Errorf("%s: Why = %q, want %q", tc.name, got, tc.why)
		}
		if got := tc.m.Via(); got != tc.via {
			t.Errorf("%s: Via = %q, want %q", tc.name, got, tc.via)
		}
	}

	// The date of what is missing is the oldest answer behind it.
	m := Merge{Published: true, Repos: []RepoMerge{
		{Repo: "api", AsOf: older.Add(time.Hour), Why: "x"},
		{Repo: "web", AsOf: older, Why: "y"},
		{Repo: "docs", Met: true, AsOf: older.Add(-time.Hour)},
		{Repo: "lib", Why: "z"},
	}}
	if got := m.AsOf(); !got.Equal(older) {
		t.Errorf("AsOf = %v, want the oldest answer behind a missing repo, %v", got, older)
	}
	if got := (Merge{Published: true, Repos: []RepoMerge{{Why: "x"}}}).AsOf(); !got.IsZero() {
		t.Errorf("AsOf of answers read now = %v, want the zero time", got)
	}
	if !strings.Contains(m.Why(), "api: x; web: y; lib: z") {
		t.Errorf("Why = %q, want each missing repo, in the project's order", m.Why())
	}
}

// TestTicketsBlockersMergeFacts: a merged blocker is met by its merge fact, not
// by having published; one with no merge read is met by publishing; a stacked
// blocker is met by publishing whatever the merge says; each blocker that is
// not met says why.
func TestTicketsBlockersMergeFacts(t *testing.T) {
	t.Parallel()
	open := Merge{Published: true, Repos: []RepoMerge{{Why: "its PR is open (u)"}}}
	merged := Merge{Published: true, Repos: []RepoMerge{{Met: true, By: ByPR}}}
	work := states("a", "queued")
	one := []store.Slice{{ID: "a"}}

	got := map[string]Status{}
	for _, s := range Tickets([]Ticket{
		{ID: "P-open", Published: true, Merge: &open},
		{ID: "P-merged", Published: true, Merge: &merged},
		{ID: "P-unread", Published: true},
		{ID: "P-draft"},
		{ID: "W-open", Blockers: []Blocker{{"P-open", "merged"}}, Slices: one, States: work},
		{ID: "W-merged", Blockers: []Blocker{{"P-merged", "merged"}}, Slices: one, States: work},
		{ID: "W-unread", Blockers: []Blocker{{"P-unread", "merged"}}, Slices: one, States: work},
		{ID: "W-draft", Blockers: []Blocker{{"P-draft", "merged"}}, Slices: one, States: work},
		{ID: "W-stacked", Blockers: []Blocker{{"P-open", "stacked"}}, Slices: one, States: work},
		{ID: "W-gone", Blockers: []Blocker{{"P-gone", "merged"}}, Slices: one, States: work},
		{ID: "W-both", Blockers: []Blocker{{"P-merged", "merged"}, {"P-open", "merged"}}, Slices: one, States: work},
	}) {
		got[s.ID] = s
	}
	for id, want := range map[string]string{
		"W-open": StateBlocked, "W-merged": StateReady, "W-unread": StateReady, "W-draft": StateBlocked,
		"W-stacked": StateReady, "W-gone": StateBlocked, "W-both": StateBlocked,
	} {
		if got[id].State != want {
			t.Errorf("%s: state = %q, want %q", id, got[id].State, want)
		}
	}
	for _, tc := range []struct{ id, blocker, why string }{
		{"W-open", "P-open", "its PR is open (u)"},
		{"W-draft", "P-draft", NotPublished},
		{"W-gone", "P-gone", "it is not a ticket of the store"},
		{"W-both", "P-open", "its PR is open (u)"},
	} {
		if why := got[tc.id].Why[tc.blocker]; why != tc.why {
			t.Errorf("%s: why %s is missing = %q, want %q", tc.id, tc.blocker, why, tc.why)
		}
	}
	if len(got["W-both"].Blocked) != 1 || got["W-both"].Blocked[0].Ticket != "P-open" {
		t.Errorf("W-both blocked by %v, want only the blocker that is not met", got["W-both"].Blocked)
	}
	if len(got["W-merged"].Why) != 0 {
		t.Errorf("W-merged says why %v, want nothing missing", got["W-merged"].Why)
	}
}
