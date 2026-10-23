package correct

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func TestBriefDriftNamesTheVerifiedSlicesWhoseSectionsChanged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	slices, err := w.st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	verified := map[string]bool{"a": true, "b": true}

	if got := BriefDrift([]byte(testBrief), slices, verified); got != nil {
		t.Errorf("an unchanged brief drifts: %+v", got)
	}
	edited := strings.Replace(testBrief, "Fix Clamp.", "Fix Clamp and its callers.", 1)
	got := BriefDrift([]byte(edited), slices, verified)
	want := []Drift{{Slice: "a", Gone: []string{w.hash["Slice A - clamp"]}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a changed section of slice a = %+v, want %+v", got, want)
	}
	if got := BriefDrift([]byte(edited), slices, map[string]bool{"b": true}); got != nil {
		t.Errorf("the changed section belongs to a slice that is not verified: %+v, want no drift", got)
	}
	// A section that is gone is a section that changed; a section added drifts from nothing.
	if got := BriefDrift([]byte(testBrief+"\n## Extra\n\nmore\n"), slices, verified); got != nil {
		t.Errorf("a new section drifts: %+v", got)
	}
	if got := BriefDrift([]byte("# no sections at all\n"), slices, verified); len(got) != 2 {
		t.Errorf("a brief without sections = %+v, want both verified slices to have drifted", got)
	}
	// A slice that cites no section (a gate fix slice) cannot drift.
	free := []store.Slice{{ID: "fix-1"}}
	if got := BriefDrift([]byte(edited), free, map[string]bool{"fix-1": true}); got != nil {
		t.Errorf("a slice that cites nothing drifts: %+v", got)
	}
}

func TestVerifiedSlicesStopBeingVerifiedWhenRequeued(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		{Slice: "a", Event: journal.EventVerified},
		{Slice: "b", Event: journal.EventVerified},
		{Slice: "b", Event: journal.EventRequeue},
		{Slice: "c", Event: journal.EventOracle, Outcome: "pass"},
		{Slice: "d", Event: journal.EventVerified},
		{Slice: "d", Event: journal.EventRequeue},
		{Slice: "d", Event: journal.EventVerified},
	}
	want := map[string]bool{"a": true, "d": true}
	if got := Verified(lines); !reflect.DeepEqual(got, want) {
		t.Errorf("Verified = %v, want %v", got, want)
	}
}

func TestDriftRefIsTheSameForTheSameDriftAndTheTextNamesTheRemedy(t *testing.T) {
	t.Parallel()
	d := []Drift{{Slice: "a", Gone: []string{"h1", "h2"}}, {Slice: "b", Gone: []string{"h3"}}}
	if driftRef(d) != driftRef([]Drift{d[0], d[1]}) || driftRef(d) == driftRef(d[:1]) {
		t.Errorf("driftRef does not follow what drifted: %q, %q", driftRef(d), driftRef(d[:1]))
	}
	for _, want := range []string{"slices a, b cite 3 sections", "jig requeue JIG-1 --from-brief-diff", "queues them again"} {
		if got := driftText("JIG-1", d); !strings.Contains(got, want) {
			t.Errorf("driftText = %q, want %q", got, want)
		}
	}
	if got := driftText("JIG-1", d[1:]); !strings.Contains(got, "slice b cites 1 section the brief") || !strings.Contains(got, "queues it again") {
		t.Errorf("driftText for one slice = %q", got)
	}
}
