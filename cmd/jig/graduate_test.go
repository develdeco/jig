package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// setupGraduateStore initializes a standalone store in a fresh repo and
// returns a jig() runner plus the store's root directory.
func setupGraduateStore(t *testing.T) (jig func(args ...string) (int, string), storeRoot string) {
	t.Helper()
	t.Setenv("JIG_HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	t.Chdir(repo)

	jig = func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := Main(args, &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := jig("init", "--standalone"); code != 0 {
		t.Fatalf("jig init --standalone: exit %d\n%s", code, out)
	}
	cfgs, err := filepath.Glob(filepath.Join(filepath.Dir(repo), "*", "project.yaml"))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("find the standalone store's project.yaml: %v, %v", cfgs, err)
	}
	return jig, filepath.Dir(cfgs[0])
}

// writeChart writes charts/<name>/tickets.yaml under storeRoot.
func writeChart(t *testing.T, storeRoot, name, content string) {
	t.Helper()
	dir := filepath.Join(storeRoot, "charts", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tickets.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGraduateResolvesSameChartBlockedBy covers the common case: an entry
// blocked_by an earlier entry in the same chart, with an explicit and a
// defaulted kind, resolving to the sibling's newly minted id.
func TestGraduateResolvesSameChartBlockedBy(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      - ref: "#1"
  - title: "Slice C"
    blocked_by:
      - ref: "#1"
      - ref: "#2"
        kind: stacked
`)

	code, out := jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart: exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].ID == "" || entries[1].ID == "" || entries[2].ID == "" {
		t.Fatalf("entries after graduate = %+v, want all three ids written", entries)
	}
	idA, idB, idC := entries[0].ID, entries[1].ID, entries[2].ID

	if _, err := os.Stat(filepath.Join(st.TicketDir(idA), "ticket.yaml")); !os.IsNotExist(err) {
		t.Fatalf("ticket.yaml for %s: want absent (no blockers), stat err = %v", idA, err)
	}

	depsB, err := st.ReadTicketDeps(idB)
	if err != nil {
		t.Fatal(err)
	}
	wantB := []store.TicketBlockedBy{{Ticket: idA, Kind: "merged"}}
	if len(depsB) != 1 || depsB[0] != wantB[0] {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", idB, depsB, wantB)
	}

	depsC, err := st.ReadTicketDeps(idC)
	if err != nil {
		t.Fatal(err)
	}
	wantC := []store.TicketBlockedBy{{Ticket: idA, Kind: "merged"}, {Ticket: idB, Kind: "stacked"}}
	if len(depsC) != 2 || depsC[0] != wantC[0] || depsC[1] != wantC[1] {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", idC, depsC, wantC)
	}
}

// TestGraduateCrossChartRef covers a ref into another chart that has already
// graduated, and the self-named-chart spelling of an own-chart ref.
func TestGraduateCrossChartRef(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "parent", `tickets:
  - title: "Parent slice"
`)
	if code, out := jig("graduate", "parent"); code != 0 {
		t.Fatalf("jig graduate parent: exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	parentEntries, err := st.ReadChart("parent")
	if err != nil {
		t.Fatal(err)
	}
	parentID := parentEntries[0].ID
	if parentID == "" {
		t.Fatalf("parent chart entry has no id: %+v", parentEntries)
	}

	writeChart(t, storeRoot, "child", `tickets:
  - title: "Child slice"
    blocked_by:
      - ref: "parent#1"
        kind: stacked
  - title: "Sibling"
    blocked_by:
      - ref: "child#1"
`)
	code, out := jig("graduate", "child")
	if code != 0 {
		t.Fatalf("jig graduate child: exit %d\n%s", code, out)
	}

	childEntries, err := st.ReadChart("child")
	if err != nil {
		t.Fatal(err)
	}
	childID := childEntries[0].ID
	siblingID := childEntries[1].ID
	if childID == "" || siblingID == "" {
		t.Fatalf("child chart entries after graduate = %+v", childEntries)
	}

	deps, err := st.ReadTicketDeps(childID)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.TicketBlockedBy{{Ticket: parentID, Kind: "stacked"}}
	if len(deps) != 1 || deps[0] != want[0] {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", childID, deps, want)
	}

	siblingDeps, err := st.ReadTicketDeps(siblingID)
	if err != nil {
		t.Fatal(err)
	}
	wantSibling := []store.TicketBlockedBy{{Ticket: childID, Kind: "merged"}}
	if len(siblingDeps) != 1 || siblingDeps[0] != wantSibling[0] {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", siblingID, siblingDeps, wantSibling)
	}
}

// TestSameChartFile covers sameChartFile against real files on disk: an
// exact match always counts, a differently-spelled ref counts only when it
// resolves to the same charts/<name>/tickets.yaml file this filesystem
// actually has, a ref to an unrelated chart never counts, and a ref whose
// file does not exist reports false rather than erroring (so the caller
// falls through to the cross-chart branch's own "no tickets.yaml" error).
func TestSameChartFile(t *testing.T) {
	storeRoot := t.TempDir()
	writeChart(t, storeRoot, "mychart", "tickets: []\n")
	writeChart(t, storeRoot, "other", "tickets: []\n")
	st := &store.Store{Root: storeRoot}

	if !sameChartFile(st, "mychart", "mychart") {
		t.Error(`sameChartFile(st, "mychart", "mychart") = false, want true (exact match)`)
	}
	if sameChartFile(st, "other", "mychart") {
		t.Error(`sameChartFile(st, "other", "mychart") = true, want false (different file)`)
	}
	if sameChartFile(st, "nosuchchart", "mychart") {
		t.Error(`sameChartFile(st, "nosuchchart", "mychart") = true, want false (ref's file does not exist)`)
	}

	selfInfo, err := os.Stat(st.ChartFile("mychart"))
	if err != nil {
		t.Fatal(err)
	}
	if variantInfo, err := os.Stat(st.ChartFile("MyChart")); err == nil {
		want := os.SameFile(selfInfo, variantInfo)
		if got := sameChartFile(st, "MyChart", "mychart"); got != want {
			t.Errorf("sameChartFile(st, %q, %q) = %v, want %v (matching this filesystem's own case folding)", "MyChart", "mychart", got, want)
		}
	}
}

// TestGraduateCaseVariantSelfRef covers a blocked_by ref that names this
// chart by a differently-spelled alias: on a filesystem where that spelling
// opens the same tickets.yaml file, it must resolve like a same-chart "#k"
// ref rather than fall through to the cross-chart branch and demand an id
// this run is itself about to mint. On a filesystem where it does not,
// "CaseChart" is a genuinely different, nonexistent chart, and the run must
// refuse with that chart's own missing-tickets.yaml error. This runs on
// every OS: the test discovers which case applies by stat-ing both
// spellings itself, rather than assuming it from runtime.GOOS.
func TestGraduateCaseVariantSelfRef(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "casechart", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      - ref: "CaseChart#1"
`)

	lowerInfo, err := os.Stat(filepath.Join(storeRoot, "charts", "casechart", "tickets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	upperInfo, upperErr := os.Stat(filepath.Join(storeRoot, "charts", "CaseChart", "tickets.yaml"))
	caseInsensitive := upperErr == nil && os.SameFile(lowerInfo, upperInfo)

	code, out := jig("graduate", "casechart")

	if !caseInsensitive {
		if code == 0 {
			t.Fatalf("jig graduate casechart on a case-sensitive filesystem: want failure (chart %q does not exist), got exit 0\n%s", "CaseChart", out)
		}
		if !strings.Contains(out, "which has no charts/CaseChart/tickets.yaml") {
			t.Fatalf("jig graduate casechart on a case-sensitive filesystem: exit %d\n%s\nwant an error naming chart %q as missing", code, out, "CaseChart")
		}
		return
	}

	if code != 0 {
		t.Fatalf("jig graduate casechart: exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("casechart")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID == "" || entries[1].ID == "" {
		t.Fatalf("entries after graduate = %+v, want both ids written", entries)
	}
	idA, idB := entries[0].ID, entries[1].ID

	depsB, err := st.ReadTicketDeps(idB)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.TicketBlockedBy{{Ticket: idA, Kind: "merged"}}
	if len(depsB) != 1 || depsB[0] != want[0] {
		t.Fatalf("ticket.yaml for %s = %+v, want %+v", idB, depsB, want)
	}
}

// TestGraduateRefusesBadRefs covers every ref failure the run must refuse
// before creating anything, leaving no ticket folders behind.
func TestGraduateRefusesBadRefs(t *testing.T) {
	cases := []struct {
		name  string
		chart string
		want  string
	}{
		{
			name: "out of range",
			chart: `tickets:
  - title: "A"
    blocked_by:
      - ref: "#5"
`,
			want: "is out of range",
		},
		{
			name: "self reference",
			chart: `tickets:
  - title: "A"
    blocked_by:
      - ref: "#1"
`,
			want: "names its own entry",
		},
		{
			name: "forward reference among pending entries",
			chart: `tickets:
  - title: "A"
    blocked_by:
      - ref: "#2"
  - title: "B"
`,
			want: "must point at an earlier one",
		},
		{
			name: "duplicate ref",
			chart: `tickets:
  - title: "A"
  - title: "B"
    blocked_by:
      - ref: "#1"
      - ref: "#1"
`,
			want: "already listed",
		},
		{
			name: "bad kind",
			chart: `tickets:
  - title: "A"
  - title: "B"
    blocked_by:
      - ref: "#1"
        kind: bogus
`,
			want: "must be \"merged\" or \"stacked\"",
		},
		{
			name: "unknown chart",
			chart: `tickets:
  - title: "A"
    blocked_by:
      - ref: "no-such-chart#1"
`,
			want: "no charts/no-such-chart/tickets.yaml",
		},
		{
			name: "unknown ticket id",
			chart: `tickets:
  - title: "A"
    blocked_by:
      - ref: "T-999"
`,
			want: "not found",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jig, storeRoot := setupGraduateStore(t)
			writeChart(t, storeRoot, "mychart", c.chart)

			code, out := jig("graduate", "mychart")
			if code == 0 {
				t.Fatalf("jig graduate mychart: exit 0, want a refusal:\n%s", out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("jig graduate mychart: output missing %q:\n%s", c.want, out)
			}

			st, err := store.Open(storeRoot)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := st.ReadChart("mychart")
			if err != nil {
				t.Fatal(err)
			}
			for i, e := range entries {
				if e.ID != "" {
					t.Fatalf("entry %d got an id %q on a refused run", i+1, e.ID)
				}
			}
			ents, err := os.ReadDir(storeRoot)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range ents {
				if e.IsDir() && strings.HasPrefix(e.Name(), "T-") {
					t.Fatalf("a ticket folder %q was left behind by a refused run", e.Name())
				}
			}
		})
	}
}

// TestGraduateRefusesCrossChartRefWithoutID covers a cross-chart ref naming
// an entry that has not been graduated yet.
func TestGraduateRefusesCrossChartRefWithoutID(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "parent", `tickets:
  - title: "Parent slice"
`)
	writeChart(t, storeRoot, "child", `tickets:
  - title: "Child slice"
    blocked_by:
      - ref: "parent#1"
`)

	code, out := jig("graduate", "child")
	if code == 0 {
		t.Fatalf("jig graduate child: exit 0, want a refusal:\n%s", out)
	}
	if !strings.Contains(out, "no id yet") {
		t.Fatalf("output missing \"no id yet\":\n%s", out)
	}
}

// TestGraduateAdvisoryOnDrift covers an existing entry whose ticket.yaml no
// longer matches the chart's resolved blockers: the run still creates the
// missing tickets and exits 0, but prints an advisory naming the file to
// edit, and never touches the existing ticket's ticket.yaml.
func TestGraduateAdvisoryOnDrift(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
`)
	if code, out := jig("graduate", "mychart"); code != 0 {
		t.Fatalf("jig graduate mychart (first run): exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	idA := entries[0].ID

	// Hand-edit the ticket's own ticket.yaml after graduation.
	if err := st.WriteTicketDeps(idA, []store.TicketBlockedBy{{Ticket: idA, Kind: "merged"}}); err != nil {
		t.Fatal(err)
	}

	// Add a new entry blocked on the existing one, but leave entry 1's
	// blocked_by empty in the chart, so it no longer matches ticket.yaml.
	writeChart(t, storeRoot, "mychart", `tickets:
  - id: `+idA+`
    title: "Slice A"
  - title: "Slice B"
    blocked_by:
      - ref: "`+idA+`"
`)

	code, out := jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart (second run): exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "ticket.yaml") || !strings.Contains(out, idA) {
		t.Fatalf("output missing an advisory naming %s/ticket.yaml:\n%s", idA, out)
	}

	// The existing entry's ticket.yaml must be untouched.
	deps, err := st.ReadTicketDeps(idA)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0].Ticket != idA {
		t.Fatalf("ticket.yaml for %s changed: %+v", idA, deps)
	}
}

// TestGraduateAdvisesRatherThanAbortsOnUnparseableTicketDeps covers an
// existing entry whose ticket.yaml fails to parse, on a run that also has a
// new entry to create: the drift check must not turn that into a hard
// return, since by the time it runs the new ticket already exists on disk
// with its id recorded in the chart, and aborting here would leave both
// uncommitted while telling the operator about neither. It must instead
// advise (naming the broken file) and let the run reach Push.
func TestGraduateAdvisesRatherThanAbortsOnUnparseableTicketDeps(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
`)
	if code, out := jig("graduate", "mychart"); code != 0 {
		t.Fatalf("jig graduate mychart (first run): exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	idA := entries[0].ID

	// Hand-break the existing entry's ticket.yaml so it no longer parses.
	depsPath := filepath.Join(st.TicketDir(idA), "ticket.yaml")
	if err := os.WriteFile(depsPath, []byte("blocked_by: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	// Add a new entry this run must still create.
	writeChart(t, storeRoot, "mychart", `tickets:
  - id: `+idA+`
    title: "Slice A"
  - title: "Slice B"
`)

	code, out := jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart (second run): exit %d, want 0 (advisory, not abort)\n%s", code, out)
	}
	// The advisory is one line naming both the entry and the broken file,
	// the same way TestWriteChartMergesBlockedByItemByItem pins per-line
	// attachment rather than a substring anywhere in the output: idA also
	// appears in the entries table on every successful run, so checking for
	// it alone would pass regardless of which entry or file the advisory
	// actually names.
	want := idA + "/ticket.yaml could not be read"
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "entry 1") && strings.Contains(line, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("output missing a line naming entry 1 and %q as unparseable:\n%s", want, out)
	}

	entries, err = st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].ID == "" {
		t.Fatalf("entries after run = %+v, want entry 2 to have a minted id", entries)
	}
	idB := entries[1].ID
	if _, err := os.Stat(st.TicketDir(idB)); err != nil {
		t.Fatalf("ticket folder for %s missing: %v", idB, err)
	}

	status, err := gitx.Run(storeRoot, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(status) != "" {
		t.Fatalf("store left dirty after a run that should have committed:\n%s", status)
	}
}

// TestGraduateFullyGraduatedStillAdvises covers a re-run with nothing left
// to create: it still compares existing entries against their ticket.yaml
// and advises on drift, without writing anything.
func TestGraduateFullyGraduatedStillAdvises(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
  - title: "Slice B"
    blocked_by:
      - ref: "#1"
`)
	if code, out := jig("graduate", "mychart"); code != 0 {
		t.Fatalf("jig graduate mychart (first run): exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	idA, idB := entries[0].ID, entries[1].ID

	// Drift: hand-clear B's blockers in its own ticket.yaml.
	if err := st.WriteTicketDeps(idB, nil); err != nil {
		t.Fatal(err)
	}

	code, out := jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart (re-run): exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "fully graduated") {
		t.Fatalf("output missing \"fully graduated\":\n%s", out)
	}
	if !strings.Contains(out, idB) || !strings.Contains(out, "ticket.yaml") {
		t.Fatalf("output missing an advisory naming %s/ticket.yaml:\n%s", idB, out)
	}

	// The fully-graduated branch must still print the results table, with a
	// row per entry naming its id, not just the KV status line and the
	// advisory.
	if !strings.Contains(out, "entries[2]{position,id,title,status}:") {
		t.Fatalf("output missing the entries table header:\n%s", out)
	}
	if !strings.Contains(out, idA) {
		t.Fatalf("output missing a table row for entry A (%s):\n%s", idA, out)
	}
	if !strings.Contains(out, "existing") {
		t.Fatalf("output missing an \"existing\" status in the table:\n%s", out)
	}
}

// TestGraduateMidRunFailureIsRecoverable covers the command-level invariant
// the per-mint write-back exists for: after a run fails partway through, a
// re-run creates exactly the tickets still missing, rather than minting a
// duplicate for an entry that already succeeded. The local adapter's Mint
// skips non-directories when it picks max+1 (internal/tracker/local.go),
// so a plain file named T-2 makes entry B's Mint fail once entry A has
// already taken T-1 - no fake adapter needed.
func TestGraduateMidRunFailureIsRecoverable(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	blocker := filepath.Join(storeRoot, "T-2")
	if err := os.WriteFile(blocker, []byte("not a ticket folder"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a failure partway through:\n%s", out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	idA := entries[0].ID
	if idA == "" {
		t.Fatalf("entry A has no id after the partial run; the write-back that records it before entry B is minted did not happen: %+v", entries)
	}
	if entries[1].ID != "" {
		t.Fatalf("entry B got an id %q despite its Mint failing", entries[1].ID)
	}

	// The failed run is the operator's only view of a store left half
	// graduated: it must name the ticket already created for entry A, not
	// just fail, and it must carry a Help line about re-running.
	if !strings.Contains(out, idA) {
		t.Fatalf("failed run's output does not name the ticket already created for entry A (%s):\n%s", idA, out)
	}
	if !strings.Contains(out, "help[1]:") {
		t.Fatalf("failed run's output has no Help block:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "re-run") {
		t.Fatalf("failed run's output has no Help line about re-running:\n%s", out)
	}

	// Clear the obstruction and re-run: only entry B should be created, and
	// entry A's id must be unchanged - a regression here mints a duplicate
	// ticket for entry A.
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}

	code, out = jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart (re-run): exit %d\n%s", code, out)
	}

	entries, err = st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].ID != idA {
		t.Fatalf("entry A's id changed on re-run: got %q, want %q (a duplicate ticket was minted)", entries[0].ID, idA)
	}
	idB := entries[1].ID
	if idB == "" || idB == idA {
		t.Fatalf("entry B did not get a fresh id on re-run: %+v", entries)
	}
	if !strings.Contains(out, idB) {
		t.Fatalf("output missing a row for the newly created entry B (%s):\n%s", idB, out)
	}
}

// TestGraduateFailureDistinguishesOrphanFromRecorded covers graduateFailure
// directly: entry 1's ticket reached onMinted (recorded[0] = true, safe to
// re-run over), but entry 2's ticket was minted (ids[1] set) before a
// generic, non-axi.Error failure struck - the tracker.Graduate return shape
// a MkdirAll or ticket.yaml write failure leaves, which never reached
// onMinted for entry 2. The result must not tell the operator to just
// re-run: that would mint a duplicate for entry 2 and orphan its ticket.
func TestGraduateFailureDistinguishesOrphanFromRecorded(t *testing.T) {
	err := graduateFailure(
		"mychart",
		[]int{0, 1},
		[]string{"T-1", "T-2"},
		[]bool{true, false},
		errors.New("tracker: graduate: write ticket.yaml for T-2: boom"),
	)

	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("graduateFailure returned %T, want *axi.Error", err)
	}
	if !strings.Contains(ae.Msg, "already created: entry 1 (T-1)") {
		t.Fatalf("Msg = %q, want it to still name entry 1 as already created", ae.Msg)
	}
	if !strings.Contains(ae.Msg, "T-2") || !strings.Contains(ae.Msg, "entry 2") {
		t.Fatalf("Msg = %q, want it to name ticket T-2 and entry 2 as minted but not recorded", ae.Msg)
	}
	if len(ae.Help) != 1 {
		t.Fatalf("Help = %+v, want exactly one line naming the fix for entry 2", ae.Help)
	}
	want := "Put `id: T-2` on entry 2 of charts/mychart/tickets.yaml (or delete that ticket folder), then re-run"
	if ae.Help[0] != want {
		t.Fatalf("Help[0] = %q, want %q", ae.Help[0], want)
	}
	if strings.Contains(strings.Join(ae.Help, "\n"), "it creates only the entries") {
		t.Fatalf("Help = %+v, must not give the blanket re-run advice: entry 2 is not safe to re-run over", ae.Help)
	}
}

// TestPushFailureNamesCreatedTicketsAndKeepsUnderlyingHelp covers
// pushFailure directly: given a STORE_CONFLICT *axi.Error (the shape
// Store.Push itself returns for a mid-rebase/mid-merge refusal or an
// aborted conflicting pull), the wrapped error must still carry Push's own
// Help (the specific `git rebase --abort`/`git status` guidance only Store
// can give), name every id already created and recorded, and add a Help
// line pointing at the commit to make by hand.
func TestPushFailureNamesCreatedTicketsAndKeepsUnderlyingHelp(t *testing.T) {
	underlying := &axi.Error{
		Msg:  "the store at /store has an unfinished rebase",
		Code: "STORE_CONFLICT",
		Help: []string{"Check the store's state there with `git status`, resolve it, then rerun."},
	}
	err := pushFailure("/store", "mychart", []string{"T-1", "T-2"}, `chart mychart: graduate T-1, T-2`, underlying)

	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("pushFailure returned %T, want *axi.Error", err)
	}
	if ae.Code != "STORE_CONFLICT" {
		t.Fatalf("Code = %q, want the underlying error's own STORE_CONFLICT preserved", ae.Code)
	}
	if !strings.Contains(ae.Msg, "T-1") || !strings.Contains(ae.Msg, "T-2") {
		t.Fatalf("Msg = %q, want it to name every id already created", ae.Msg)
	}
	if !strings.Contains(ae.Msg, "charts/mychart/tickets.yaml") {
		t.Fatalf("Msg = %q, want it to say the ids are recorded in tickets.yaml", ae.Msg)
	}
	if !strings.Contains(ae.Msg, underlying.Msg) {
		t.Fatalf("Msg = %q, want it to keep the underlying Push error's own message", ae.Msg)
	}
	if len(ae.Help) != 2 {
		t.Fatalf("Help = %+v, want the underlying Help kept plus one line naming the commit to make", ae.Help)
	}
	if ae.Help[0] != underlying.Help[0] {
		t.Fatalf("Help[0] = %q, want the underlying Push error's own Help kept first", ae.Help[0])
	}
	if !strings.Contains(ae.Help[1], "chart mychart: graduate T-1, T-2") {
		t.Fatalf("Help[1] = %q, want it to name the commit message to make by hand", ae.Help[1])
	}
	if !strings.Contains(strings.ToLower(ae.Help[1]), "re-run") {
		t.Fatalf("Help[1] = %q, want it to also mention re-running once the store's git state is resolved", ae.Help[1])
	}
}

