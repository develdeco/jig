package observe

import (
	"testing"

	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// TestLandPhaseIsNeverNamedAsMoreWork: a published ticket that has not landed
// is at Land with the code PHASE_DONE, and the phase note the hooks give a
// session skips it as it skips any done ticket: a run ends at published unless
// the person's word is "Land".
func TestLandPhaseIsNeverNamedAsMoreWork(t *testing.T) {
	t.Parallel()
	derive := func(*store.Store, string) (phase.Ticket, error) {
		return phase.Ticket{Current: phase.Land, Code: phase.CodeDone, Prompt: "Land A-7"}, nil
	}
	h := &handler{d: Deps{Derive: derive}}
	if got := h.phaseNotes(sessionMemory{delivered: map[string]bool{}}, []string{"A-7"}); len(got) != 0 {
		t.Errorf("notes = %+v, want none for a ticket at Land", got)
	}
	if phaseSkill[phase.Land] != "land" {
		t.Errorf("the skill of Land = %q, want land", phaseSkill[phase.Land])
	}
}
