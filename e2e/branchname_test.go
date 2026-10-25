package e2e

// End-to-end tests of branch names (digester decision 31): a ticket's branch is
// <id>-<slug>, led by the GitHub issue number once the mirror has given the
// ticket one; the name binds a branch to its ticket with no claim; and a branch
// a session chose for itself is recorded by its claim and is the one publish
// works on.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// writeTicketYAML writes ticket's ticket.yaml in the fixture store, as a store
// of tickets from before the slug field would have it (a title, no slug).
func writeTicketYAML(t *testing.T, fx *fixture.Fixture, ticket, content string) {
	t.Helper()
	dir := filepath.Join(fx.StoreDir, "tickets", ticket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(dir, "ticket.yaml"), []byte(content))
}

// writeIssueRecord records, as the GitHub mirror does, that ticket's issue in
// the tracking repo is number issue.
func writeIssueRecord(t *testing.T, fx *fixture.Fixture, ticket string, issue int) {
	t.Helper()
	dir := filepath.Join(fx.StoreDir, "tickets", ticket, "tracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	n := strconv.Itoa(issue)
	record := "# This file is written by jig's GitHub mirror; edits here are overwritten.\nrepo: acme/tracking\nissue: " + n + "\nnode_id: I_" + n + "\n"
	writeFileOrFatal(t, filepath.Join(dir, "github.yaml"), []byte(record))
}

// ticketYAML reads ticket's ticket.yaml from the fixture store.
func ticketYAML(t *testing.T, fx *fixture.Fixture, ticket string) string {
	t.Helper()
	return string(readFileOrFatal(t, filepath.Join(fx.StoreDir, "tickets", ticket, "ticket.yaml")))
}

// TestNextNamesGitHubBranch: a ticket the mirror gave GitHub issue 102 is told
// to branch as GitHub's own "create a branch" would name it, the issue number
// and the slug of the title. The name is derived, never written to the
// record, so the ticket is not marked as one that adopted a branch.
func TestNextNamesGitHubBranch(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	writeTicketYAML(t, fx, fx.Ticket, "schema_version: 1\ntitle: Upload item gallery\n")
	writeIssueRecord(t, fx, fx.Ticket, 102)
	const want = "102-upload-item-gallery"

	n := nextJSON[nextTicketOut](t, env, fx.RepoDir, fx.Ticket)
	if n.Branch != want {
		t.Errorf("jig next --json: branch = %q, want %q", n.Branch, want)
	}
	r := runJig(t, env, fx.RepoDir, "next", fx.Ticket)
	for _, line := range []string{"  branch: " + want + "\n", "git switch -c " + want} {
		if !strings.Contains(r.Stdout, line) {
			t.Errorf("jig next lacks %q:\n%s", line, r.Stdout)
		}
	}
	if rec := ticketYAML(t, fx, fx.Ticket); strings.Contains(rec, "branch:") {
		t.Errorf("naming the branch wrote it to ticket.yaml, which would mark the ticket adopted:\n%s", rec)
	}
}

// TestNoTrackerStoreIDBranch: with no tracker to name branches, a ticket's
// branch leads with its store id. A ticket minted by `jig ticket new` carries
// its slug from then on, so editing the title later does not rename the
// branch; a ticket that is a tracker's but not yet mirrored leads the same
// way.
func TestNoTrackerStoreIDBranch(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	id := mintTicket(t, env, fx.StoreDir, "Upload item gallery", "")
	if id != "JIG-2" {
		t.Fatalf("test setup: the minted ticket is %s, want JIG-2 after the fixture's JIG-1", id)
	}
	if rec := ticketYAML(t, fx, id); !strings.Contains(rec, "\nslug: upload-item-gallery\n") {
		t.Errorf("jig ticket new did not record the slug:\n%s", rec)
	}
	const want = "JIG-2-upload-item-gallery"
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, id); n.Branch != want {
		t.Errorf("jig next --json %s: branch = %q, want %q", id, n.Branch, want)
	}

	// The title is edited afterwards: the branch keeps its name.
	writeTicketYAML(t, fx, id, "schema_version: 1\ntitle: A better title\nslug: upload-item-gallery\n")
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, id); n.Branch != want {
		t.Errorf("after a retitle: branch = %q, want it unchanged, %q", n.Branch, want)
	}

	// The ticket the fixture holds has no title and no slug: its brief's
	// heading names the branch; with no brief either it is named by its id,
	// and not by the jig/ prefix every ticket once had.
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, fx.Ticket); n.Branch != fx.Branch {
		t.Errorf("a ticket with a brief and no title: branch = %q, want %s", n.Branch, fx.Branch)
	}
	if err := os.Remove(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "brief.md")); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, fx.Ticket); n.Branch != fx.Ticket {
		t.Errorf("a ticket with nothing to name a branch after: branch = %q, want %s", n.Branch, fx.Ticket)
	}

	// The branch binds to the ticket by its store id.
	h := newHarness(t, fx, env, "s-binds")
	h.Branch(want)
	if got := hereJSON(t, env.without("JIG_SLICE", "JIG_SESSION"), fx.RepoDir); got.Ticket.ID != id || got.Ticket.How != "branch" {
		t.Errorf("jig here on %s: ticket = %+v, want %s by branch", want, got.Ticket, id)
	}
}