// TestPushFailureOnRawError covers pushFailure given a plain (non-axi.Error)
// error - the shape a push that cannot even be retried with pull --rebase
// (an unreachable or misconfigured remote) leaves. There is no underlying
// Help to preserve. createdIDs is non-empty, the only shape the CLI's one
// call site ever passes: it is reached only after at least one mint has
// already appended to it.
func TestPushFailureOnRawError(t *testing.T) {
	raw := errors.New("git push origin main: fatal: repository not found: exit status 128")
	err := pushFailure("/store", "mychart", []string{"T-1"}, "chart mychart: graduate T-1", raw)

	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("pushFailure returned %T, want *axi.Error", err)
	}
	if ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("Code = %q, want VALIDATION_ERROR for a plain error with no code of its own", ae.Code)
	}
	if !strings.Contains(ae.Msg, raw.Error()) {
		t.Fatalf("Msg = %q, want it to keep the raw error's own message", ae.Msg)
	}
	if !strings.Contains(ae.Msg, "T-1") || !strings.Contains(ae.Msg, "already exists") {
		t.Fatalf("Msg = %q, want it to name the already-created ticket", ae.Msg)
	}
	if len(ae.Help) != 1 {
		t.Fatalf("Help = %+v, want exactly one line naming the commit to make", ae.Help)
	}
	if !strings.Contains(ae.Help[0], "chart mychart: graduate T-1") {
		t.Fatalf("Help[0] = %q, want it to name the commit message to make by hand", ae.Help[0])
	}
}

