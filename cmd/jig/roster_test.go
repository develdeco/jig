package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/project/projecttest"
)

// rosterRows decodes what `jig next --json --roster` printed.
func rosterRows(t *testing.T, out string) (rows []map[string]any, top map[string]json.RawMessage) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatalf("the roster is not one JSON object: %v\n%s", err, out)
	}
	if err := json.Unmarshal(top["tickets"], &rows); err != nil {
		t.Fatalf("tickets: %v\n%s", err, out)
	}
	return rows, top
}

// TestRosterPrintsRowsAndChartsOnly: --roster prints the tickets' rows and the
// charts and nothing a screen does not read, and each row carries the instant
// of the newest line of its journal, absent for a ticket that has none.
func TestRosterPrintsRowsAndChartsOnly(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	out, code := r.run(t, "next", "--json", "--roster")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	rows, top := rosterRows(t, out)
	for key := range top {
		if key != "tickets" && key != "charts" && key != "next" {
			t.Errorf("the roster has %q, which no screen reads", key)
		}
	}
	for _, key := range []string{"issue_order", "shared_ranks", "workspace_overlaps", "overlaps"} {
		if _, ok := top[key]; ok {
			t.Errorf("the roster prints %q", key)
		}
	}
	if len(rows) != 13 {
		t.Fatalf("%d rows, want 13", len(rows))
	}
	updated := map[string]string{}
	for _, row := range rows {
		id, _ := row["id"].(string)
		if row["digest"] == nil {
			t.Errorf("%s has no digest", id)
		}
		updated[id], _ = row["updated"].(string)
	}
	// TK-9 has journal lines, TK-7 has none.
	if _, err := time.Parse(time.RFC3339, updated["TK-9"]); err != nil {
		t.Errorf("TK-9 updated = %q, want an RFC 3339 instant: %v", updated["TK-9"], err)
	}
	if updated["TK-7"] != "" {
		t.Errorf("TK-7 has no journal and says updated %q", updated["TK-7"])
	}
	lines, err := journal.Read(r.st, "TK-9")
	if err != nil || len(lines) == 0 {
		t.Fatalf("TK-9's journal: %v, %v", lines, err)
	}
	want, err := time.Parse(time.RFC3339Nano, lines[len(lines)-1].TS)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := time.Parse(time.RFC3339, updated["TK-9"]); !got.Equal(want.Truncate(time.Second)) {
		t.Errorf("TK-9 updated = %s, want its last line's instant %s", updated["TK-9"], want)
	}

	// Without --roster the listing is what it was, with its orders.
	full, code := r.run(t, "next", "--json")
	if code != 0 || !strings.Contains(full, `"next":`) {
		t.Errorf("jig next --json lost its next list: exit %d\n%s", code, full)
	}
}

// TestRosterRefusesWhatItDoesNotRead: --roster is the whole store's rows; a
// ticket or chart, --for, --topology and --tickets ask about one, and the text
// form is not offered.
func TestRosterRefusesWhatItDoesNotRead(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	for name, args := range map[string][]string{
		"a ticket": {"next", "TK-2", "--json", "--roster"},
		"a chart":  {"next", "chart-a", "--json", "--roster"},
		"--for":    {"next", "--json", "--roster", "--for", "build"},
		"topology": {"next", "--json", "--roster", "--topology"},
		"tickets":  {"next", "--json", "--roster", "--tickets", "TK-2"},
		"no json":  {"next", "--roster"},
	} {
		out, code := r.run(t, args...)
		if code == 0 || !strings.Contains(out, "--roster") || strings.Contains(out, `"tickets"`) {
			t.Errorf("%s: exit %d, want a refusal that names --roster:\n%s", name, code, out)
		}
	}
}

