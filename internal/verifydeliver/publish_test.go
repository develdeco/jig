package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// gateToClean drives the fixture's build and gate rounds to a clean
// verdict: round 1 (fix-1 found), fix-1 played green, round 2 (clean).
func gateToClean(t *testing.T, fx *fixture.Fixture, d Deps) {
	t.Helper()
	driveBuild(t, fx, "rung-a")
	src := NewFakeGateSource(fx.ScenarioDir)
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("round 1 verdict = %q, want fix-slices", report.Verdict)
	}
	driveFix1(t, fx, "rung-a")
	report, err = Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report.Verdict != "clean" {
		t.Fatalf("round 2 verdict = %q, want clean", report.Verdict)
	}
}

// advanceTarget commits a new file directly to the fixture repo's bare
// remote main branch, through a throwaway clone, so the target moves out
// from under an already-clean gate.
func advanceTarget(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	clone := t.TempDir()
	if _, err := gitx.Run(filepath.Dir(clone), "clone", fx.RepoRemote, clone); err != nil {
		t.Fatalf("clone remote: %v", err)
	}
	if _, err := gitx.Run(clone, "config", "user.name", "jig-fixture"); err != nil {
		t.Fatalf("config user.name: %v", err)
	}
	if _, err := gitx.Run(clone, "config", "user.email", "fixture@example.invalid"); err != nil {
		t.Fatalf("config user.email: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clone, "UPSTREAM.md"), []byte("upstream moved on\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := gitx.Run(clone, "add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := gitx.RunEnv(clone, buildGitEnv, "commit", "-m", "upstream: unrelated change"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := gitx.Run(clone, "push", "origin", "main"); err != nil {
		t.Fatalf("push: %v", err)
	}
}

// wantFailureCommit asserts that d.Store's working copy is clean, pushed to
// origin/branch (local HEAD matches the remote tip), and that its latest
// commit subject is exactly wantSubject - the shape every post-journal
// Publish failure must leave the store in, whichever step raised it. This
// must fail on the unfixed code: without Publish's deferred push (or with
// journaled set too late) the working copy stays dirty, or the pushed tip
// lags the local commit, and the porcelain or HEAD check below fails first.
func wantFailureCommit(t *testing.T, d Deps, branch, wantSubject string) {
	t.Helper()
	status, err := gitx.Run(d.Store.Root, "status", "--porcelain")
	if err != nil {
		t.Fatalf("store status: %v", err)
	}
	if status != "" {
		t.Fatalf("store working copy is dirty after the failed publish:\n%s", status)
	}
	local := run(t, d.Store.Root, "rev-parse", "HEAD")
	remote := run(t, d.Store.Root, "ls-remote", "origin", "refs/heads/"+branch)
	fields := strings.Fields(remote)
	if len(fields) == 0 {
		t.Fatalf("ls-remote origin refs/heads/%s returned nothing", branch)
	}
	if local != fields[0] {
		t.Fatalf("store local HEAD %s != pushed remote tip %s for %s", local, fields[0], branch)
	}
	subject := run(t, d.Store.Root, "log", "-1", "--format=%s")
	if subject != wantSubject {
		t.Fatalf("store failure commit subject = %q, want %q", subject, wantSubject)
	}
}

// TestCheckNonEmptyRangeRefusesEmptyRange checks that a ticket branch with
// no commits beyond origin/target (merge-base == HEAD) is refused with
// NOTHING_TO_PUBLISH rather than reaching squash's own bare git error.
func TestCheckNonEmptyRangeRefusesEmptyRange(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	run(t, clone, "fetch", "origin")

	err := checkNonEmptyRange(clone, "main", "HEAD")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "NOTHING_TO_PUBLISH" {
		t.Fatalf("err = %v, want *axi.Error NOTHING_TO_PUBLISH", err)
	}
}

// TestCheckNonEmptyRangeAllowsRealCommits is the non-empty-range control:
// a branch with a real commit beyond the target must not be refused.
func TestCheckNonEmptyRangeAllowsRealCommits(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "branch work", "branch work")
	run(t, clone, "fetch", "origin")

	if err := checkNonEmptyRange(clone, "main", "HEAD"); err != nil {
		t.Fatalf("checkNonEmptyRange: %v", err)
	}
}

// pushRecorder stands in for Deps.GuardedPush in a step that is not about the
// push: nothing reaches origin, so the next step starts from the state the one
// before left, and the recorder says what each push was asked. err, when set, is
// what the push fails with.
type pushRecorder struct {
	branches  []string
	confirmed []bool
	err       error
}

// push is the recorder's Deps.GuardedPush.
func (p *pushRecorder) push(_, _, branch string, confirmed bool) error {
	p.branches = append(p.branches, branch)
	p.confirmed = append(p.confirmed, confirmed)
	return p.err
}

// forgetRenderedPR removes the pull request files a publish rendered into
// ticket's folder, and pushes the removal: a publish renders them before it
// asks or pushes anything, so one that fails afterwards leaves them behind for
// its deferred push to commit, under a subject naming the failure - but only
// the first one does, since the next renders the same text and has nothing new
// to commit. A step that is about that commit starts from a ticket with none.
func forgetRenderedPR(t *testing.T, d Deps, ticket string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(d.Store.TicketDir(ticket), "pr")); err != nil {
		t.Fatalf("remove the rendered pull request files: %v", err)
	}
	if err := d.Store.Push(ticket + ": forget the rendered pull request"); err != nil {
		t.Fatalf("push the removal: %v", err)
	}
}