// TestGraduateRefusesBadChartName covers every chart name validateChartName
// rejects: ".", "..", and a name containing a path separator - the same
// guard that keeps a blocked_by ref's chart name (e.g. "../x#1") from
// escaping charts/.
func TestGraduateRefusesBadChartName(t *testing.T) {
	for _, name := range []string{".", "..", "a/b", `a\b`} {
		t.Run(name, func(t *testing.T) {
			jig, _ := setupGraduateStore(t)
			code, out := jig("graduate", name)
			if code == 0 {
				t.Fatalf("jig graduate %q: exit 0, want a refusal:\n%s", name, out)
			}
		})
	}
}

// TestGraduateRefusesMissingTicketsFile covers a chart name that is valid
// but names no charts/<chart>/tickets.yaml at all.
func TestGraduateRefusesMissingTicketsFile(t *testing.T) {
	jig, _ := setupGraduateStore(t)
	code, out := jig("graduate", "no-such-chart")
	if code == 0 {
		t.Fatalf("jig graduate no-such-chart: exit 0, want a refusal:\n%s", out)
	}
	if !strings.Contains(out, "charts/no-such-chart/tickets.yaml") {
		t.Fatalf("output missing charts/no-such-chart/tickets.yaml:\n%s", out)
	}
}

// TestGraduateRefusesEmptyChart covers a tickets.yaml that parses but names
// no entries at all: WriteChart would refuse the same file, so graduate must
// refuse it too rather than report a false "fully graduated".
func TestGraduateRefusesEmptyChart(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", "tickets: []\n")

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a refusal:\n%s", out)
	}
	if strings.Contains(out, "fully graduated") {
		t.Fatalf("output falsely reports \"fully graduated\" for an empty chart:\n%s", out)
	}
	if !strings.Contains(out, "no entries") {
		t.Fatalf("output missing \"no entries\":\n%s", out)
	}
}

