package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// writeBlockedTicket lays ticket JIG-2 into the store: a brief, two slices x
// and y, and a blocker JIG-1 of the given kind. The store's next checkpoint
// commits it.
func writeBlockedTicket(t *testing.T, st *store.Store, kind string) {
	t.Helper()
	dir := st.TicketDir("JIG-2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"ticket.yaml": fmt.Sprintf("schema_version: 1\ntitle: Follows JIG-1\nblocked_by:\n  - ticket: JIG-1\n    kind: %s\n", kind),
		"brief.md":    "# Follows JIG-1\n\n## Goal\n\nBuild x and y.\n",
		"slices.yaml": "slices:\n" +
			"  - id: x\n    workspace: alpha\n    goal: Build x.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n" +
			"  - id: y\n    workspace: alpha\n    goal: Build y.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n",
	}
	for name, content := range files {
		writeFileOrFatal(t, filepath.Join(dir, name), []byte(content))
	}
}

// approve records an approval of the brief of JIG-2 as it is on disk.
func approve(t *testing.T, st *store.Store) {
	t.Helper()
	sum, err := st.BriefSHA256("JIG-2")
	if err != nil {
		t.Fatal(err)
	}
	a := store.Approval{By: "Test Person", At: time.Now().UTC().Format(time.RFC3339), BriefSHA256: sum, Provenance: "person"}
	if err := st.WriteApproval("JIG-2", a); err != nil {
		t.Fatal(err)
	}
}

func sliceState(t *testing.T, st *store.Store, ticket, slice string) string {
	t.Helper()
	ss, err := st.ReadSliceState(ticket, slice)
	if err != nil {
		t.Fatal(err)
	}
	return ss.State
}

