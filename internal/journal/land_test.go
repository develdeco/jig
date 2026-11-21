package journal

import (
	"testing"
)

// TestLandJournalLineIsPerIssue: a landed line carries its repo, pull request,
// base, container, merge commit and provenance through the journal, and ReadFor
// keeps a shared task's landed line for its own issue alone.
func TestLandJournalLineIsPerIssue(t *testing.T) {
	t.Parallel()
	st := sharedStore(t, map[string]string{
		"a": "issue: I-1\ntickets:\n  - id: T-1\n    title: t\n",
		"b": "issue: I-2\ntickets:\n  - id: T-1\n    title: t\n",
	})
	want := Line{Event: EventLanded, Repo: "app", URL: "https://github.com/acme/app/pull/1", Base: "jig/I-1", Into: "I-1", Commit: "abc", Provenance: ProvenanceAgent}
	for _, l := range []Line{want, {Event: EventLanded, Into: "I-2", Provenance: ProvenanceHost}} {
		if err := Append(st, "T-1", l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadFor(st, "T-1", "I-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ReadFor(I-1) = %+v, %v, want one landed line", got, err)
	}
	g := got[0]
	if g.Event != EventLanded || g.Repo != want.Repo || g.URL != want.URL || g.Base != want.Base || g.Into != want.Into || g.Commit != want.Commit || g.Provenance != want.Provenance {
		t.Errorf("line = %+v, want %+v", g, want)
	}
	if other, _ := ReadFor(st, "T-1", "I-2"); len(other) != 1 || other[0].Provenance != ProvenanceHost {
		t.Errorf("ReadFor(I-2) = %+v, want its own line", other)
	}
	if IsPublishEvent(EventLanded) {
		t.Error("a landed line is not a publish line")
	}
}