// journalFrom returns the journal lines of ticket after the first n.
func journalFrom(t *testing.T, d Deps, ticket string, n int) []journal.Line {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	return lines[n:]
}

// journalLen is the number of lines in ticket's journal.
func journalLen(t *testing.T, d Deps, ticket string) int {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	return len(lines)
}

// publishLeaseDir returns the fixture ticket's publish lease directory.
func publishLeaseDir(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	dir, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Publish)
	if err != nil {
		t.Fatalf("resolve publish lease: %v", err)
	}
	return dir
}

// TestPublishFullChain is the one chain of the publish: a ticket gated clean by
// scripted rounds publishes its reviewed head as it is. The target has moved on
// since the gate, and publish still reconciles nothing, squashes nothing and
// commits nothing: origin's branch is exactly the build lease's, commit for
// commit, the journal holds no reconcile, revalidate, memorize or squash line,
// and no retrieval notes were added to the branch. What a publish leaves in the
// store - changelogs, the ledger entry, the contract index, the evidence - is
// written once the branch is out.
//
// Every rule of the publish that needs a gated ticket is a step of it, so the
// ticket is built and gated once. The steps run in order, each leaving the
// ticket as the next expects it: the refusals that write nothing first, then the
// runs that do everything but push (their push is a recorder's, so the branch
// never reaches origin until the last of them), and last the real run:
//
//   - a ticket whose latest gate round is not clean is refused, however green
//     its slices: an unrebutted fix-slices verdict means a review that was never
//     re-confirmed clean (this is between round 1 and round 2);
//   - a recorded branch Store.TicketBranch refuses (here the target itself,
//     where a push would land without the PR) fails publish with the refusal's
//     own code, before it pushes anything. Origin's main and the ticket's
//     default branch, which the command must not fall back to, keep their shas;
//   - a ticket.yaml publish cannot read (here one a newer jig wrote) is read
//     once, up front, so publish fails with the refusal's own code before its
//     first store write - no memorize journal line - and pushes nothing, instead
//     of publishing the ticket under the default branch and title as if the
//     record were not there;
//   - a lookup of the branch's pull request that fails is not "no pull request",
//     which would open a second one: the publish is refused before its first
//     store write and before anything is pushed;
//   - the confirmation: a declined prompt stops before any push and the store
//     ends up clean, pushed and carrying "<ticket>: publish failed: <code>"; a
//     push's failure reaches the store as the error's own axi code only, never
//     its message; both --yes and an accepted prompt pass confirmed=true to the
//     push - never a hardcoded literal, and never proceeding past a decline -
//     and the prompt and the push both name the ticket's own branch; the prompt
//     names the open pull request a yes would update, and a no touches neither
//     it nor the branch (failureCode's own branches are pinned directly by
//     TestFailureCode);
//   - a pull request that could not be updated or opened is not reported as
//     done: the publish returns the failed call's error, its journal has no `pr`
//     line (nor the route and publish-done lines after it), and the deferred push
//     commits what it did write under a subject naming the failure;
//   - when posting the review notes as a comment fails, publish still succeeds:
//     the pull request stands, the file is kept for manual posting, the operator
//     is warned (prefixed "jig:", like every other stderr warning, and naming
//     the file and the pull request), and the journal and store are committed
//     with the publish complete;
//   - with no open pull request the lookup answers none and publish opens one
//     and posts the review notes as a comment on it, exactly once, with
//     pr/review-notes.md as its --body-file;
//   - a brief with no "## " section with text to publish as the body's ## Intent
//     publishes a body without that section - the owner's rule, pinned by the
//     render tests - and publish warns the operator that it did, prefixed "jig:"
//     like every other publish warning and naming the brief, and says it before
//     the confirmation prompt: the whole point of the warning is that the
//     operator can still answer "n" - editing the brief and gating again is
//     cheaper than editing a published pull request. The brief this chain is
//     gated with is the one the omission covers whose first "## " section is
//     empty, which firstBriefSection reads the same way as a brief with no
//     heading at all (TestFirstBriefSectionEmptyForAnEmptyFirstSection), and the
//     warning's wording has to fit it: "no \"## \" section with text to publish
//     as one", since "no \"## \" section" would send its operator to add a
//     heading the brief already has;
//   - PUBLISH_NO_DIVERGENCE - a branch that changes nothing against the target
//     (here the target fast-forwarded to the branch's own tip, then put back) -
//     is raised before publish writes anything to the store: a refusal leaves no
//     trace, so the store is clean, pushed, and its latest commit is still the
//     one it was, not a failure commit;
//   - the real run, with the target moved on and the host the fixture's own (a
//     plain local path, where repohost.New finds no pull-request host): a scripted
//     round records no reviewed head, so whatever the branch holds is shipped,
//     here a commit made after the last round; the confirmation asks the push-only
//     question; the oracles are not run again and no revalidate line is
//     journaled, whether the target moved or not; nothing is opened, and the
//     report says why;
//   - the build lease is in step with the branch publish pushed: pushing the
//     reviewed head as it is, without squashing or adding a commit, means the next
//     build acquire (a `jig run` after a post-publish gate round or requeue) finds
//     the two in step and carries on, where a publish that rewrote the branch
//     would have left them diverged;
//   - the branch's copy is then origin's, whether the build lease is still here
//     or not (TestPointAtTicketBranchSaysWhoseCopyItChose has the other states).
func TestPublishFullChain(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	briefPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")
	const brief = "# Fixture ticket brief\n\n## Intent\n\n## Plan\n\nTwo small fixes, under the second heading only.\n"
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatalf("write brief.md: %v", err)
	}
	branch := ticketBranch(fx.Ticket)
	const omitted = `no "## " section with text to publish as one`

	driveBuild(t, fx, "rung-a")
	src := NewFakeGateSource(fx.ScenarioDir)
	if report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket}); err != nil || report.Verdict != "fix-slices" {
		t.Fatalf("Gate round 1: verdict %q, err %v, want fix-slices", report.Verdict, err)
	}
	driveFix1(t, fx, "rung-a")
	step(t, "a ticket whose latest round is not clean is refused", func(t *testing.T) {
		// fix-1 goes green, but round 2 (which would re-review it) never ran:
		// the latest recorded gate verdict is still "fix-slices".
		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		wantAxiCode(t, err, "PUBLISH_NOT_CLEAN")
	})
	if report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket}); err != nil || report.Verdict != "clean" {
		t.Fatalf("Gate round 2: verdict %q, err %v, want clean", report.Verdict, err)
	}

	mainBefore := originRef(t, fx.RepoRemote, "refs/heads/main")
	step(t, "a recorded branch the target refuses fails with the refusal's own code", func(t *testing.T) {
		restore := saveTicketRecord(t, d, fx.Ticket)
		recordBranch(t, d.Store, fx.Ticket, "main")
		if _, err := d.Store.TicketBranch(fx.Ticket, "main"); err == nil {
			t.Fatal("test setup: a recorded branch equal to the target must be refused")
		}

		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		wantTicketBranchInvalid(t, err)
		if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
			t.Fatalf("origin main = %q after the refused publish, want %q unchanged", got, mainBefore)
		}
		if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
			t.Fatalf("origin %s = %q after the refused publish, want nothing pushed", branch, got)
		}
		restore(t)
	})

	step(t, "a ticket record it cannot read fails before the first store write", func(t *testing.T) {
		restore := saveTicketRecord(t, d, fx.Ticket)
		if err := os.WriteFile(d.Store.TicketFilePath(fx.Ticket), []byte("schema_version: 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := d.Store.Push(fx.Ticket + ": a newer jig's record"); err != nil {
			t.Fatalf("push the unreadable record: %v", err)
		}
		if _, err := d.Store.ReadTicket(fx.Ticket); err == nil {
			t.Fatal("test setup: a newer schema_version must be unreadable")
		}

		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "TICKET_SCHEMA_UNSUPPORTED" {
			t.Fatalf("Publish over a newer-schema ticket.yaml: err = %v, want an *axi.Error TICKET_SCHEMA_UNSUPPORTED", err)
		}

		lines, jerr := journal.Read(d.Store, fx.Ticket)
		if jerr != nil {
			t.Fatalf("journal.Read: %v", jerr)
		}
		for _, l := range lines {
			if l.Event == "memorize" {
				t.Fatalf("journal has a %q line after the refused publish: it wrote to the store before reading the record", l.Event)
			}
		}
		if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
			t.Fatalf("origin main = %q after the refused publish, want %q unchanged", got, mainBefore)
		}
		if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
			t.Fatalf("origin %s = %q after the refused publish, want nothing pushed", branch, got)
		}
		restore(t)
	})

	// One fake gh serves the steps that are about a pull request, built once and
	// pointed by each step at the pull requests and the failure it is about.
	ghd := d
	ghLog, setGh := useGithubHost(t, &ghd, "", "")
	ghSeen := 0
	gh := func(t *testing.T, pulls, fail string) Deps {
		t.Helper()
		setGh("GH_STUB_PULLS="+pulls, "GH_STUB_FAIL="+fail)
		ghSeen = len(loggedGh(t, ghLog))
		dd := d
		dd.Host = ghd.Host
		return dd
	}
	ghCallsNow := func(t *testing.T) [][]string {
		t.Helper()
		return loggedGh(t, ghLog)[ghSeen:]
	}
	wantNothingWritten := func(t *testing.T, dd Deps, journalBefore int, storeHead string) {
		t.Helper()
		if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
			t.Errorf("origin has %s = %s after the refused publish, want nothing pushed", branch, got)
		}
		if got := journalLen(t, dd, fx.Ticket); got != journalBefore {
			t.Errorf("the journal grew from %d to %d lines over a refused publish", journalBefore, got)
		}
		if got := run(t, dd.Store.Root, "rev-parse", "HEAD"); got != storeHead {
			t.Errorf("the store moved from %s to %s over a refused publish", storeHead, got)
		}
	}

	step(t, "a lookup that cannot tell whether there is a pull request refuses the publish", func(t *testing.T) {
		dd := gh(t, "", "api repos/owner/fixture-repo/pulls")
		journalBefore := journalLen(t, dd, fx.Ticket)
		storeHead := run(t, dd.Store.Root, "rev-parse", "HEAD")

		_, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: true})
		if err == nil || !strings.Contains(err.Error(), "gh api repos/owner/fixture-repo/pulls") {
			t.Fatalf("Publish over a gh that cannot list: err = %v, want the failed call named", err)
		}
		if calls := ghCallsNow(t); ghCalls(calls, "pr", "create") != 0 || ghCalls(calls, "pr", "edit") != 0 {
			t.Errorf("gh calls = %v, want no pull request opened or edited", calls)
		}
		wantNothingWritten(t, dd, journalBefore, storeHead)
	})

	step(t, "a declined prompt pushes the store and not the branch", func(t *testing.T) {
		forgetRenderedPR(t, d, fx.Ticket)
		dd := d
		pushes := &pushRecorder{}
		dd.GuardedPush = pushes.push
		dd.Confirm = func(string, string, string, bool) bool { return false }

		_, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: false})
		wantAxiCode(t, err, "PUBLISH_DECLINED")
		if len(pushes.branches) != 0 {
			t.Fatal("guardedPush was called despite a declined confirm")
		}

		// No leftover tracked change from the journal lines or documents
		// Publish wrote before the decline: the store is already clean,
		// pushed, and the commit names the decline - only the ticket
		// branch itself (guardedPush, stubbed above) is never pushed.
		wantFailureCommit(t, dd, "main", fx.Ticket+": publish failed: PUBLISH_DECLINED")
	})

	step(t, "the prompt names the open pull request, and a no touches neither it nor the branch", func(t *testing.T) {
		dd := gh(t, openPullOf(t, branch), "")
		var gotBranch, gotPR string
		dd.Confirm = func(branch, _, openPR string, _ bool) bool {
			gotBranch, gotPR = branch, openPR
			return false
		}

		_, err := Publish(dd, PublishOpts{Ticket: fx.Ticket})
		wantAxiCode(t, err, "PUBLISH_DECLINED")
		if gotBranch != branch || gotPR != existingPR {
			t.Errorf("confirm asked about (%q, %q), want the ticket's branch and the open pull request %s", gotBranch, gotPR, existingPR)
		}
		if calls := ghCallsNow(t); ghCalls(calls, "pr", "edit") != 0 || ghCalls(calls, "pr", "create") != 0 {
			t.Errorf("gh calls = %v after a declined publish, want no pull request touched", calls)
		}
		if got := originRef(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
			t.Errorf("origin has %s = %s after a declined publish", branch, got)
		}
	})

	step(t, "a push's failure reaches the store as its axi code only", func(t *testing.T) {
		forgetRenderedPR(t, d, fx.Ticket)
		dd := d
		pushes := &pushRecorder{err: &axi.Error{Msg: "push rejected: /host/secret/abs/path", Code: "PUBLISH_PUSH_REJECTED"}}
		dd.GuardedPush = pushes.push

		if _, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: true}); err == nil {
			t.Fatal("Publish: want an error from the stubbed guardedPush")
		}
		wantFailureCommit(t, dd, "main", fx.Ticket+": publish failed: PUBLISH_PUSH_REJECTED")
	})

	step(t, "--yes threads confirmed true to the push", func(t *testing.T) {
		dd := d
		pushes := &pushRecorder{}
		dd.GuardedPush = pushes.push
		if _, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if len(pushes.confirmed) != 1 || !pushes.confirmed[0] {
			t.Fatalf("guardedPush confirmed = %v, want true when --yes is set", pushes.confirmed)
		}
	})

	step(t, "an accepted prompt threads confirmed true, and the prompt and the push name the ticket's branch", func(t *testing.T) {
		dd := d
		pushes := &pushRecorder{}
		dd.GuardedPush = pushes.push
		var prompted, promptedTicket string
		dd.Confirm = func(branch, ticket, _ string, _ bool) bool {
			prompted, promptedTicket = branch, ticket
			return true
		}

		if _, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: false}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if len(pushes.confirmed) != 1 || !pushes.confirmed[0] {
			t.Fatalf("guardedPush confirmed = %v, want true after an accepted prompt", pushes.confirmed)
		}
		if prompted != branch || len(pushes.branches) != 1 || pushes.branches[0] != branch {
			t.Fatalf("prompt named branch %q and push named %q, want both to name the ticket's own branch %q", prompted, pushes.branches, branch)
		}
		if promptedTicket != fx.Ticket {
			t.Fatalf("prompt named ticket %q, want %q", promptedTicket, fx.Ticket)
		}
	})

	for _, tc := range []struct {
		name  string
		pulls func(t *testing.T, branch string) string
		fail  string
		call  string
	}{
		{"a pull request that cannot be updated is not reported as done", openPullOf, "pr edit", "gh pr edit"},
		{"a pull request that cannot be opened is not reported as done", func(*testing.T, string) string { return "" }, "pr create", "gh pr create"},
	} {
		step(t, tc.name, func(t *testing.T) {
			forgetRenderedPR(t, d, fx.Ticket)
			dd := gh(t, tc.pulls(t, branch), tc.fail)
			pushes := &pushRecorder{}
			dd.GuardedPush = pushes.push
			journalBefore := journalLen(t, dd, fx.Ticket)

			_, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: true})
			if err == nil || !strings.Contains(err.Error(), tc.call) {
				t.Fatalf("Publish over a gh that fails %q: err = %v, want the failed call named", tc.fail, err)
			}
			for _, l := range journalFrom(t, dd, fx.Ticket, journalBefore) {
				if l.Event == "pr" || l.Event == "route" || l.Event == "publish-done" {
					t.Errorf("journal has a %q line (%+v) after a publish whose pull request could not be written", l.Event, l)
				}
			}
			wantFailureCommit(t, dd, "main", fx.Ticket+": publish failed: INTERNAL")
		})
	}

	step(t, "a review notes comment that cannot be posted still publishes, with a warning", func(t *testing.T) {
		dd := gh(t, "", "pr comment")
		dd.GuardedPush = (&pushRecorder{}).push
		var warning string
		dd.Warn = func(format string, args ...any) { warning = fmt.Sprintf(format, args...) }
		journalBefore := journalLen(t, dd, fx.Ticket)

		if _, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
			t.Fatalf("Publish: %v (expected to succeed despite comment failure)", err)
		}

		reviewNotesPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "pr", "review-notes.md")
		if !strings.HasPrefix(warning, "jig: ") {
			t.Errorf("warning = %q, want it prefixed %q like every other stderr warning", warning, "jig: ")
		}
		if !strings.Contains(warning, reviewNotesPath) {
			t.Errorf("warning = %q, want it to name %s", warning, reviewNotesPath)
		}
		if !strings.Contains(warning, "gh pr comment") {
			t.Errorf("warning = %q, want it to carry the failed call's own error", warning)
		}
		if calls := ghCallsNow(t); ghCalls(calls, "pr", "create") != 1 {
			t.Errorf("pr create not called or called multiple times: %v", calls)
		}
		if _, err := os.Stat(reviewNotesPath); err != nil {
			t.Errorf("review notes file was not kept at %s: %v", reviewNotesPath, err)
		}
		var publishDone bool
		for _, l := range journalFrom(t, dd, fx.Ticket, journalBefore) {
			publishDone = publishDone || l.Event == "publish-done"
		}
		if !publishDone {
			t.Errorf("journal has no publish-done line despite successful publish")
		}
	})

	step(t, "a branch with no open pull request gets one, and the review notes as a comment on it", func(t *testing.T) {
		dd := gh(t, "", "")
		dd.GuardedPush = (&pushRecorder{}).push
		journalBefore := journalLen(t, dd, fx.Ticket)

		report, err := Publish(dd, PublishOpts{Ticket: fx.Ticket, Yes: true})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}

		calls := ghCallsNow(t)
		if ghCalls(calls, lookupCall(branch)...) != 1 || ghCalls(calls, "pr", "create", "--repo", "owner/fixture-repo") != 1 || ghCalls(calls, "pr", "edit") != 0 {
			t.Errorf("gh calls = %v, want one lookup, one pr create and no pr edit", calls)
		}
		if report.PRUpdated["fixture-repo"] || report.PRURL["fixture-repo"] == "" {
			t.Errorf("report: PR %q updated=%v, want a pull request opened", report.PRURL["fixture-repo"], report.PRUpdated["fixture-repo"])
		}
		var prLines []journal.Line
		for _, l := range journalFrom(t, dd, fx.Ticket, journalBefore) {
			if l.Event == "pr" {
				prLines = append(prLines, l)
			}
		}
		if len(prLines) != 1 || prLines[0].Outcome != "opened" {
			t.Errorf("journal pr lines = %+v, want one recording the outcome opened", prLines)
		}
		comments := reviewNotesCommentCalls(calls)
		if len(comments) != 1 {
			t.Fatalf("pr comment called %d time(s), want exactly once: %v", len(comments), calls)
		}
		wantReviewNotesBodyFile(t, comments[0])
	})

	step(t, "the omitted ## Intent is warned about before the confirmation prompt", func(t *testing.T) {
		dd := d
		var warnings, warnedWhenAsked []string
		asked := false
		dd.Warn = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
		dd.Confirm = func(string, string, string, bool) bool {
			asked = true
			warnedWhenAsked = append(warnedWhenAsked, warnings...)
			return false
		}

		_, err := Publish(dd, PublishOpts{Ticket: fx.Ticket})
		wantAxiCode(t, err, "PUBLISH_DECLINED")
		if !asked {
			t.Fatal("confirm was never asked, so nothing here can say when the warning came")
		}
		if body, _ := readPRFiles(t, dd, fx.Ticket, "fixture-repo"); strings.Contains(body, "## Intent") {
			t.Fatalf("pr/fixture-repo.md has an ## Intent section for a brief whose first section is empty:\n%s", body)
		}
		var warning string
		for _, w := range warnedWhenAsked {
			if strings.Contains(w, "## Intent") {
				warning = w
				break
			}
		}
		if warning == "" {
			t.Fatalf("publish had warned %q by the confirmation prompt, want the omitted ## Intent section already warned about there, while an operator can still decline", warnedWhenAsked)
		}
		if !strings.HasPrefix(warning, "jig: ") {
			t.Errorf("warning = %q, want it prefixed %q like every other publish warning", warning, "jig: ")
		}
		if !strings.Contains(warning, briefPath) {
			t.Errorf("warning = %q, want it to name %s, the brief the section would have come from", warning, briefPath)
		}
		if !strings.Contains(warning, omitted) {
			t.Errorf("warning = %q, want a reason that fits a brief whose first \"## \" section is merely empty: %s", warning, omitted)
		}
	})

	step(t, "a branch that changes nothing against the target is refused before the store is written", func(t *testing.T) {
		// Fast-forward origin's target branch to exactly the build lease's
		// ticket-branch tip: the branch then differs from the target by nothing.
		run(t, buildLeaseDir(t, fx), "push", "origin", branch+":main")
		defer run(t, fx.RepoRemote, "update-ref", "refs/heads/main", mainBefore)
		subjectBefore := run(t, d.Store.Root, "log", "-1", "--format=%s")

		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
		var ae *axi.Error
		if !errors.As(err, &ae) || (ae.Code != "PUBLISH_NO_DIVERGENCE" && ae.Code != "NOTHING_TO_PUBLISH") {
			t.Fatalf("err = %v, want *axi.Error PUBLISH_NO_DIVERGENCE or NOTHING_TO_PUBLISH", err)
		}
		if status := run(t, d.Store.Root, "status", "--porcelain"); status != "" {
			t.Fatalf("the refused publish left the store dirty:\n%s", status)
		}
		if subject := run(t, d.Store.Root, "log", "-1", "--format=%s"); subject != subjectBefore {
			t.Fatalf("the store's latest commit is %q, was %q: a refusal before the first write must leave no commit", subject, subjectBefore)
		}
	})

	step(t, "a clean ticket publishes its reviewed head as it is, the target having moved on", func(t *testing.T) {
		// A scripted round records no reviewed head, and publish keeps its
		// behavior there: whatever the branch holds is shipped. The commit
		// after the last round proves no head is held.
		rep, err := latestGateReport(d.Store, fx.Ticket)
		if err != nil {
			t.Fatalf("latestGateReport: %v", err)
		}
		if len(rep.ReviewedSHA) != 0 {
			t.Fatalf("test setup: a scripted round recorded reviewed_sha %v", rep.ReviewedSHA)
		}
		writeAndCommit(t, buildLeaseDir(t, fx), "late.txt", "written after the last round", "a change after the last round")
		buildHead := run(t, buildLeaseDir(t, fx), "rev-parse", "HEAD")
		buildLog := run(t, buildLeaseDir(t, fx), "log", "--format=%H", fx.Branch)
		advanceTarget(t, fx)

		dd := d
		oracles := &oracleRecorder{}
		dd.Oracle = oracles.run
		var gotBranch, gotPR string
		var gotHasHost bool
		dd.Confirm = func(branch, _, openPR string, hasHost bool) bool {
			gotBranch, gotPR, gotHasHost = branch, openPR, hasHost
			return true
		}
		journalBefore := journalLen(t, dd, fx.Ticket)

		report, err := Publish(dd, PublishOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if gotBranch != branch || gotPR != "" || gotHasHost {
			t.Errorf("confirm asked (branch=%q, openPR=%q, hasHost=%v), want the ticket's branch, no open PR and hasHost=false", gotBranch, gotPR, gotHasHost)
		}
		if report.Tier != "none" {
			t.Fatalf("Tier = %q, want none: publish revalidates nothing", report.Tier)
		}
		if ran := oracles.ran(); len(ran) != 0 {
			t.Fatalf("publish ran oracles %v, want none", ran)
		}
		if head := report.Head["fixture-repo"]; head != buildHead {
			t.Fatalf("Head[fixture-repo] = %q, want the reviewed head %s", head, buildHead)
		}
		prPath, ok := report.PRBody["fixture-repo"]
		if !ok || prPath == "" {
			t.Fatalf("PRBody[fixture-repo] missing, got %v", report.PRBody)
		}
		// The fixture repo's remote is a plain local path: repohost.New finds no
		// pull-request host there, so PRURL is empty and PRNote carries why.
		if url := report.PRURL["fixture-repo"]; url != "" || report.PRUpdated["fixture-repo"] {
			t.Fatalf("PR %q updated=%v, want empty and false: the fixture's remote has no pull-request host", url, report.PRUpdated["fixture-repo"])
		}
		if note := report.PRNote["fixture-repo"]; note != "no pull-request host" {
			t.Fatalf("PRNote[fixture-repo] = %q, want it to say the remote has no pull-request host", note)
		}

		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatalf("journal.Read: %v", err)
		}
		seen := map[string]journal.Line{}
		for _, l := range lines {
			seen[l.Event] = l
		}
		for _, event := range []string{"pr", "changelog", "publish-done"} {
			if _, ok := seen[event]; !ok {
				t.Fatalf("journal missing event %q", event)
			}
		}
		for _, event := range []string{"reconcile", "revalidate", "memorize", "squash"} {
			if l, ok := seen[event]; ok {
				t.Fatalf("journal has %+v, but publish reconciles, revalidates, memorizes and squashes nothing", l)
			}
		}
		if pr := seen["pr"]; pr.Outcome != "none:no-host" || pr.Commit != buildHead || pr.Branch != fx.Branch {
			t.Fatalf("pr line = %+v, want none:no-host for %s from %s", pr, buildHead, fx.Branch)
		}
		var prLines []journal.Line
		for _, l := range lines[journalBefore:] {
			if l.Event == "pr" {
				prLines = append(prLines, l)
			}
		}
		if len(prLines) != 1 || prLines[0].Outcome != "none:no-host" {
			t.Fatalf("journal pr lines of the run = %+v, want exactly one recording none:no-host", prLines)
		}

		// The reviewed head went out as it is, on top of nothing: origin's history
		// of the branch is the build lease's, with no memorize or squash commit.
		if remoteHead := originRef(t, fx.RepoRemote, "refs/heads/"+fx.Branch); remoteHead != buildHead {
			t.Fatalf("origin %s = %s, want the reviewed head %s", fx.Branch, remoteHead, buildHead)
		}
		if got := run(t, fx.RepoRemote, "log", "--format=%H", fx.Branch); got != buildLog {
			t.Fatalf("origin's history of %s is\n%s\nwant the build lease's\n%s", fx.Branch, got, buildLog)
		}
		if _, err := gitx.Run(publishLeaseDir(t, fx), "cat-file", "-e", buildHead+":.claude/retrieval/"+fx.Ticket+".md"); err == nil {
			t.Fatal("the reviewed head holds retrieval notes: publish committed on the branch")
		}

		ticketDir := d.Store.TicketDir(fx.Ticket)
		for _, f := range []string{
			filepath.Join("changelog", "alpha.md"),
			filepath.Join("changelog", "beta.md"),
			filepath.Join("changelog", "consolidated.md"),
			filepath.Join("pr", "fixture-repo.md"),
			filepath.Join("pr", "evidence.md"),
		} {
			if _, err := os.Stat(filepath.Join(ticketDir, f)); err != nil {
				t.Fatalf("missing store file %s: %v", f, err)
			}
		}

		ledger, err := os.ReadFile(filepath.Join(d.Store.Root, "ledger.md"))
		if err != nil {
			t.Fatalf("read ledger.md: %v", err)
		}
		if !strings.Contains(string(ledger), "## "+fx.Ticket) {
			t.Fatalf("ledger.md missing ## %s entry:\n%s", fx.Ticket, ledger)
		}
		if !strings.Contains(string(ledger), "**Answers:**") {
			t.Fatalf("ledger.md missing Answers tail:\n%s", ledger)
		}

		index, err := os.ReadFile(filepath.Join(d.Store.Root, "platform", "contract-index.md"))
		if err != nil {
			t.Fatalf("read contract-index.md: %v", err)
		}
		if !strings.Contains(string(index), "- "+fx.Ticket+":") {
			t.Fatalf("contract-index.md missing entry:\n%s", index)
		}

		// The store's remote advanced with the publish commits.
		storeRemoteHead, err := gitx.Run(fx.StoreRemote, "rev-parse", "main")
		if err != nil {
			t.Fatalf("resolve store remote main: %v", err)
		}
		storeHead, err := gitx.Run(fx.StoreDir, "rev-parse", "main")
		if err != nil {
			t.Fatalf("resolve store head: %v", err)
		}
		if storeRemoteHead != storeHead {
			t.Fatalf("store remote main = %s, want it to match local %s", storeRemoteHead, storeHead)
		}
	})

	step(t, "the build lease is in step with the branch publish pushed", func(t *testing.T) {
		lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Build)
		if err != nil {
			t.Fatalf("build Acquire after a publish: %v, want the build lease in step with origin's branch", err)
		}
		standing, err := pool.Compare(lease.Dir, branch)
		if err != nil || standing != pool.InStep {
			t.Fatalf("the build lease stands %v against origin (%v), want in step", standing, err)
		}
	})

	step(t, "the branch's copy is origin's once a publish pushed it", func(t *testing.T) {
		wantCopy(t, d, fx, fx.Ticket, branch, false, originsCopy, "publish pushed the build lease's head as it is, so the two are one")
	})

	step(t, "and with no build lease here", func(t *testing.T) {
		if err := os.RemoveAll(buildLeaseOf(t, fx, fx.Ticket)); err != nil {
			t.Fatalf("remove the build lease: %v", err)
		}
		wantCopy(t, d, fx, fx.Ticket, branch, false, originsCopy, "it holds every commit jig built, exactly as it built them")
	})
}