// TestGraduateRefusesNoTitle covers an entry with no title.
func TestGraduateRefusesNoTitle(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - id: T-1
`)

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a refusal:\n%s", out)
	}
	if !strings.Contains(out, "no title") {
		t.Fatalf("output missing \"no title\":\n%s", out)
	}
}

// TestGraduateRefusesUnknownTicketFolder covers an entry whose id the store
// has no folder for.
func TestGraduateRefusesUnknownTicketFolder(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - id: T-999
    title: "Slice A"
`)

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a refusal:\n%s", out)
	}
	if !strings.Contains(out, "not found") {
		t.Fatalf("output missing \"not found\":\n%s", out)
	}
}

// TestGraduateRefusesDuplicateID covers two entries sharing the same id.
func TestGraduateRefusesDuplicateID(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	if err := os.MkdirAll(filepath.Join(storeRoot, "T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeChart(t, storeRoot, "mychart", `tickets:
  - id: T-1
    title: "Slice A"
  - id: T-1
    title: "Slice B"
`)

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a refusal:\n%s", out)
	}
	if !strings.Contains(out, "both have id") {
		t.Fatalf("output missing \"both have id\":\n%s", out)
	}
}

// TestGraduateRefusesCrossChartTraversal covers a blocked_by ref whose chart
// name would escape charts/ (e.g. "../../secrets#1"): the same rule the
// command's own chart-name argument is held to.
func TestGraduateRefusesCrossChartTraversal(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
    blocked_by:
      - ref: "../../secrets#1"
`)

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a refusal:\n%s", out)
	}
	if !strings.Contains(out, "invalid chart") {
		t.Fatalf("output missing \"invalid chart\":\n%s", out)
	}
}

