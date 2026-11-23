package observe

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// callFiles is a call of agent that touched files, as an editing tool's is.
func (r *rig) callFiles(agent, tool string, files ...string) string {
	r.t.Helper()
	r.handle(r.ev(KindTool, func(e *Event) { e.Agent, e.Tool, e.Files = agent, tool, files }))
	lines := r.ledgerOf(journal.EventTool)
	return lines[len(lines)-1].Ticket
}

// TestAPlannerCallBindsByTheTicketItNames: a drafter's call is on a ticket
// when it names it: a jig verb's ticket word, or a path under the store's
// tickets/<T>/ folder, in the command or among the files a tool touched. A
// read of code names nothing, and the calls after the work are on none.
func TestAPlannerCallBindsByTheTicketItNames(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))
	dir := filepath.ToSlash(filepath.Join(r.fx.StoreDir, "tickets", r.fx.Ticket))

	expectTicket(t, "a jig verb's ticket word", r.call("", "jig validate "+r.fx.Ticket), r.fx.Ticket)
	expectTicket(t, "a write of brief.md", r.callFiles("", "Write", filepath.FromSlash(dir+"/brief.md")), r.fx.Ticket)
	expectTicket(t, "an edit of slices.yaml", r.callFiles("", "Edit", filepath.FromSlash(dir+"/slices.yaml")), r.fx.Ticket)
	expectTicket(t, "a command that names a path there", r.call("", "cat "+dir+"/brief.md"), r.fx.Ticket)
	expectTicket(t, "a read of code", r.callFiles("", "Read", filepath.Join(r.fx.RepoDir, "go.mod")), "")
	expectTicket(t, "a path under another folder named alike", r.call("", "cat "+filepath.ToSlash(r.fx.Dir)+"/other/tickets/"+r.fx.Ticket+"/brief.md"), "")
	expectTicket(t, "the calls after the work", r.call("", "git log"), "")
}

// TestAGraduateOfSeveralTasksBindsNoneOfThemToLaterCalls: a verb that ties a
// session to several tickets starts no role; the calls after it that name
// none are on none.
func TestAGraduateOfSeveralTasksBindsNoneOfThemToLaterCalls(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if err := r.st.CreateTicketRecord("JIG-2", store.Ticket{Slug: "two"}); err != nil {
		t.Fatal(err)
	}
	r.handle(r.ev(KindSessionStart))
	for _, id := range []string{r.fx.Ticket, "JIG-2"} {
		if err := ledger.TouchSession(r.fx.Home, r.clock.Now(), ledger.Registration{ID: "s1", Ticket: id}); err != nil {
			t.Fatal(err)
		}
	}
	expectTicket(t, "the graduate call", r.call("", "jig graduate some-chart"), "")
	expectTicket(t, "a later call", r.call("", "go test ./..."), "")
	expectTicket(t, "a later read of the brief", r.call("", "jig skill intake"), "")
}

// TestAnIssueMintStartsNoRole: `jig ticket new` of an issue ties the session
// to it and no approval ever comes; the calls after that name none are on none.
func TestAnIssueMintStartsNoRole(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))
	if err := ledger.TouchSession(r.fx.Home, r.clock.Now(), ledger.Registration{ID: "s1", Ticket: r.fx.Ticket}); err != nil {
		t.Fatal(err)
	}
	expectTicket(t, "the mint", r.call("", `jig ticket new --title "An issue"`), "")
	expectTicket(t, "a later call", r.call("", "ls"), "")
}
