package main

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// landRig is the container rig with the fake gh as the pull-request host: TK-2
// and TK-3 (which waits for TK-2 to merge) are published into the issue TK-1,
// pull requests 2 and 3, both open.
func landRig(t *testing.T) chartRig {
	t.Helper()
	r := containerRig(t)
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "fixture@example.invalid"}, {"config", "user.name", "fixture"}, {"add", "-A"}, {"commit", "-m", "store"}} {
		if _, err := gitx.Run(r.root, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fixture.GhStub(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for id, url := range map[string]string{"TK-2": "https://github.example/owner/repo/pull/2", "TK-3": "https://github.example/owner/repo/pull/3"} {
		r.green(t, id)
		for _, l := range []journal.Line{
			{Event: journal.EventPr, Into: "TK-1", URL: url},
			{Event: journal.EventPublishDone, Into: "TK-1"},
		} {
			if err := journal.Append(r.st, id, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	return r
}

// host sets what the fake gh holds: pull requests 2 and 3, each open or
// merged, into the issue's branch.
func (r chartRig) host(t *testing.T, merged2, merged3 bool) {
	t.Helper()
	rec, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := r.st.ResolveTicketBranch("TK-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	pull := func(n int, merged bool) fixture.GhPull {
		p := fixture.GhPull{Number: n, Base: branch}
		if merged {
			p.State, p.Merged, p.MergeCommit = "closed", true, "c0ffee"+string(rune('0'+n))
		}
		return p
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, pull(2, merged2), pull(3, merged3)))
}

func landedLines(t *testing.T, r chartRig, id string) []journal.Line {
	t.Helper()
	lines, err := journal.Read(r.st, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range lines {
		if l.Event == journal.EventLanded {
			out = append(out, l)
		}
	}
	return out
}

// TestLandVerbIssueAndTwoTasks: the second task, merged on the first, is
// refused naming the first, and nothing is written; the first names its merge
// into the issue's branch and writes nothing; once the host says merged it
// writes landed (an agent's, since this machine named the merge), says already
// landed when run again, and the second may then be landed. jig runs no merge.
func TestLandVerbIssueAndTwoTasks(t *testing.T) {
	r := landRig(t)
	r.host(t, false, false)

	out, code := r.run(t, "land", "TK-3")
	if code == 0 || !strings.Contains(out, "LAND_WAITS") || !strings.Contains(out, "TK-2") {
		t.Fatalf("land TK-3: exit %d, want LAND_WAITS naming TK-2:\n%s", code, out)
	}
	if len(landedLines(t, r, "TK-3")) != 0 {
		t.Error("a refusal wrote a landed line")
	}

	out, code = r.run(t, "land", "TK-2")
	if code != 0 || !strings.Contains(out, "gh pr merge https://github.example/owner/repo/pull/2 --squash") {
		t.Fatalf("land TK-2: exit %d, want the merge named:\n%s", code, out)
	}
	if len(landedLines(t, r, "TK-2")) != 0 {
		t.Error("naming a merge wrote a landed line")
	}

	r.host(t, true, false)
	out, code = r.run(t, "land", "TK-2")
	if code != 0 || !strings.Contains(out, "landed") {
		t.Fatalf("land TK-2 after the merge: exit %d\n%s", code, out)
	}
	lines := landedLines(t, r, "TK-2")
	if len(lines) != 1 {
		t.Fatalf("landed lines = %+v, want one", lines)
	}
	l := lines[0]
	if l.URL != "https://github.example/owner/repo/pull/2" || l.Into != "TK-1" || l.Commit != "c0ffee2" || l.Provenance != journal.ProvenanceAgent || l.Base == "" || l.Base == "main" {
		t.Errorf("landed line = %+v", l)
	}
	if !strings.Contains(out, "jig land TK-3") {
		t.Errorf("the next landing is not named:\n%s", out)
	}

	out, code = r.run(t, "land", "TK-2")
	if code != 0 || !strings.Contains(out, "already_landed") || len(landedLines(t, r, "TK-2")) != 1 {
		t.Errorf("land TK-2 again: exit %d, lines %d\n%s", code, len(landedLines(t, r, "TK-2")), out)
	}

	out, code = r.run(t, "land", "TK-3")
	if code != 0 || !strings.Contains(out, "gh pr merge https://github.example/owner/repo/pull/3") {
		t.Errorf("land TK-3 after TK-2 landed: exit %d\n%s", code, out)
	}
}

// TestLandVerbMergeOnTheHost: a pull request merged on the host with no `jig
// land` before is recorded by it, as the host's (nothing was named); a method
// the person asked for, and the refusals of a closed pull request and of one
// into the wrong base, are as the facts say.
func TestLandVerbMergeOnTheHost(t *testing.T) {
	r := landRig(t)
	r.host(t, true, false)
	out, code := r.run(t, "land", "TK-2")
	lines := landedLines(t, r, "TK-2")
	if code != 0 || len(lines) != 1 || lines[0].Provenance != journal.ProvenanceHost {
		t.Fatalf("land TK-2 merged on the host: exit %d, lines %+v\n%s", code, lines, out)
	}

	out, code = r.run(t, "land", "TK-3", "--method", "rebase")
	if code != 0 || !strings.Contains(out, "--rebase") {
		t.Errorf("land TK-3 --method rebase: exit %d\n%s", code, out)
	}
	if out, code = r.run(t, "land", "TK-3", "--method", "octopus"); code == 0 {
		t.Errorf("land TK-3 --method octopus: exit %d, want a refusal\n%s", code, out)
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 2, Base: "main", State: "closed", Merged: true}, fixture.GhPull{Number: 3, Base: "main"}))
	if out, code = r.run(t, "land", "TK-3"); code == 0 || !strings.Contains(out, "LAND_BASE_DIFFERS") || !strings.Contains(out, "jig publish TK-3") {
		t.Errorf("land TK-3 into main: exit %d, want LAND_BASE_DIFFERS\n%s", code, out)
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, fixture.GhPull{Number: 2, Base: "main", State: "closed", Merged: true}, fixture.GhPull{Number: 3, Base: "main", State: "closed"}))
	if out, code = r.run(t, "land", "TK-3"); code == 0 || !strings.Contains(out, "LAND_CLOSED") {
		t.Errorf("land TK-3 closed: exit %d, want LAND_CLOSED\n%s", code, out)
	}
}
