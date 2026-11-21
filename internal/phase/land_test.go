package phase

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// published is the facts and journal of a ticket that published into one repo:
// a pull request recorded and the publish done.
func publishedWithPR() (Facts, []journal.Line) {
	f := allGreen()
	f.Published = true
	lines := append(reviewedClean(), ln(journal.EventPr, func(l *journal.Line) { l.URL = "https://github.com/acme/app/pull/7" }), ln(journal.EventPublishDone))
	return f, lines
}

// reviewedClean is the journal of a build that went green and a clean gate.
func reviewedClean() []journal.Line {
	return []journal.Line{ln(journal.EventGateClean, commit("h1"))}
}

// TestLandPhaseIsTheFifth: Land joins the names, the prompt is `Land T`, and a
// ticket has five phases of which the first four are its work.
func TestLandPhaseIsTheFifth(t *testing.T) {
	t.Parallel()
	if len(Names) != 5 || Names[4] != Land {
		t.Fatalf("Names = %v, want Land fifth", Names)
	}
	if got := Prompt(Land, tk); got != "Land "+tk {
		t.Errorf("Prompt(Land) = %q", got)
	}
	if n, ok := Parse("Land"); !ok || n != Land {
		t.Errorf("Parse(Land) = %v, %v", n, ok)
	}
	tkt := Derive(approved(), nil)
	if len(tkt.Phases) != 5 || len(tkt.Work()) != Work || tkt.Of(Land).Name != Land {
		t.Errorf("phases = %d, work = %d", len(tkt.Phases), len(tkt.Work()))
	}
	if tkt.Of(Land).State != StatePending || tkt.Current == Land {
		t.Errorf("a ticket not published: land %s, current %s", tkt.Of(Land).State, tkt.Current)
	}
}

// TestLandPhaseIsCurrentAfterPublishUntilLanded: published, with a pull request
// and no landed line, Land is current with `waits for a merge`, the code stays
// PHASE_DONE and the prompt is `Land T`; a landed line per pull request makes it
// done and the ticket reads as a published one always did.
func TestLandPhaseIsCurrentAfterPublishUntilLanded(t *testing.T) {
	t.Parallel()
	f, lines := publishedWithPR()
	tkt := Derive(f, lines)
	land := tkt.Of(Land)
	if tkt.Current != Land || land.State != StateCurrent || land.Text != "waits for a merge" {
		t.Fatalf("published: current %s, land %s %q", tkt.Current, land.State, land.Text)
	}
	if tkt.Code != CodeDone || tkt.Prompt != "Land "+tk || tkt.Ready {
		t.Errorf("published: code %s, prompt %q, ready %v, want PHASE_DONE, Land prompt, not ready", tkt.Code, tkt.Prompt, tkt.Ready)
	}
	if a := tkt.For(Land); !a.Ready || a.Warning != "" {
		t.Errorf("For(Land) = %+v, want ready", a)
	}
	if a := tkt.For(Publish); a.Warning == "" || !strings.Contains(a.Warning, "Land "+tk) {
		t.Errorf("For(Publish) = %+v, want a warning naming the Land prompt", a)
	}

	// Merged on the host and not recorded: noticed, still Land's to record.
	f.MergedOnHost = true
	m := Derive(f, lines)
	if m.Current != Land || m.Of(Land).Text != "merged on the host, not recorded" || !m.MergedOnHost() {
		t.Errorf("merged on the host: current %s, %q", m.Current, m.Of(Land).Text)
	}

	// Landed: every pull request has a line.
	landed := append(append([]journal.Line{}, lines...), ln(journal.EventLanded, func(l *journal.Line) { l.URL = "https://github.com/acme/app/pull/7" }))
	d := Derive(f, landed)
	if d.Current != Publish || d.Of(Land).State != StateDone || d.Of(Land).Text != "landed" || d.Prompt != "" || d.Code != CodeDone || d.MergedOnHost() {
		t.Errorf("landed: current %s, land %s %q, prompt %q, code %s", d.Current, d.Of(Land).State, d.Of(Land).Text, d.Prompt, d.Code)
	}
}

// TestLandPhaseCoversEveryRepoAndIssue: a ticket published in two repos, or
// into two issues, is landed only when each pull request has its own line.
func TestLandPhaseCoversEveryRepoAndIssue(t *testing.T) {
	t.Parallel()
	pr := func(repo, into string) journal.Line {
		return ln(journal.EventPr, func(l *journal.Line) { l.URL = "https://github.com/acme/" + repo + "/pull/1"; l.Repo, l.Into = repo, into })
	}
	landed := func(repo, into string) journal.Line {
		return ln(journal.EventLanded, func(l *journal.Line) { l.Repo, l.Into = repo, into })
	}
	for _, tc := range []struct {
		name  string
		lines []journal.Line
		want  bool
	}{
		{"two repos, one landed", []journal.Line{pr("api", ""), pr("web", ""), landed("api", "")}, false},
		{"two repos, both landed", []journal.Line{pr("api", ""), pr("web", ""), landed("api", ""), landed("web", "")}, true},
		{"two issues, one landed", []journal.Line{pr("", "I-1"), pr("", "I-2"), landed("", "I-1")}, false},
		{"two issues, both landed", []journal.Line{pr("", "I-1"), pr("", "I-2"), landed("", "I-1"), landed("", "I-2")}, true},
		{"a line with no into stands for any", []journal.Line{pr("", "I-1"), landed("", "")}, true},
	} {
		if got, hasPR := LandedIn(tc.lines); got != tc.want || !hasPR {
			t.Errorf("%s: LandedIn = %v, %v, want %v", tc.name, got, hasPR, tc.want)
		}
	}
}

// TestLandPhaseClosedAndNoPullRequestHaveNone: a ticket closed without a pull
// request has no Land, and neither has one that published none.
func TestLandPhaseClosedAndNoPullRequestHaveNone(t *testing.T) {
	t.Parallel()
	f := allGreen()
	f.Closed = "not planned"
	c := Derive(f, nil)
	if c.Of(Land).State != StateDone || !strings.HasPrefix(c.Of(Land).Text, "none") || c.Current == Land || c.Code != CodeClosed {
		t.Errorf("closed: land %s %q, current %s, code %s", c.Of(Land).State, c.Of(Land).Text, c.Current, c.Code)
	}
	f = allGreen()
	f.Published = true
	n := Derive(f, append(reviewedClean(), ln(journal.EventPublishDone)))
	if n.Current == Land || n.Of(Land).State != StateDone || n.Prompt != "" {
		t.Errorf("no pull request: current %s, land %s, prompt %q", n.Current, n.Of(Land).State, n.Prompt)
	}
}

// TestLandPhaseBehindIsNotLandable: a published ticket owed a restack is at its
// publish, not at Land.
func TestLandPhaseBehindIsNotLandable(t *testing.T) {
	t.Parallel()
	f, lines := publishedWithPR()
	f.Restacks = []Restack{{Repo: "app", Kind: RestackParent, Ref: "JIG-2"}}
	tkt := Derive(f, lines)
	if !tkt.Behind() || tkt.Current == Land || tkt.Of(Land).State == StateCurrent {
		t.Errorf("behind: current %s, land %s", tkt.Current, tkt.Of(Land).State)
	}
}
