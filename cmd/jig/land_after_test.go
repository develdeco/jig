package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// TestAfterShownIssueLandsFirstAndNothingRefuses: issue TK-8 is suggested to
// land after TK-7. `jig next TK-8` and `jig next` show it, `jig land TK-8`
// notes it, names its merge and records the landing, and landing TK-7 says
// what follows it. Nothing is refused and no landed line changes.
func TestAfterShownIssueLandsFirstAndNothingRefuses(t *testing.T) {
	r := landRig(t)
	r.ticket(t, "TK-7", "kind: issue\n", false)
	r.ticket(t, "TK-8", "kind: issue\nafter:\n  - TK-7\n", false)
	for _, c := range [][]string{{"add", "-A"}, {"commit", "-m", "issues"}} {
		if _, err := gitx.Run(r.root, c...); err != nil {
			t.Fatal(err)
		}
	}
	urls := map[string]string{"TK-7": "https://github.example/owner/repo/pull/7", "TK-8": "https://github.example/owner/repo/pull/8"}
	for id, url := range urls {
		for _, l := range []journal.Line{{Event: journal.EventPr, URL: url}, {Event: journal.EventPublishDone}} {
			if err := journal.Append(r.st, id, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	pulls := func(merged7, merged8 bool) {
		pull := func(n int, merged bool) fixture.GhPull {
			p := fixture.GhPull{Number: n, Base: "main"}
			if merged {
				p.State, p.Merged, p.MergeCommit = "closed", true, "c0ffee"+string(rune('0'+n))
			}
			return p
		}
		t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, pull(7, merged7), pull(8, merged8)))
	}
	pulls(false, false)

	out, code := r.run(t, "next", "TK-8")
	if code != 0 || !strings.Contains(out, "lands_after[1]{ticket,title,state}:\n  TK-7,Ticket TK-7,not landed") || !strings.Contains(out, "Suggested order: land TK-7 before TK-8") {
		t.Fatalf("next TK-8: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "ready: no") {
		t.Errorf("a suggestion blocks nothing:\n%s", out)
	}
	if out, _ := r.run(t, "next"); !strings.Contains(out, "issue_order[1]{issue,after}:\n  TK-8,TK-7") {
		t.Errorf("next lacks the issue order:\n%s", out)
	}

	out, code = r.run(t, "land", "TK-7")
	if code != 0 || !strings.Contains(out, "TK-8 is suggested to land after this") || !strings.Contains(out, "gh pr merge "+urls["TK-7"]) {
		t.Fatalf("land TK-7: exit %d\n%s", code, out)
	}
	out, code = r.run(t, "land", "TK-8")
	for _, want := range []string{
		"lands_after[1]{ticket,title,state}:\n  TK-7,Ticket TK-7,not landed",
		"TK-8 is suggested to land after TK-7, not landed",
		"gh pr merge " + urls["TK-8"] + " --squash",
	} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("land TK-8 before TK-7: exit %d, want %q\n%s", code, want, out)
		}
	}
	pulls(false, true)
	if out, code = r.run(t, "land", "TK-8"); code != 0 || !strings.Contains(out, "landed") {
		t.Fatalf("land TK-8 recording: exit %d\n%s", code, out)
	}
	if got := landedLines(t, r, "TK-8"); len(got) != 1 {
		t.Fatalf("TK-8 landed lines = %d, want 1", len(got))
	}
	if got := landedLines(t, r, "TK-7"); len(got) != 0 {
		t.Fatalf("landing TK-8 first landed TK-7: %v", got)
	}

	pulls(true, true)
	if out, code = r.run(t, "land", "TK-7"); code != 0 || strings.Contains(out, "is suggested to land after this") {
		t.Fatalf("land TK-7 recording: exit %d\n%s", code, out)
	}
	out, code = r.run(t, "next", "TK-8")
	if code != 0 || !strings.Contains(out, "TK-7,Ticket TK-7,landed") || strings.Contains(out, "Suggested order") {
		t.Errorf("next TK-8 after TK-7 landed: exit %d\n%s", code, out)
	}
	if out, _ := r.run(t, "next"); strings.Contains(out, "issue_order") {
		t.Errorf("an order line shows for a landed predecessor:\n%s", out)
	}
}
