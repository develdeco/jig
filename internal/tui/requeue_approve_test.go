package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// stuckStatus is `jig status JIG-1` with the given slices stalled or env-blocked.
func stuckStatus(stuck map[string]string) string {
	rows := [][]string{{"a", "green", "1", "-", "-"}, {"b", "building", "2", "-", "-"}, {"c", "building", "1", "-", "-"}, {"d", "queued", "0", "b+c", "-"}}
	for _, r := range rows {
		if state, ok := stuck[r[0]]; ok {
			r[1] = state
		}
	}
	return strings.Join([]string{
		"ticket: JIG-1",
		"state: stalled",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question"}, rows),
		axi.Help("Run `jig requeue JIG-1 --slice <id>`"),
	}, "\n") + "\n"
}

// stuckRig is a rig of JIG-1 in its build with the slices of stuck stalled or
// env-blocked, over a roster that reads the same, so a line a key leaves stays.
func stuckRig(t *testing.T, stuck map[string]string) *rig {
	t.Helper()
	d := buildUnder()
	d.Slices = &digestSlices{Total: 4, Green: 1, Queued: 1, Building: 4 - 2 - len(stuck)}
	for _, state := range stuck {
		if state == StateStalled {
			d.Slices.Stalled++
		} else {
			d.Slices.EnvBlocked++
		}
	}
	g := quietRigAt(t, "JIG-1", d)
	g.r.answer("status JIG-1 --store /s", stuckStatus(stuck), 0)
	g.feed(jig1()...)
	return g
}

// TestRequeueTakesTheStalledSlice: u requeues the only stalled slice with no
// confirmation and the panel shows the verb's own sentence; with none stalled it
// says so and runs nothing, and the hint line lists the key only while one is.
func TestRequeueTakesTheStalledSlice(t *testing.T) {
	t.Parallel()
	g := stuckRig(t, map[string]string{"b": StateStalled})
	if hint := footerKeys(g.view()); !strings.Contains(hint, "u requeue") {
		t.Errorf("the hint line does not offer u with a slice stalled:\n%s", hint)
	}
	g.r.answer("requeue JIG-1 --slice b --store /s", "requeue:\n  ticket: JIG-1\n  slice: b\nhelp[1]:\n  Slice b is queued again\n", 0)
	g.press("u")
	if g.m.ask != nil {
		t.Errorf("u asked before it requeued")
	}
	if got, want := g.r.lines(), []string{"requeue JIG-1 --slice b --store /s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want %q", got, want)
	}
	if v := g.view(); !strings.Contains(v, "✓ jig requeue JIG-1 --slice b  Slice b is queued again") {
		t.Errorf("the panel does not show the verb's sentence:\n%s", v)
	}

	// An env-blocked slice is one to requeue too.
	e := stuckRig(t, map[string]string{"c": StateEnvBlocked})
	e.press("u")
	if got, want := e.r.lines(), []string{"requeue JIG-1 --slice c --store /s"}; !reflect.DeepEqual(got, want) {
		t.Errorf("env-blocked: commands run = %q, want %q", got, want)
	}

	// Nothing stalled: no key, and a press says why.
	n := stuckRig(t, nil)
	if hint := footerKeys(n.view()); strings.Contains(hint, "u requeue") {
		t.Errorf("the hint line offers u with no slice stalled:\n%s", hint)
	}
	n.press("u")
	if n.m.notice != "no slice is stalled or env-blocked" || len(n.r.lines()) != 0 {
		t.Errorf("u with none stalled: notice %q, commands %q", n.m.notice, n.r.lines())
	}
}

// TestRequeuePicksByTheCursor: with several slices stalled u on a row of none
// says to put the cursor on one; on a slice's row, or on a slice's own screen, it
// requeues that one, and a slice that is not stuck is refused by name.
func TestRequeuePicksByTheCursor(t *testing.T) {
	t.Parallel()
	g := stuckRig(t, map[string]string{"b": StateStalled, "c": StateStalled})
	g.press("u")
	if want := "several slices are stalled: put the cursor on the one to requeue"; g.m.notice != want || len(g.r.lines()) != 0 {
		t.Fatalf("u with two stalled and the cursor on none: notice %q, commands %q", g.m.notice, g.r.lines())
	}

	g.press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want build", g.m.Screen())
	}
	onSlice := func(id string) {
		t.Helper()
		for range 8 {
			if ref := g.m.selected(); ref != nil && ref.slice == id {
				return
			}
			g.press("down")
		}
		t.Fatalf("the cursor never reached slice %s", id)
	}
	onSlice("c")
	g.press("u")
	if got, want := g.r.lines(), []string{"requeue JIG-1 --slice c --store /s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("u on slice c ran %q, want %q", got, want)
	}

	// A slice that is queued, not stalled.
	g.press("up", "up", "up")
	onSlice("a")
	before := len(g.r.lines())
	g.press("u")
	if g.m.notice != "slice a is not stalled or env-blocked" || len(g.r.lines()) != before {
		t.Errorf("u on a green slice: notice %q, commands %q", g.m.notice, g.r.lines())
	}

	// From the slice's own screen.
	onSlice("b")
	g.press("enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want slice", g.m.Screen())
	}
	g.press("u")
	if got := g.r.lines(); got[len(got)-1] != "requeue JIG-1 --slice b --store /s" {
		t.Errorf("u on slice b's screen ran %q", got)
	}
}

