package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

const sliceTestBrief = "# DEMO-1: t\n\n## Problem\n\nSomething.\n\n## Seams\n\nThe seams.\n"

// sliceRig is a dependRig whose first ticket has a brief and one slice.
func newSliceRig(t *testing.T, withBrief bool) dependRig {
	t.Helper()
	r := newDependRig(t)
	dir := filepath.Join(r.storeDir, "tickets", "DEMO-1")
	if withBrief {
		if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(sliceTestBrief), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r dependRig) slices(t *testing.T) []store.Slice {
	t.Helper()
	st := &store.Store{Root: r.storeDir}
	got, err := st.ReadSlices("DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (r dependRig) sectionHash(t *testing.T, heading string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.storeDir, "tickets", "DEMO-1", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	return store.BriefSectionHashes(data)[heading]
}

// TestSliceAdd: the verb stamps a new slice from the current brief (Seams by
// default, all, or named headings), writes it queued, and refuses what
// validate would refuse before it writes anything.
func TestSliceAdd(t *testing.T) {
	t.Parallel()
	r := newSliceRig(t, true)
	base := []string{"slice", "add", "DEMO-1", "--oracle", "true"}

	out, code := r.jig(t, append(base, "--id", "a", "--goal", "first")...)
	if code != 0 {
		t.Fatalf("slice add a: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "validate DEMO-1 --check") {
		t.Fatalf("output does not point at validate --check:\n%s", out)
	}
	if lines := r.journalLines(t, journal.EventSliceAdd); len(lines) != 1 || lines[0].Slice != "a" || lines[0].Ref != "a" || lines[0].Summary != "Seams" {
		t.Fatalf("slice-add journal lines = %+v, want one for a stamped from Seams", lines)
	}
	got := r.slices(t)
	if len(got) != 1 || got[0].ID != "a" || got[0].Workspace != "root" || got[0].Goal != "first" || got[0].Oracle != "true" {
		t.Fatalf("slices = %+v", got)
	}
	if want := []string{r.sectionHash(t, "Seams")}; strings.Join(got[0].FromBrief, ",") != strings.Join(want, ",") {
		t.Fatalf("default stamp = %v, want Seams %v", got[0].FromBrief, want)
	}

	if out, code := r.jig(t, append(base, "--id", "b", "--goal", "all", "--from-brief", "all", "--blocked-by", "a")...); code != 0 {
		t.Fatalf("slice add b: exit %d\n%s", code, out)
	}
	if out, code := r.jig(t, append(base, "--id", "c", "--goal", "named", "--from-brief", "Problem, Seams", "--env", "db")...); code != 0 {
		t.Fatalf("slice add c: exit %d\n%s", code, out)
	}
	got = r.slices(t)
	if len(got) != 3 || len(got[1].FromBrief) != 2 || got[1].BlockedBy[0] != "a" || len(got[2].FromBrief) != 2 || got[2].Env != "db" {
		t.Fatalf("slices = %+v", got)
	}
	if out, code := r.jig(t, "validate", "DEMO-1", "--check"); code != 0 {
		t.Fatalf("validate --check: exit %d\n%s", code, out)
	}

	before, headBefore := r.slicesYAML(t), r.head(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"duplicate id", []string{"--id", "a", "--goal", "g"}, "SLICE_ID_DUPLICATE"},
		{"unknown heading", []string{"--id", "d", "--goal", "g", "--from-brief", "Nope"}, "Nope"},
		{"unknown blocked_by", []string{"--id", "d", "--goal", "g", "--blocked-by", "zzz"}, "zzz"},
		{"reserved id", []string{"--id", "gate", "--goal", "g"}, "reserved"},
		{"unknown workspace", []string{"--id", "d", "--goal", "g", "--workspace", "nope"}, "nope"},
		{"no goal", []string{"--id", "d"}, "--goal"},
	} {
		out, code := r.jig(t, append(base, tc.args...)...)
		if code == 0 || !strings.Contains(out, tc.want) {
			t.Errorf("%s: exit %d, want a refusal naming %q\n%s", tc.name, code, tc.want, out)
		}
	}
	if r.slicesYAML(t) != before || r.head(t) != headBefore {
		t.Fatalf("a refusal changed slices.yaml or the store history")
	}
}

// TestSliceAddNoBrief: a ticket with no brief has nothing to stamp from.
func TestSliceAddNoBrief(t *testing.T) {
	t.Parallel()
	r := newSliceRig(t, false)
	out, code := r.jig(t, "slice", "add", "DEMO-1", "--id", "a", "--goal", "g", "--oracle", "true")
	if code == 0 || !strings.Contains(out, "no brief.md") {
		t.Fatalf("exit %d, want a refusal for the missing brief\n%s", code, out)
	}
	if len(r.slices(t)) != 0 {
		t.Fatal("a slice was written for a ticket with no brief")
	}
}

func (r dependRig) slicesYAML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.storeDir, "tickets", "DEMO-1", "slices.yaml"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}