// TestGraduateCommitAndTable covers the single Store.Push at the end of a
// run that both creates and leaves alone entries: its commit message names
// the chart and the created ids (not Go slice syntax, and no "jig: "
// prefix), the run leads with a "graduate" KV block naming the chart like
// the fully-graduated branch does, and the results table has one row per
// entry, each tagged "created" or "existing".
func TestGraduateCommitAndTable(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
`)
	if code, out := jig("graduate", "mychart"); code != 0 {
		t.Fatalf("jig graduate mychart (first run): exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	idA := entries[0].ID

	writeChart(t, storeRoot, "mychart", `tickets:
  - id: `+idA+`
    title: "Slice A"
  - title: "Slice B"
`)

	code, out := jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart (second run): exit %d\n%s", code, out)
	}

	entries, err = st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	idB := entries[1].ID
	if idB == "" {
		t.Fatalf("entry B has no id after graduate: %+v", entries)
	}

	subject, err := gitx.Run(storeRoot, "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(subject, "jig: ") {
		t.Fatalf("commit subject keeps the \"jig: \" prefix: %q", subject)
	}
	if strings.ContainsAny(subject, "[]") {
		t.Fatalf("commit subject uses Go slice syntax: %q", subject)
	}
	want := "chart mychart: graduate " + idB
	if subject != want {
		t.Fatalf("commit subject = %q, want %q", subject, want)
	}

	// The created branch must lead with a "graduate" KV block naming the
	// chart, like every other command's success output and like graduate's
	// own fully-graduated branch - not open straight into the table.
	if !strings.Contains(out, "graduate:") || !strings.Contains(out, "chart: mychart") {
		t.Fatalf("output missing a \"graduate\" KV block naming the chart:\n%s", out)
	}

	if !strings.Contains(out, "existing") {
		t.Fatalf("output missing an \"existing\" row for entry A:\n%s", out)
	}
	if !strings.Contains(out, "created") {
		t.Fatalf("output missing a \"created\" row for entry B:\n%s", out)
	}
	if !strings.Contains(out, idA) || !strings.Contains(out, idB) {
		t.Fatalf("output missing a row for both ids %s and %s:\n%s", idA, idB, out)
	}
}

// TestGraduatePushFailureNamesCreatedTicket reproduces a genuine Store.Push
// failure from inside `jig graduate`, after a ticket has already been
// minted and written back to charts/mychart/tickets.yaml: the store's fetch
// URL stays valid (so Sync's own pull --rebase, and Push's own retry pull,
// both succeed trivially) but its push URL is broken, so Push's push - and
// its retry after that pull - both fail, the same "a push that cannot be
// rebased" shape TestUnreachableRemoteIsNotReportedAsConflict documents for
// store.Push returning git's own error unwrapped. The command's own output
// must not just relay that raw error: it must name the ticket already
// created, say its id is already recorded, and carry a Help line.
func TestGraduatePushFailureNamesCreatedTicket(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)

	if _, err := gitx.Run(storeRoot, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}

	remote := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gitx.Run("", "init", "--bare", "-b", "main", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "remote", "add", "origin", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "push", "-u", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatal(err)
	}

	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
`)

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a Push failure:\n%s", out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	idA := entries[0].ID
	if idA == "" {
		t.Fatalf("entry A has no id despite its mint (and the local commit that follows) succeeding before Push failed: %+v", entries)
	}

	if !strings.Contains(out, idA) {
		t.Fatalf("failed run's output does not name the ticket already created for entry A (%s):\n%s", idA, out)
	}
	if !strings.Contains(out, "charts/mychart/tickets.yaml") {
		t.Fatalf("failed run's output does not say the id is already recorded in tickets.yaml:\n%s", out)
	}
	if !strings.Contains(out, "help[") {
		t.Fatalf("failed run's output has no Help block:\n%s", out)
	}
}

