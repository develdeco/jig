package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// issueRig is the dependRig with DEMO-1 to DEMO-3 made issues, DEMO-4 a task.
func issueRig(t *testing.T) (dependRig, *store.Store) {
	t.Helper()
	r := newDependRig(t)
	st := &store.Store{Root: r.storeDir}
	for _, id := range []string{"DEMO-1", "DEMO-2", "DEMO-3"} {
		if err := st.SetTicketKind(id, store.KindIssue, ""); err != nil {
			t.Fatal(err)
		}
	}
	gitMust(t, r.storeDir, "add", "-A")
	gitMust(t, r.storeDir, "commit", "-m", "issues")
	return r, st
}

// TestAfterRecordSetsAndClearsOneIdWithDependsShape: the record is the issue's
// own after list, written once, shown in depend's block, and no blocked_by.
func TestAfterRecordSetsAndClearsOneIdWithDependsShape(t *testing.T) {
	r, st := issueRig(t)
	out, code := r.jig(t, "depend", "DEMO-2", "--after", "DEMO-1")
	if code != 0 || !strings.Contains(out, "after: DEMO-1") || !strings.Contains(out, "result: recorded") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if out, _ := r.jig(t, "depend", "DEMO-2", "--after", "DEMO-1"); !strings.Contains(out, "result: unchanged") {
		t.Errorf("again:\n%s", out)
	}
	rec, _ := st.ReadTicket("DEMO-2")
	if len(rec.After) != 1 || rec.After[0] != "DEMO-1" || len(rec.BlockedBy) != 0 {
		t.Errorf("record = %+v", rec)
	}
	if y := r.ticketYAML(t, "DEMO-2"); !strings.Contains(y, "after:") || strings.Contains(y, "blocked_by") {
		t.Errorf("yaml:\n%s", y)
	}
	if y := r.ticketYAML(t, "DEMO-1"); strings.Contains(y, "after:") {
		t.Errorf("an empty after was written:\n%s", y)
	}
	out, code = r.jig(t, "depend", "DEMO-2", "--after", "DEMO-1", "--clear")
	if code != 0 || !strings.Contains(out, "result: cleared") || !strings.Contains(out, "after: none") {
		t.Fatalf("clear: exit %d\n%s", code, out)
	}
	if y := r.ticketYAML(t, "DEMO-2"); strings.Contains(y, "after:") {
		t.Errorf("a cleared after stays:\n%s", y)
	}
}

// TestAfterRecordRefusesWhatCannotBeTrueAndWritesNothing: a task, a missing
// ticket, itself and a loop each refuse with their code and leave the store.
func TestAfterRecordRefusesWhatCannotBeTrueAndWritesNothing(t *testing.T) {
	r, _ := issueRig(t)
	if _, code := r.jig(t, "depend", "DEMO-2", "--after", "DEMO-1"); code != 0 {
		t.Fatal("setup")
	}
	if _, code := r.jig(t, "depend", "DEMO-3", "--after", "DEMO-2"); code != 0 {
		t.Fatal("setup")
	}
	head := r.head(t)
	cases := []struct {
		args []string
		code string
	}{
		{[]string{"DEMO-1", "--after", "DEMO-4"}, "AFTER_NOT_ISSUE"},
		{[]string{"DEMO-4", "--after", "DEMO-1"}, "AFTER_NOT_ISSUE"},
		{[]string{"DEMO-1", "--after", "DEMO-99"}, "AFTER_NOT_ISSUE"},
		{[]string{"DEMO-1", "--after", "DEMO-1"}, "AFTER_SELF"},
		{[]string{"DEMO-1", "--after", "DEMO-3"}, "AFTER_CYCLE"},
	}
	for _, c := range cases {
		out, code := r.jig(t, append([]string{"depend"}, c.args...)...)
		if code == 0 || !strings.Contains(out, c.code) {
			t.Errorf("%v: exit %d, want %s\n%s", c.args, code, c.code, out)
		}
		if c.code == "AFTER_CYCLE" && !strings.Contains(out, "DEMO-1 -> DEMO-3 -> DEMO-2 -> DEMO-1") {
			t.Errorf("the loop is not named:\n%s", out)
		}
	}
	if r.head(t) != head {
		t.Error("a refusal wrote to the store")
	}
	if y := r.ticketYAML(t, "DEMO-1"); strings.Contains(y, "after:") {
		t.Errorf("a refusal wrote the record:\n%s", y)
	}
}

// TestAfterRecordTakesNoOnOrKind: it is a suggestion, not a wait.
func TestAfterRecordTakesNoOnOrKind(t *testing.T) {
	r, _ := issueRig(t)
	for _, extra := range [][]string{{"--on", "DEMO-3"}, {"--kind", "stacked"}} {
		out, code := r.jig(t, append([]string{"depend", "DEMO-2", "--after", "DEMO-1"}, extra...)...)
		if code == 0 || !strings.Contains(out, "VALIDATION_ERROR") {
			t.Errorf("%v: exit %d\n%s", extra, code, out)
		}
	}
}

// TestAfterRecordValidateReportsAHandEdit: validate names the three refusals
// and an after on a task.
func TestAfterRecordValidateReportsAHandEdit(t *testing.T) {
	r, st := issueRig(t)
	set := func(id string, after ...string) {
		t.Helper()
		for _, a := range after {
			if err := st.SetTicketAfter(id, a); err != nil {
				t.Fatal(err)
			}
		}
	}
	set("DEMO-1", "DEMO-1", "DEMO-4", "DEMO-2", "DEMO-99")
	set("DEMO-2", "DEMO-1")
	set("DEMO-4", "DEMO-1")
	out, code := r.jig(t, "validate", "DEMO-1")
	if code == 0 {
		t.Fatalf("validate passed:\n%s", out)
	}
	for _, want := range []string{"AFTER_SELF", "AFTER_NOT_ISSUE", "AFTER_CYCLE", `"DEMO-4"`, `"DEMO-99"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
	if out, _ := r.jig(t, "validate", "DEMO-4"); !strings.Contains(out, "only an issue has one") {
		t.Errorf("an after on a task:\n%s", out)
	}
}
