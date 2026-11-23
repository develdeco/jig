package mergefacts

import "testing"

// TestCachedKeepsTheMemo: a merge fact the reader already read is the one its
// Cached reader gives, with no read again; a ticket it never read is read from the
// cache alone (here, a pull request nothing holds: unknown).
func TestCachedKeepsTheMemo(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	const url = "https://example.test/acme/app/pull/1"
	st := ticketStore(t, true, url)
	prs := &fakePRs{reads: map[string]PRRead{url: {State: "OPEN"}}}
	rd := reader(st, r, prs)
	first, err := rd.Of("JIG-1")
	if err != nil || len(first.Repos) != 1 || first.Repos[0].State != "OPEN" {
		t.Fatalf("the host's word: %+v, %v", first, err)
	}
	asked := len(prs.asked)
	got, err := rd.Cached().Of("JIG-1")
	if err != nil || len(got.Repos) != 1 || got.Repos[0].State != "OPEN" {
		t.Errorf("the cached reader lost the fact the reader had read: %+v, %v", got, err)
	}
	if len(prs.asked) != asked {
		t.Errorf("the cached reader asked the host again: %v", prs.asked)
	}

	// A reader that read nothing gives a cached one nothing to keep.
	fresh, err := reader(st, r, prs).Cached().Of("JIG-1")
	if err != nil || len(fresh.Repos) != 1 || fresh.Repos[0].State != "" {
		t.Errorf("a ticket the reader never read has the state %+v, want none", fresh)
	}
}