// TestApproveAsksThenValidates: v asks to approve the brief as it is now and
// runs `jig validate` only on y; a brief that does not validate shows the verb's
// problems and the hint line lists v only while the brief waits for its approval.
func TestApproveAsksThenValidates(t *testing.T) {
	t.Parallel()
	g := quietRigAt(t, "JIG-1", briefWaiting())
	g.feed(jig1()...)
	if hint := footerKeys(g.view()); !strings.Contains(hint, "v approve") {
		t.Errorf("the hint line does not offer v while the brief waits:\n%s", hint)
	}
	g.press("v")
	if g.m.ask == nil || !strings.Contains(g.view(), "approve the brief of JIG-1 as it is now?") {
		t.Fatalf("v did not ask:\n%s", g.view())
	}
	if len(g.r.lines()) != 0 {
		t.Fatalf("v ran %q before it was confirmed", g.r.lines())
	}
	g.press("n")
	if g.m.ask != nil || len(g.r.lines()) != 0 {
		t.Fatalf("n: ask %+v, commands %q, want the question gone and nothing run", g.m.ask, g.r.lines())
	}

	g.r.answer("validate JIG-1 --store /s", "error: slice b has no oracle\ncode: BRIEF_INVALID\nhelp[1]:\n  Fix the slices\n", 1)
	g.press("v", "y")
	if got, want := g.r.lines(), []string{"validate JIG-1 --store /s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want %q", got, want)
	}
	if v := g.view(); !strings.Contains(v, "✖ jig validate JIG-1  slice b has no oracle (BRIEF_INVALID)") {
		t.Errorf("the verb's problem is not on the frame:\n%s", v)
	}

	g.r.answer("validate JIG-1 --store /s", "approval:\n  ticket: JIG-1\n  provenance: person\nhelp[1]:\n  The brief is approved\n", 0)
	g.press("v", "y")
	if v := g.view(); !strings.Contains(v, "✓ jig validate JIG-1  The brief is approved") {
		t.Errorf("the approval's sentence is not on the frame:\n%s", v)
	}

	// An approved brief offers nothing, and says why.
	a := quietRigAt(t, "JIG-1", buildUnder())
	a.feed(jig1()...)
	if hint := footerKeys(a.view()); strings.Contains(hint, "v approve") {
		t.Errorf("the hint line offers v for an approved brief:\n%s", hint)
	}
	a.press("v")
	if a.m.ask != nil || a.m.notice != "the brief is not waiting for your approval" {
		t.Errorf("v on an approved brief: ask %+v, notice %q", a.m.ask, a.m.notice)
	}
}

// TestKeysWaitForTheRecordsTheyActOn: a key is listed when the records its verb
// acts on say it can run, and until they are read its reason is "reading…", not
// a refusal that is false: u waits for the status, a for the question the roster
// counts, and a slice's u for the status too.
func TestKeysWaitForTheRecordsTheyActOn(t *testing.T) {
	t.Parallel()
	g := stuckRig(t, map[string]string{"b": StateStalled})
	if ok, why := g.m.can(actRequeue); !ok || why != "" {
		t.Fatalf("u with the status read and b stalled = %v, %q", ok, why)
	}
	tk := g.m.w.tickets["JIG-1"]
	tk.status = nil
	if ok, why := g.m.can(actRequeue); ok || why != readingText {
		t.Errorf("u before the status is read = %v, %q, want reading", ok, why)
	}
	if hint := footerKeys(g.view()); strings.Contains(hint, "u requeue") {
		t.Errorf("u is listed before the status is read:\n%s", hint)
	}
	g.m.nav = append(g.m.nav[:2:2], navEntry{screen: screenSlice, ticket: "JIG-1", slice: "b"})
	if ok, why := g.m.can(actRequeue); ok || why != readingText {
		t.Errorf("a slice's u before the status is read = %v, %q, want reading", ok, why)
	}

	// The roster counts a question the stream has not brought.
	g = stuckRig(t, map[string]string{"b": StateStalled})
	tk = g.m.w.tickets["JIG-1"]
	tk.lines = nil
	tk.digest.Slices.NeedsInput = 1
	if ok, why := g.m.can(actAnswer); ok || why != readingText {
		t.Errorf("a with only the roster's count = %v, %q, want reading", ok, why)
	}
}
