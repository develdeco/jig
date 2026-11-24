package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/phase"
)

// twoRepoRig is JIG-2 published in two repos, each with a pull request the digest
// numbers.
func twoRepoRig(t *testing.T) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Repos(), 0)
	g.feed(jig2Repos()...)
	d := task("land", false, "done|a", "done|b", "done|c", "done|published", "current|waits for a merge")
	d.PRs = []digestPR{
		{Repo: "api", Number: 7, URL: "https://example.test/acme/api/pull/7", State: "open", Target: "main"},
		{Repo: "web", Number: 3, URL: "https://example.test/acme/web/pull/3", State: "open", Target: "main"},
	}
	g.pin("JIG-2", d)
	g.syncDigests()
	return g
}

// TestPublishChecklistAndLandListArePanels: the publish screen is a Checklist
// panel of its checks under their names, with how many are done at its edge, and
// the Land screen's pull requests a list under the names of its columns; every
// pull request is read by number, state and target and no address is in either;
// at three sizes.
func TestPublishChecklistAndLandListArePanels(t *testing.T) {
	t.Parallel()
	for _, size := range layoutSizes {
		w, h := size[0], size[1]
		for _, c := range []struct {
			key   string
			names map[string]string
		}{
			{"4", map[string]string{"Checklist": "step", "Publish activity": "time"}},
			{"5", map[string]string{"Pull requests": "repo", "Land activity": "time"}},
		} {
			g := twoRepoRig(t)
			g.send(tea.WindowSizeMsg{Width: w, Height: max(h, 60)})
			g.press(c.key)
			view := g.view()
			wantPanels(t, fmt.Sprintf("key %s %dx%d", c.key, w, h), view, c.names)
			if strings.Contains(view, "https://") {
				t.Errorf("key %s %dx%d: an address is in a list:\n%s", c.key, w, h, view)
			}
			words := readWords(view)
			for _, want := range []string{"PR #7 open into main", "PR #3 open into main"} {
				// A narrow terminal cuts a long reading with an ellipsis, so the
				// number and the state are what it keeps.
				if !strings.Contains(words, want) && !strings.Contains(words, strings.SplitN(want, " into", 2)[0]) {
					t.Errorf("key %s %dx%d: no pull request reads %q:\n%s", c.key, w, h, want, view)
				}
			}
		}
	}
}

// TestAPullRequestReadsByNumberStateAndTarget: a pull request named by its
// address is read as the digest numbers it, with its state and the branch it goes
// into, in the checks and in the field of the sub-step; an address the digest
// does not number is shown whole.
func TestAPullRequestReadsByNumberStateAndTarget(t *testing.T) {
	t.Parallel()
	g := twoRepoRig(t)
	tk := g.m.w.tickets["JIG-2"]
	for url, want := range map[string]string{
		"https://example.test/acme/api/pull/7": "PR #7 open into main",
		"https://example.test/acme/web/pull/3": "PR #3 open into main",
		"https://example.test/acme/x/pull/99":  "https://example.test/acme/x/pull/99",
		"":                                     "",
	} {
		if got := g.m.prReading(tk, url); got != want {
			t.Errorf("%q reads %q, want %q", url, got, want)
		}
	}
	// The digest says merged: the reading says so.
	tk.digest.PRs[0].State = "merged"
	if got := g.m.prReading(tk, tk.digest.PRs[0].URL); got != "PR #7 merged into main" {
		t.Errorf("a merged pull request reads %q", got)
	}

	// The field of a publish sub-step reads the pull request the same way.
	one := can1(t, task("land", false, "done|a", "done|b", "done|c", "done|published", "current|waits for a merge"))
	one.m.w.tickets["JIG-2"].digest.PRs = []digestPR{{Repo: "demo", Number: 7, URL: "https://example.test/acme/demo/pull/7", State: "open", Target: "main"}}
	var got string
	for _, f := range one.m.subFields(one.m.w.tickets["JIG-2"], phase.Sub{Key: "pr"}, subTrack{}) {
		if f.label == "pull req" {
			got = strings.Join(f.rows, " ")
		}
	}
	if got != "PR #7 open into main" {
		t.Errorf("the sub-step field reads %q, want the pull request by number, state and target", got)
	}
}
