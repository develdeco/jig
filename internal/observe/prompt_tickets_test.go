package observe

import (
	"fmt"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// phaseRig is a rig whose phase derivation says what the test sets, and whose
// store holds the extra tickets the test asks for (JIG-2, JIG-3, ...).
type phaseRig struct {
	*rig
	phases map[string]phase.Ticket
}

func newPhaseRig(t *testing.T, extra ...string) *phaseRig {
	t.Helper()
	r := &phaseRig{rig: newRig(t), phases: map[string]phase.Ticket{}}
	for _, id := range extra {
		if err := r.st.CreateTicketRecord(id, store.Ticket{Slug: strings.ToLower(id)}); err != nil {
			t.Fatal(err)
		}
	}
	r.phases[r.fx.Ticket] = phase.Ticket{Current: phase.Build, Ready: true}
	r.d.Derive = func(_ *store.Store, id string) (phase.Ticket, error) {
		p, ok := r.phases[id]
		if !ok {
			return phase.Ticket{}, fmt.Errorf("no phase set for %s", id)
		}
		return p, nil
	}
	return r
}

// prompt is the context the session hears for a prompt of this text.
func (r *phaseRig) prompt(text string) string {
	return r.handle(r.ev(KindPrompt, func(e *Event) { e.Prompt = text })).Context
}

func TestAPromptNamingATicketHearsItsPhaseOncePerPhase(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.handle(r.ev(KindSessionStart))
	first := r.prompt("go on with JIG-1")
	for _, want := range []string{"JIG-1 is at its build phase, ready", "`jig skill build`", "`jig next JIG-1`"} {
		if !strings.Contains(first, want) {
			t.Errorf("the note lacks %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "works on no ticket") {
		t.Errorf("a prompt naming a ticket heard the fresh-work note:\n%s", first)
	}
	if again := r.prompt("and JIG-1 again"); strings.Contains(again, "jig skill build") {
		t.Errorf("the note was told twice:\n%s", again)
	}
	r.phases["JIG-1"] = phase.Ticket{Current: phase.Review, Ready: false, Reason: "a round is open"}
	changed := r.prompt("JIG-1?")
	for _, want := range []string{"JIG-1 is at its review phase, not ready (a round is open)", "`jig skill review`"} {
		if !strings.Contains(changed, want) {
			t.Errorf("after the phase changed, the note lacks %q:\n%s", want, changed)
		}
	}
	if refs := r.ledgerOf("note-delivered"); len(refs) != 2 || refs[0].Ref != "phase:JIG-1:build" || refs[1].Ref != "phase:JIG-1:review" {
		t.Errorf("note-delivered lines = %+v, want phase:JIG-1:build then phase:JIG-1:review", refs)
	}
}

func TestAPromptNamingNoTicketOfTheStoreKeepsTheFreshWorkNote(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.handle(r.ev(KindSessionStart))
	// JIG-99 is no ticket of the store, FOO-1 has no declared key, and a word
	// that merely resembles a ticket (xJIG-1, JIG-1a) names none.
	got := r.prompt("fix JIG-99, FOO-1, xJIG-1 and JIG-1a please")
	if !strings.Contains(got, "works on no ticket") || strings.Contains(got, "jig skill build") {
		t.Errorf("context = %s\nwant the fresh-work note alone", got)
	}
}

func TestADoneTicketNamedInAPromptHearsNoNoteAndNoFreshWork(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.phases["JIG-1"] = phase.Ticket{Current: phase.Publish, Code: phase.CodeDone}
	r.handle(r.ev(KindSessionStart))
	if got := r.prompt("what became of JIG-1"); got != "" {
		t.Errorf("a done ticket named in a prompt: context = %s, want none", got)
	}
}

func TestAPromptHearsAtMostThreeTicketsInTheOrderNamed(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t, "JIG-2", "JIG-3", "JIG-4")
	for _, id := range []string{"JIG-2", "JIG-3", "JIG-4"} {
		r.phases[id] = phase.Ticket{Current: phase.Review, Ready: true}
	}
	r.handle(r.ev(KindSessionStart))
	got := r.prompt("jig-3 then JIG-1, JIG-3 again, JIG-2 and JIG-4")
	i3, i1, i2 := strings.Index(got, "JIG-3 is"), strings.Index(got, "JIG-1 is"), strings.Index(got, "JIG-2 is")
	if i3 < 0 || i1 < i3 || i2 < i1 || strings.Contains(got, "JIG-4 is") {
		t.Errorf("context = %s\nwant JIG-3, JIG-1, JIG-2 in that order and no JIG-4", got)
	}
}

func TestAPromptOnATicketsBranchHearsTheNamedTicketsToo(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t, "JIG-2")
	r.phases["JIG-2"] = phase.Ticket{Current: phase.Brief, Ready: true}
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	got := r.prompt("is JIG-2 ready for review?")
	if !strings.Contains(got, "JIG-2 is at its brief phase") || !strings.Contains(got, "`jig skill intake`") {
		t.Errorf("context = %s\nwant JIG-2's brief note and the intake skill", got)
	}
}

func TestADoneTicketsNamedFirstDoNotUseThePlacesOfALiveOne(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t, "JIG-2", "JIG-3", "JIG-4")
	for _, id := range []string{"JIG-2", "JIG-3", "JIG-4"} {
		r.phases[id] = phase.Ticket{Current: phase.Publish, Code: phase.CodeDone}
	}
	r.phases["JIG-3"] = phase.Ticket{Current: phase.Publish, Code: phase.CodeClosed}
	r.handle(r.ev(KindSessionStart))
	got := r.prompt("like JIG-2, JIG-3 and JIG-4 did, carry on with JIG-1")
	if !strings.Contains(got, "JIG-1 is at its build phase") || strings.Contains(got, "works on no ticket") {
		t.Errorf("context = %s\nwant JIG-1's note though three done or closed tickets come first", got)
	}
}

