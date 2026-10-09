package mirror

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// newDemoStore builds the store the golden (brief.md#What an issue shows)
// is drawn from: project.yaml declares one product repo (example/demo) and
// its issues live in a different repo (example/tracking), so bare "#N" in
// rendered text is qualified to "example/demo#N".
func newDemoStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	projectYAML := `schema_version: 2
name: demo
keys:
  DEMO: everything in demo
trackers:
  - github:
      repo: example/tracking
      project: https://github.com/users/example/projects/7
repos:
  - remote: git@github.com:example/demo.git
platform: platform/
`
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(projectYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// wantDemo7Body is the golden issue body for ticket DEMO-7
// (brief.md#What an issue shows), its issues in a repo other than the
// product one, so "#5" and "#31" (its own text) read bare and qualified
// respectively.
const wantDemo7Body = "A ticket in the store becomes an issue, and the issue follows the ticket from then on (see example/demo#5).\n\n" +
	"- Each change to the ticket's record reaches the issue at the next checkpoint.\n" +
	"- Edits on GitHub are drift.\n\n" +
	"    jig trackers sync --dry-run\n" +
	"    jig status\n\n" +
	"**Waits for:** DEMO-2 (#12, merged), DEMO-4 (stacked)\n\n" +
	"## Slices\n\n" +
	"- [x] `tracer` Create the issue for one ticket at a checkpoint.\n" +
	"- [ ] `board` Put the issue on the project with its Status.\n" +
	"- [ ] `drift` Report every jig-owned field edited on GitHub as drift and restore the store's value, keeping the record of what the last sync wrote for title, body, state, parent, blocked-by links, Status and Store… *(stalled)*\n\n" +
	"## Pull requests\n\n" +
	"- example/demo#31 (merged)\n\n" +
	"<details><summary>Brief</summary>\n\n" +
	"# DEMO-7: Mirror\n\n" +
	"The store is the source of truth, and the issue only mirrors it (example/demo#5).\n\n" +
	"</details>\n\n" +
	"---\n<sub>Mirrored from the jig ticket store (`DEMO-7`). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n" +
	"<!-- jig:DEMO-7 -->"

func setUpDemo7(t *testing.T, st *store.Store) {
	t.Helper()

	demo7Body := "A ticket in the store becomes an issue, and the issue follows the\n" +
		"ticket from then on (see #5).\n\n" +
		"- Each change to the ticket's record reaches the\n" +
		"  issue at the next checkpoint.\n" +
		"- Edits on GitHub are drift.\n\n" +
		"    jig trackers sync --dry-run\n" +
		"    jig status\n"
	if err := st.CreateTicketRecord("DEMO-7", store.Ticket{
		Title: "Mirror: a ticket becomes an issue",
		Body:  demo7Body,
		BlockedBy: []store.TicketBlockedBy{
			{Ticket: "DEMO-2", Kind: "merged"},
			{Ticket: "DEMO-4", Kind: "stacked"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("DEMO-2", store.Ticket{Title: "merged ticket"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("DEMO-4", store.Ticket{Title: "stacked ticket"}); err != nil {
		t.Fatal(err)
	}
	if err := writeRecord(ticketRecordAbs(st, "DEMO-2"), githubRecord{Repo: "example/tracking", Issue: 12, NodeID: "NODE_12"}); err != nil {
		t.Fatal(err)
	}

	if err := st.AppendSlices("DEMO-7", []store.Slice{
		{ID: "tracer", Goal: "Create the issue for one ticket at a checkpoint."},
		{ID: "board", Goal: "Put the issue on the project with its Status."},
		{ID: "drift", Goal: "Report every jig-owned field edited on GitHub as drift and restore the store's value, keeping the record of what the last sync wrote for title, body, state, parent, blocked-by links, Status and Store ID, across every ticket and chart."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState("DEMO-7", "tracer", store.SliceState{State: "green"}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState("DEMO-7", "drift", store.SliceState{State: "stalled", Reason: "stall"}); err != nil {
		t.Fatal(err)
	}

	briefMD := "# DEMO-7: Mirror\n\n" +
		"The store is the source of truth, and the issue\n" +
		"only mirrors it (#5).\n"
	if err := os.WriteFile(filepath.Join(st.TicketDir("DEMO-7"), "brief.md"), []byte(briefMD), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ticketRecordAbs(st *store.Store, ticket string) string {
	_, abs := ticketRecordPath(st, ticket)
	return abs
}

// TestRenderTicketBodyMatchesTheGolden is the renderer seam
// (brief.md#Seams: "The renderer: a ticket's and a chart's title and body
// from store state, against the golden in 'What an issue shows'."): ticket
// DEMO-7's body, exactly as brief.md's golden spells it out.
func TestRenderTicketBodyMatchesTheGolden(t *testing.T) {
	st := newDemoStore(t)
	setUpDemo7(t, st)
	cfg := loadCfg(t, st)

	title, _, err := ResolveTicketTitleBody(st, "DEMO-7")
	if err != nil {
		t.Fatalf("ResolveTicketTitleBody: %v", err)
	}
	if title != "Mirror: a ticket becomes an issue" {
		t.Fatalf("title = %q, want the ticket.yaml title", title)
	}

	prs := []PRRef{{Owner: "example", Repo: "demo", Number: 31, State: "MERGED"}}
	body, err := RenderTicketBody(st, cfg, "DEMO-7", prs)
	if err != nil {
		t.Fatalf("RenderTicketBody: %v", err)
	}
	if body != wantDemo7Body {
		t.Fatalf("RenderTicketBody =\n%q\nwant\n%q", body, wantDemo7Body)
	}
}

// TestRenderTicketBodySameRepoStaysBare checks the "except the issues live
// in the product repo itself" case the brief calls out explicitly: with no
// repo to qualify against, #5 (and example/demo#31, already qualified)
// stay exactly as written.
func TestRenderTicketBodySameRepoStaysBare(t *testing.T) {
	root := t.TempDir()
	projectYAML := `schema_version: 2
name: demo
keys:
  DEMO: everything in demo
trackers:
  - github:
      repo: example/demo
      project: https://github.com/users/example/projects/7
repos:
  - remote: git@github.com:example/demo.git
platform: platform/
`
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte(projectYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	setUpDemo7(t, st)
	cfg := loadCfg(t, st)

	prs := []PRRef{{Owner: "example", Repo: "demo", Number: 31, State: "MERGED"}}
	body, err := RenderTicketBody(st, cfg, "DEMO-7", prs)
	if err != nil {
		t.Fatalf("RenderTicketBody: %v", err)
	}
	wantBare := "A ticket in the store becomes an issue, and the issue follows the ticket from then on (see #5)."
	if body[:len(wantBare)] != wantBare {
		t.Fatalf("RenderTicketBody's first line = %q, want %q (no qualifying, same repo)", body[:len(wantBare)], wantBare)
	}
}

// TestRenderChartBodyMatchesTheGolden is the chart half of the same seam:
// chart "demo"'s body, exactly as brief.md's golden spells it out.
func TestRenderChartBodyMatchesTheGolden(t *testing.T) {
	st := newDemoStore(t)
	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	mapMD := "# Chart: demo\n\n" +
		"Labels are chart-local, and\n" +
		"the Tickets table records each id (#5).\n"
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "map.md"), []byte(mapMD), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadCfg(t, st)

	title, err := chartTitle(st, "demo")
	if err != nil {
		t.Fatalf("chartTitle: %v", err)
	}
	if title != "Chart: demo" {
		t.Fatalf("chartTitle = %q, want %q", title, "Chart: demo")
	}

	body, err := RenderChartBody(st, cfg, "demo")
	if err != nil {
		t.Fatalf("RenderChartBody: %v", err)
	}
	want := "Labels are chart-local, and the Tickets table records each id (example/demo#5).\n\n" +
		"---\n<sub>Mirrored from the jig ticket store (`charts/demo/`). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n" +
		"<!-- jig:chart:demo -->"
	if body != want {
		t.Fatalf("RenderChartBody =\n%q\nwant\n%q", body, want)
	}
}

// TestRenderTicketBodyJoinsIndentedContinuationsOfListItems pins
// brief.md#What an issue shows's unwrapping rule for the case the bridge
// gets right and a naive "4+ spaces is always code" reading gets wrong: a
// nested bullet's and a numbered step's hard-wrapped continuation, each
// indented four or more spaces, is not an indented code block (it follows
// no blank line, and the line before it is a list item), so it joins onto
// the item before it like any other continuation.
func TestRenderTicketBodyJoinsIndentedContinuationsOfListItems(t *testing.T) {
	st := newDemoStore(t)
	body := "- the github adapter returns a repo and a\n" +
		"    number;\n" +
		"- a plain item.\n\n" +
		"2. read GitHub: the issue repo's issues with their parent, blocked-by\n" +
		"     links and sub-issues.\n" +
		"3. create every missing issue.\n"
	if err := st.CreateTicketRecord("DEMO-9", store.Ticket{
		Title: "List continuations",
		Body:  body,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := loadCfg(t, st)

	got, err := RenderTicketBody(st, cfg, "DEMO-9", nil)
	if err != nil {
		t.Fatalf("RenderTicketBody: %v", err)
	}
	want := "- the github adapter returns a repo and a number;\n" +
		"- a plain item.\n\n" +
		"2. read GitHub: the issue repo's issues with their parent, blocked-by links and sub-issues.\n" +
		"3. create every missing issue.\n\n" +
		"---\n<sub>Mirrored from the jig ticket store (`DEMO-9`). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n" +
		"<!-- jig:DEMO-9 -->"
	if got != want {
		t.Fatalf("RenderTicketBody =\n%q\nwant\n%q", got, want)
	}
}

// TestRenderTicketBodyKeepsBridgeRuleCorners pins the cases where the
// bridge's own unwrapping rule (brief.md#What an issue shows) and a naive
// reading of it disagree, each found by diffing a pre-publish dry run
// against the live store: (1) a nested bullet under a numbered item, at
// four or more spaces of indent, still starts its own line rather than
// joining onto the numbered line; (2) a line whose text starts with "<", at
// any indent, is an HTML line that keeps its own line; (3) and (4) a list
// item indented four or more spaces is itself a block start (kept on its
// own line), but its own hard-wrapped continuation - not itself a list item
// - joins onto it like any other lazy continuation, with no indented-code
// rule nested inside a list item: the bridge joins both T-22's own brief's
// "Each change ... reaches the" / "issue at the next checkpoint." and a
// chart's "A6 builds on L1's ..." / "retires the bridge sync ...", which
// the renderer's prior, stricter rule wrongly kept apart.
func TestRenderTicketBodyKeepsBridgeRuleCorners(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "nested bullet under a numbered item",
			body: "11. **A6 builds the chart.**:\n" +
				"    - A6 builds the chart that wires every ticket.\n",
		},
		{
			name: "an indented HTML line",
			body: "An id can end in a glob, meaning \"everything in\n" +
				"    <name>\";\n",
		},
		{
			name: "a list item's own continuation joins, T-22's brief",
			body: "A ticket in the store becomes an issue.\n\n" +
				"    - Each change to the ticket's record reaches the\n" +
				"      issue at the next checkpoint.\n" +
				"    - Edits on GitHub are drift.\n",
			want: "A ticket in the store becomes an issue.\n\n" +
				"    - Each change to the ticket's record reaches the issue at the next checkpoint.\n" +
				"    - Edits on GitHub are drift.\n",
		},
		{
			name: "a list item's own continuation joins, a chart's map",
			body: "A6 is the mirror's own chart.\n\n" +
				"    - A6 builds on L1's claimed ids and project fields,\n" +
				"      retires the bridge sync that moved them before.\n",
			want: "A6 is the mirror's own chart.\n\n" +
				"    - A6 builds on L1's claimed ids and project fields, retires the bridge sync that moved them before.\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newDemoStore(t)
			if err := st.CreateTicketRecord("DEMO-9", store.Ticket{
				Title: "Bridge rule corners",
				Body:  c.body,
			}); err != nil {
				t.Fatal(err)
			}
			cfg := loadCfg(t, st)

			got, err := RenderTicketBody(st, cfg, "DEMO-9", nil)
			if err != nil {
				t.Fatalf("RenderTicketBody: %v", err)
			}
			wantUnwrapped := c.want
			if wantUnwrapped == "" {
				wantUnwrapped = c.body
			}
			wantUnwrapped = strings.TrimRight(wantUnwrapped, "\n")
			want := wantUnwrapped + "\n\n" +
				"---\n<sub>Mirrored from the jig ticket store (`DEMO-9`). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n" +
				"<!-- jig:DEMO-9 -->"
			if got != want {
				t.Fatalf("RenderTicketBody =\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// TestResolveTicketTitleBodyFallsBackToChartEntry checks fallback rank 2
// (brief.md#What an issue shows): a ticket with no ticket.yaml title takes
// its title and body from its entry in a chart's tickets.yaml.
func TestResolveTicketTitleBodyFallsBackToChartEntry(t *testing.T) {
	st := newDemoStore(t)
	if err := st.CreateTicketRecord("DEMO-9", store.Ticket{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Root, "charts", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := "tickets:\n  - id: DEMO-9\n    title: From the chart\n    body: chart body\n"
	if err := os.WriteFile(filepath.Join(st.Root, "charts", "demo", "tickets.yaml"), []byte(chartYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	title, body, err := ResolveTicketTitleBody(st, "DEMO-9")
	if err != nil {
		t.Fatalf("ResolveTicketTitleBody: %v", err)
	}
	if title != "From the chart" || body != "chart body" {
		t.Fatalf("ResolveTicketTitleBody = (%q, %q), want the chart entry's own title and body", title, body)
	}
}

// TestResolveTicketTitleBodyFallsBackToID checks the last fallback rank: a
// ticket with none of the above is titled by its own id, with no body.
func TestResolveTicketTitleBodyFallsBackToID(t *testing.T) {
	st := newDemoStore(t)
	if err := st.CreateTicketRecord("DEMO-9", store.Ticket{}); err != nil {
		t.Fatal(err)
	}

	title, body, err := ResolveTicketTitleBody(st, "DEMO-9")
	if err != nil {
		t.Fatalf("ResolveTicketTitleBody: %v", err)
	}
	if title != "DEMO-9" || body != "" {
		t.Fatalf("ResolveTicketTitleBody = (%q, %q), want (%q, \"\")", title, body, "DEMO-9")
	}
}
