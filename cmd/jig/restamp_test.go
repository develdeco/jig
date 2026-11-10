package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// journalLines returns DEMO-1's journal lines of one event.
func (r dependRig) journalLines(t *testing.T, event string) []journal.Line {
	t.Helper()
	lines, err := journal.Read(&store.Store{Root: r.storeDir}, "DEMO-1")
	if err != nil {
		t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range lines {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

func (r dependRig) setBrief(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.storeDir, "tickets", "DEMO-1", "brief.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r dependRig) setState(t *testing.T, slice string, ss store.SliceState) {
	t.Helper()
	if err := (&store.Store{Root: r.storeDir}).WriteSliceState("DEMO-1", slice, ss); err != nil {
		t.Fatal(err)
	}
}

func (r dependRig) state(t *testing.T, slice string) store.SliceState {
	t.Helper()
	ss, err := (&store.Store{Root: r.storeDir}).ReadSliceState("DEMO-1", slice)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

// restampRig has slices a (green, stamped from Seams), b (queued, from all)
// and c (green, from Problem) on a committed brief, then a brief whose Problem
// and Seams sections were edited and committed.
func restampRig(t *testing.T) dependRig {
	t.Helper()
	r := newSliceRig(t, true)
	for _, a := range [][]string{
		{"--id", "a", "--from-brief", "Seams"},
		{"--id", "b", "--from-brief", "all"},
		{"--id", "c", "--from-brief", "Problem"},
	} {
		args := append([]string{"slice", "add", "DEMO-1", "--goal", "g", "--oracle", "true"}, a...)
		if out, code := r.jig(t, args...); code != 0 {
			t.Fatalf("slice add: exit %d\n%s", code, out)
		}
	}
	r.setState(t, "a", store.SliceState{State: "green", Attempts: 2})
	r.setState(t, "c", store.SliceState{State: "green", Attempts: 1})
	return r
}

const restampEdited = "# DEMO-1: t\n\n## Problem\n\nSomething.\n\n## Decision\n\nA new decision.\n\n## Seams\n\nThe seams, edited.\n"

// TestValidateRestamp: after a brief edit the stale slices carry the current
// hashes of the sections they were stamped from, whatever their state, and
// nothing else about them changes; the approval is not recorded.
func TestValidateRestamp(t *testing.T) {
	t.Parallel()
	r := restampRig(t)
	r.setBrief(t, restampEdited)
	gitMust(t, r.storeDir, "add", "-A")
	gitMust(t, r.storeDir, "commit", "-m", "edit the brief")

	if out, code := r.jig(t, "validate", "DEMO-1", "--check"); code == 0 || !strings.Contains(out, "--restamp") {
		t.Fatalf("validate --check on stale stamps: exit %d, want a refusal with the restamp hint\n%s", code, out)
	}
	out, code := r.jig(t, "validate", "DEMO-1", "--restamp")
	if code != 0 {
		t.Fatalf("validate --restamp: exit %d\n%s", code, out)
	}
	for _, want := range []string{"restamped[2]{id,state,sections}", "a,green,Seams", "b,queued,\"Problem, Seams\"", "valid: yes", "not recorded (--restamp)", "sections["} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "c,green") {
		t.Errorf("slice c is stamped from the unchanged Problem and was restamped:\n%s", out)
	}
	if got := r.state(t, "a"); got.State != "green" || got.Attempts != 2 {
		t.Fatalf("a restamped slice's state changed: %+v", got)
	}
	// b was stamped from Problem and Seams; Problem is unchanged, so it
	// keeps that hash beside Seams' new one.
	for _, sl := range r.slices(t) {
		if sl.ID == "b" && strings.Join(sl.FromBrief, ",") != r.sectionHash(t, "Problem")+","+r.sectionHash(t, "Seams") {
			t.Errorf("b from_brief = %v, want the current Problem and Seams hashes", sl.FromBrief)
		}
	}
	// One restamp line per restamped slice, ref the id, summary the headings.
	if lines := r.journalLines(t, journal.EventRestamp); len(lines) != 2 || lines[0].Ref != "a" || lines[0].Summary != "Seams" || lines[1].Ref != "b" || lines[1].Summary != "Problem, Seams" {
		t.Errorf("restamp journal lines = %+v, want one for a and one for b", lines)
	}
	if got := r.state(t, "b"); got.State != "queued" {
		t.Fatalf("b state = %+v", got)
	}
	if out, code := r.jig(t, "validate", "DEMO-1", "--check"); code != 0 {
		t.Fatalf("validate --check after the restamp: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(r.storeDir, "tickets", "DEMO-1", "approval.yaml")); err == nil {
		t.Fatal("--restamp recorded the approval")
	}

	// Nothing is stale now: an empty table, and the file as it was.
	before := r.slicesYAML(t)
	out, code = r.jig(t, "validate", "DEMO-1", "--restamp")
	if code != 0 || !strings.Contains(out, "restamped[0]") || r.slicesYAML(t) != before {
		t.Fatalf("a second restamp: exit %d, changed=%v\n%s", code, r.slicesYAML(t) != before, out)
	}
	if lines := r.journalLines(t, journal.EventRestamp); len(lines) != 2 {
		t.Errorf("a restamp that changed nothing journaled: %+v", lines)
	}
	if out, code := r.jig(t, "validate", "DEMO-1", "--restamp", "--check"); code == 0 {
		t.Fatalf("--restamp with --check: exit 0\n%s", out)
	}
}

// TestValidateRestampBuilding: a held claim on a stale slice refuses the
// restamp and writes nothing.
func TestValidateRestampBuilding(t *testing.T) {
	t.Parallel()
	r := restampRig(t)
	r.setBrief(t, restampEdited)
	gitMust(t, r.storeDir, "add", "-A")
	gitMust(t, r.storeDir, "commit", "-m", "edit the brief")
	r.setState(t, "b", store.SliceState{State: "building", Attempts: 1})

	before, head := r.slicesYAML(t), r.head(t)
	out, code := r.jig(t, "validate", "DEMO-1", "--restamp")
	if code == 0 || !strings.Contains(out, "SLICE_BUILDING") {
		t.Fatalf("exit %d, want SLICE_BUILDING\n%s", code, out)
	}
	if r.slicesYAML(t) != before || r.head(t) != head {
		t.Fatal("a refused restamp wrote slices.yaml or the store history")
	}
}

// TestValidateRestampUnresolved: a stamp whose heading is gone, and one no
// committed revision holds, are listed and fail the command, while the rest
// are restamped.
func TestValidateRestampUnresolved(t *testing.T) {
	t.Parallel()
	r := restampRig(t)
	// Problem is removed (c's heading is gone); Seams is edited (a resolves).
	r.setBrief(t, "# DEMO-1: t\n\n## Seams\n\nThe seams, edited.\n")
	gitMust(t, r.storeDir, "add", "-A")
	gitMust(t, r.storeDir, "commit", "-m", "edit the brief")

	out, code := r.jig(t, "validate", "DEMO-1", "--restamp")
	if code == 0 || !strings.Contains(out, "unresolved[") || !strings.Contains(out, "c,") {
		t.Fatalf("exit %d, want c listed as unresolved\n%s", code, out)
	}
	if !strings.Contains(out, "a,green,Seams") {
		t.Errorf("a was not restamped beside the unresolved one:\n%s", out)
	}
	by := map[string][]string{}
	for _, sl := range r.slices(t) {
		by[sl.ID] = sl.FromBrief
	}
	if by["a"][0] != r.sectionHash(t, "Seams") {
		t.Errorf("a still carries a stale stamp: %v", by["a"])
	}
	if by["c"][0] == r.sectionHash(t, "Seams") || len(by["c"]) != 1 {
		t.Errorf("c's stamp was guessed: %v", by["c"])
	}

	// A hash that never was in a committed revision.
	st := &store.Store{Root: r.storeDir}
	if err := st.AppendSlices("DEMO-1", []store.Slice{{ID: "d", Workspace: "root", Goal: "g", Oracle: "true", BlockedBy: []string{}, FromBrief: []string{strings.Repeat("0", 64)}}}); err != nil {
		t.Fatal(err)
	}
	out, code = r.jig(t, "validate", "DEMO-1", "--restamp")
	if code == 0 || !strings.Contains(out, "d,"+strings.Repeat("0", 64)) {
		t.Fatalf("exit %d, want d listed as unresolved\n%s", code, out)
	}
}

// TestValidateRestampOneUnresolved: a single stamp history cannot explain is
// still listed, with the unresolved-hash error, rather than falling through to the generic
// stale-hash failure.
func TestValidateRestampOneUnresolved(t *testing.T) {
	t.Parallel()
	r := restampRig(t)
	st := &store.Store{Root: r.storeDir}
	zero := strings.Repeat("0", 64)
	if err := st.AppendSlices("DEMO-1", []store.Slice{{ID: "d", Workspace: "root", Goal: "g", Oracle: "true", BlockedBy: []string{}, FromBrief: []string{zero}}}); err != nil {
		t.Fatal(err)
	}
	out, code := r.jig(t, "validate", "DEMO-1", "--restamp")
	if code == 0 || !strings.Contains(out, "unresolved[1]{id,hash}") || !strings.Contains(out, "d,"+zero) || !strings.Contains(out, "no committed brief revision") {
		t.Fatalf("exit %d, want the one unresolved row and the unresolved-hash message\n%s", code, out)
	}
}