// TestRosterRowsEqualTheFullListing: every row the roster prints is the row
// `jig next --json` prints for the ticket, whichever the store is in.
func TestRosterRowsEqualTheFullListing(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	keptPRStates(t, r.home, map[string]string{digestPR2: "OPEN", digestPR6: "OPEN", digestPR11: "MERGED", digestPR12: "MERGED"})
	roster, code := r.run(t, "next", "--json", "--roster")
	if code != 0 {
		t.Fatalf("roster: exit %d\n%s", code, roster)
	}
	full, code := r.run(t, "next", "--json")
	if code != 0 {
		t.Fatalf("full: exit %d\n%s", code, full)
	}
	got, gotTop := rosterRows(t, roster)
	want, wantTop := rosterRows(t, full)
	if len(got) != len(want) {
		t.Fatalf("%d rows against %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("row %v differs:\nroster %v\nfull   %v", want[i]["id"], got[i], want[i])
		}
	}
	if string(gotTop["next"]) != string(wantTop["next"]) || string(gotTop["next"]) == "" {
		t.Errorf("the start order differs:\nroster %s\nfull   %s", gotTop["next"], wantTop["next"])
	}
	if string(gotTop["charts"]) != string(wantTop["charts"]) {
		t.Errorf("charts differ:\nroster %s\nfull   %s", gotTop["charts"], wantTop["charts"])
	}
}

// TestRosterAsksNoHost: the roster reads a pull request in the state this
// machine last kept of it, and starts no look for the host's program, while
// the full listing looks for it.
func TestRosterAsksNoHost(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	keptPRStates(t, r.home, map[string]string{digestPR2: "OPEN", digestPR11: "MERGED"})
	looked := 0
	e := r.env().withLookPath(func(string) (string, error) { looked++; return "", fmt.Errorf("not found") })
	out, code := runMain(t, e, "", "next", "--json", "--roster", "--store", r.root)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if looked != 0 {
		t.Errorf("the roster looked for the host's program %d times", looked)
	}
	rows, _ := rosterRows(t, out)
	states := map[string]string{}
	for _, row := range rows {
		d, _ := row["digest"].(map[string]any)
		prs, _ := d["prs"].([]any)
		for _, p := range prs {
			pr := p.(map[string]any)
			states[pr["url"].(string)] = pr["state"].(string)
		}
	}
	if states[digestPR2] != "open" || states[digestPR11] != "merged" {
		t.Errorf("a kept pull request state is not shown: %v", states)
	}
	if states[digestPR6] == "open" || states[digestPR6] == "merged" {
		t.Errorf("a pull request with no kept state reads %q, want unknown", states[digestPR6])
	}
	if _, code := runMain(t, e, "", "next", "--json", "--store", r.root); code != 0 || looked == 0 {
		t.Errorf("the full listing looked for the host %d times, want it to", looked)
	}
}

// rosterWorld is a store of tickets and a clone "app" holding a local branch
// for the first branches of them, which published: every third of those is
// stacked on the one before, half of the branches are in the target and the
// rest are not.
type rosterWorld struct {
	rig chartRig
	cfg project.Config
	mp  project.MachineProject
	e   env
}