// TestBranchesFromNamesWhichTrackerLeads: the jig.yaml marker says which
// tracker's ids lead a project's branches. Unset or github, a mirrored ticket's
// branch leads with its issue number; naming another tracker leaves the store's
// id to lead it, even for a ticket GitHub holds.
func TestBranchesFromNamesWhichTrackerLeads(t *testing.T) {
	t.Parallel()
	env := projEnv(t)
	parent := t.TempDir()
	repo := projGitRepo(t, filepath.Join(parent, "myrepo"))
	projRun(t, env, repo, "init")
	id := mintTicket(t, env, repo, "Fix the thing", "")
	if id != "MYREPO-1" {
		t.Fatalf("test setup: the minted ticket is %s, want MYREPO-1", id)
	}
	// The mirror's record, as it leaves it in the store.
	recordDir := filepath.Join(parent, "myrepo-tickets", "tickets", id, "tracker")
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(recordDir, "github.yaml"), []byte("repo: acme/tracking\nissue: 41\nnode_id: I_41\n"))

	branchOf := func() string {
		t.Helper()
		return nextJSON[nextTicketOut](t, env, repo, id).Branch
	}
	if got := branchOf(); got != "41-fix-the-thing" {
		t.Errorf("no tracker named: branch = %q, want 41-fix-the-thing", got)
	}
	for from, want := range map[string]string{"github": "41-fix-the-thing", "jira": "MYREPO-1-fix-the-thing"} {
		m := projMarker(t, repo)
		m.Branches = &project.BranchRule{From: from}
		if err := project.WriteMarker(repo, m); err != nil {
			t.Fatal(err)
		}
		if got := branchOf(); got != want {
			t.Errorf("branches: from: %s: branch = %q, want %q", from, got, want)
		}
	}
}

// TestBranchBindsWithoutClaim: a branch named for a ticket by its store id, or
// for the issue the mirror gave it, is on that ticket the moment it is checked
// out: nothing was claimed, no branch was recorded, and the verbs that take a
// ticket find it from the branch alone.
func TestBranchBindsWithoutClaim(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	writeTicketYAML(t, fx, fx.Ticket, "schema_version: 1\ntitle: Upload item gallery\n")
	writeIssueRecord(t, fx, fx.Ticket, 102)
	h := newHarness(t, fx, env, "s-binds")

	for _, branch := range []string{"102-upload-item-gallery", "102-something-else-entirely", "JIG-1-upload-item-gallery", "jig/JIG-1"} {
		h.Branch(branch)
		got := hereJSON(t, env, fx.RepoDir)
		if got.Repo.Branch != branch || got.Ticket.ID != fx.Ticket || got.Ticket.How != "branch" {
			t.Errorf("jig here on %s: repo = %+v, ticket = %+v; want JIG-1 bound by the branch", branch, got.Repo, got.Ticket)
		}
	}

	// A verb that writes finds the ticket the same way.
	h.Branch("102-upload-item-gallery")
	if r := h.Jig("note", "found by the branch alone"); r.Code != 0 || !strings.Contains(r.Stdout, "ticket: "+fx.Ticket) {
		t.Fatalf("jig note on the issue-numbered branch: exit %d, want 0 naming %s\nstdout:\n%s\nstderr:\n%s", r.Code, fx.Ticket, r.Stdout, r.Stderr)
	}
	var noted, claimed bool
	for _, l := range readJournal(t, fx, fx.Ticket) {
		noted = noted || l.Event == journal.EventNote
		claimed = claimed || l.Event == journal.EventClaim
	}
	if !noted || claimed {
		t.Errorf("journal: note line %v, claim line %v; want a note and no claim", noted, claimed)
	}
	if rec := ticketYAML(t, fx, fx.Ticket); strings.Contains(rec, "branch:") {
		t.Errorf("binding recorded a branch in ticket.yaml:\n%s", rec)
	}

	// A branch that leads with no ticket binds nothing, and neither does an
	// issue number the mirror never gave a ticket.
	for _, branch := range []string{"gallery-work", "103-another-issue", "feature/102-upload"} {
		h.Branch(branch)
		if got := hereJSON(t, env, fx.RepoDir); got.Ticket.ID != "" {
			t.Errorf("jig here on %s: ticket = %+v, want none", branch, got.Ticket)
		}
	}
}

