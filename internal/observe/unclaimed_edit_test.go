package observe

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/phase"
)

// buildPhase is a ticket at its build phase whose slices are the sub-steps
// given (slice id -> sub state), each in the state a claim leaves it in.
func buildPhase(slices map[string]string, order ...string) phase.Ticket {
	var subs []phase.Sub
	for _, id := range order {
		subs = append(subs, phase.Sub{Key: "slice:" + id, State: slices[id]})
	}
	t := phase.Ticket{Current: phase.Build, Ready: true}
	t.Phases[2] = phase.Phase{Name: phase.Build, State: "current", Subs: subs}
	return t
}

// briefPhase is a ticket whose brief waits for its approval.
func briefPhase() phase.Ticket {
	t := phase.Ticket{Current: phase.Brief, Ready: true}
	t.Phases[0] = phase.Phase{Name: phase.Brief, State: "current", Subs: []phase.Sub{
		{Key: "drafted", State: phase.SubDone},
		{Key: "slices", State: phase.SubDone},
		{Key: "approved", State: phase.SubNeedsYou},
	}}
	return t
}

// edit is the context the session hears for an Edit of the files, in the
// clone unless a file is absolute elsewhere.
func (r *phaseRig) edit(files ...string) string {
	return r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Files = "Edit", files })).Context
}

func (r *phaseRig) inClone(name string) string { return filepath.Join(r.fx.RepoDir, name) }

func TestAnEditWithNoSliceClaimedHearsToClaimOne(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.onTicketBranch()
	r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending, "b": phase.SubWaiting, "c": phase.SubDone}, "a", "b", "c")
	r.handle(r.ev(KindSessionStart))
	got := r.edit(r.inClone("main.go"))
	for _, want := range []string{"jig claim " + r.fx.Ticket + " --slice", "ready: a"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ready: a,") {
		t.Errorf("the note names a slice that is not ready:\n%s", got)
	}
	if again := r.edit(r.inClone("other.go")); strings.Contains(again, "jig claim") {
		t.Errorf("the note was told twice:\n%s", again)
	}
	lines := r.ledgerOf("note-delivered")
	if len(lines) != 1 || lines[0].Ref != "unclaimed-edit:"+r.fx.Ticket+":claim" {
		t.Errorf("note-delivered lines = %+v, want one with the ref unclaimed-edit:%s:claim", lines, r.fx.Ticket)
	}
}

func TestAnEditWithASliceClaimedHearsNothing(t *testing.T) {
	t.Parallel()
	for _, state := range []string{phase.SubActive, phase.SubNeedsYou, phase.SubFailed} {
		r := newPhaseRig(t)
		r.onTicketBranch()
		r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": state, "b": phase.SubPending}, "a", "b")
		r.handle(r.ev(KindSessionStart))
		if got := r.edit(r.inClone("main.go")); got != "" {
			t.Errorf("a slice in state %s is claimed: context = %s, want none", state, got)
		}
	}
}

func TestAnEditWhileTheBriefHasNoApprovalHearsToFinishTheBrief(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.onTicketBranch()
	r.phases[r.fx.Ticket] = briefPhase()
	r.handle(r.ev(KindSessionStart))
	got := r.edit(r.inClone("main.go"))
	for _, want := range []string{"intake", "`jig validate", "before changing code"} {
		if !strings.Contains(got, want) {
			t.Errorf("the brief note lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "jig claim") {
		t.Errorf("an unapproved brief was told to claim:\n%s", got)
	}
	if again := r.edit(r.inClone("main.go")); again != "" {
		t.Errorf("the brief note was told twice:\n%s", again)
	}
	// The brief is approved: the state changed, so the claim note is a new fact.
	r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
	if next := r.edit(r.inClone("main.go")); !strings.Contains(next, "jig claim") {
		t.Errorf("after the approval the edit heard no claim note:\n%s", next)
	}
	refs := r.ledgerOf("note-delivered")
	if len(refs) != 2 || refs[0].Ref != "unclaimed-edit:"+r.fx.Ticket+":brief" || refs[1].Ref != "unclaimed-edit:"+r.fx.Ticket+":claim" {
		t.Errorf("note-delivered lines = %+v, want the brief ref then the claim ref", refs)
	}
}

func TestAnEditOutsideTheCheckoutHearsNothing(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.onTicketBranch()
	r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
	r.handle(r.ev(KindSessionStart))
	outside := filepath.Join(t.TempDir(), "scratch.txt")
	for _, files := range [][]string{{outside}, {filepath.Join(r.fx.StoreDir, "tickets", "x.md")}, nil} {
		if got := r.edit(files...); got != "" {
			t.Errorf("an edit of %v: context = %s, want none", files, got)
		}
	}
}

// TestTheCheckoutIsMatchedInBothSlashForms: a harness on Windows may send a
// path with either slash. The cwd stays in its native form: a backslash cwd
// makes hooks silent today (RUN-13), so that case cannot pass before RUN-13.
func TestTheCheckoutIsMatchedInBothSlashForms(t *testing.T) {
	t.Parallel()
	for _, form := range []func(string) string{filepath.ToSlash, func(p string) string { return strings.ReplaceAll(p, "/", `\`) }} {
		r := newPhaseRig(t)
		r.onTicketBranch()
		r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
		r.handle(r.ev(KindSessionStart))
		file := form(r.inClone("pkg/main.go"))
		if filepath.Separator == '/' && strings.Contains(file, `\`) {
			continue // a backslash is a filename character here, not a separator
		}
		if got := r.edit(file); !strings.Contains(got, "jig claim") {
			t.Errorf("an edit of %s: context = %s, want the claim note", file, got)
		}
	}
}

func TestAnEditThatIsNotOnATicketOrNotAnEditHearsNothing(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t) // the branch binds no ticket
	r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
	r.handle(r.ev(KindSessionStart))
	if got := r.edit(r.inClone("main.go")); strings.Contains(got, "jig claim") {
		t.Errorf("an edit on no ticket heard to claim:\n%s", got)
	}
	r.onTicketBranch()
	if got := r.handle(r.ev(KindTool, func(e *Event) { e.Tool = "Bash"; e.Command = "ls" })).Context; got != "" {
		t.Errorf("a tool call that edited nothing: context = %s, want none", got)
	}
}
