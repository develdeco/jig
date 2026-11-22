package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
// store, and each ticket's screen has the phase words, the kind and the pull
// request target `jig next <ticket> --json` gives. A chart's row reads as the
// ticket's own row on the project's list.
func TestTUIWordsEqualNextAcrossTheAuditsStates(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	keptPRStates(t, r.home, map[string]string{digestPR2: "OPEN", digestPR6: "OPEN", digestPR11: "MERGED", digestPR12: "MERGED"})
	// The host cannot be run: every read of a pull request falls back to what
	// this machine kept of it.
	e := r.env().withLookPath(func(string) (string, error) { return filepath.Join(t.TempDir(), "gh-is-down"), nil })
	scope := []string{"--store", r.root}

	var roster nextStore
	out, code := runMain(t, e, "", append([]string{"next", "--json"}, scope...)...)
	if err := json.Unmarshal([]byte(out), &roster); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
	}
	if len(roster.Tickets) != 13 {
		t.Fatalf("the roster has %d tickets, want 13", len(roster.Tickets))
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

		// The ticket's screen.
		keys, screen, wait := startTUI(t, e, append([]string{id}, scope...)...)
		screen.until(t, "› "+id+"  Ticket "+id) // the roster has been read: the title is its
		if n.Phase != nil {
			for i, p := range n.Phase.Phases[:4] {
				screen.until(t, head(p.Text, 24))
				_ = i
			}
		}
		frame := lastScreen(screen.String())
		if n.Phase != nil {
			land := n.Phase.Phases[4]
			if land.State != "pending" && !strings.Contains(frame, "land  "+head(land.Text, 20)) {
				t.Errorf("%s: the screen has no Land line %q:\n%s", id, land.Text, frame)
			}
			if land.State == "pending" && strings.Contains(frame, "land  ") {
				t.Errorf("%s: the screen has a Land line before the publish:\n%s", id, frame)
			}
		}
		// The kind, and a task's issue.
		kind := n.Kind
		if kind == "" && n.Parent != "" {
			kind = "task of " + n.Parent
		}
		if kind != "" && !strings.Contains(frame, kind) {
			t.Errorf("%s: the screen does not name %q:\n%s", id, kind, frame)
		}
		// Where its pull requests go.
		for _, pr := range d.PRs {
			into := "PR #" + strconv.Itoa(pr.Number) + " into " + pr.Target
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
		}
	}

	// A chart's rows are the ticket rows of the project's list: the same steps.
	keys, chart, wait := startTUI(t, e, append([]string{"plan"}, scope...)...)
	chart.until(t, "› plan")
	chart.until(t, "TK-2")
	cframe := lastScreen(chart.String())
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui plan exited non-zero\n%s", chart.String())
	}
	for _, id := range []string{"TK-2", "TK-3"} {
		var rowOf, listOf string
		for _, l := range strings.Split(cframe, "\n") {
			if strings.Contains(l, "Ticket "+id+" ") {
				rowOf = icons.FindString(l)
			}
		}
		for _, l := range strings.Split(list, "\n") {
			if strings.Contains(l, "Ticket "+id+" ") {
				listOf = icons.FindString(l)
			}
		}
		if rowOf == "" || rowOf != listOf {
			t.Errorf("%s: the chart's row has the steps %q, the project's list %q\n%s", id, rowOf, listOf, cframe)
		}
	}
}
