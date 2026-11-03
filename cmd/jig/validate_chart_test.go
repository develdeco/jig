package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// storeDigest is every file under root with its content: two digests of a store
// that are equal mean nothing was written, changed or removed in between.
func storeDigest(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// helpLinesOf is the lines of the help block of a rendered output, in order.
func helpLinesOf(out string) []string {
	var lines []string
	in := false
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "help["):
			in = true
		case in && strings.HasPrefix(l, "  "):
			lines = append(lines, strings.TrimPrefix(l, "  "))
		default:
			in = false
		}
	}
	return lines
}

// TestValidateChartListsEveryProblem: `jig validate <chart>` lists every problem
// of the chart's entries in the order `jig graduate` meets them (titles, keys,
// duplicate ids and ticket folders, then refs), one line each, then the closing
// line; it exits 2 and writes no file.
func TestValidateChartListsEveryProblem(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", false)
	r.chart(t, "checkout", `tickets:
  - title: One
    blocked_by:
      - ref: "#99"
  - title: Two
    blocked_by:
      - ref: "#3"
  - title: Three
    blocked_by:
      - ref: "#1"
        kind: bogus
  - title: Four
    blocked_by:
      - ref: "#1"
      - ref: "#1"
  - body: nobody titled this
  - title: Six
    key: NOPE
  - id: SHOP-1
    title: Seven
  - id: SHOP-1
    title: Eight
  - id: SHOP-404
    title: Nine
  - title: Ten
    blocked_by:
      - ref: SHOP-99
`, "")
	before := storeDigest(t, r.root)

	out, code := r.run(t, "validate", "checkout")
	if code != 2 {
		t.Fatalf("jig validate checkout: exit %d, want 2\n%s", code, out)
	}
	for _, want := range []string{"error: chart checkout failed validation", "code: VALIDATION_ERROR"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	missing := func(id string) string { return "ticket " + id + " not found in the store at " + r.root }
	want := []string{
		`entry 5 has no title`,
		`entry 6 ("Six"): project.yaml does not declare key "NOPE"`,
		`entry 7 and entry 8 both have id "SHOP-1"`,
		`entry 9 ("Nine"): ` + missing("SHOP-404"),
		`entry 1 ("One"): blocked_by ref "#99" is out of range`,
		`entry 2 ("Two"): blocked_by ref "#3" names a later entry that is also being created; it must point at an earlier one`,
		`entry 3 ("Three"): blocked_by ref "#1" has kind "bogus"; must be "merged" or "stacked"`,
		`entry 4 ("Four"): blocked_by ref "#1" is already listed in this entry's blocked_by`,
		`entry 10 ("Ten"): blocked_by ref "SHOP-99" ` + missing("SHOP-99"),
		"Fix them in charts/checkout/tickets.yaml, then run `jig validate checkout` again",
	}
	if got := helpLinesOf(out); !reflect.DeepEqual(got, want) {
		t.Errorf("help lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if after := storeDigest(t, r.root); !reflect.DeepEqual(after, before) {
		t.Errorf("jig validate changed the store: before %d files, after %d", len(before), len(after))
	}

	// --check changes nothing: a chart has no approval to record.
	again, code := r.run(t, "validate", "checkout", "--check")
	if code != 2 || again != out {
		t.Errorf("jig validate checkout --check: exit %d, output differs from without it:\n%s", code, again)
	}
}

// TestValidateChartPasses: a chart whose entries are all right prints "valid:
// yes", the counts of its entries and the one help line that says what is next;
// --check says the same, and nothing is written.
func TestValidateChartPasses(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", false)
	r.ticket(t, "SHOP-2", "", false)
	r.chart(t, "checkout", `tickets:
  - id: SHOP-1
    title: One
  - id: SHOP-2
    title: Two
  - title: Three
    blocked_by:
      - ref: "#1"
      - ref: SHOP-2
        kind: stacked
  - title: Four
    blocked_by:
      - ref: "#3"
`, "")
	before := storeDigest(t, r.root)

	for _, args := range [][]string{{"validate", "checkout"}, {"validate", "checkout", "--check"}} {
		out, code := r.run(t, args...)
		if code != 0 {
			t.Fatalf("jig %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
		for _, want := range []string{
			"valid: yes\n",
			"chart:\n  name: checkout\n  entries: 4\n  graduated: 2\n  ungraduated: 2\n",
			"help[1]:\n  Run `jig graduate checkout` to create the 2 entries that have no ticket yet",
		} {
			if !strings.Contains(strings.ReplaceAll(out, "\r\n", "\n"), want) {
				t.Errorf("jig %s: output lacks %q:\n%s", strings.Join(args, " "), want, out)
			}
		}
	}
	if after := storeDigest(t, r.root); !reflect.DeepEqual(after, before) {
		t.Errorf("jig validate changed the store")
	}

	// One entry left, and none left.
	r.chart(t, "checkout", "tickets:\n  - id: SHOP-1\n    title: One\n  - title: Two\n", "")
	if out, code := r.run(t, "validate", "checkout"); code != 0 || !strings.Contains(out, "Run `jig graduate checkout` to create the 1 entry that has no ticket yet") {
		t.Errorf("one entry left: exit %d\n%s", code, out)
	}
	r.chart(t, "checkout", "tickets:\n  - id: SHOP-1\n    title: One\n  - id: SHOP-2\n    title: Two\n", "")
	out, code := r.run(t, "validate", "checkout")
	if code != 0 || !strings.Contains(out, "ungraduated: 0") || !strings.Contains(out, "Every entry has a ticket: `jig next checkout` says where the chart stands") {
		t.Errorf("none left: exit %d\n%s", code, out)
	}
}

// TestValidateChartWithNoEntries: a chart with no entries, and a tickets.yaml
// that cannot be read, are problems the validate lists like any other.
func TestValidateChartWithNoEntries(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.chart(t, "fresh", "tickets: []\n", "")
	r.chart(t, "broken", "tickets: [\n", "")

	out, code := r.run(t, "validate", "fresh")
	if code != 2 {
		t.Fatalf("jig validate fresh: exit %d, want 2\n%s", code, out)
	}
	want := []string{"charts/fresh/tickets.yaml has no entries", "Fix them in charts/fresh/tickets.yaml, then run `jig validate fresh` again"}
	if got := helpLinesOf(out); !reflect.DeepEqual(got, want) {
		t.Errorf("fresh: help lines %q, want %q", got, want)
	}

	out, code = r.run(t, "validate", "broken")
	got := helpLinesOf(out)
	if code != 2 || len(got) != 2 || !strings.HasPrefix(got[0], "charts/broken/tickets.yaml could not be read: ") || !strings.HasPrefix(got[1], "Fix them in charts/broken/tickets.yaml") {
		t.Errorf("broken: exit %d, help lines %q", code, got)
	}
}

// TestValidateTicketWinsOverAChart: a word that is a ticket's alias is the
// ticket, with its "is now" line, even when a chart has that name; a word that
// is neither a ticket nor a chart is still reported as a ticket with no brief.
func TestValidateTicketWinsOverAChart(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-30", "aliases:\n  - coupons\n", false)
	r.chart(t, "coupons", "tickets:\n  - title: One\n", "")

	out, code := r.run(t, "validate", "coupons", "--check")
	if !strings.HasPrefix(out, "coupons is now SHOP-30\n") {
		t.Errorf("jig validate coupons: output does not open as the ticket's: exit %d\n%s", code, out)
	}
	for _, not := range []string{"chart coupons failed validation", "chart:"} {
		if strings.Contains(out, not) {
			t.Errorf("jig validate coupons read the chart, not the ticket (%q):\n%s", not, out)
		}
	}

	out, code = r.run(t, "validate", "neither")
	if code != 2 || !strings.Contains(out, "ticket neither failed validation") || !strings.Contains(out, "brief.md not found") {
		t.Errorf("jig validate neither: exit %d, want the ticket's failure\n%s", code, out)
	}
}

// TestValidateChartAndGraduateShareTheirChecks: jig graduate refuses on the first
// problem checkChart lists, with exactly its error, and a chart without
// problems passes both. The wrappers graduate calls return the first Err.
func TestValidateChartAndGraduateShareTheirChecks(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", false)
	cfg := project.Config{Keys: map[string]string{"SHOP": "everything"}}

	cases := []struct {
		name  string
		chart string
		line  string
	}{
		{"a title", "tickets:\n  - body: x\n", "entry 1 has no title"},
		{"a key", "tickets:\n  - title: A\n    key: NOPE\n", `entry 1 ("A"): project.yaml does not declare key "NOPE"`},
		{"a duplicate id", "tickets:\n  - id: SHOP-1\n    title: A\n  - id: SHOP-1\n    title: B\n", `entry 1 and entry 2 both have id "SHOP-1"`},
		{"a missing ticket", "tickets:\n  - id: SHOP-9\n    title: A\n", `entry 1 ("A"): ticket SHOP-9 not found in the store at ` + r.root},
		{"a ref", "tickets:\n  - title: A\n    blocked_by:\n      - ref: \"#4\"\n", `entry 1 ("A"): blocked_by ref "#4" is out of range`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r.chart(t, "c", c.chart, "")
			entries, err := r.st.ReadChart("c")
			if err != nil {
				t.Fatal(err)
			}
			_, problems := checkChart(r.st, cfg, "c", entries)
			if len(problems) != 1 || problems[0].Line != c.line {
				t.Fatalf("checkChart = %+v, want the one line %q", problems, c.line)
			}
			first := problems[0].Err
			var got error
			if err := validateChartEntries(r.st, cfg, entries, "c"); err != nil {
				got = err
			} else if _, err := resolveChartEntryRefs(r.st, "c", entries); err != nil {
				got = err
			}
			var ae *axi.Error
			if !errors.As(got, &ae) || !reflect.DeepEqual(*ae, *first) {
				t.Fatalf("graduate's checks returned %v, want the first problem's Err %v", got, first)
			}
			if first.Code != "VALIDATION_ERROR" || len(first.Help) == 0 {
				t.Errorf("Err = %+v, want a VALIDATION_ERROR with help", first)
			}
		})
	}

	r.chart(t, "ok", "tickets:\n  - id: SHOP-1\n    title: A\n  - title: B\n    blocked_by:\n      - ref: \"#1\"\n", "")
	entries, err := r.st.ReadChart("ok")
	if err != nil {
		t.Fatal(err)
	}
	refs, problems := checkChart(r.st, cfg, "ok", entries)
	if len(problems) != 0 || len(refs) != 2 || len(refs[1]) != 1 || refs[1][0].knownID != "SHOP-1" {
		t.Errorf("checkChart(ok) = %+v, %+v, want no problems and entry 2 blocked by SHOP-1", refs, problems)
	}
}
