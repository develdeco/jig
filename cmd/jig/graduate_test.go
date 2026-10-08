package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	repo := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if _, err := gitx.Run(repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	e := testEnv(t.TempDir()).inDir(repo)

	jig = func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := run(e, args, &buf, strings.NewReader(""))
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

// rewriteTicketFormat replaces storeRoot's project.yaml ticket_format
// ("T-{n}", jig init --standalone's default) with format, so a test can mint
// through a ticket_format of its own choosing.
func rewriteTicketFormat(t *testing.T, storeRoot, format string) {
	t.Helper()
	path := filepath.Join(storeRoot, "project.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(data), "T-{n}", format, 1)
	if rewritten == string(data) {
		t.Fatalf("project.yaml has no T-{n} ticket_format to rewrite:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGraduateResolvesSameChartBlockedBy covers the common case: an entry
// blocked_by an earlier entry in the same chart, with an explicit and a
// defaulted kind, resolving to the sibling's newly minted id.
func TestGraduateResolvesSameChartBlockedBy(t *testing.T) {
	t.Parallel()
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

	gotA, err := st.ReadTicket(idA)
	if err != nil {
		t.Fatal(err)
	}
	if gotA.Title != "Slice A" || len(gotA.BlockedBy) != 0 {
		t.Fatalf("ticket.yaml for %s = %+v, want title %q and no blockers", idA, gotA, "Slice A")
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	replaceTicketDeps(t, st, idA, []store.TicketBlockedBy{{Ticket: idA, Kind: "merged"}})

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
	t.Parallel()
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

	// Entry B's claim committed its own folder and the chart's id for it;
	// idA's hand-broken ticket.yaml is exactly what the claim must leave
	// alone (store.Claim stages and commits only the paths it claims), so it
	// is still there, uncommitted, afterward - not swept into this run's
	// commit the way a broad `add -A` once would have.
	status, err := gitx.Run(storeRoot, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(status) != "M "+idA+"/ticket.yaml" {
		t.Fatalf("status = %q, want only idA's pre-existing hand-broken ticket.yaml left dirty", status)
	}
	subject, err := gitx.Run(storeRoot, "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	if subject != "chart mychart: graduate "+idB {
		t.Fatalf("commit subject = %q, want %q", subject, "chart mychart: graduate "+idB)
	}
}

// TestGraduateAdvisoryCarriesTheReadRefusalsNextSteps covers what follows the
// "could not be read" advisory for an existing entry: the next steps the
// refusal carries (upgrade jig for a newer schema, not the hand edit that
// would be wrong for it), verbatim, as `jig validate` gives them; and, for a
// read failure with none of its own, the fixed "edit it" step.
func TestGraduateAdvisoryCarriesTheReadRefusalsNextSteps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		place   func(t *testing.T, path string)
		refusal bool
	}{
		{name: "unknown key", refusal: true, place: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("schema_version: 1\nfrobnicate: yes\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "newer schema", refusal: true, place: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("schema_version: 2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a directory in the file's place", place: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			id := entries[0].ID
			tc.place(t, st.TicketFilePath(id))
			_, readErr := st.ReadTicketDeps(id)
			if readErr == nil {
				t.Fatal("test setup: ReadTicketDeps must fail")
			}
			var ae *axi.Error
			if errors.As(readErr, &ae) != tc.refusal {
				t.Fatalf("test setup: ReadTicketDeps = %v, want an *axi.Error only when the case expects a refusal (%v)", readErr, tc.refusal)
			}

			code, out := jig("graduate", "mychart")
			if code != 0 {
				t.Fatalf("jig graduate mychart (re-run): exit %d, want 0 (advisory, not abort)\n%s", code, out)
			}
			if !strings.Contains(out, id+"/ticket.yaml could not be read") {
				t.Fatalf("output lacks the could-not-be-read advisory for %s:\n%s", id, out)
			}
			fixedStep := "edit " + id + "/ticket.yaml to fix it"
			if !tc.refusal {
				if !strings.Contains(out, fixedStep) {
					t.Fatalf("output lacks the fixed step %q for a failure with no next steps of its own:\n%s", fixedStep, out)
				}
				return
			}
			for _, step := range ae.Help {
				if !strings.Contains(out, step) {
					t.Fatalf("output lacks the refusal's own next step %q:\n%s", step, out)
				}
			}
			if strings.Contains(out, fixedStep) {
				t.Fatalf("output gives the fixed step %q where the refusal has its own:\n%s", fixedStep, out)
			}
		})
	}
}

// TestGraduateFullyGraduatedStillAdvises covers a re-run with nothing left
// to create: it still compares existing entries against their ticket.yaml
// and advises on drift, without writing anything.
func TestGraduateFullyGraduatedStillAdvises(t *testing.T) {
	t.Parallel()
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
	replaceTicketDeps(t, st, idB, nil)

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
// duplicate for an entry that already succeeded. store.Mint skips
// non-directories when it picks max+1 (internal/store/mint.go), so a plain
// file named T-2 makes entry B's mint fail once entry A has already taken
// T-1 - no fake adapter needed.
func TestGraduateMidRunFailureIsRecoverable(t *testing.T) {
	t.Parallel()
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

// TestGraduateFailureNamesTicketsAlreadyClaimed covers graduateFailure
// directly: every ticket this run already claimed (createdIDs) is already
// committed, and - on a store with an origin - pushed, by its own
// store.Claim call, so a plain (non-axi.Error) failure for the next one gets
// a generic re-run Help line, while an error that already carries its own
// code and Help (store.Claim's own ID_NOT_CLAIMED, in particular) keeps
// both, gaining only the already-claimed context in its message. The error
// passed in is not changed in place.
func TestGraduateFailureNamesTicketsAlreadyClaimed(t *testing.T) {
	t.Parallel()
	raw := errors.New("boom")
	err := graduateFailure("mychart", []string{"T-1"}, raw)
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("graduateFailure returned %T, want *axi.Error", err)
	}
	if !strings.Contains(ae.Msg, "T-1") || !strings.Contains(ae.Msg, "already claimed") {
		t.Fatalf("Msg = %q, want it to name T-1 as already claimed", ae.Msg)
	}
	if !strings.Contains(ae.Msg, "boom") {
		t.Fatalf("Msg = %q, want it to keep the underlying error's own message", ae.Msg)
	}
	if ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("Code = %q, want VALIDATION_ERROR for a plain error with no code of its own", ae.Code)
	}
	if len(ae.Help) != 1 || !strings.Contains(strings.ToLower(ae.Help[0]), "re-run") {
		t.Fatalf("Help = %+v, want one line about re-running", ae.Help)
	}

	withNone := graduateFailure("mychart", nil, raw)
	var aeNone *axi.Error
	if !errors.As(withNone, &aeNone) {
		t.Fatalf("graduateFailure returned %T, want *axi.Error", withNone)
	}
	if strings.Contains(aeNone.Msg, "already claimed") {
		t.Fatalf("Msg = %q, want no \"already claimed\" clause with nothing created yet", aeNone.Msg)
	}

	claimFailed := &axi.Error{
		Msg:  "the id could not be claimed on the store's origin: boom",
		Code: "ID_NOT_CLAIMED",
		Help: []string{"Retry once the origin is reachable"},
	}
	wrapped := graduateFailure("mychart", []string{"T-1"}, claimFailed)
	var aeWrapped *axi.Error
	if !errors.As(wrapped, &aeWrapped) {
		t.Fatalf("graduateFailure returned %T, want *axi.Error", wrapped)
	}
	if aeWrapped.Code != "ID_NOT_CLAIMED" {
		t.Fatalf("Code = %q, want the underlying error's own ID_NOT_CLAIMED preserved", aeWrapped.Code)
	}
	if !reflect.DeepEqual(aeWrapped.Help, claimFailed.Help) {
		t.Fatalf("Help = %+v, want the underlying error's own Help kept as is", aeWrapped.Help)
	}
	if claimFailed.Msg != "the id could not be claimed on the store's origin: boom" {
		t.Fatalf("the underlying error was changed in place: %q", claimFailed.Msg)
	}
}

// TestGraduatePushFailureRemovesTheClaim reproduces a genuine push failure
// from inside `jig graduate`: the store's fetch URL stays valid (so Sync's
// own pull --rebase succeeds trivially) but its push URL is broken, the same
// "a push that cannot be rebased" shape TestUnreachableRemoteIsNotReportedAs
// Conflict documents for store.Push. Per the claim slice's contract, a push
// failure other than a rejection undoes the claim entirely: the chart entry
// gets no id, the ticket folder is removed, and the command refuses with
// ID_NOT_CLAIMED rather than leaving a ticket claimed only locally.
func TestGraduatePushFailureRemovesTheClaim(t *testing.T) {
	t.Parallel()
	jig, storeRoot := setupGraduateStore(t)

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

	// The chart entry itself is already committed and pushed, as it would be
	// in real use (whoever edits charts/mychart/tickets.yaml commits that
	// edit; graduate's own job is only to mint ids into it): otherwise
	// Store.Sync, which cmdGraduate calls before Claim ever runs, would
	// commit this uncommitted edit on its own, moving HEAD before the claim
	// this test means to exercise even starts.
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
`)
	if _, err := gitx.Run(storeRoot, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", "chart: add Slice A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "push", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	if _, err := gitx.Run(storeRoot, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatal(err)
	}

	headBefore, err := gitx.Run(storeRoot, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a push failure:\n%s", out)
	}
	if !strings.Contains(out, "ID_NOT_CLAIMED") {
		t.Fatalf("output missing ID_NOT_CLAIMED:\n%s", out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].ID != "" {
		t.Fatalf("entry A got an id %q despite its claim's push failing", entries[0].ID)
	}
	ents, err := os.ReadDir(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "T-") {
			t.Fatalf("a ticket folder %q was left behind by a failed claim", e.Name())
		}
	}
	headAfter, err := gitx.Run(storeRoot, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if headAfter != headBefore {
		t.Fatalf("HEAD moved from %s to %s: the failed claim's commit was not undone", headBefore, headAfter)
	}
}

// TestGraduateRefusesBadChartName covers every chart name validateChartName
// rejects: ".", "..", and a name containing a path separator - the same
// guard that keeps a blocked_by ref's chart name (e.g. "../x#1") from
// escaping charts/.
func TestGraduateRefusesBadChartName(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// TestGraduateRunsItsOwnCheckpointSync is the symmetric fix to r2-f8's
// TestTicketNewRunsItsOwnCheckpointSync: `jig graduate` reaches the store
// through Store.Claim alone too, so it must run the checkpoint sync itself,
// explicitly, once every entry's claim has landed - one sync covering every
// ticket this run minted. A ticket a chart graduates must have a GitHub
// issue recorded before the command returns, rather than waiting on some
// later, unrelated command's Push.
func TestGraduateRunsItsOwnCheckpointSync(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	writeChart(t, clone, "mychart", `tickets:
  - title: "Slice A"
`)

	e.mirrorClient = &stubGitHub{}

	var buf bytes.Buffer
	code := run(e, []string{"graduate", "mychart", "--store", clone}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig graduate mychart: exit %d\n%s", code, buf.String())
	}

	data, err := os.ReadFile(filepath.Join(clone, "T-1", "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read T-1's github.yaml: %v, want jig graduate's own checkpoint sync to have created it", err)
	}
	if !strings.Contains(string(data), "issue: 1") {
		t.Fatalf("github.yaml = %s, want issue: 1 from the checkpoint sync's CreateIssue", data)
	}
}

// TestGraduateSyncsAlreadyClaimedTicketsOnAPartialFailure covers r4-f2: a
// chart whose first entry claims cleanly but whose second entry's mint fails
// for a standing reason (here, a plain file named T-2 sitting where that id's
// ticket folder must go, so CreateTicketRecord's own MkdirAll refuses it)
// must still run the checkpoint sync for every ticket already claimed before
// the command returns its failure - the claim landed and was pushed, so a
// GitHub issue and board card are owed, not just a bare error.
func TestGraduateSyncsAlreadyClaimedTicketsOnAPartialFailure(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	writeChart(t, clone, "mychart", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)
	if err := os.WriteFile(filepath.Join(clone, "T-2"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	e.mirrorClient = &stubGitHub{}

	var buf bytes.Buffer
	code := run(e, []string{"graduate", "mychart", "--store", clone}, &buf, strings.NewReader(""))
	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a refusal on entry 2's mint:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "T-1") {
		t.Fatalf("jig graduate mychart output lacks T-1 as already claimed:\n%s", buf.String())
	}

	data, err := os.ReadFile(filepath.Join(clone, "T-1", "tracker", "github.yaml"))
	if err != nil {
		t.Fatalf("read T-1's github.yaml: %v, want the checkpoint sync to have run for it despite entry 2's failure", err)
	}
	if !strings.Contains(string(data), "issue: 1") {
		t.Fatalf("github.yaml = %s, want issue: 1 from the checkpoint sync's CreateIssue", data)
	}
}

// TestGraduateSkipsTheCheckpointSyncOnAWriteChartFailureAfterMint covers
// r5-f1: an entry whose st.Mint lands but whose chart write then fails
// leaves its ticket folder and ticket.yaml on disk, uncommitted, with the
// chart entry still missing its id - a half-minted ticket a full sync (not
// scoped to createdIDs) would still find on its scan of the store root and
// open a GitHub issue for, pushing a record no commit backs. jig graduate
// must skip the checkpoint sync entirely on this failure, even though entry
// A landed whole earlier in the same run, rather than risk that.
//
// The chart write failure is driven through env.writeChart (r6-f1):
// entry A's own WriteChart call passes through to the real st.WriteChart, and
// entry B's is forced to fail, the same write-after-mint shape on every OS -
// an OS-specific read-only chmod raced against store.AtomicWrite's own
// os.Rename only fails this way on Windows (POSIX rename needs no write
// permission on the target file itself, only its directory).
func TestGraduateSkipsTheCheckpointSyncOnAWriteChartFailureAfterMint(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	writeChart(t, clone, "mychart", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)

	writeCalls := 0
	e.writeChart = func(st *store.Store, chart string, entries []store.ChartEntry) error {
		writeCalls++
		if writeCalls == 2 {
			return errors.New("forced failure on entry B's chart write")
		}
		return st.WriteChart(chart, entries)
	}

	e.mirrorClient = &stubGitHub{}

	var buf bytes.Buffer
	code := run(e, []string{"graduate", "mychart", "--store", clone}, &buf, strings.NewReader(""))

	if code == 0 {
		t.Fatalf("jig graduate mychart: exit 0, want a refusal on entry B's chart write:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "failed to write chart") {
		t.Fatalf("output missing \"failed to write chart\":\n%s", buf.String())
	}

	if _, err := os.Stat(filepath.Join(clone, "T-2")); err != nil {
		t.Fatalf("T-2's half-minted ticket folder is gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, "T-2", "tracker", "github.yaml")); err == nil {
		t.Fatal("T-2 (half-minted, uncommitted) got a GitHub issue record despite the WriteChart failure")
	}
	if _, err := os.Stat(filepath.Join(clone, "T-1", "tracker", "github.yaml")); err == nil {
		t.Fatal("T-1 got synced despite the checkpoint hook being skipped entirely on this failure")
	}
}

// TestGraduateRecordsChartEntryBodyInTheRecord covers the other writer of a
// ticket's body: graduate carries each chart entry's own body into the
// ticket.yaml it mints, and an entry with no body mints a record with none.
func TestGraduateRecordsChartEntryBodyInTheRecord(t *testing.T) {
	t.Parallel()
	jig, storeRoot := setupGraduateStore(t)
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
    body: |
      Why this entry matters.
  - title: "Slice B"
`)
	if code, out := jig("graduate", "mychart"); code != 0 {
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

	gotA, err := st.ReadTicket(entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotA.Body != "Why this entry matters.\n" {
		t.Fatalf("entry A's ticket.yaml body = %q, want %q", gotA.Body, "Why this entry matters.\n")
	}

	gotB, err := st.ReadTicket(entries[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotB.Body != "" {
		t.Fatalf("entry B's ticket.yaml body = %q, want empty (its chart entry has none)", gotB.Body)
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
	t.Parallel()
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
	t.Parallel()
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

// TestClaimOneChartEntryRediscoversAnotherClonesGraduation reproduces two
// clones graduating a two-entry chart: a second clone of the store's origin
// claims the first entry and pushes it before this clone's own claim does,
// so this clone's push is rejected. Per Claim's contract, the rejection
// undoes this clone's commit, pulls the other clone's work in, and
// claimOneChartEntry must re-read the chart before minting again - this time
// finding entry 1 already has an id and continuing with entry 2, the one
// that still has none, minting and pushing a ticket for it alone rather than
// a second one for entry 1.
//
// It is not parallel: the two clones' claims differ only in their commit
// dates, which the commit code reads from the process environment
// (GIT_AUTHOR_DATE, GIT_COMMITTER_DATE), so it sets them with t.Setenv.
func TestClaimOneChartEntryRediscoversAnotherClonesGraduation(t *testing.T) {
	_, storeRoot := setupGraduateStore(t)
	e := testEnv(t.TempDir())

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

	// Both chart entries are already committed and pushed, as they would be
	// in real use (see TestGraduatePushFailureRemovesTheClaim's own comment on
	// why): both clones below must see the same tickets.yaml.
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "Slice A"
  - title: "Slice B"
`)
	if _, err := gitx.Run(storeRoot, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "-c", "user.name=jig", "-c", "user.email=jig@invalid", "commit", "-m", "chart: add Slice A and Slice B"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(storeRoot, "push", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	other := t.TempDir()
	if _, err := gitx.Run("", "clone", remote, other); err != nil {
		t.Fatal(err)
	}
	otherSt, err := store.Open(other)
	if err != nil {
		t.Fatal(err)
	}

	// The two clones' claims would otherwise commit the exact same tree atop
	// the exact same parent with the exact same author, committer and
	// message, so pinning distinct commit dates is what makes them two
	// distinct commits (and so what makes storeRoot's own push below a
	// genuine rejection) rather than, by the coincidence of two commits built
	// from identical inputs landing in the same wall-clock second, the same
	// commit object twice - a push that would then trivially "succeed" as a
	// no-op fast-forward onto the hash it already pushed.
	t.Setenv("GIT_AUTHOR_DATE", "2024-01-01T00:00:00+00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2024-01-01T00:00:00+00:00")

	// The other clone graduates Slice A and pushes it to the origin, standing
	// in for `jig graduate mychart` run from a second clone.
	otherID, _, otherDone, err := claimOneChartEntry(e, otherSt, "T-{n}", "mychart")
	if err != nil {
		t.Fatalf("claimOneChartEntry on the other clone: %v", err)
	}
	if otherDone || otherID == "" {
		t.Fatalf("claimOneChartEntry on the other clone = id %q done %v, want it to claim Slice A", otherID, otherDone)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_AUTHOR_DATE", "2024-01-02T00:00:00+00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2024-01-02T00:00:00+00:00")

	// storeRoot never synced with the other clone's push: its own claim below
	// mints against a stale chart, so its push is rejected by what the other
	// clone already landed on the origin.
	id, pos, done, err := claimOneChartEntry(e, st, "T-{n}", "mychart")
	if err != nil {
		t.Fatalf("claimOneChartEntry: %v", err)
	}
	if done || id == "" {
		t.Fatalf("claimOneChartEntry = id %q pos %d done %v, want it to continue past the other clone's entry and claim Slice B", id, pos, done)
	}
	if id == otherID {
		t.Fatalf("claimOneChartEntry minted %q, the same id the other clone already claimed for Slice A", id)
	}
	if pos != 1 {
		t.Fatalf("claimOneChartEntry pos = %d, want 1: the re-read must move past entry 1 (already claimed by the other clone) to entry 2", pos)
	}

	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != otherID || entries[1].ID != id {
		t.Fatalf("entries after the rejected claim's pull and retry = %+v, want entry 1's id to be the other clone's %q and entry 2's id to be the newly minted %q, with entry 1 unclobbered", entries, otherID, id)
	}

	remoteLog, err := gitx.Run("", "--git-dir", remote, "log", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(remoteLog, "graduate "+otherID); n != 1 {
		t.Fatalf("remote log has %d commits graduating %s, want exactly 1 (the rejected clone must not have minted and pushed a second ticket for Slice A)", n, otherID)
	}
	if n := strings.Count(remoteLog, "graduate "+id); n != 1 {
		t.Fatalf("remote log has %d commits graduating %s, want exactly 1 (the retried claim's own push for Slice B)", n, id)
	}

	ents, err := os.ReadDir(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	ticketDirs := 0
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "T-") {
			ticketDirs++
		}
	}
	if ticketDirs != 2 {
		t.Fatalf("storeRoot has %d ticket folders after the rejected claim's retry, want exactly 2 (the other clone's %s pulled in, plus this retry's own %s)", ticketDirs, otherID, id)
	}
}

// TestGraduateRefusesAMintedIDJigCannotUse covers an id ticket_format mints
// that jig cannot use (pool.CheckTicket: a reserved lease suffix here):
// graduate refuses it before writing anything under it - no folder or
// record - and the chart entry stays without an id.
func TestGraduateRefusesAMintedIDJigCannotUse(t *testing.T) {
	t.Parallel()
	jig, storeRoot := setupGraduateStore(t)
	rewriteTicketFormat(t, storeRoot, "T-{n}-gate")
	writeChart(t, storeRoot, "mychart", `tickets:
  - title: "New entry"
`)

	code, out := jig("graduate", "mychart")
	if code == 0 {
		t.Fatalf("jig graduate with ticket_format T-{n}-gate: exit 0, want a refusal:\n%s", out)
	}
	for _, want := range []string{"T-1-gate", "reserves"} {
		if !strings.Contains(out, want) {
			t.Fatalf("jig graduate with ticket_format T-{n}-gate: output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "delete that ticket folder") {
		t.Fatalf("jig graduate with ticket_format T-{n}-gate gave the orphan help:\n%s", out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.TicketDir("T-1-gate")); !os.IsNotExist(err) {
		t.Fatalf("the refused ticket T-1-gate left a store folder behind (stat err %v)", err)
	}
	entries, err := st.ReadChart("mychart")
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].ID != "" {
		t.Fatalf("the chart entry was recorded as %q, want no id", entries[0].ID)
	}
}
