package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

// originHead is the subject and the sha of the tip of the bare remote.
func originHead(t *testing.T, remote string) (subject, sha string) {
	t.Helper()
	subject, err := gitx.Run("", "--git-dir", remote, "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	sha, err = gitx.Run("", "--git-dir", remote, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return subject, sha
}

// TestChartNew: jig chart new makes the chart the way jig ticket new makes a
// ticket. It commits charts/<name>/ alone as "chart <name>: new chart" on the
// origin, leaves the clone clean, prints the chart block and the three help
// lines, and writes nothing to the session ledger. The name may come after
// the flags, and the title is kept as typed, trimmed.
func TestChartNew(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	session := "s-chart"
	vars := map[string]string{"JIG_HOME": home, "HOME": home, "USERPROFILE": home, locate.SessionEnv: session}
	if err := ledger.StartSession(home, time.Time{}, ledger.Registration{ID: session, Harness: "claude", Model: "claude-sonnet-5"}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginClone(t, clone)

	var buf bytes.Buffer
	code := run(envFrom(vars), []string{"chart", "new", "--title", "  Coupons at checkout ", "--store", clone, "coupons"}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig chart new: exit %d\n%s", code, buf.String())
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"chart:",
		"name: coupons",
		"title: Coupons at checkout",
		filepath.Join(st.Root, "charts", "coupons"),
		"help[3]:",
		"Fill charts/coupons/map.md and add the chart's tickets to charts/coupons/tickets.yaml",
		"Check them with `jig validate coupons`: it lists every problem and mints nothing",
		"Run `jig graduate coupons` to create the tickets",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	subject, _ := originHead(t, remote)
	if subject != "chart coupons: new chart" {
		t.Fatalf("origin HEAD subject = %q, want %q", subject, "chart coupons: new chart")
	}
	files, err := gitx.Run("", "--git-dir", remote, "show", "--name-only", "--pretty=format:", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(files); len(got) != 2 || got[0] != "charts/coupons/map.md" || got[1] != "charts/coupons/tickets.yaml" {
		t.Fatalf("the commit holds %q, want charts/coupons/map.md and charts/coupons/tickets.yaml", got)
	}
	if dirty, err := gitx.Run(clone, "status", "--porcelain"); err != nil || dirty != "" {
		t.Fatalf("the clone is not clean after the claim: %q, %v", dirty, err)
	}

	mapMD, err := os.ReadFile(st.ChartMapFile("coupons"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(mapMD), "# Coupons at checkout\n\n## Destination\n\n") {
		t.Fatalf("map.md = %q, want the trimmed title as its heading, then the sections", mapMD)
	}
	entries, err := st.ReadChart("coupons")
	if err != nil || len(entries) != 0 {
		t.Fatalf("ReadChart = %+v, %v, want no entries", entries, err)
	}

	lines, err := ledger.Read(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Event == journal.EventSessionTouch {
			t.Errorf("jig chart new wrote a session-touch line %+v, want none", l)
		}
	}
}

// TestChartNewRefusals: each refusal is a VALIDATION_ERROR (exit 2) that says
// what to do, and leaves the store as it was. The first three are decided
// before the store is resolved, so they hold with a --store that does not
// exist; the taken-name ones are decided after the sync.
func TestChartNewRefusals(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginClone(t, clone)

	// The store has a chart, and a ticket one of whose aliases is a word.
	if code, out := runChartCmd(t, e, "chart", "new", "taken", "--title", "Taken", "--store", clone); code != 0 {
		t.Fatalf("jig chart new taken: exit %d\n%s", code, out)
	}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("DEMO-30", store.Ticket{Title: "Renamed", Aliases: []string{"oldname"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "-c", "user.name=tester", "-c", "user.email=tester@example.invalid", "commit", "-m", "DEMO-30: new ticket"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(clone, "push", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	_, headBefore := originHead(t, remote)
	longName := strings.Repeat("a", 65)
	const rule = `use lower-case letters, digits, "-" and "_", starting and ending with a letter or digit, at most 64 characters`
	gone := filepath.Join(t.TempDir(), "no-such-store")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no name", []string{"chart", "new", "--title", "T", "--store", gone}, "missing required chart name"},
		{"two names", []string{"chart", "new", "a", "b", "--title", "T", "--store", gone}, "takes at most one chart name, got 2"},
		{"no title", []string{"chart", "new", "fresh", "--store", gone}, "jig chart new requires --title"},
		{"a blank title", []string{"chart", "new", "fresh", "--title", "   ", "--store", gone}, "jig chart new requires --title"},
		{"a title of two lines", []string{"chart", "new", "fresh", "--title", "One\nTwo", "--store", gone}, "--title must be one line"},
		{"upper case", []string{"chart", "new", "Fresh", "--title", "T", "--store", gone}, `chart name "Fresh" is not valid: ` + rule},
		{"a path separator", []string{"chart", "new", "a/b", "--title", "T", "--store", gone}, `chart name "a/b" is not valid: ` + rule},
		{"a dot", []string{"chart", "new", "..", "--title", "T", "--store", gone}, `chart name ".." is not valid: ` + rule},
		{"a leading dash", []string{"chart", "new", "--title", "T", "--store", gone, "./-x"}, `is not valid: ` + rule},
		{"a trailing dash", []string{"chart", "new", "fresh-", "--title", "T", "--store", gone}, `chart name "fresh-" is not valid: ` + rule},
		{"too long", []string{"chart", "new", longName, "--title", "T", "--store", gone}, "is not valid: " + rule},
		{"a lease suffix", []string{"chart", "new", "fresh-gate", "--title", "T", "--store", gone}, `chart name "fresh-gate" is not valid: a name that ends in`},
		{"another subcommand", []string{"chart", "list", "--store", gone}, `unknown chart subcommand "list"; only "new" is supported`},
		{"a chart that exists", []string{"chart", "new", "taken", "--title", "T", "--store", clone}, `chart "taken" already exists in the store at `},
		{"a ticket alias", []string{"chart", "new", "oldname", "--title", "T", "--store", clone}, `"oldname" is a ticket of the store: a ticket id or alias wins over a chart name, so choose another name`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out := runChartCmd(t, e, c.args...)
			if code != 2 {
				t.Fatalf("exit %d, want 2:\n%s", code, out)
			}
			for _, want := range []string{"code: VALIDATION_ERROR", c.want} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if !strings.Contains(out, "help[") {
				t.Errorf("the refusal says nothing to do next:\n%s", out)
			}
		})
	}

	if _, headAfter := originHead(t, remote); headAfter != headBefore {
		t.Fatalf("a refusal moved the origin from %s to %s", headBefore, headAfter)
	}
	if dirty, err := gitx.Run(clone, "status", "--porcelain"); err != nil || dirty != "" {
		t.Fatalf("a refusal left the clone dirty: %q, %v", dirty, err)
	}
	names, err := st.ChartNames()
	if err != nil || len(names) != 1 || names[0] != "taken" {
		t.Fatalf("ChartNames = %v, %v, want only the chart made first", names, err)
	}
}

// TestChartNewHelp: jig chart -h and jig chart new -h print the verb's flags.
func TestChartNewHelp(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	for _, args := range [][]string{{"chart", "-h"}, {"chart", "new", "-h"}, {"chart", "new", "x", "--help"}} {
		code, out := runChartCmd(t, e, args...)
		if code != 0 {
			t.Fatalf("jig %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
		for _, want := range []string{"usage: jig chart new [flags]", "flags{chart}", "--title", "chart title (required)", "--store", "--project"} {
			if !strings.Contains(out, want) {
				t.Errorf("jig %s: output lacks %q:\n%s", strings.Join(args, " "), want, out)
			}
		}
	}
}

// TestChartNewRunsItsOwnCheckpointSync: jig chart new reaches the store through
// Store.Claim alone, which never runs the checkpoint hook, so the verb runs the
// sync itself once the claim has landed: the chart has its issue recorded in
// the tracker tree before any ticket graduates under it. The real binary would
// reach api.github.com, so this chain runs in process with the stub client.
func TestChartNewRunsItsOwnCheckpointSync(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	e.mirrorClient = &stubGitHub{}

	var buf bytes.Buffer
	code := run(e, []string{"chart", "new", "coupons", "--title", "Coupons at checkout", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig chart new: exit %d\n%s", code, buf.String())
	}

	data, ok := treeRecordOfChart(t, clone, "coupons")
	if !ok {
		t.Fatalf("the chart has no record in the tracker tree, want jig chart new's own checkpoint sync to have made its issue\n%s", buf.String())
	}
	if !strings.Contains(data, "issue: 1") {
		t.Fatalf("the chart's record = %s, want issue: 1 from the checkpoint sync's CreateIssue", data)
	}
}

// treeRecordOfChart is treeRecordOf for a chart: the text of the record the
// tracker tree under root holds for chart (record.yaml naming "chart: <name>").
func treeRecordOfChart(t *testing.T, root, chart string) (string, bool) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "trackers", "github", "*", "*", "*", "record.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\nchart: "+chart+"\n") {
			return string(data), true
		}
	}
	return "", false
}

// runChartCmd runs the CLI in process with env e and returns its exit code and
// stdout, in the order the chart tests read them.
func runChartCmd(t *testing.T, e env, args ...string) (int, string) {
	t.Helper()
	out, code := runMain(t, e, "", args...)
	return code, out
}
