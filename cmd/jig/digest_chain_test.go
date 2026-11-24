package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/store"
)

// keptPRStates writes the pull request states this machine keeps (the cache the
// listing reads), as the host last said them, so a test has a host that needs no
// network: a state kept within the listing's age stands, and with the host
// unreachable a ticket's own read falls back to the last answer it gave.
func keptPRStates(t *testing.T, jigHome string, states map[string]string) {
	t.Helper()
	type record struct {
		State string    `json:"state"`
		At    time.Time `json:"at"`
	}
	kept := map[string]record{}
	for url, state := range states {
		kept[url] = record{State: state, At: time.Now()}
	}
	data, err := json.Marshal(map[string]any{"pull_requests": kept})
	if err != nil {
		t.Fatal(err)
	}
	path := home.PullRequestStatesPath(jigHome)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// head is the first n runes of s: a step's words are cut at the width of the
// screen, so the start of them is what a frame is read for.
func head(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// screensOf are the screens a pipe was printed, each whole, in order.
func screensOf(out string) []string {
	var screens []string
	for _, f := range strings.Split(out, "\n\f\n") {
		if strings.TrimSpace(f) != "" {
			screens = append(screens, f)
		}
	}
	return screens
}

// lastScreen is the screen a pipe was printed last.
func lastScreen(out string) string {
	screens := screensOf(out)
	if len(screens) == 0 {
		return ""
	}
	return screens[len(screens)-1]
}

// TestTUIWordsEqualNextAcrossTheAuditsStates: a store with a ticket in each of
// the states the audit found (new, brief waiting for approval, building with a
// failed and a stopped slice, in review, published with an open pull request,
// merged on the host and not recorded, landed, closed, an issue and a task of
// it) is read by `jig tui` on a pipe: the first screen says it is reading the
// store, and each ticket's screen has the phase words of its five steps, the kind and the pull
// request target `jig next <ticket> --json` gives. A chart's row reads as the
// ticket's own row on the project's list.
func TestTUIWordsEqualNextAcrossTheAuditsStates(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	keptPRStates(t, r.home, map[string]string{digestPR2: "OPEN", digestPR6: "OPEN", digestPR11: "MERGED", digestPR12: "MERGED"})
	// The host cannot be run: every read of a pull request falls back to what
	// this machine kept of it.
	down := filepath.Join(t.TempDir(), "gh-is-down")
	e := r.env().withLookPath(func(string) (string, error) { return down, nil })
	scope := []string{"--store", r.root}

	var roster nextStore
	out, code := runMain(t, e, "", append([]string{"next", "--json"}, scope...)...)
	if err := json.Unmarshal([]byte(out), &roster); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
	}
	if len(roster.Tickets) != 13 {
		t.Fatalf("the roster has %d tickets, want 13", len(roster.Tickets))
	}

	// The roster's digest carries what a step's mark reads of the sub-steps, so a
	// screen that never read a status marks a step as one that did.
	waiting := 0
	for _, row := range roster.Tickets {
		for _, p := range row.Digest.Phases {
			if row.Digest.Closed == "" && strings.Contains(p.Text, "waiting for your approval") {
				waiting++
				if !p.NeedsYou {
					t.Errorf("%s: its brief waits for the person's approval, and the digest does not say a sub-step needs them", row.ID)
				}
			}
		}
	}
	if waiting == 0 {
		t.Errorf("no ticket of the store has a brief waiting for approval")
	}

	// The project's list, on the screens, from the first screen to the one that
	// has read the roster.
	keys, main, wait := startTUI(t, e, scope...)
	main.until(t, "13 tickets")
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui exited non-zero\n%s", main.String())
	}
	frames := screensOf(main.String())
	if first := frames[0]; !strings.Contains(first, "reading the store…") || strings.Contains(first, "no tickets yet") {
		t.Errorf("the first screen does not say it is reading the store:\n%s", first)
	}
	list := frames[len(frames)-1]
	icons := regexp.MustCompile(`[^\s] [^\s] [^\s] [^\s]`)

	started := 0
	for _, row := range roster.Tickets {
		id, d := row.ID, row.Digest
		var n nextTicket
		if d.Kind == store.KindIssue {
			// An issue with a chart answers for its run: its screen is the
			// issue screen's own.
			n.Kind = d.Kind
		} else {
			out, code := runMain(t, e, "", append([]string{"next", id, "--json"}, scope...)...)
			if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
				t.Fatalf("jig next %s --json: exit %d, %v\n%s", id, code, err, out)
			}
		}
		// The digest is what `jig next <ticket>` says.
		if n.Phase != nil && (d.Current != n.Phase.Name || len(d.Phases) != len(n.Phase.Phases)) {
			t.Fatalf("%s: the roster says %s %v, jig next says %s %v", id, d.Current, d.Phases, n.Phase.Name, n.Phase.Phases)
		}

		if d.Kind == store.KindIssue {
			// An issue's screen is its plan: the chart that plans it.
			keys, screen, wait := startTUI(t, e, append([]string{id}, scope...)...)
			screen.until(t, "Need")
			screen.until(t, "Tasks")
			keys.Write([]byte("q"))
			if wait() != 0 {
				t.Fatalf("%s: jig tui exited non-zero\n%s", id, screen.String())
			}
			continue
		}

		// The ticket's screen.
		keys, screen, wait := startTUI(t, e, append([]string{id}, scope...)...)
		screen.until(t, "  Ticket "+id) // the roster has been read: the title is its
		if n.Phase != nil {
			for i, p := range n.Phase.Phases {
				want := p.Text
				if d.Closed != "" && i < 3 && slices.Contains(d.Unfinished, p.Name) {
					// A step a closed ticket never finished reads closed.
					want = "closed"
				}
				if i == 3 && len(d.PRs) > 0 && !strings.HasPrefix(p.Text, "closed") {
					// A published ticket's Publish row names its pull request.
					want = "published · PR #" + strconv.Itoa(d.PRs[0].Number)
				}
				screen.until(t, head(want, 24))
			}
		}
		frame := lastScreen(screen.String())
		if n.Phase != nil {
			land := n.Phase.Phases[4]
			if !strings.Contains(frame, "5 Land") || !strings.Contains(frame, head(land.Text, 20)) {
				t.Errorf("%s: the screen has no fifth step saying %q:\n%s", id, land.Text, frame)
			}
		}
		// The kind, and a task's issue.
		kind := n.Kind
		if kind == "" && n.Parent != "" {
			kind = "task of " + n.Parent
		}
		// A task of an issue is said so, as a task of a multi-task is.
		if want, ok := map[string]string{"TK-1": "issue", "TK-2": "task of TK-1", "TK-4": "multi-task", "TK-5": "task of TK-4"}[id]; ok && !strings.Contains(frame, want) {
			t.Errorf("%s: the screen does not say %q:\n%s", id, want, frame)
		}
		if kind != "" && !strings.Contains(frame, kind) {
			t.Errorf("%s: the screen does not name %q:\n%s", id, kind, frame)
		}
		// Where its pull requests go.
		for _, pr := range d.PRs {
			// The host's word on it, as the screen says it: "state not read" when
			// the digest has none.
			into := "PR #" + strconv.Itoa(pr.Number) + " " + pr.State + " into " + pr.Target
			if pr.State != "open" && pr.State != "merged" && pr.State != "closed" {
				into = "PR #" + strconv.Itoa(pr.Number) + " into " + pr.Target + " (state not read)"
			}
			for _, s := range n.Stack {
				if pr.Target != s.PRTarget {
					t.Errorf("%s: the digest's pull request goes into %s, jig next says %s", id, pr.Target, s.PRTarget)
				}
			}
			if !strings.Contains(frame, into) {
				t.Errorf("%s: the screen does not say %q:\n%s", id, into, frame)
			}
		}
		keys.Write([]byte("q"))
		if wait() != 0 {
			t.Fatalf("%s: jig tui exited non-zero\n%s", id, screen.String())
		}

		// Its row on the project's list holds the steps of the screen.
		var rowLine string
		for _, l := range strings.Split(list, "\n") {
			if strings.Contains(l, "Ticket "+id+" ") {
				rowLine = l
			}
		}
		if rowLine == "" {
			t.Errorf("%s: the project's list has no row for it:\n%s", id, list)
		} else if n.Phase != nil && icons.FindString(rowLine) == "" {
			t.Errorf("%s: its row has no steps: %q", id, rowLine)
		} else if d.Closed == "" && len(d.Phases) > 0 && d.Phases[0].State == "done" && d.Phases[0].NeedsYou {
			// A done brief the work started without an approval is marked so on
			// the list, which reads no status: the field crossed from `jig next`.
			started++
			if !strings.HasPrefix(icons.FindString(rowLine), "!") {
				t.Errorf("%s: its brief was never approved and the list marks it %q", id, icons.FindString(rowLine))
			}
		}
	}

	if started == 0 {
		t.Errorf("no ticket of the store has a done brief the work started without an approval")
	}

	// A chart's rows are the ticket rows of the project's list: the same steps,
	// in every frame the chart draws, from the one before its tickets' statuses
	// were read to the one after the last was: a step's mark does not wait on
	// the status.
	keys, chart, wait := startTUI(t, e, append([]string{"plan"}, scope...)...)
	chart.until(t, "› plan")
	chart.until(t, "TK-2")
	// The chart reads each ticket's status as it opens; let the reads land.
	for last, still := "", 0; still < 30; time.Sleep(10 * time.Millisecond) {
		if now := chart.String(); now != last {
			last, still = now, 0
		} else {
			still++
		}
	}
	cframes := screensOf(chart.String())
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui plan exited non-zero\n%s", chart.String())
	}
	marksOf := func(frame, id string) string {
		for _, l := range strings.Split(frame, "\n") {
			if strings.Contains(l, "Ticket "+id+" ") {
				return icons.FindString(l)
			}
		}
		return ""
	}
	for _, id := range []string{"TK-2", "TK-3"} {
		want := marksOf(list, id)
		seen := 0
		for _, frame := range cframes {
			got := marksOf(frame, id)
			if got == "" {
				continue // before the roster named the ticket
			}
			seen++
			if got != want {
				t.Errorf("%s: a chart frame has the steps %q, the project's list %q\n%s", id, got, want, frame)
			}
		}
		if seen == 0 || want == "" {
			t.Errorf("%s: the chart drew its row in %d frames, the list's row has the steps %q", id, seen, want)
		}
	}
}