// TestOldJigBranchBindsViaAlias: a branch begun as jig/<id> before a migration
// renamed its ticket still binds to the ticket, through the alias the migration
// left, with or without the old prefix and a slug after it.
func TestOldJigBranchBindsViaAlias(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	writeTicketYAML(t, fx, fx.Ticket, "schema_version: 1\ntitle: Upload item gallery\naliases:\n  - OLD-7\n")
	h := newHarness(t, fx, env, "s-old")

	for _, branch := range []string{"jig/OLD-7", "jig/OLD-7-follow-up", "OLD-7-upload-item-gallery"} {
		h.Branch(branch)
		got := hereJSON(t, env, fx.RepoDir)
		if got.Ticket.ID != fx.Ticket || got.Ticket.How != "branch-alias" {
			t.Errorf("jig here on %s: ticket = %+v, want %s through its alias", branch, got.Ticket, fx.Ticket)
		}
	}
	h.Branch("jig/" + fx.Ticket)
	if got := hereJSON(t, env, fx.RepoDir); got.Ticket.ID != fx.Ticket || got.Ticket.How != "branch" {
		t.Errorf("jig here on jig/%s: ticket = %+v, want it by its own id", fx.Ticket, got.Ticket)
	}
}

// TestClaimRecordsABranchThatNamesNoTicket: a session that starts on a branch
// of its own, which says nothing about a ticket, has the claim record that
// branch as the ticket's. A branch that names a ticket (this one's or another's),
// the target branch and a ticket that already has a branch are left as they are.
func TestClaimRecordsABranchThatNamesNoTicket(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	other := mintTicket(t, env, fx.StoreDir, "Another ticket", "")
	h := newHarness(t, fx, env, "s-claims")

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	claim := func(ticket, slice string) jigResult {
		t.Helper()
		r := h.Jig("claim", ticket, "--slice", slice)
		if r.Code != 0 {
			t.Fatalf("jig claim %s --slice %s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", ticket, slice, r.Code, r.Stdout, r.Stderr)
		}
		return r
	}
	recorded := func() string {
		t.Helper()
		rec, err := st.ReadTicket(fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		return rec.Branch
	}

	// Branches that are, or name, something of the ticket's own: not recorded.
	for _, branch := range []string{"main", "JIG-1-upload-item-gallery", "jig/JIG-1", other + "-another-ticket"} {
		h.Branch(branch)
		claim(fx.Ticket, "a")
		if got := recorded(); got != "" {
			t.Fatalf("a claim on %s recorded %q as the ticket's branch", branch, got)
		}
		if r := h.Jig("claim", fx.Ticket, "--slice", "a", "--release"); r.Code != 0 {
			t.Fatalf("jig claim --release exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
		}
	}

	// A branch of the session's own is.
	h.Branch("gallery-work")
	r := claim(fx.Ticket, "a")
	if rec := ticketYAML(t, fx, fx.Ticket); !strings.Contains(rec, "\nbranch: gallery-work\n") {
		t.Fatalf("a claim on gallery-work did not record it:\n%s", rec)
	}
	if !strings.Contains(r.Stdout, "Branch gallery-work names no ticket") {
		t.Errorf("the claim does not say it recorded the branch:\n%s", r.Stdout)
	}
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, fx.Ticket); n.Branch != "gallery-work" {
		t.Errorf("jig next: branch = %q, want the recorded gallery-work", n.Branch)
	}

	// The record is the ticket's: a later claim on another branch does not replace it.
	h.Branch("other-work")
	claim(fx.Ticket, "c")
	if rec := ticketYAML(t, fx, fx.Ticket); !strings.Contains(rec, "\nbranch: gallery-work\n") {
		t.Errorf("a second claim replaced the recorded branch:\n%s", rec)
	}
}

// TestPublishBodyTargetsRecordedName: the branch a session claimed on is the one
// the review and the pull request body name, whatever name the ticket would
// have had by default. The body is rendered for the recorded branch's reviewed
// head, journaled with that branch, and no branch of the default's name is made.
func TestPublishBodyTargetsRecordedName(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dropEnvFromSlices(t, fx)
	writeTicketYAML(t, fx, fx.Ticket, "schema_version: 1\ntitle: Greet people\nslug: greet-people\n")
	h := newHarness(t, fx, env, "s-builder")
	const recordedName = "gallery-work"
	h.Branch(recordedName)
	if r := h.Jig("claim", fx.Ticket, "--slice", "a"); r.Code != 0 {
		t.Fatalf("jig claim exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	// Publish takes a whole ticket: every slice is built and verified, as in
	// newBuiltTicket (slice b's first attempt fails its test and is never
	// verified).
	type built struct{ slice, sha string }
	steps := []built{{"a", h.CommitScenario("a", 1)}}
	h.CommitScenario("b", 1)
	steps = append(steps, built{"b", h.CommitScenario("b", 2)}, built{"c", h.CommitScenario("c", 2)}, built{"d", h.CommitScenario("d", 1)})
	sha := steps[len(steps)-1].sha
	func() {
		// Each verification is a `go test` in a worktree, and the suite runs
		// its tests in parallel (verifySlots).
		verifySlots <- struct{}{}
		defer func() { <-verifySlots }()
		for _, s := range steps {
			res := h.Jig("verify", fx.Ticket, s.sha, "--slice", s.slice)
			if res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
				t.Fatalf("jig verify %s exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", s.slice, res.Code, res.Stdout, res.Stderr)
			}
		}
		for _, s := range steps {
			l := waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
				return l.Slice == s.slice && ((l.Event == journal.EventVerified && l.Commit == s.sha) || (l.Event == journal.EventOracle && l.Outcome == "fail"))
			})
			if l.Event != journal.EventVerified {
				t.Fatalf("slice %s failed its oracle at %s: %s", s.slice, s.sha, l.Summary)
			}
		}
		h.WaitVerifiers()
	}()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	b := builtTicket{whole: true, fx: fx, env: env, builder: h, branch: recordedName, sha: sha, st: st}

	// The default would have been the other name; the claim's record wins.
	if got, err := st.TicketBranch(fx.Ticket, "main"); err != nil || got != recordedName {
		t.Fatalf("TicketBranch = %q, %v; want the recorded %s", got, err, recordedName)
	}
	const defaultName = "JIG-1-greet-people"
	if !strings.Contains(ticketYAML(t, fx, fx.Ticket), "\nbranch: "+recordedName+"\n") {
		t.Fatalf("the claim did not record the branch:\n%s", ticketYAML(t, fx, fx.Ticket))
	}

	b.openRound(t)
	b.reviewedClean(t)
	body := runJig(t, env, h.Clone, "publish", fx.Ticket, "--body")
	if body.Code != 0 || !strings.Contains(body.Stdout, "branch: "+recordedName+"\n") || !strings.Contains(body.Stdout, "head: "+sha) {
		t.Fatalf("jig publish --body exit = %d, want 0, branch %s and the reviewed head\nstdout:\n%s\nstderr:\n%s", body.Code, recordedName, body.Stdout, body.Stderr)
	}
	var bodyLine journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventPrBody {
			bodyLine = l
		}
	}
	if bodyLine.Branch != recordedName || bodyLine.Commit != sha {
		t.Errorf("pr-body line = %+v, want commit %s on %s, not %s", bodyLine, sha, recordedName, defaultName)
	}
	if text := string(readFileOrFatal(t, filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md"))); !strings.Contains(text, sha[:7]) {
		t.Errorf("pr/fixture-repo.md does not name the reviewed head %s:\n%s", sha[:7], text)
	}
	if got := originRefOrEmpty(t, fx.RepoRemote, "refs/heads/"+defaultName); got != "" {
		t.Errorf("origin has the default-named branch %s: %s", defaultName, got)
	}
}
