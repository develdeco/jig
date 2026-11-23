package phase

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestSubStepsNameWhoTheyWaitFor: a sub-step that waits for the person (an
// approval, an answer, a decision, a refusal to act on) says so, an approval
// still says so once given (the time it stood was a wait), and a landing's
// merge waits for the host; a sub-step that waits on other slices, on its
// environment, or on the builder to push has no one it waits for.
func TestSubStepsNameWhoTheyWaitFor(t *testing.T) {
	t.Parallel()
	published, lines := publishedWithPR()

	unapproved := approved()
	unapproved.Approval = ApprovalNone
	changed := approved()
	changed.Approval = ApprovalChanged
	asked := allGreen()
	asked.Asks = []Ask{{ID: "r1-f1"}}
	decided := allGreen()
	decided.Asks = []Ask{{ID: "r1-f1", Decision: "keep", DecidedBy: "person", Pending: true}}
	parked := approved()
	parked.Slices = []Slice{{ID: "a", State: "needs-input", OpenQuestion: true}, {ID: "b", State: "queued", BlockedBy: []string{"a"}}, {ID: "c", State: "env-blocked"}, {ID: "d", State: "building"}}
	closed := published
	closed.Closed = "not planned"
	hosted := published
	hosted.MergedOnHost = true

	for _, c := range []struct {
		name  string
		facts Facts
		lines []journal.Line
		phase Name
		sub   string
		want  string
	}{
		{"a drafted brief waits for its approval", unapproved, nil, Brief, "approved", WaitsForPerson},
		{"a brief changed after its approval waits for another", changed, nil, Brief, "approved", WaitsForPerson},
		{"an approval given was a wait", approved(), nil, Brief, "approved", WaitsForPerson},
		{"a brief not yet drafted has no approval to wait for", Facts{Ticket: tk}, nil, Brief, "approved", ""},
		{"the brief's drafting is work", unapproved, nil, Brief, "drafted", ""},
		{"a slice with an open question waits for the answer", parked, nil, Build, "slice:a", WaitsForPerson},
		{"a slice that waits on another slice waits for no one", parked, nil, Build, "slice:b", ""},
		{"a slice that waits on its environment waits for no one", parked, nil, Build, "slice:c", ""},
		{"a slice being built is work", parked, nil, Build, "slice:d", ""},
		{"asks to decide wait for the person", asked, built(), Review, "asks", WaitsForPerson},
		{"asks decided are left to the next gate", decided, built(), Review, "asks", ""},
		{"a refused round waits for the person", allGreen(), append(built(), refusedLine("INTENT_CONFLICT", "x")), Review, "round:1", WaitsForPerson},
		{"a refusal the builder clears waits for no one", allGreen(), append(built(), refusedLine("BRANCH_NOT_FOUND", "x")), Review, "round:1", ""},
		{"an agent's publish approval leaves the person's owed", allGreen(), append(built(), ln(journal.EventGateClean, commit("abcdef0123")), ln(journal.EventPrBody, commit("abcdef0123")), ln(journal.EventPublishApproved, provenance("agent"))), Publish, "approval", WaitsForPerson},
		{"a publish approval given was a wait", allGreen(), append(built(), ln(journal.EventGateClean, commit("abcdef0123")), ln(journal.EventPrBody, commit("abcdef0123")), ln(journal.EventPublishApproved, provenance("person"))), Publish, "approval", WaitsForPerson},
		{"the body is work", allGreen(), append(built(), ln(journal.EventGateClean, commit("abcdef0123")), ln(journal.EventPrBody, commit("abcdef0123"))), Publish, "body", ""},
		{"a landing waits for the host's merge", published, lines, Land, "merge", WaitsForHost},
		{"a merge made on the host was waited for", hosted, lines, Land, "merge", WaitsForHost},
		{"the record of a merge is work", hosted, lines, Land, "record", ""},
		{"a landing not yet published has no merge to wait for", allGreen(), built(), Land, "merge", ""},
		{"a closed ticket waits for nothing", closed, lines, Land, "merge", ""},
	} {
		d := Derive(c.facts, c.lines)
		var got *Sub
		for _, s := range d.Of(c.phase).Subs {
			if s.Key == c.sub {
				got = &s
			}
		}
		switch {
		case got == nil && c.want == "":
			// A phase with no such sub-step waits for no one either.
		case got == nil:
			t.Errorf("%s: %s has no sub-step %q: %v", c.name, c.phase, c.sub, subKeys(d.Of(c.phase)))
		case got.WaitsFor != c.want:
			t.Errorf("%s: %s %s (%s) waits for %q, want %q", c.name, c.phase, c.sub, got.State, got.WaitsFor, c.want)
		}
	}
}