func TestATicketWhoseAliasIsNamedHearsItsNoteOnceUnderItsId(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	if err := r.st.CreateTicketRecord("JIG-2", store.Ticket{Slug: "two", Aliases: []string{"JIG-9"}}); err != nil {
		t.Fatal(err)
	}
	r.phases["JIG-2"] = phase.Ticket{Current: phase.Review, Ready: true}
	r.handle(r.ev(KindSessionStart))
	got := r.prompt("JIG-9 and JIG-2")
	if strings.Count(got, "JIG-2 is at its review phase") != 1 || strings.Contains(got, "works on no ticket") {
		t.Errorf("context = %s\nwant one note for JIG-2", got)
	}
}

func TestAClosedTicketHearsNoNote(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.phases["JIG-1"] = phase.Ticket{Current: phase.Build, Code: phase.CodeClosed}
	r.handle(r.ev(KindSessionStart))
	if got := r.prompt("what became of JIG-1"); got != "" {
		t.Errorf("a closed ticket named in a prompt: context = %s, want none", got)
	}
}

func TestAPublishPhaseNoteNamesTheDraftedSkillAndATicketWithoutAPhaseCostsOnlyItself(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t, "JIG-2")
	r.phases["JIG-1"] = phase.Ticket{Current: phase.Publish, Ready: true}
	delete(r.phases, "JIG-2") // Derive fails for JIG-2
	r.handle(r.ev(KindSessionStart))
	// The failure is reported, and costs JIG-2 its note alone.
	res, _ := Handle(r.d, r.ev(KindPrompt, func(e *Event) { e.Prompt = "JIG-2 then JIG-1" }))
	got := res.Context
	if !strings.Contains(got, "JIG-1 is at its publish phase, ready: use the publish skill (`jig skill publish`)") || strings.Contains(got, "JIG-2 is") {
		t.Errorf("context = %s\nwant JIG-1's publish note alone", got)
	}
}

func TestTicketsAlreadyHeardDoNotUseThePlacesOfANewOne(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t, "JIG-2", "JIG-3", "JIG-4")
	for _, id := range []string{"JIG-2", "JIG-3", "JIG-4"} {
		r.phases[id] = phase.Ticket{Current: phase.Review, Ready: true}
	}
	r.handle(r.ev(KindSessionStart))
	first := r.prompt("JIG-1, JIG-2 and JIG-3")
	for _, id := range []string{"JIG-1", "JIG-2", "JIG-3"} {
		if !strings.Contains(first, id+" is at its") {
			t.Fatalf("the first prompt lacks %s's note:\n%s", id, first)
		}
	}
	got := r.prompt("JIG-1, JIG-2, JIG-3 and now JIG-4")
	if !strings.Contains(got, "JIG-4 is at its review phase") {
		t.Errorf("context = %s\nwant JIG-4's note though three heard tickets come first", got)
	}
	for _, id := range []string{"JIG-1", "JIG-2", "JIG-3"} {
		if strings.Contains(got, id+" is at its") {
			t.Errorf("%s's note was told twice:\n%s", id, got)
		}
	}
}
