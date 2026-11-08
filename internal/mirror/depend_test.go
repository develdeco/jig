package mirror

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestWaitsForLineOfADependEdgeMatchesGraduates: an edge written the way
// `jig depend` writes it (SetTicketDep on an existing record) renders the
// same "Waits for" line as the same edges written whole at mint, as `jig
// graduate` writes them, so the mirror needs no change for it.
func TestWaitsForLineOfADependEdgeMatchesGraduates(t *testing.T) {
	st := newDemoStore(t)
	cfg := loadCfg(t, st)
	for _, id := range []string{"DEMO-2", "DEMO-4"} {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: "blocker " + id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateTicketRecord("DEMO-5", store.Ticket{Title: "Graduated", BlockedBy: []store.TicketBlockedBy{
		{Ticket: "DEMO-2", Kind: "merged"}, {Ticket: "DEMO-4", Kind: "stacked"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("DEMO-6", store.Ticket{Title: "Depended"}); err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{"DEMO-2", "merged"}, {"DEMO-4", "stacked"}} {
		if err := st.SetTicketDep("DEMO-6", edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}

	line := func(id string) string {
		t.Helper()
		body, err := RenderTicketBody(st, cfg, id, nil)
		if err != nil {
			t.Fatalf("RenderTicketBody %s: %v", id, err)
		}
		for _, l := range strings.Split(body, "\n") {
			if strings.HasPrefix(l, "**Waits for:**") {
				return l
			}
		}
		t.Fatalf("the body of %s has no Waits for line:\n%s", id, body)
		return ""
	}
	if got, want := line("DEMO-6"), line("DEMO-5"); got != want || got != "**Waits for:** DEMO-2 (merged), DEMO-4 (stacked)" {
		t.Errorf("Waits for of a depend edge = %q, of a graduate edge = %q", got, want)
	}
}
