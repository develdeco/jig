package tui

import (
	"errors"
	"strings"
	"testing"
)

// opened is what the rig's opener was given.
type opened struct{ urls []string }

func (o *opened) open(url string) error { o.urls = append(o.urls, url); return nil }

// landedWith is a ticket at its landing stop with prs.
func landedWith(prs ...digestPR) *digestInfo {
	d := landStop()
	d.PRs = prs
	return d
}

// TestOOpensEachPullRequest: o hands each pull request of the subject to the
// opener and names each by number and target: the ticket whose screen is open,
// the issue on its plan, the ticket under the cursor on the project's list; a
// ticket with none says so and opens nothing.
func TestOOpensEachPullRequest(t *testing.T) {
	t.Parallel()
	api := digestPR{Repo: "api", Number: 12, URL: "https://example.test/acme/api/pull/12", State: "open", Target: "main"}
	web := digestPR{Repo: "web", Number: 13, URL: "https://example.test/acme/web/pull/13", State: "merged", Target: "501-stack"}

	// One, and two repos.
	for name, c := range map[string]struct {
		prs    []digestPR
		urls   []string
		notice string
	}{
		"one":       {[]digestPR{api}, []string{api.URL}, "opened PR #12 into main"},
		"two repos": {[]digestPR{api, web}, []string{api.URL, web.URL}, "opened PR #12 into main · opened PR #13 into 501-stack"},
	} {
		g := can1(t, landedWith(c.prs...))
		var o opened
		g.m.cfg.Open = o.open
		if hint := footerKeys(g.view()); !strings.Contains(hint, "o open PRs") {
			t.Errorf("%s: the hint line lacks o:\n%s", name, hint)
		}
		g.press("o")
		if strings.Join(o.urls, " ") != strings.Join(c.urls, " ") || g.m.notice != c.notice {
			t.Errorf("%s: opened %v with notice %q, want %v and %q", name, o.urls, g.m.notice, c.urls, c.notice)
		}
		// From the ticket's step screens too.
		g.press("3")
		o.urls = nil
		g.press("o")
		if len(o.urls) != len(c.urls) {
			t.Errorf("%s: o on the review screen opened %v", name, o.urls)
		}
	}

	// An issue's plan opens the issue's, whatever task the cursor is on.
	p := planRig(t, planSpec{})
	var po opened
	p.m.cfg.Open = po.open
	p.press("o")
	if len(po.urls) != 1 || !strings.Contains(po.urls[0], "/pull/512") || p.m.notice != "opened PR #512 into main" {
		t.Errorf("o on a plan opened %v with notice %q", po.urls, p.m.notice)
	}

	// The project's list: the ticket under the cursor.
	m := mainRig(t)
	var mo opened
	m.m.cfg.Open = mo.open
	m.pin("JIG-2", landedWith(web))
	m.syncDigests()
	m.press("home")
	rows := ticketRows(m)
	for i, id := range rows {
		if id == "JIG-2" {
			m.press("home")
			for range i {
				m.press("down")
			}
		}
	}
	if ref := m.m.selected(); ref == nil || ref.id != "JIG-2" {
		t.Fatalf("the cursor is on %+v, want JIG-2", ref)
	}
	m.press("o")
	if len(mo.urls) != 1 || mo.urls[0] != web.URL || m.m.notice != "opened PR #13 into 501-stack" {
		t.Errorf("o on main's JIG-2 opened %v with notice %q", mo.urls, m.m.notice)
	}

	// None: the reason, and nothing opened. The hint line does not offer it.
	n := sized(can1(t, reviewReady()), 80, 40)
	var no opened
	n.m.cfg.Open = no.open
	if hint := footerKeys(n.view()); strings.Contains(hint, "o open PRs") {
		t.Errorf("a ticket with no pull request offers o:\n%s", hint)
	}
	n.press("o")
	if len(no.urls) != 0 || n.m.notice != "JIG-2 has no pull request" {
		t.Errorf("o on a ticket with none opened %v with notice %q", no.urls, n.m.notice)
	}
	n.press("?")
	if v := readWords(n.view()); !strings.Contains(v, "o open PRs not now: JIG-2 has no pull request") {
		t.Errorf("? does not say why:\n%s", n.view())
	}

	// An opener that refuses says why and carries the address.
	r := can1(t, landedWith(api))
	r.m.cfg.Open = func(string) error { return errors.New("no opener here") }
	r.press("o")
	if want := "could not open PR #12 into main: no opener here: " + api.URL; r.m.notice != want {
		t.Errorf("a refused open: notice %q, want %q", r.m.notice, want)
	}
}

// TestONamesTheURLWithNoOpener: with no opener (a pipe, a test) the notice
// carries the address of each pull request, by number and target.
func TestONamesTheURLWithNoOpener(t *testing.T) {
	t.Parallel()
	api := digestPR{Repo: "api", Number: 12, URL: "https://example.test/acme/api/pull/12", State: "open", Target: "main"}
	web := digestPR{Repo: "web", Number: 13, URL: "https://example.test/acme/web/pull/13", State: "open", Target: "main"}
	g := can1(t, landedWith(api, web))
	g.press("o")
	want := "PR #12 into main: " + api.URL + " · PR #13 into main: " + web.URL
	if g.m.notice != want {
		t.Errorf("notice %q, want %q", g.m.notice, want)
	}
	if v := readWords(g.view()); !strings.Contains(v, readWords(want)) {
		t.Errorf("the frame lacks the addresses:\n%s", g.view())
	}
}
