package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// itemRig is containerRig with a GitHub tracker and a record for each of a few
// items: 48 is the issue TK-1 (which has the chart plan), 50 the task TK-2,
// 51 names a ticket the store lacks, 52 a chart (the old key) and 77 is TK-1
// again, in a store that has a chart named 77.
func itemRig(t *testing.T) chartRig {
	t.Helper()
	r := containerRig(t)
	r.put(t, "project.yaml", "schema_version: 2\nname: demo\nkeys:\n  TK: everything in demo\ntrackers:\n  - github:\n      repo: example/tracking\n      project: https://github.com/users/example/projects/7\nrepos:\n  - remote: /srv/app.git\n    label: app\n")
	for ref, owner := range map[string]string{"48": "ticket: TK-1", "50": "ticket: TK-2", "51": "ticket: TK-404", "52": "chart: plan", "77": "ticket: TK-1"} {
		r.put(t, "trackers/github/example/tracking/"+ref+"/record.yaml", owner+"\nissue: "+ref+"\n")
	}
	r.chart(t, "77", "tickets:\n  - id: TK-6\n    title: Six\n", "")
	return r
}

// withoutItemLines drops the three lines a ref adds to the next block.
func withoutItemLines(text string) string {
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if l == "  ref: 48" || l == "  ref: 50" || l == "  ref: 77" || l == "  repo: example/tracking" || strings.HasPrefix(l, "  bound: ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

// sameAnswer asserts that `jig next <ref> <flags>` is `jig next <id> <flags>`
// with ref, repo and bound added, in text and in JSON, and returns the text.
func sameAnswer(t *testing.T, r chartRig, ref, id, bound string, flags ...string) string {
	t.Helper()
	plain, pcode := r.run(t, append([]string{"next", id}, flags...)...)
	got, gcode := r.run(t, append([]string{"next", ref}, flags...)...)
	if pcode != gcode || withoutItemLines(got) != plain {
		t.Errorf("jig next %s %v (exit %d) is not jig next %s (exit %d) plus the item lines:\n%s\n--- plain:\n%s", ref, flags, gcode, id, pcode, got, plain)
	}
	if pcode == 0 && !strings.Contains(got, "next:\n  ref: "+ref+"\n  repo: example/tracking\n  bound: "+bound+"\n") {
		t.Errorf("jig next %s %v lacks the item lines at the top of the next block:\n%s", ref, flags, got)
	}

	pj, _ := r.run(t, append(append([]string{"next", id}, flags...), "--json")...)
	gj, _ := r.run(t, append(append([]string{"next", ref}, flags...), "--json")...)
	var pm, gm map[string]any
	if json.Unmarshal([]byte(pj), &pm) != nil || json.Unmarshal([]byte(gj), &gm) != nil {
		if pcode == 0 {
			t.Fatalf("json: %s / %s", pj, gj)
		}
		return got
	}
	if gm["ref"] != ref || gm["repo"] != "example/tracking" {
		t.Errorf("json of %s lacks ref and repo: %s", ref, gj)
	}
	b, _ := gm["bound"].(map[string]any)
	if want := strings.Fields(bound); b["kind"] != want[0] || b["ticket"] != want[1] {
		t.Errorf("json bound of %s = %v, want %s", ref, gm["bound"], bound)
	}
	delete(gm, "ref")
	delete(gm, "repo")
	delete(gm, "bound")
	if !reflect.DeepEqual(pm, gm) {
		t.Errorf("json of %s %v is not %s's:\n%s\n%s", ref, flags, id, gj, pj)
	}
	return got
}

// TestNextItemBound: a bound ref answers exactly as its owner does, flags
// included, with ref, repo and bound added at the top; a plain ticket id, a
// chart name and a ref that cannot be followed behave as they should.
func TestNextItemBound(t *testing.T) {
	t.Parallel()
	r := itemRig(t)

	// The issue has a chart: its run is the answer.
	if text := sameAnswer(t, r, "48", "TK-1", "issue TK-1"); !strings.Contains(text, "chart: plan") {
		t.Errorf("jig next 48 is not the chart's standing:\n%s", text)
	}
	sameAnswer(t, r, "48", "TK-1", "issue TK-1", "--tickets", "TK-2")
	sameAnswer(t, r, "48", "TK-1", "issue TK-1", "--for", "build")
	sameAnswer(t, r, "48", "TK-1", "issue TK-1", "--topology")
	// A task owner answers as the task, and says its kind.
	sameAnswer(t, r, "50", "TK-2", "task TK-2")
	sameAnswer(t, r, "50", "TK-2", "task TK-2", "--topology")
	sameAnswer(t, r, "50", "TK-2", "task TK-2", "--for", "review")
	// Other spellings of the same item.
	for _, word := range []string{"#48", "example/tracking#48", "EXAMPLE/Tracking#48", "https://github.com/example/tracking/issues/48"} {
		got, code := r.run(t, "next", word)
		if code != 0 || !strings.Contains(got, "next:\n  ref: 48\n  repo: example/tracking\n  bound: issue TK-1\n") {
			t.Errorf("jig next %s: exit %d, want the same answer as 48:\n%s", word, code, got)
		}
	}

	// A flag the owner refuses is refused after the resolution, in its words.
	if out, code := r.run(t, "next", "48", "--for", "nonsense"); code == 0 || !strings.Contains(out, `--for "nonsense" is none of the phases`) {
		t.Errorf("--for nonsense: exit %d\n%s", code, out)
	}
	if out, code := r.run(t, "next", "50", "--tickets", "TK-2"); code == 0 || !strings.Contains(out, "--tickets") {
		t.Errorf("--tickets on a task: exit %d, want the refusal\n%s", code, out)
	}

	// A bare number that is also a chart's name is the item, and says so.
	text, code := r.run(t, "next", "77")
	if code != 0 || !strings.Contains(text, "  bound: issue TK-1\n") || !strings.Contains(text, "advisories[1]:\n  77 is also the name of a chart") {
		t.Errorf("jig next 77: exit %d, want the item with the shadow advisory:\n%s", code, text)
	}
	if out, _ := r.run(t, "next", "77", "--json"); !strings.Contains(out, `"advisories":["77 is also the name of a chart`) {
		t.Errorf("jig next 77 --json lacks the advisory:\n%s", out)
	}

	// A record that cannot be followed names its path.
	for ref, want := range map[string]string{"51": "TK-404", "52": "names no ticket"} {
		out, code := r.run(t, "next", ref)
		if code == 0 || !strings.Contains(filepath.ToSlash(out), ref+"/record.yaml") || !strings.Contains(out, want) {
			t.Errorf("jig next %s: exit %d, want an error naming the record and %q:\n%s", ref, code, want, out)
		}
	}

	// Words that are not refs behave as before: no item lines.
	for _, word := range []string{"TK-1", "TK-2", "plan"} {
		if out, _ := r.run(t, "next", word); strings.Contains(out, "  ref: ") || strings.Contains(out, "  bound: ") {
			t.Errorf("jig next %s grew item lines:\n%s", word, out)
		}
		if out, _ := r.run(t, "next", word, "--json"); strings.Contains(out, `"ref":`) || strings.Contains(out, `"bound":`) {
			t.Errorf("jig next %s --json grew item fields:\n%s", word, out)
		}
	}
	if out, code := r.run(t, "next", "TK-404"); code == 0 || !strings.Contains(out, `"TK-404"`) {
		t.Errorf("jig next TK-404: exit %d\n%s", code, out)
	}
	// The listing is untouched.
	if out, code := r.run(t, "next"); code != 0 || strings.Contains(out, "  ref: ") {
		t.Errorf("jig next: exit %d\n%s", code, out)
	}
}
