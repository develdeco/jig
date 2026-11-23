package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestTUIEndStatesEqualNext: a store with a task published into its issue's
// branch behind one that has not landed, a task that waits to land, a merge the
// host made and nobody recorded, a task that landed, a task with no pull request
// and a closed ticket with its reason is read by `jig tui` on a pipe. The frames
// of the project's list, of each ticket's screen and of its land screen (after
// the key 5) say the end state, the Land step's words and the pull request's
// target `jig next <ticket> --json` gives, the four states read apart on the
// project's list, the chart's row and the ticket's screen, and a landing sits
// under the land step.
func TestTUIEndStatesEqualNext(t *testing.T) {
	t.Parallel()
	r, _ := landFirstRig(t)
	// TK-17 published and has no pull request to merge; the issue's chart lists
	// TK-14, so its row is on the chart's screen.
	r.ticket(t, "TK-17", "", true)
	r.green(t, "TK-17")
	if err := journal.Append(r.st, "TK-17", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	r.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-3\n    title: Three\n  - id: TK-4\n    title: Four\n    kind: multi-task\n  - id: TK-5\n    title: Five\n  - id: TK-14\n    title: Fourteen\n", "")
	// The host cannot be run: every read of a pull request is what this machine
	// kept of it.
	keptPRStates(t, r.home, map[string]string{
		digestPR2: "OPEN", digestPR6: "OPEN", digestPR11: "MERGED", digestPR12: "MERGED",
		landFirstPR14: "OPEN", landFirstPR15: "OPEN", landFirstPR16: "MERGED",
	})
	down := filepath.Join(t.TempDir(), "gh-is-down")
	e := r.env().withLookPath(func(string) (string, error) { return down, nil })
	scope := []string{"--store", r.root}

	var roster nextStore
	out, code := runMain(t, e, "", append([]string{"next", "--json"}, scope...)...)
	if err := json.Unmarshal([]byte(out), &roster); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
	}
	rows := map[string]nextRow{}
	for _, row := range roster.Tickets {
		rows[row.ID] = row
	}

	// What each ticket reads as: the icon, the header's word and the words of
	// its row, a state apart from the others.
	type end struct{ icon, word, words string }
	want := map[string]end{
		"TK-14": {"▲", "published", "published · PR #34 open into "},
		"TK-2":  {"◇", "waits to land", `waits to land · say "Land TK-2"`},
		"TK-6":  {"◇", "waits to land", `waits to land · say "Land TK-6"`},
		"TK-11": {"◇", "waits to land", `merged on the host, not recorded · say "Land TK-11"`},
		"TK-12": {"✓", "landed", "landed"},
		"TK-16": {"✓", "landed", "landed"},
		"TK-17": {"✓", "landed", "none · no pull request to merge"},
		"TK-13": {"⊖", "closed", "closed · covered by develdeco/jig#82"},
		"TK-15": {"⊖", "closed", "closed · not planned"},
	}

	keys, main, wait := startTUI(t, e, scope...)
	main.until(t, "17 tickets")
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui exited non-zero\n%s", main.String())
	}
	list := lastScreen(main.String())

	for id, w := range want {
		var n nextTicket
		out, code := runMain(t, e, "", append([]string{"next", id, "--json"}, scope...)...)
		if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil || n.Phase == nil {
			t.Fatalf("jig next %s --json: exit %d, %v\n%s", id, code, err, out)
		}
		d := rows[id].Digest
		if strings.Join(d.LandFirst, ",") != strings.Join(n.LandFirst, ",") {
			t.Errorf("%s: the roster's land_first is %v, jig next says %v", id, d.LandFirst, n.LandFirst)
		}
		if (id == "TK-14") != (len(n.LandFirst) > 0) {
			t.Errorf("%s: land_first is %v", id, n.LandFirst)
		}
		land := n.Phase.Phases[4]

		// The project's list: the row's icon and words.
		row := ""
		for _, l := range strings.Split(list, "\n") {
			if strings.Contains(l, "Ticket "+id+" ") {
				row = l
			}
		}
		if !strings.Contains(row, w.icon+" "+id) || !strings.Contains(row, head(w.words, 28)) {
			t.Errorf("%s: its row on the project's list is not %s with %q:\n%s", id, w.icon, w.words, row)
		}

		// The ticket's screen.
		keys, screen, wait := startTUI(t, e, append([]string{id}, scope...)...)
		screen.until(t, "› "+id+"  Ticket "+id)
		screen.until(t, head(land.Text, 20))
		if id == "TK-13" {
			screen.until(t, "⊖ 1 Brief") // the status is read: the steps it never finished are told
		}
		frame := lastScreen(screen.String())
		if header := strings.Split(frame, "\n")[0]; !strings.Contains(header, w.icon+" "+w.word) {
			t.Errorf("%s: its header does not say %s %s:\n%s", id, w.icon, w.word, header)
		}
		if !strings.Contains(frame, "5 Land") || !strings.Contains(frame, head(land.Text, 20)) {
			t.Errorf("%s: its fifth step does not say %q:\n%s", id, land.Text, frame)
		}
		// A closed ticket is offered no merge and its publish keeps the words it
		// finished with: no pull request is named for it.
		if id == "TK-13" {
			// Closed before its work was done: the steps it never finished read closed.
			for _, bad := range []string{"waiting for your approval", "0 of 1 green", "ready for a fresh session"} {
				if strings.Contains(frame, bad) {
					t.Errorf("%s: a closed ticket's screen says %q:\n%s", id, bad, frame)
				}
			}
		}
		into := prTargets(d)
		if w.word == "closed" {
			into = nil
		}
		for _, target := range into {
			if !strings.Contains(frame, "into "+target) {
				t.Errorf("%s: the screen does not say its pull request goes into %s:\n%s", id, target, frame)
			}
		}

		// Its land screen, after 5.
		keys.Write([]byte("5"))
		screen.until(t, "› 5 land")
		lframe := lastScreen(screen.String())
		if !strings.Contains(lframe, head(w.words, 40)) {
			t.Errorf("%s: the land screen does not open with %q:\n%s", id, w.words, lframe)
		}
		for _, target := range into {
			if !strings.Contains(lframe, "into "+target) {
				t.Errorf("%s: the land screen does not say %s:\n%s", id, target, lframe)
			}
		}
		if w.word == "landed" && d != nil && len(d.PRs) > 0 {
			if !strings.Contains(lframe, "✓") || !strings.Contains(lframe, "record · landed") {
				t.Errorf("%s: the landing is not under the land step:\n%s", id, lframe)
			}
		}
		if w.word == "closed" && strings.Contains(lframe, "merge PR") {
			t.Errorf("%s: a closed ticket is offered a merge:\n%s", id, lframe)
		}
		keys.Write([]byte("q"))
		if wait() != 0 {
			t.Fatalf("%s: jig tui exited non-zero\n%s", id, screen.String())
		}
	}

	// The four states read apart.
	icons := map[string]bool{}
	for _, w := range want {
		icons[w.icon] = true
	}
	if len(icons) != 4 {
		t.Errorf("the states have %d icons, want four: %v", len(icons), icons)
	}

	// The chart's row of the published ticket and of the one that waits.
	keys, chart, wait := startTUI(t, e, append([]string{"plan"}, scope...)...)
	chart.until(t, "› plan")
	chart.until(t, "TK-14")
	cframe := lastScreen(chart.String())
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui plan exited non-zero\n%s", chart.String())
	}
	for _, id := range []string{"TK-14", "TK-2"} {
		row := ""
		for _, l := range strings.Split(cframe, "\n") {
			if strings.Contains(l, "Ticket "+id+" ") {
				row = l
			}
		}
		if w := want[id]; !strings.Contains(row, w.icon+" "+id) || !strings.Contains(row, head(w.words, 18)) {
			t.Errorf("%s: the chart's row is not %s with %q:\n%s", id, w.icon, w.words, cframe)
		}
	}
}

// prTargets are the branches the pull requests of a digest go into.
func prTargets(d *nextDigest) []string {
	var out []string
	if d == nil {
		return nil
	}
	for _, pr := range d.PRs {
		out = append(out, pr.Target)
	}
	return out
}