// TestGraduateFullyGraduatedLeavesStoreUntouched covers the fully-graduated
// branch on a store with no remote: a re-run over a chart where every entry
// already has an id must create, commit and push nothing itself, even when
// the store's working tree is dirty with state unrelated to this chart.
// Committing that state - as an earlier revision of this branch did via a
// Dirty-gated Push - would sweep up whatever else happens to be sitting in
// the store and do so under a message naming this chart even though the
// swept files have nothing to do with it. This setup has no remote, so
// Store.Sync (called before the chart is even read) is a no-op and HEAD
// truly does not move; see TestGraduateFullyGraduatedWithRemoteStillSyncsPendingState
// for the same branch when a remote makes Sync commit on entry.
func TestGraduateFullyGraduatedLeavesStoreUntouched(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
`)
	if code, out := jig("graduate", "mychart"); code != 0 {
		t.Fatalf("jig graduate mychart (first run): exit %d\n%s", code, out)
	}

	stray := filepath.Join(storeRoot, "otherchart")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "map.md"), []byte("half-written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	status, err := gitx.Run(storeRoot, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if status == "" {
		t.Fatal("test setup did not leave the store dirty")
	}

	headBefore, err := gitx.Run(storeRoot, "log", "-1", "--pretty=%H")
	if err != nil {
		t.Fatal(err)
	}

	code, out := jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart (re-run over a fully-graduated chart): exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "fully graduated") {
		t.Fatalf("output missing \"fully graduated\":\n%s", out)
	}

	headAfter, err := gitx.Run(storeRoot, "log", "-1", "--pretty=%H")
	if err != nil {
		t.Fatal(err)
	}
	if headAfter != headBefore {
		t.Fatalf("the fully-graduated run moved HEAD: before %s, after %s", headBefore, headAfter)
	}

	status, err = gitx.Run(storeRoot, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if status == "" {
		t.Fatal("the fully-graduated run committed the unrelated dirty state it found, instead of leaving the working tree untouched")
	}
}

// TestGraduateFullyGraduatedWithRemoteStillSyncsPendingState covers the
// fully-graduated branch on a store with a remote: Store.Sync runs before
// cmdGraduate even reads the chart (internal/store/store.go Sync) and, on
// any store with a remote, commits whatever it finds dirty under its own
// "jig: record uncommitted store state" message - that is Sync's job on
// every command, not something the fully-graduated branch does or could
// opt out of. What that branch must still not do is push, or make a second,
// graduate-specific commit of its own on top of Sync's.
func TestGraduateFullyGraduatedWithRemoteStillSyncsPendingState(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
`)
	if code, out := jig("graduate", "mychart"); code != 0 {
		t.Fatalf("jig graduate mychart (first run): exit %d\n%s", code, out)
	}

	remote := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gitx.Run("", "init", "--bare", "-b", "main", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "remote", "add", "origin", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "push", "-u", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	remoteHeadBefore, err := gitx.Run(remote, "log", "-1", "--pretty=%H")
	if err != nil {
		t.Fatal(err)
	}

	stray := filepath.Join(storeRoot, "otherchart")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "map.md"), []byte("half-written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	headBefore, err := gitx.Run(storeRoot, "log", "-1", "--pretty=%H")
	if err != nil {
		t.Fatal(err)
	}

	code, out := jig("graduate", "mychart")
	if code != 0 {
		t.Fatalf("jig graduate mychart (re-run over a fully-graduated chart, with a remote): exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "fully graduated") {
		t.Fatalf("output missing \"fully graduated\":\n%s", out)
	}

	headAfter, err := gitx.Run(storeRoot, "log", "-1", "--pretty=%H")
	if err != nil {
		t.Fatal(err)
	}
	if headAfter == headBefore {
		t.Fatal("HEAD did not move: with a remote, Store.Sync should have committed the dirty otherchart/map.md before graduate ever read the chart")
	}

	subject, err := gitx.Run(storeRoot, "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(subject) != "jig: record uncommitted store state" {
		t.Fatalf("commit subject = %q, want Sync's own recovery message, not one naming this chart's graduate run", subject)
	}

	status, err := gitx.Run(storeRoot, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("working tree still dirty after Sync's own commit:\n%s", status)
	}

	remoteHeadAfter, err := gitx.Run(remote, "log", "-1", "--pretty=%H")
	if err != nil {
		t.Fatal(err)
	}
	if remoteHeadAfter != remoteHeadBefore {
		t.Fatal("the fully-graduated run pushed something to the remote; it must create, commit and push nothing itself")
	}
}
