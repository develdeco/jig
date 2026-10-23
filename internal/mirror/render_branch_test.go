package mirror

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestRenderTicketBodyNamesTheBranchWhenAnotherTrackerLeadsIt: GitHub finds a
// ticket's branch by its name when the issue's number leads it, so an issue
// says nothing of the branch. A project whose jig.yaml has another tracker's ids
// lead its branches has a branch GitHub cannot find by name; the footer, which
// already carries the store id, names it too.
func TestRenderTicketBodyNamesTheBranchWhenAnotherTrackerLeadsIt(t *testing.T) {
	st := newDemoStore(t)
	if err := st.CreateTicketRecord("DEMO-9", store.Ticket{Title: "Upload item gallery", Slug: "upload-item-gallery"}); err != nil {
		t.Fatal(err)
	}
	cfg := loadCfg(t, st)
	const lead = "---\n<sub>Mirrored from the jig ticket store (`DEMO-9`"
	const tail = "). The store is the source of truth: the sync overwrites edits to this title, body, state, parent, blocked-by links and Status.</sub>\n\n<!-- jig:DEMO-9 -->"

	for _, from := range []string{"", "github"} {
		st.BranchesFrom = from
		got, err := RenderTicketBody(st, cfg, "DEMO-9", nil)
		if err != nil {
			t.Fatalf("RenderTicketBody: %v", err)
		}
		if want := lead + tail; !strings.HasSuffix(got, want) {
			t.Errorf("branches from %q: footer =\n%q\nwant it to end with the footer that names no branch", from, got)
		}
	}

	st.BranchesFrom = "jira"
	got, err := RenderTicketBody(st, cfg, "DEMO-9", nil)
	if err != nil {
		t.Fatalf("RenderTicketBody: %v", err)
	}
	if want := lead + ", branch `DEMO-9-upload-item-gallery`" + tail; !strings.HasSuffix(got, want) {
		t.Errorf("branches from jira: body =\n%q\nwant it to end with\n%q", got, want)
	}

	// A branch recorded for the ticket is the one it names.
	if err := st.WriteTicketBranch("DEMO-9", "feature/gallery"); err != nil {
		t.Fatal(err)
	}
	got, err = RenderTicketBody(st, cfg, "DEMO-9", nil)
	if err != nil {
		t.Fatalf("RenderTicketBody: %v", err)
	}
	if want := lead + ", branch `feature/gallery`" + tail; !strings.HasSuffix(got, want) {
		t.Errorf("with a recorded branch: body =\n%q\nwant it to end with\n%q", got, want)
	}

	// A record whose branch git rejects costs the footer its branch, not the body.
	if err := st.WriteTicketBranch("DEMO-9", "bad..name"); err != nil {
		t.Fatal(err)
	}
	got, err = RenderTicketBody(st, cfg, "DEMO-9", nil)
	if err != nil {
		t.Fatalf("RenderTicketBody with an invalid recorded branch: %v", err)
	}
	if want := lead + tail; !strings.HasSuffix(got, want) {
		t.Errorf("with an invalid recorded branch: body =\n%q\nwant it to end with the footer that names no branch", got)
	}
}
