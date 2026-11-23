package observe

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestABuildersCallsAfterAVerificationStayOnItsClaim: a builder's calls between
// a slice's verification and its next claim are still its ticket's.
func TestABuildersCallsAfterAVerificationStayOnItsClaim(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))
	expectTicket(t, "the claim", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventVerified, Slice: "a", Session: "s1"})
	expectTicket(t, "a call after the verification", r.call("a-1", "git log"), r.fx.Ticket)
}

// TestAMainSessionsClaimEndsAtItsTicketsPublish: a hand-built ticket ends at
// its publish; the main session's later calls in another worktree bind by that
// worktree and in the main checkout to none.
func TestAMainSessionsClaimEndsAtItsTicketsPublish(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	other := r.worktreeOn("JIG-2")
	r.handle(r.ev(KindSessionStart))
	expectTicket(t, "the claim", r.claimCall("", r.fx.Ticket, "a"), r.fx.Ticket)
	expectTicket(t, "a call while it is held", r.call("", "go test ./..."), r.fx.Ticket)
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventPr, Session: "s1"})
	expectTicket(t, "a call in another worktree after the publish", r.call("", "git push", other), "JIG-2")
	expectTicket(t, "a call in the main checkout after it", r.call("", "git log"), "")
}

// TestACdIntoATrackerNumberedWorktreeWalksNoStore: a worktree on a branch that
// leads with a tracker number is bound by that number's one tracker record. A
// walk of the store's tickets would find two owners of the number and bind
// none.
func TestACdIntoATrackerNumberedWorktreeWalksNoStore(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	open := r.d.OpenStore
	r.d.OpenStore = func(w locate.Where) (*store.Store, project.Config, error) {
		st, cfg, err := open(w)
		cfg.GitHub = &project.GitHubTracker{Repo: "acme/shop"}
		return st, cfg, err
	}
	if err := r.st.CreateTicketRecord("JIG-2", store.Ticket{Slug: "two"}); err != nil {
		t.Fatal(err)
	}
	entry := store.TrackerEntry{Provider: store.ProviderGitHub, Scope: "acme/shop", ID: "77"}
	rec := r.st.TrackerRecordPath(entry)
	if err := os.MkdirAll(filepath.Dir(rec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, []byte("ticket: "+r.fx.Ticket+"\nissue: 77\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second ticket's legacy record claims the same number.
	legacy := filepath.Join(r.st.TicketDir("JIG-2"), "tracker", "github.yaml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("repo: acme/shop\nissue: 77\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(r.fx.Dir, "wt-77")
	if _, err := gitx.Run(r.fx.RepoDir, "worktree", "add", "-q", "-b", "77-fix-cart", wt); err != nil {
		t.Fatal(err)
	}
	r.handle(r.ev(KindSessionStart))
	expectTicket(t, "a cd into the numbered worktree", r.call("a-1", "cd "+filepath.ToSlash(wt)+" && git status"), r.fx.Ticket)
}

// TestACompactedSessionsWalkEndsAtItsStartup: past a compact the walk goes on
// to the session's own startup line and no further, so a claim made in an
// earlier life of the id is not read.
func TestACompactedSessionsWalkEndsAtItsStartup(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.start("startup")
	expectTicket(t, "a claim of the earlier life", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
	r.clock.advance(time.Minute)
	r.start("startup")
	r.clock.advance(time.Minute)
	r.start("compact")
	expectTicket(t, "a call after the compact", r.call("a-1", "go test ./..."), "")
}

// TestAClaimWordThatIsNoTicketIsSkipped: a redirect or a flag after `jig
// claim` is no ticket word, so the call binds by what it is run on.
func TestAClaimWordThatIsNoTicketIsSkipped(t *testing.T) {
	t.Parallel()
	if got := claimWord("jig claim 2>&1"); got != "" {
		t.Errorf("claimWord of a redirect = %q, want none", got)
	}
	if got := claimWord("jig claim --slice a JIG-3"); got != "JIG-3" {
		t.Errorf("claimWord after a flag = %q, want JIG-3", got)
	}
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))
	expectTicket(t, "a claim with a redirect", r.call("a-1", "jig claim --slice a 2>&1"), "")
}