func newRosterWorld(t *testing.T, tickets, branches int, worktreeConfig bool) rosterWorld {
	t.Helper()
	r := newChartRig(t)
	r.put(t, "project.yaml", "schema_version: 2\nname: demo\nkeys:\n  TK: everything in demo\ntrackers: []\nrepos:\n  - remote: /srv/app.git\n    label: app\n")
	dir := cmdGitRepo(t, filepath.Join(t.TempDir(), "app"))
	if worktreeConfig {
		gitIn(t, dir, "config", "extensions.worktreeConfig", "true")
	}
	base := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "commit", "--allow-empty", "-m", "target moves")
	moved := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "-b", "side", base)
	gitIn(t, dir, "commit", "--allow-empty", "-m", "side work")
	side := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "main")
	writeRef := func(name, sha string) {
		path := filepath.Join(dir, ".git", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(sha+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeRef("refs/remotes/origin/main", moved)
	for i := 0; i < tickets; i++ {
		id := fmt.Sprintf("TK-%d", i+1)
		extra := ""
		stacked := i < branches && i%3 == 2
		if stacked {
			extra = fmt.Sprintf("blocked_by:\n  - ticket: TK-%d\n    kind: stacked\n", i)
		}
		r.ticket(t, id, extra, false)
		if i >= branches {
			continue
		}
		r.green(t, id)
		for _, l := range []journal.Line{
			{Event: journal.EventPr, Outcome: "opened", URL: fmt.Sprintf("https://github.com/acme/app/pull/%d", 100+i)},
			{Event: journal.EventPublishDone},
		} {
			if err := journal.Append(r.st, id, l); err != nil {
				t.Fatal(err)
			}
		}
		tip := base
		if i%2 == 1 {
			tip = side
		}
		rec, err := r.st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		branch, err := r.st.ResolveTicketBranch(id, rec, "main")
		if err != nil {
			t.Fatal(err)
		}
		writeRef("refs/heads/"+branch, tip)
	}
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	mp := project.MachineProject{Clones: map[string]string{"app": dir}}
	if err := projecttest.SaveMachine(r.home, map[string]project.MachineProject{"demo": mp}); err != nil {
		t.Fatal(err)
	}
	return rosterWorld{rig: r, cfg: cfg, mp: mp, e: r.env()}
}

// read is one roster read by the verb itself, `jig next --json --roster` on the
// world's store, and the rows it printed: what the screens run is what is
// measured.
func (w rosterWorld) read(t *testing.T) nextRoster {
	t.Helper()
	out, code := runMain(t, w.e, "", "next", "--json", "--roster", "--store", w.rig.root)
	if code != 0 {
		t.Fatalf("jig next --json --roster: exit %d\n%s", code, out)
	}
	var n nextRoster
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "{") {
			if err := json.Unmarshal([]byte(line), &n); err != nil {
				t.Fatalf("the roster does not parse: %v", err)
			}
			return n
		}
	}
	t.Fatalf("the roster printed no object:\n%s", out)
	return n
}

// TestRosterGitProcessesDoNotGrowWithTicketsOrBranches: the git processes of
// one roster read are the same for a store of 12 tickets and 6 branches and a
// store of 120 tickets and 60, in a plain clone and in one that git's own reads
// in process decline, so a read costs git once for each repo and nothing for
// each ticket, branch or pair. Not parallel: it counts the process's git.
func TestRosterGitProcessesDoNotGrowWithTicketsOrBranches(t *testing.T) {
	for _, layout := range []struct {
		name           string
		worktreeConfig bool
	}{{"plain clone", false}, {"clone with worktree config", true}} {
		small := newRosterWorld(t, 12, 6, layout.worktreeConfig)
		large := newRosterWorld(t, 120, 60, layout.worktreeConfig)
		// Both are read once before counting, so what is counted is a read
		// that finds the answers about ancestry kept.
		var counts [2]int
		var rows nextRoster
		for i, w := range []rosterWorld{small, large} {
			w.read(t)
			counts[i] = gittest.CountGit(t, func() { rows = w.read(t) })
		}
		// The rows are the store's tickets, in its id order, each with a digest.
		ids, err := large.rig.st.Tickets()
		if err != nil || len(rows.Tickets) != len(ids) {
			t.Fatalf("%s: %d rows for %d tickets (%v)", layout.name, len(rows.Tickets), len(ids), err)
		}
		withPR := 0
		for i, row := range rows.Tickets {
			if row.ID != ids[i] || row.Digest == nil {
				t.Fatalf("%s: row %d is %s with digest %v, want %s with one", layout.name, i, row.ID, row.Digest, ids[i])
			}
			if len(row.Digest.PRs) > 0 {
				withPR++
			}
		}
		if withPR != 60 {
			t.Errorf("%s: %d rows carry a pull request, want the 60 that published", layout.name, withPR)
		}
		if counts[0] != counts[1] {
			t.Errorf("%s: a read starts %d git processes of 12 tickets and %d of 120", layout.name, counts[0], counts[1])
		}
		if counts[1] > 12 {
			t.Errorf("%s: a read starts %d git processes, want a few for the repo", layout.name, counts[1])
		}
		t.Logf("%s: %d git processes for 12 tickets, %d for 120", layout.name, counts[0], counts[1])
	}
}

