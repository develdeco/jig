package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// nextBlockedBy is the blocker a `jig next --json` lists, with the fact it
// misses.
type nextBlockedBy struct {
	Ticket string `json:"ticket"`
	Kind   string `json:"kind"`
	Fact   string `json:"fact"`
	AsOf   string `json:"as_of"`
}

// listedTicket is a ticket of `jig next --json` with the facts its blockers miss.
type listedTicket struct {
	ID        string          `json:"id"`
	State     string          `json:"state"`
	BlockedBy []nextBlockedBy `json:"blocked_by"`
}

// listed is the row of ticket in the listing of the whole store.
func listed(t *testing.T, env jigEnv, cwd, ticket string) listedTicket {
	t.Helper()
	list := nextJSON[struct {
		Tickets []listedTicket `json:"tickets"`
	}](t, env, cwd)
	for _, row := range list.Tickets {
		if row.ID == ticket {
			return row
		}
	}
	t.Fatalf("jig next lists no %s", ticket)
	return listedTicket{}
}

// ghOnPath puts the fake gh first on PATH, logging every call it gets, and
// returns the environment and the log. The pull requests it holds are
// withPulls'.
func ghOnPath(t *testing.T, env jigEnv) (jigEnv, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "gh.log")
	return env.with(
		"PATH="+fixture.GhStub(t)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_LOG="+log,
	), log
}

// withPulls sets the pull requests the fake gh holds; the pull request numbered
// n is at pullURL(n).
func withPulls(t *testing.T, env jigEnv, pulls ...fixture.GhPull) jigEnv {
	t.Helper()
	return env.with("GH_STUB_PULLS=" + fixture.GhPulls(t, pulls...))
}

func pullURL(n int) string { return pullURLIn("repo", n) }

// pullURLIn is pullURL for a pull request on the repository repo
// (fixture.GhPull.Repo).
func pullURLIn(repo string, n int) string {
	return "https://github.example/owner/" + repo + "/pull/" + strconv.Itoa(n)
}

// prViews counts the calls the fake gh logged that read a pull request's state.
func prViews(t *testing.T, log string) int {
	t.Helper()
	n := 0
	for _, c := range ghCalls(t, log) {
		if strings.HasPrefix(c, "pr view ") {
			n++
		}
	}
	return n
}

// publishedWith appends the lines of a ticket that published: a pr line for each
// of urls, then publish-done.
func publishedWith(t *testing.T, st *store.Store, ticket string, urls ...string) {
	t.Helper()
	for _, u := range urls {
		if err := journal.Append(st, ticket, journal.Line{Event: journal.EventPr, Outcome: "recorded", URL: u}); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Append(st, ticket, journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
}

// TestBlockerMetOnlyWhenItsPRMerged: JIG-2 waits for JIG-1, which published and
// recorded its pull request. While that is open, or was closed without merging,
// JIG-2 is blocked and `jig next` and the claim say which; once it is merged
// JIG-2 is ready and the claim goes through. A merged pull request stays merged:
// it is never asked about again, while an open one is asked every time a ticket
// is read for its own sake and may be taken from this machine's memory, dated,
// by the listing of the whole store.
func TestBlockerMetOnlyWhenItsPRMerged(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env, log := ghOnPath(t, env.without("JIG_SLICE", "JIG_SESSION"))
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockedTicket(t, st, "merged")
	approve(t, st)
	pr := pullURL(7)
	publishedWith(t, st, "JIG-1", pr)

	// The recorded pull request is open.
	env = withPulls(t, env, fixture.GhPull{Number: 7})
	open := "its PR is open (" + pr + ")"
	n := nextJSON[struct {
		State     string          `json:"state"`
		BlockedBy []nextBlockedBy `json:"blocked_by"`
		Phase     struct {
			Ready  bool   `json:"ready"`
			Reason string `json:"reason"`
		} `json:"phase"`
	}](t, env, fx.RepoDir, "JIG-2")
	if n.State != "blocked" || len(n.BlockedBy) != 1 || n.BlockedBy[0].Fact != open || n.BlockedBy[0].AsOf != "" {
		t.Fatalf("an open pull request: %+v, want JIG-2 blocked by JIG-1, %q, read now", n, open)
	}
	if n.Phase.Ready || !strings.Contains(n.Phase.Reason, open) {
		t.Errorf("the phase of JIG-2 = %+v, want it held, saying %q", n.Phase, open)
	}
	r := runJig(t, env, fx.RepoDir, "next", "JIG-2")
	for _, want := range []string{"blocked_by[1]{ticket,kind,fact}:\n", "JIG-1,merged,", open} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next JIG-2 lacks %q:\n%s", want, r.Stdout)
		}
	}
	refused := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1")
	if refused.Code != 1 || !strings.Contains(refused.Stdout, "code: CLAIM_BLOCKED_MERGED") ||
		!strings.Contains(refused.Stdout, "JIG-2 waits for JIG-1: "+open) || !strings.Contains(refused.Stdout, "Merge "+pr) {
		t.Errorf("claim while the pull request is open: exit %d, want CLAIM_BLOCKED_MERGED naming the fact and the pull request\n%s", refused.Code, refused.Stdout)
	}

	// The listing of the store takes what this machine was just told, and dates it.
	jig2 := listed(t, env, fx.RepoDir, "JIG-2")
	if jig2.State != "blocked" || len(jig2.BlockedBy) != 1 ||
		!strings.HasPrefix(jig2.BlockedBy[0].Fact, open+" (as of ") || jig2.BlockedBy[0].AsOf == "" {
		t.Errorf("the listing: %+v, want JIG-2 blocked by an open pull request, dated", jig2)
	}
	if text := runJig(t, env, fx.RepoDir, "next").Stdout; !strings.Contains(text, "waiting[1]{ticket,blocker,fact}:\n") || !strings.Contains(text, "JIG-2,JIG-1,") {
		t.Errorf("jig next lists no fact for JIG-2's blocker:\n%s", text)
	}

	// The pull request was closed without merging: a ticket read for its own
	// sake asks the host again.
	env = withPulls(t, env, fixture.GhPull{Number: 7, State: "closed"})
	closed := "its PR was closed without merging (" + pr + ")"
	n2 := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-2")
	if n2.State != "blocked" || len(n2.BlockedBy) != 1 {
		t.Fatalf("a pull request closed unmerged: state %q, blocked by %+v, want blocked", n2.State, n2.BlockedBy)
	}
	if r := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1"); r.Code != 1 ||
		!strings.Contains(r.Stdout, "JIG-2 waits for JIG-1: "+closed) || !strings.Contains(r.Stdout, "jig publish JIG-1 --pr <url>") {
		t.Errorf("claim after the pull request was closed: exit %d, want it refused for %q with the recording command\n%s", r.Code, closed, r.Stdout)
	}

	// Merged: JIG-2 is ready and the claim goes through.
	env = withPulls(t, env, fixture.GhPull{Number: 7, State: "closed", Merged: true})
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-2"); n.State != "ready" || len(n.BlockedBy) != 0 {
		t.Fatalf("a merged pull request: state %q, blocked by %+v, want ready", n.State, n.BlockedBy)
	}
	if r := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1"); r.Code != 0 ||
		strings.Contains(r.Stdout, "warnings") || !strings.Contains(r.Stdout, "result: claimed") {
		t.Errorf("claim after the merge: exit %d, want it claimed with no warning\n%s", r.Code, r.Stdout)
	}

	// A merged pull request stays merged: nothing asks the host again, though
	// the fake host now says it is open.
	asked := prViews(t, log)
	env = withPulls(t, env, fixture.GhPull{Number: 7})
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-2"); n.State == "blocked" {
		t.Errorf("a merged pull request read open: JIG-2 is %q, want a merge to stay merged", n.State)
	}
	if jig2 := listed(t, env, fx.RepoDir, "JIG-2"); jig2.State == "blocked" {
		t.Errorf("the listing blocks JIG-2 again: %+v", jig2)
	}
	if got := prViews(t, log); got != asked {
		t.Errorf("the host was asked %d more time(s) about a merged pull request", got-asked)
	}
}