// TestPublishTitlesThePullRequestWithTheRecordedTitle covers the title
// fallback through publish itself (consolidatedTitle is only unit-tested with
// the title handed to it): when no slice has a goal, the pull request opened
// through the host carries the title recorded in ticket.yaml, not the bare
// ticket id.
func TestPublishTitlesThePullRequestWithTheRecordedTitle(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	logFile, _ := useGithubHost(t, &d, "", "")
	// The title is recorded first: it names the ticket's default branch, so a
	// title recorded after the gate would move the ticket off the branch the
	// gate reviewed.
	if err := d.Store.CreateTicketRecord(fx.Ticket, store.Ticket{Title: "Recorded title"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	gateToClean(t, fx, d)

	sls, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	for i := range sls {
		sls[i].Goal = ""
	}
	data, err := yaml.Marshal(store.SliceFile{Slices: sls})
	if err != nil {
		t.Fatalf("marshal slices: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "slices.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Store.Push(fx.Ticket + ": no slice goals"); err != nil {
		t.Fatalf("push the slices: %v", err)
	}

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var create []string
	for _, argv := range loggedGh(t, logFile) {
		if len(argv) >= 2 && argv[0] == "pr" && argv[1] == "create" {
			create = argv
		}
	}
	titleAt := slices.Index(create, "--title")
	if titleAt < 0 || titleAt+1 >= len(create) || create[titleAt+1] != "Recorded title" {
		t.Fatalf("gh pr create = %v, want --title \"Recorded title\"", create)
	}
}