// TestRosterOfAThousandTicketsByHand reads a store of 1,000 tickets and 500
// local branches and logs the wall time and the processes of a first read and
// of the next: the figure of the pull request's demo (under 2 s once the facts
// are kept). It builds the store for seconds, so it runs only by hand:
// JIG_ROSTER_THOUSAND=1 go test ./cmd/jig -run ByHand -v.
func TestRosterOfAThousandTicketsByHand(t *testing.T) {
	if os.Getenv("JIG_ROSTER_THOUSAND") == "" {
		t.Skip("by hand: set JIG_ROSTER_THOUSAND=1")
	}
	w := newRosterWorld(t, 1000, 500, true)
	started := time.Now()
	first := gittest.CountGit(t, func() { w.read(t) })
	t.Logf("1,000 tickets and 500 branches, a first read: %s, %d git processes", time.Since(started), first)
	started = time.Now()
	count := gittest.CountGit(t, func() { w.read(t) })
	t.Logf("1,000 tickets and 500 branches: %s, %d git processes", time.Since(started), count)
}

// TestRosterVerbReadsInOneScope: the verb the screens run opens the read scope
// itself, so the cost rule holds for the process and not only for the functions
// under it. A second read of the roster and of `jig next` starts the same few
// git processes, and `jig next` the second time starts no diff and no merge,
// since what it asked of git about each branch is kept under the jig home. The
// chart index stands for the run. Not parallel: it counts the process's git.
func TestRosterVerbReadsInOneScope(t *testing.T) {
	w := newRosterWorld(t, 30, 12, true)
	w.read(t)
	argv := gittest.GitArgv(t, func() { w.read(t) })
	if len(argv) > 4 {
		t.Errorf("a roster read of 30 tickets and 12 branches started %d git processes: %v", len(argv), argv)
	}

	// Every pull request is open on this machine's word, so every branch is an
	// open ticket's and the overlaps ask about it.
	open := map[string]string{}
	for i := 0; i < 12; i++ {
		open[fmt.Sprintf("https://github.com/acme/app/pull/%d", 100+i)] = "OPEN"
	}
	keptPRStates(t, w.rig.home, open)
	full := func() [][]string {
		out, code := runMain(t, w.e, "", "next", "--json", "--store", w.rig.root)
		if code != 0 {
			t.Fatalf("jig next --json: exit %d\n%s", code, out)
		}
		return nil
	}
	first := gittest.GitArgv(t, func() { full() })
	second := gittest.GitArgv(t, func() { full() })
	if askedOf(first, "diff") == 0 {
		t.Fatalf("a first read of the overlaps asked no diff of 12 branches: %v", first)
	}
	if got := askedOf(second, "diff") + askedOf(second, "merge-tree"); got != 0 {
		t.Errorf("a second jig next asked %d diffs and merges, want none: %v", got, second)
	}

	// The scope pins the chart index: a chart made while it stands is not seen
	// by it, and is once it ends.
	end := readScope(w.rig.st, w.mp, w.rig.home, true)
	before, err := w.rig.st.ChartNames()
	if err != nil {
		t.Fatal(err)
	}
	w.rig.chart(t, "late", "issue: ISS-9\ntickets:\n  - id: TK-1\n    title: One\n", "")
	during, err := w.rig.st.ChartNames()
	if err != nil {
		t.Fatal(err)
	}
	end()
	after, err := w.rig.st.ChartNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(during) != len(before) || len(after) != len(before)+1 {
		t.Errorf("charts %v before, %v during the scope, %v after: the index is not pinned for the scope", before, during, after)
	}
}