// TestBlockerWithoutPRMetByAncestry: a blocker that published with no pull
// request recorded is met when its branch tip is in origin's target branch as
// the clone has it, and by nothing else: with neither a pull request nor its
// branch in the target it stays blocked, and the claim says how to record it.
func TestBlockerWithoutPRMetByAncestry(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockedTicket(t, st, "merged")
	approve(t, st)
	publishedWith(t, st, "JIG-1")

	branch := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-1").Branch
	gitLog(t, fx.RepoDir, "switch", "-c", branch, "main")
	writeFileOrFatal(t, filepath.Join(fx.RepoDir, "blocker.txt"), []byte("the blocker's work\n"))
	gitLog(t, fx.RepoDir, "add", "-A")
	commitPinned(t, fx.RepoDir, "the blocker's work")
	gitLog(t, fx.RepoDir, "push", "origin", branch)
	gitLog(t, fx.RepoDir, "switch", "main")
	gitLog(t, fx.RepoDir, "fetch", "origin")

	// Published, its branch pushed, not merged: nothing is on record.
	const hint = "no merge on record: record its PR with `jig publish JIG-1 --pr <url>`"
	n := nextJSON[struct {
		State     string          `json:"state"`
		BlockedBy []nextBlockedBy `json:"blocked_by"`
	}](t, env, fx.RepoDir, "JIG-2")
	if n.State != "blocked" || len(n.BlockedBy) != 1 || n.BlockedBy[0].Fact != hint {
		t.Fatalf("a branch not in main: %+v, want JIG-2 blocked by JIG-1, %q", n, hint)
	}
	refused := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1")
	if refused.Code != 1 || !strings.Contains(refused.Stdout, "code: CLAIM_BLOCKED_MERGED") || !strings.Contains(refused.Stdout, "JIG-2 waits for JIG-1: "+hint) {
		t.Errorf("claim with nothing on record: exit %d, want CLAIM_BLOCKED_MERGED naming %q\n%s", refused.Code, hint, refused.Stdout)
	}
	if text := runJig(t, env, fx.RepoDir, "next", "JIG-2").Stdout; !strings.Contains(text, hint) {
		t.Errorf("jig next JIG-2 does not name the recording command:\n%s", text)
	}

	// Merged in the clone's main but not pushed: origin does not have it, so
	// there is no evidence yet.
	gitLog(t, fx.RepoDir, "merge", "--no-ff", "-m", "merge "+branch, branch)
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-2"); n.State != "blocked" {
		t.Errorf("merged only in the clone: state %q, want blocked until origin has it", n.State)
	}

	// Pushed: the branch is in origin/main.
	gitLog(t, fx.RepoDir, "push", "origin", "main")
	if n := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-2"); n.State != "ready" || len(n.BlockedBy) != 0 {
		t.Fatalf("a branch in origin/main: state %q, blocked by %+v, want ready", n.State, n.BlockedBy)
	}
	if r := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1"); r.Code != 0 ||
		strings.Contains(r.Stdout, "warnings") || !strings.Contains(r.Stdout, "result: claimed") {
		t.Errorf("claim after the merge: exit %d, want it claimed with no warning\n%s", r.Code, r.Stdout)
	}
}