func claimLines(t *testing.T, fx *fixture.Fixture, ticket, event string) []journal.Line {
	t.Helper()
	var out []journal.Line
	for _, l := range readJournal(t, fx, ticket) {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

// TestClaimBlockedUnattendedRefusedInteractiveWarns: a ticket whose merged
// blocker has not published is refused a claim when nobody is watching
// (CLAIM_BLOCKED_MERGED, exit 1, nothing written) and warned about, and
// claimed all the same, when a person is; once the blocker published the
// unattended claim goes through. An approved brief edited afterwards is
// refused the same way (CLAIM_NOT_APPROVED).
func TestClaimBlockedUnattendedRefusedInteractiveWarns(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockedTicket(t, st, "merged")
	approve(t, st)

	// Unattended: refused, and nothing is written.
	r := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1")
	if r.Code != 1 || !strings.Contains(r.Stdout, "code: CLAIM_BLOCKED_MERGED") || !strings.Contains(r.Stdout, "JIG-1") {
		t.Fatalf("unattended claim: exit %d, want 1 with CLAIM_BLOCKED_MERGED naming JIG-1\n%s", r.Code, r.Stdout)
	}
	if got := sliceState(t, st, "JIG-2", "x"); got != "queued" {
		t.Errorf("after a refused claim the slice is %s, want queued", got)
	}
	if lines := claimLines(t, fx, "JIG-2", journal.EventClaim); len(lines) != 0 {
		t.Errorf("a refused claim left claim lines: %+v", lines)
	}

	// With a person watching: a warning, and the claim made.
	r = runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--session", "s-1")
	if r.Code != 0 {
		t.Fatalf("interactive claim: exit %d, want 0\n%s", r.Code, r.Stdout)
	}
	for _, want := range []string{"claim:\n", "  slice: x\n", "  result: claimed\n", "  state: building\n", "  session: s-1\n",
		"warnings[1]{code,message,help}:\n", "CLAIM_BLOCKED_MERGED,"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("interactive claim output lacks %q:\n%s", want, r.Stdout)
		}
	}
	if got := sliceState(t, st, "JIG-2", "x"); got != "building" {
		t.Errorf("after the claim the slice is %s, want building", got)
	}
	lines := claimLines(t, fx, "JIG-2", journal.EventClaim)
	if len(lines) != 1 || lines[0].Slice != "x" || lines[0].Session != "s-1" || lines[0].Unattended || lines[0].Branch != "main" {
		t.Errorf("claim lines = %+v, want one for slice x by s-1, attended, on main", lines)
	}
	// The fixture clone never fetched, so it has no origin/main to measure
	// from: the start is the merge base with its own main.
	wantStart := gitLog(t, fx.RepoDir, "merge-base", "HEAD", "main")
	if got, err := os.ReadFile(st.StartSHAPath("JIG-2", "fixture-repo")); err != nil || strings.TrimSpace(string(got)) != wantStart {
		t.Errorf("start sha = %q, %v, want the merge base %s", got, err, wantStart)
	}

	// Another session finds the slice held; --take is the way past.
	r = runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--session", "s-2")
	if r.Code != 1 || !strings.Contains(r.Stdout, "code: SLICE_HELD") || !strings.Contains(r.Stdout, "s-1") {
		t.Errorf("a second session's claim: exit %d, want 1 with SLICE_HELD naming s-1\n%s", r.Code, r.Stdout)
	}
	r = runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--session", "s-2", "--take")
	if r.Code != 0 || !strings.Contains(r.Stdout, "result: taken-over") || !strings.Contains(r.Stdout, "took_over_from: s-1") {
		t.Errorf("claim --take: exit %d, want 0 taken over from s-1\n%s", r.Code, r.Stdout)
	}

	// JIG-1 publishes: release, then the unattended claim goes through.
	if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	r = runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--session", "s-2", "--release")
	if r.Code != 0 || !strings.Contains(r.Stdout, "result: released") || !strings.Contains(r.Stdout, "state: queued") {
		t.Fatalf("release: exit %d, want 0 released and queued\n%s", r.Code, r.Stdout)
	}
	if got := sliceState(t, st, "JIG-2", "x"); got != "queued" {
		t.Errorf("after the release the slice is %s, want queued", got)
	}
	r = runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-3")
	if r.Code != 0 || strings.Contains(r.Stdout, "warnings") || !strings.Contains(r.Stdout, "result: claimed") {
		t.Fatalf("unattended claim after the publish: exit %d, want 0 and no warning\n%s", r.Code, r.Stdout)
	}
	if last := claimLines(t, fx, "JIG-2", journal.EventClaim); !last[len(last)-1].Unattended || last[len(last)-1].Session != "s-3" {
		t.Errorf("the unattended claim line = %+v, want Unattended set, session s-3", last[len(last)-1])
	}

	// The brief changes after its approval: the next unattended claim is refused.
	brief := filepath.Join(st.TicketDir("JIG-2"), "brief.md")
	writeFileOrFatal(t, brief, append(readFileOrFatal(t, brief), []byte("\nAnd also z.\n")...))
	r = runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "y", "--unattended", "--session", "s-3")
	if r.Code != 1 || !strings.Contains(r.Stdout, "code: CLAIM_NOT_APPROVED") || !strings.Contains(r.Stdout, "changed after it was approved") {
		t.Errorf("claim after the brief changed: exit %d, want 1 with CLAIM_NOT_APPROVED\n%s", r.Code, r.Stdout)
	}
}

// TestClaimTakesTheSessionClaudeCodeSets: with neither --session nor
// JIG_SESSION, a claim run by a Claude Code agent is recorded under the id
// Claude Code sets in its commands' environment, whichever shell tool ran it.
func TestClaimTakesTheSessionClaudeCodeSets(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION").with("CLAUDE_CODE_SESSION_ID=s-claude")
	r := runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "a")
	if r.Code != 0 {
		t.Fatalf("jig claim exit = %d\n%s", r.Code, r.Stdout)
	}
	lines := claimLines(t, fx, fx.Ticket, journal.EventClaim)
	if len(lines) != 1 || lines[0].Session != "s-claude" {
		t.Errorf("claim lines = %+v, want one by session s-claude", lines)
	}
}
