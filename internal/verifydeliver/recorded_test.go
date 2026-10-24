package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// cloneBuilt is the fixture's ticket as the digester sees it: slice a's patch
// committed on the ticket's branch in the operator's clone (mapped in
// Deps.Machine), never pushed, and every slice green. It returns the Deps
// that gate over it and the clone's head.
func cloneBuilt(t *testing.T) (*fixture.Fixture, Deps, string) {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	run(t, fx.RepoDir, "checkout", "-b", ticketBranch(fx.Ticket))
	applyScenarioPatch(t, fx, fx.RepoDir, fx.Ticket, "a", 1)
	head := run(t, fx.RepoDir, "rev-parse", "HEAD")
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := d.Store.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatalf("write slice state %s: %v", id, err)
		}
	}
	if err := d.Store.Push(fx.Ticket + ": every slice green"); err != nil {
		t.Fatalf("push the slice states: %v", err)
	}
	return fx, d, head
}

// cleanReport is the report of a reviewer who found nothing and read every
// file the request made it read.
func cleanReport(p Prepared) ReviewResult {
	return ReviewResult{Findings: []ResultFinding{}, StillPresent: []StillPresentEntry{}, ReviewedPaths: p.Request.MustReview, Summary: "nothing found"}
}

// TestGatePrepareOpensARoundWithoutMakingOne: the gate over a branch only the
// operator's clone has opens round 1: the request and the open record are in
// the store's work directory, the lease is a pristine checkout of the clone's
// head, and there is no gate/round-1 yet. Neither file names a directory of
// this machine's jig home (the store is shared); the Prepared a caller gets
// spells the lease out.
func TestGatePrepareOpensARoundWithoutMakingOne(t *testing.T) {
	t.Parallel()
	fx, d, head := cloneBuilt(t)

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Round != 1 || p.Head != head || p.Branch != ticketBranch(fx.Ticket) || p.Copy != string(clonesCopy) || p.Scope != "full" {
		t.Fatalf("Prepared = round %d at %s on %s from %s (%s), want round 1 at %s on the ticket's branch from the clone's copy, full", p.Round, p.Head, p.Branch, p.Copy, p.Scope, head)
	}
	if got := run(t, p.Lease, "rev-parse", "HEAD"); got != head || p.Request.Worktree != p.Lease || p.Lease != gateLeaseOf(t, fx, fx.Ticket) {
		t.Fatalf("lease %q (request worktree %q) at %s, want the gate lease checked out at the clone's head %s", p.Lease, p.Request.Worktree, got, head)
	}
	if originRef(t, fx.RepoRemote, "refs/heads/"+p.Branch) != "" {
		t.Fatal("the gate pushed the branch to origin")
	}
	if _, err := os.Stat(gateRoundDir(d.Store, fx.Ticket, 1)); err == nil {
		t.Fatal("opening a round made gate/round-1")
	}
	if n, err := existingGateRounds(d.Store, fx.Ticket); err != nil || n != 0 {
		t.Fatalf("existingGateRounds = %d (%v), want 0: an open round is not a round", n, err)
	}
	for _, path := range []string{p.ReviewPath, p.OpenPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if found := hostSpellingsIn(string(data), d.Home); len(found) > 0 {
			t.Errorf("%s names the jig home (%v): the store is shared", path, found)
		}
	}
	var open openRoundFile
	data, _ := os.ReadFile(p.OpenPath)
	if err := yaml.Unmarshal(data, &open); err != nil {
		t.Fatal(err)
	}
	if open.Lease != "pool/fixture-repo/JIG-1-gate" || open.Head != head || open.Opened == "" {
		t.Errorf("open record = %+v, want the lease relative to the jig home, the head and the time it opened", open)
	}

	again, ok, err := CurrentRound(d, fx.Ticket)
	if err != nil || !ok {
		t.Fatalf("CurrentRound = ok %v, %v, want the round just opened", ok, err)
	}
	again.Request.Worktree = p.Request.Worktree
	if !reflect.DeepEqual(again.Request, p.Request) || again.Lease != p.Lease || again.Head != head {
		t.Errorf("CurrentRound = %+v, want what GatePrepare returned: %+v", again, p)
	}
}

// TestGatePrepareRefusesWhatHasNoCopy: the branch is read from the clone, else
// from origin, else not at all. A branch neither has is BRANCH_NOT_FOUND, and a
// refusal leaves no trace: no journal line, no open record.
func TestGatePrepareRefusesWhatHasNoCopy(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := d.Store.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatal(err)
		}
	}

	_, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "BRANCH_NOT_FOUND")
	if lines, _ := journal.Read(d.Store, fx.Ticket); len(lines) != 0 {
		t.Fatalf("the refused gate journaled %+v", lines)
	}
}

// TestGatePrepareChoosesBetweenTheClonesCopyAndOrigins: the copy the round
// reviews is the newer of the clone's and origin's. The clone ahead of origin:
// the clone's. Origin ahead of the clone: origin's. The two the same: either
// holds it. Each with commits the other lacks: BRANCH_DIVERGED, which names the
// clone and what to do.
func TestGatePrepareChoosesBetweenTheClonesCopyAndOrigins(t *testing.T) {
	t.Parallel()
	branch := ticketBranch("JIG-1")
	cases := []struct {
		name     string
		setup    func(t *testing.T, fx *fixture.Fixture, clone string)
		wantCopy string // "" for a refusal
		wantHead func(clone, origin string) string
	}{
		{"clone ahead of origin", func(t *testing.T, fx *fixture.Fixture, clone string) {
			run(t, clone, "push", "origin", branch)
			writeAndCommit(t, clone, "more.txt", "only the clone", "a commit only the clone has")
		}, string(clonesCopy), func(clone, _ string) string { return clone }},
		{"origin ahead of the clone", func(t *testing.T, fx *fixture.Fixture, clone string) {
			run(t, clone, "push", "origin", branch)
			other := cloneFrom(t, fx.RepoRemote)
			run(t, other, "checkout", branch)
			writeAndCommit(t, other, "more.txt", "only origin", "a commit only origin has")
			run(t, other, "push", "origin", branch)
		}, string(originsCopy), func(_, origin string) string { return origin }},
		{"in step", func(t *testing.T, fx *fixture.Fixture, clone string) {
			run(t, clone, "push", "origin", branch)
		}, string(clonesCopy), func(clone, _ string) string { return clone }},
		{"diverged", func(t *testing.T, fx *fixture.Fixture, clone string) {
			run(t, clone, "push", "origin", branch)
			other := cloneFrom(t, fx.RepoRemote)
			run(t, other, "checkout", branch)
			writeAndCommit(t, other, "origin.txt", "only origin", "a commit only origin has")
			run(t, other, "push", "origin", branch)
			writeAndCommit(t, clone, "clone.txt", "only the clone", "a commit only the clone has")
		}, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx, d, _ := cloneBuilt(t)
			tc.setup(t, fx, fx.RepoDir)
			cloneHead := run(t, fx.RepoDir, "rev-parse", "HEAD")
			originHead := originRef(t, fx.RepoRemote, "refs/heads/"+branch)

			p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
			if tc.wantCopy == "" {
				wantAxiCode(t, err, "BRANCH_DIVERGED")
				if !strings.Contains(err.Error(), fx.RepoDir) {
					t.Errorf("the refusal %q does not name the clone to join the two in", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("GatePrepare: %v", err)
			}
			if p.Copy != tc.wantCopy || p.Head != tc.wantHead(cloneHead, originHead) {
				t.Fatalf("round reviews %s at %s, want %s at %s", p.Copy, p.Head, tc.wantCopy, tc.wantHead(cloneHead, originHead))
			}
		})
	}
}

// TestReopeningARoundKeepsItsDraftsOnlyWhileTheHeadStands: `jig gate` over a
// round already open is the same round (the next number, since nothing has
// recorded it): with the head unchanged, the findings drafted so far and what
// the reviewer said of itself are kept; with the clone's branch moved on, they
// were about code that is no longer the branch's, and are dropped.
func TestReopeningARoundKeepsItsDraftsOnlyWhileTheHeadStands(t *testing.T) {
	t.Parallel()
	fx, d, head := cloneBuilt(t)

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if _, err := RecordReviewer(d, p, Provenance{Session: "s-rev", How: ReviewerByFlag, Model: "m"}); err != nil {
		t.Fatalf("RecordReviewer: %v", err)
	}
	if _, err := AppendDraft(d, p, Draft{Finding: &ResultFinding{File: p.Request.MustReview[0], Line: 1, Title: "t", Action: ActionNote, Risk: RiskLow, RiskRationale: "r"}}); err != nil {
		t.Fatalf("AppendDraft: %v", err)
	}

	same, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare again: %v", err)
	}
	if same.Round != 1 || same.Head != head || same.Reviewer.Session != "s-rev" || same.Reviewer.Model != "m" {
		t.Fatalf("reopened at the same head: round %d at %s, reviewer %+v; want round 1, the same head and the reviewer kept", same.Round, same.Head, same.Reviewer)
	}
	if drafts, err := ReadDrafts(same); err != nil || len(drafts) != 1 {
		t.Fatalf("drafts after reopening at the same head = %v (%v), want the one kept", drafts, err)
	}

	moved := writeAndCommit(t, fx.RepoDir, "moved.txt", "a later commit", "the clone moved on")
	if tip, err := BranchMoved(d, same); err != nil || tip != moved {
		t.Fatalf("BranchMoved = %q (%v), want the clone's new head %s", tip, err, moved)
	}
	after, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare after the clone moved: %v", err)
	}
	if after.Head != moved || after.Round != 1 || after.Reviewer.Known() {
		t.Fatalf("reopened after the move: round %d at %s, reviewer %+v; want round 1 at %s with the reviewer forgotten", after.Round, after.Head, after.Reviewer, moved)
	}
	if drafts, err := ReadDrafts(after); err != nil || len(drafts) != 0 {
		t.Fatalf("drafts after reopening at a new head = %v (%v), want none: they were about the old head", drafts, err)
	}
}

// TestAppendDraftHoldsADraftToTheRulesOfAResult: a draft is checked as it is
// taken, by the rules ParseReviewResult holds a result to and the request's own
// (the manifest's oracles, the findings listed under open and dismissed);
// what needs the rest of the report waits for the round to be done.
func TestAppendDraftHoldsADraftToTheRulesOfAResult(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	file := p.Request.MustReview[0]
	good := ResultFinding{File: file, Line: 1, Title: "t", Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: p.Request.Oracles[0]}
	bad := map[string]Draft{
		"no title":                            {Finding: &ResultFinding{File: file, Action: ActionNote, Risk: RiskLow, RiskRationale: "r"}},
		"an unknown action":                   {Finding: &ResultFinding{File: file, Title: "t", Action: "maybe", Risk: RiskLow, RiskRationale: "r"}},
		"an unknown risk":                     {Finding: &ResultFinding{File: file, Title: "t", Action: ActionNote, Risk: "huge", RiskRationale: "r"}},
		"an absolute file":                    {Finding: &ResultFinding{File: "/etc/passwd", Title: "t", Action: ActionNote, Risk: RiskLow, RiskRationale: "r"}},
		"an unknown oracle":                   {Finding: &ResultFinding{File: file, Title: "t", Action: ActionFix, Risk: RiskLow, RiskRationale: "r", Oracle: "nope"}},
		"an unknown prior":                    {Finding: &ResultFinding{File: file, Title: "t", Action: ActionNote, Risk: RiskLow, RiskRationale: "r", Prior: "r1-f1"}},
		"a still_present of an unknown prior": {StillPresent: &StillPresentEntry{Prior: "r1-f1", Line: 3}},
		"neither":                             {},
		"both":                                {Finding: &good, StillPresent: &StillPresentEntry{Prior: "r1-f1"}},
	}
	for name, dr := range bad {
		if n, err := AppendDraft(d, p, dr); err == nil || n != 0 {
			t.Errorf("AppendDraft(%s) = %d, %v, want a refusal", name, n, err)
		}
	}
	if drafts, _ := ReadDrafts(p); len(drafts) != 0 {
		t.Fatalf("refused drafts were kept: %+v", drafts)
	}

	for i := 1; i <= 2; i++ {
		if n, err := AppendDraft(d, p, Draft{Finding: &good}); err != nil || n != i {
			t.Fatalf("AppendDraft #%d = %d, %v", i, n, err)
		}
	}
	drafts, err := ReadDrafts(p)
	if err != nil || len(drafts) != 2 {
		t.Fatalf("ReadDrafts = %v (%v), want the two taken", drafts, err)
	}
	res := DraftResult(drafts, []string{file}, "summary")
	if len(res.Findings) != 2 || res.StillPresent == nil || !reflect.DeepEqual(res.ReviewedPaths, []string{file}) || res.Summary != "summary" {
		t.Fatalf("DraftResult = %+v, want the two findings, an empty still_present list, the paths and the summary", res)
	}
	if _, err := ParseReviewResult(mustMarshalResult(t, res)); err != nil {
		t.Fatalf("a result assembled from drafts is not a valid result file: %v", err)
	}
}

// mustMarshalResult is marshalResult for a test.
func mustMarshalResult(t *testing.T, r ReviewResult) []byte {
	t.Helper()
	data, err := marshalResult(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestGateFinishRecordsTheRoundAndJudgesTheReviewer: the report of a reviewer
// who did not build the change closes the round clean and independent; its
// words reach the round's files and the journal's line carries who reported
// it. The same report from one of the builders is recorded too, and is not
// clean: the report says independent: false and the error is
// REVIEW_NOT_INDEPENDENT. A reviewer jig cannot identify is independent:
// unknown, and the round stands.
func TestGateFinishRecordsTheRoundAndJudgesTheReviewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		reviewer    Provenance
		wantIndep   Independence
		wantVerdict string
		wantErr     string
	}{
		{"a fresh reviewer", Provenance{Session: "s-reviewer", How: ReviewerByFlag, Model: "m1", Effort: "high"}, IndependentYes, "clean", ""},
		{"a builder", Provenance{Session: "s-builder", How: ReviewerByEnv}, IndependentNo, VerdictNotIndependent, "REVIEW_NOT_INDEPENDENT"},
		{"the builder's subagent", Provenance{Session: "s-builder", Agent: "sub-1", How: ReviewerByLedger}, IndependentYes, "clean", ""},
		{"nobody known", Provenance{}, IndependentUnknown, "clean", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx, d, _ := cloneBuilt(t)
			if err := journal.Append(d.Store, fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Session: "s-builder", Commit: strings.Repeat("a", 40)}); err != nil {
				t.Fatal(err)
			}
			p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
			if err != nil {
				t.Fatalf("GatePrepare: %v", err)
			}

			report, err := GateFinish(d, p, cleanReport(p), tc.reviewer)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("GateFinish: %v", err)
				}
			} else {
				wantAxiCode(t, err, tc.wantErr)
			}
			if report.Round != 1 || report.Verdict != tc.wantVerdict || report.Independent != tc.wantIndep || report.Reviewer != tc.reviewer {
				t.Fatalf("report = round %d, verdict %q, independent %q, reviewer %+v; want verdict %q, independent %q, reviewer %+v", report.Round, report.Verdict, report.Independent, report.Reviewer, tc.wantVerdict, tc.wantIndep, tc.reviewer)
			}
			rep := reportYAMLAt(t, d, fx.Ticket, 1)
			if rep.Verdict != tc.wantVerdict || rep.Independent != tc.wantIndep || !reflect.DeepEqual(rep.OracleNames, p.Request.Oracles) || rep.ReviewedSHA["fixture-repo"] != p.Head {
				t.Errorf("report.yaml = %+v, want verdict %q, independent %q, the oracle names and the reviewed head %s", rep, tc.wantVerdict, tc.wantIndep, p.Head)
			}
			if tc.reviewer.Model != "" && (rep.Model != tc.reviewer.Model || rep.Effort != tc.reviewer.Effort) {
				t.Errorf("report.yaml model = %q, effort = %q, want what the reviewer reported", rep.Model, rep.Effort)
			}
			raw, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "report.yaml"))
			if err != nil || !strings.Contains(string(raw), "independent: "+string(tc.wantIndep)+"\n") {
				t.Errorf("report.yaml = %q (%v), want a plain `independent: %s` line", raw, err, tc.wantIndep)
			}
			lines, err := journal.Read(d.Store, fx.Ticket)
			if err != nil {
				t.Fatal(err)
			}
			event := journal.EventGateRound
			if tc.wantVerdict == "clean" {
				event = journal.EventGateClean
			}
			var round journal.Line
			for _, l := range lines {
				if l.Event == event {
					round = l
				}
			}
			if round.Event == "" || round.Session != tc.reviewer.Session || round.Agent != tc.reviewer.Agent || round.Model != tc.reviewer.Model {
				t.Errorf("round line = %+v, want a %s line for the reviewer %+v", round, event, tc.reviewer)
			}
			if _, ok, err := CurrentRound(d, fx.Ticket); err != nil || ok {
				t.Errorf("CurrentRound after the round = ok %v (%v), want none open", ok, err)
			}
		})
	}
}

// TestGateFinishFindsTheReviewerWhenNobodyNamedIt: the reviewer is, in order,
// the one the caller names, the one that read the round with `jig review
// context --session`, and the session of the latest ledger tool line that ran
// `jig review context` for the ticket after the round opened.
func TestGateFinishFindsTheReviewerWhenNobodyNamedIt(t *testing.T) {
	t.Parallel()
	t.Run("recorded when it read the round", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := cloneBuilt(t)
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := RecordReviewer(d, p, Provenance{Session: "s-rev", Agent: "a-1", How: ReviewerByFlag}); err != nil {
			t.Fatal(err)
		}
		p, ok, err := CurrentRound(d, fx.Ticket)
		if err != nil || !ok {
			t.Fatal("no open round")
		}
		report, err := GateFinish(d, p, cleanReport(p), Provenance{})
		if err != nil || report.Reviewer.Session != "s-rev" || report.Reviewer.Agent != "a-1" || report.Reviewer.How != ReviewerByFlag || report.Independent != IndependentYes {
			t.Fatalf("report reviewer = %+v, independent %q (%v), want the one that read the round", report.Reviewer, report.Independent, err)
		}
	})
	t.Run("from the ledger", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := cloneBuilt(t)
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatal(err)
		}
		at := func(offset time.Duration) string { return p.Opened.Add(offset).Format(time.RFC3339Nano) }
		ticket := fx.Ticket
		for _, l := range []journal.Line{
			// Before the round opened, or about another ticket or command: not the reviewer.
			{TS: at(-time.Minute), Event: journal.EventTool, Session: "s-early", Command: "jig review context " + ticket},
			{TS: at(time.Second), Event: journal.EventTool, Session: "s-other", Command: "jig review context JIG-9"},
			{TS: at(time.Second), Event: journal.EventTool, Session: "s-readonly", Command: "git log --oneline"},
			// Two that did: the later one stands.
			{TS: at(2 * time.Second), Event: journal.EventTool, Session: "s-first", Command: "cd x && jig review context " + ticket + " --json"},
			{TS: at(3 * time.Second), Event: journal.EventTool, Session: "s-last", Agent: "sub-9", Command: `"C:\bin\jig.exe" review context ` + ticket},
		} {
			if err := ledger.Append(d.Home, l); err != nil {
				t.Fatal(err)
			}
		}
		report, err := GateFinish(d, p, cleanReport(p), Provenance{})
		if err != nil || report.Reviewer.Session != "s-last" || report.Reviewer.Agent != "sub-9" || report.Reviewer.How != ReviewerByLedger {
			t.Fatalf("report reviewer = %+v (%v), want the latest ledger record of `jig review context`", report.Reviewer, err)
		}
	})
	// A session from the environment is the harness session: a subagent
	// reviewing in the session its builders ran in is told apart from them by
	// the agent the ledger recorded reading the round, and a record from
	// another session names nobody.
	t.Run("an environment session's agent from the ledger", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name      string
			ledger    journal.Line
			wantAgent string
			wantIndep Independence
		}{
			{"read by a subagent", journal.Line{Session: "s-builder", Agent: "sub-2"}, "sub-2", IndependentYes},
			{"read in another session", journal.Line{Session: "s-other", Agent: "sub-2"}, "", IndependentNo},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				fx, d, _ := cloneBuilt(t)
				if err := journal.Append(d.Store, fx.Ticket, journal.Line{Slice: "a", Event: journal.EventClaim, Session: "s-builder"}); err != nil {
					t.Fatal(err)
				}
				p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
				if err != nil {
					t.Fatal(err)
				}
				l := tc.ledger
				l.TS, l.Event, l.Command = p.Opened.Add(time.Second).Format(time.RFC3339Nano), journal.EventTool, "jig review context "+fx.Ticket
				if err := ledger.Append(d.Home, l); err != nil {
					t.Fatal(err)
				}
				report, _ := GateFinish(d, p, cleanReport(p), Provenance{Session: "s-builder", How: ReviewerByEnv})
				if report.Reviewer.Session != "s-builder" || report.Reviewer.Agent != tc.wantAgent || report.Reviewer.How != ReviewerByEnv || report.Independent != tc.wantIndep {
					t.Errorf("report reviewer = %+v, independent %q, want s-builder/%q by env, %q", report.Reviewer, report.Independent, tc.wantAgent, tc.wantIndep)
				}
			})
		}
	})
}

// TestRunsReviewContext: the command lines a harness records that count as
// reading a round.
func TestRunsReviewContext(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]bool{
		"jig review context JIG-1":                    true,
		"jig review context":                          true,
		"jig review context --session s-1 JIG-1":      true,
		"jig.exe review context JIG-1 --json":         true,
		"/usr/local/bin/jig review context JIG-1":     true,
		"cd repo && jig review context JIG-1":         true,
		"jig review context JIG-1 && echo done":       true,
		"jig review context JIG-2":                    false,
		"jig review context JIG-2 ; jig status JIG-1": false,
		"jig review done JIG-1":                       false,
		"jig finding JIG-1":                           false,
		"git review context JIG-1":                    false,
		"":                                            false,
	} {
		if got := runsReviewContext(cmd, "JIG-1"); got != want {
			t.Errorf("runsReviewContext(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// TestBuildersAreTheAgentsThatChangedTheBranch: the session and agent on the
// journal's claim, verify-pending and oracle lines, and the ledger's
// git-commit lines and tool lines that changed files, for the ticket or its
// branch. A tool line that read, searched or listed is no building: a
// reviewer reads with the same tools.
func TestBuildersAreTheAgentsThatChangedTheBranch(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	ticket, branch := fx.Ticket, ticketBranch(fx.Ticket)
	for _, l := range []journal.Line{
		{Event: journal.EventClaim, Session: "s-claimer", Slice: "a"},
		{Event: journal.EventOracle, Session: "s-oracle", Agent: "a-2", Slice: "a", Outcome: "pass"},
		{Event: journal.EventGateOpen, Session: "s-opener"},
	} {
		if err := journal.Append(d.Store, ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []journal.Line{
		{Event: journal.EventGitCommit, Session: "s-committer", Ticket: ticket},
		{Event: journal.EventTool, Session: "s-editor", Ticket: ticket, Files: []string{"alpha/alpha.go"}},
		{Event: journal.EventTool, Session: "s-onbranch", Branch: branch, Files: []string{"a.go"}},
		{Event: journal.EventTool, Session: "s-reader", Ticket: ticket, Command: "git log"},
		{Event: journal.EventGitCommit, Session: "s-elsewhere", Ticket: "JIG-9"},
	} {
		if err := ledger.Append(d.Home, l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Builders(d, ticket, branch)
	if err != nil {
		t.Fatalf("Builders: %v", err)
	}
	var names []string
	for _, b := range got {
		names = append(names, b.Session+"/"+b.Agent)
	}
	want := []string{"s-claimer/", "s-oracle/a-2", "s-committer/", "s-editor/", "s-onbranch/"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("builders = %v, want %v", names, want)
	}

	for who, want := range map[Provenance]Independence{
		{Session: "s-reader"}:               IndependentYes,
		{Session: "s-opener"}:               IndependentYes,
		{Session: "s-oracle"}:               IndependentYes, // the main agent of a session whose subagent built
		{Session: "s-oracle", Agent: "a-2"}: IndependentNo,
		{Session: "s-claimer"}:              IndependentNo,
		{}:                                  IndependentUnknown,
	} {
		if got, err := JudgeIndependence(d, ticket, branch, who); err != nil || got != want {
			t.Errorf("JudgeIndependence(%+v) = %q (%v), want %q", who, got, err, want)
		}
	}
}

// TestIndependenceReadsAndWritesAsAPlainYAMLScalar: true and false are YAML
// booleans in report.yaml, unknown the plain word, and an absent value stays
// absent.
func TestIndependenceReadsAndWritesAsAPlainYAMLScalar(t *testing.T) {
	t.Parallel()
	type doc struct {
		Independent Independence `yaml:"independent,omitempty"`
	}
	for in, want := range map[Independence]string{IndependentYes: "independent: true\n", IndependentNo: "independent: false\n", IndependentUnknown: "independent: unknown\n", "": "{}\n"} {
		out, err := yaml.Marshal(doc{Independent: in})
		if err != nil || string(out) != want {
			t.Errorf("marshal %q = %q (%v), want %q", in, out, err, want)
		}
		var back doc
		if err := yaml.Unmarshal(out, &back); err != nil || back.Independent != in {
			t.Errorf("unmarshal %q = %q (%v), want it back", out, back.Independent, err)
		}
	}
}

// TestGateFinishRefusesWhatItCannotStandBehind: a report is refused, and the
// round stays open for a corrected one, when no round is open, when the
// reviewer changed the lease, when the clone's branch moved since the round
// opened, and when the report fails the checks a launched reviewer's does. A
// refusal writes nothing: no round, no journal line, no failure commit.
func TestGateFinishRefusesWhatItCannotStandBehind(t *testing.T) {
	t.Parallel()
	t.Run("no round open", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := cloneBuilt(t)
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := GateFinish(d, p, cleanReport(p), Provenance{}); err != nil {
			t.Fatalf("GateFinish: %v", err)
		}
		_, err = GateFinish(d, p, cleanReport(p), Provenance{})
		wantAxiCode(t, err, "REVIEW_NOT_OPEN")
	})
	t.Run("the lease changed", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := cloneBuilt(t)
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.Lease, p.Request.MustReview[0]), []byte("edited by the reviewer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err = GateFinish(d, p, cleanReport(p), Provenance{})
		wantAxiCode(t, err, "REVIEW_INVALID")
		if _, statErr := os.Stat(gateRoundDir(d.Store, fx.Ticket, 1)); statErr == nil {
			t.Fatal("a refused report made gate/round-1")
		}
	})
	t.Run("the branch moved", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := cloneBuilt(t)
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatal(err)
		}
		writeAndCommit(t, fx.RepoDir, "late.txt", "after the round opened", "a commit after the round opened")
		_, err = GateFinish(d, p, cleanReport(p), Provenance{})
		wantAxiCode(t, err, "REVIEW_STALE_HEAD")
	})
	t.Run("a report that fails the checks", func(t *testing.T) {
		t.Parallel()
		fx, d, _ := cloneBuilt(t)
		p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatal(err)
		}
		subjectBefore := run(t, d.Store.Root, "log", "-1", "--format=%s")
		linesBefore, _ := journal.Read(d.Store, fx.Ticket)
		res := cleanReport(p)
		res.ReviewedPaths = nil // misses every must_review path
		_, err = GateFinish(d, p, res, Provenance{})
		wantAxiCode(t, err, "REVIEW_INVALID")
		if _, statErr := os.Stat(gateRoundDir(d.Store, fx.Ticket, 1)); statErr == nil {
			t.Fatal("a refused report made gate/round-1")
		}
		if linesAfter, _ := journal.Read(d.Store, fx.Ticket); len(linesAfter) != len(linesBefore) {
			t.Fatalf("the refused report journaled %d line(s)", len(linesAfter)-len(linesBefore))
		}
		if subject := run(t, d.Store.Root, "log", "-1", "--format=%s", "HEAD"); subject != subjectBefore {
			t.Fatalf("the refused report committed %q to the store", subject)
		}
		if _, ok, err := CurrentRound(d, fx.Ticket); err != nil || !ok {
			t.Fatalf("CurrentRound = ok %v (%v), want the round still open", ok, err)
		}
		// The corrected report goes through.
		if _, err := GateFinish(d, p, cleanReport(p), Provenance{}); err != nil {
			t.Fatalf("GateFinish with the corrected report: %v", err)
		}
	})
}

// TestRecordedTriageAppliesWhatAPersonDecidedAndNothingElse: an ask a person
// decided with `jig triage` is decided as they said, kept or dismissed, with
// the reason and the build target they gave; a round opened with --yes keeps
// what jig can route on its own; with neither, an ask stays undecided. Fixes
// are kept in every case.
func TestRecordedTriageAppliesWhatAPersonDecidedAndNothingElse(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	if err := journal.Append(d.Store, fx.Ticket, journal.Line{
		Event: journal.EventTriage, Ref: "r1-f1", Outcome: TriageKeep, Summary: "yes, fix it",
		Command: TriageTarget("alpha", "test"), Provenance: "person",
	}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(d.Store, fx.Ticket, journal.Line{Event: journal.EventTriage, Ref: "r1-f2", Outcome: TriageKeep, Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(d.Store, fx.Ticket, journal.Line{Event: journal.EventTriage, Ref: "r1-f2", Outcome: TriageDismiss, Summary: "no, leave it", Provenance: "agent"}); err != nil {
		t.Fatal(err)
	}
	in := TriageInput{
		Fixes: []Finding{{ID: "r1-f9"}},
		Asks: []Finding{
			{ID: "r1-f1", Workspace: ""}, {ID: "r1-f2", Workspace: "alpha"}, {ID: "r1-f3", Workspace: "alpha"}, {ID: "r1-f4"},
		},
	}

	res := recordedTriage(d, fx.Ticket, false)(in)
	if len(res.DismissedFixIDs) != 0 {
		t.Errorf("DismissedFixIDs = %v, want every fix kept", res.DismissedFixIDs)
	}
	want := map[string]AskOutcome{
		"r1-f1": {Keep: true, Decision: "yes, fix it", Workspace: "alpha", Oracle: "test", Human: true},
		"r1-f2": {Keep: false, Decision: "no, leave it", Human: false}, // the last decision stands
	}
	if !reflect.DeepEqual(res.Asks, want) {
		t.Errorf("Asks = %+v, want %+v: r1-f3 and r1-f4 undecided without --yes", res.Asks, want)
	}

	// With --yes the asks nobody decided that have a full build target are kept
	// as DefaultTriage keeps them; r1-f4 has no workspace and stays undecided.
	in.Manifest.Oracles = map[string]string{"test": "true"}
	res = recordedTriage(d, fx.Ticket, true)(in)
	if got := res.Asks["r1-f3"]; !got.Keep || got.Human {
		t.Errorf("Asks[r1-f3] with --yes = %+v, want kept by default, not by a human", got)
	}
	if _, ok := res.Asks["r1-f4"]; ok {
		t.Errorf("Asks[r1-f4] with --yes is decided: %+v, want undecided (no workspace)", res.Asks["r1-f4"])
	}
	if got := res.Asks["r1-f1"]; got.Decision != "yes, fix it" || !got.Human {
		t.Errorf("Asks[r1-f1] with --yes = %+v, want the person's decision still", got)
	}
}

// TestPublishRefusesARoundThatIsNotIndependent: publish takes a round whose
// reviewer was independent or unknown, and refuses one that is recorded as
// not independent even if its verdict was edited to clean.
func TestPublishRefusesARoundThatIsNotIndependent(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GateFinish(d, p, cleanReport(p), Provenance{Session: "s-rev"}); err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	reportPath := filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "report.yaml")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "independent: true") {
		t.Fatalf("report.yaml = %s", data)
	}
	edited := strings.Replace(string(data), "independent: true", "independent: false", 1)
	if err := os.WriteFile(reportPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket}); err == nil {
		t.Fatal("PublishBody over a round recorded as not independent succeeded")
	} else {
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "PUBLISH_NOT_INDEPENDENT" {
			t.Fatalf("err = %v, want PUBLISH_NOT_INDEPENDENT", err)
		}
	}
}

// TestReadStatusSaysWhereTheGateAndThePublishStand: the open round, the latest
// round's verdict and reviewer, and what the publish has journaled.
func TestReadStatusSaysWhereTheGateAndThePublishStand(t *testing.T) {
	t.Parallel()
	fx, d, head := cloneBuilt(t)
	if s, err := ReadStatus(d.Store, fx.Ticket); err != nil || s.Rounds != 0 || s.Open != nil || s.PR != "" {
		t.Fatalf("ReadStatus before any round = %+v (%v), want nothing", s, err)
	}
	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendDraft(d, p, Draft{Finding: &ResultFinding{File: p.Request.MustReview[0], Title: "t", Action: ActionNote, Risk: RiskLow, RiskRationale: "r"}}); err != nil {
		t.Fatal(err)
	}
	s, err := ReadStatus(d.Store, fx.Ticket)
	if err != nil || s.Open == nil || s.Open.Round != 1 || s.Open.Drafts != 1 || s.Open.Head != head || s.Rounds != 0 {
		t.Fatalf("ReadStatus with a round open = %+v (%v), want round 1 open with one draft at %s", s, err, head)
	}

	if _, err := GateFinish(d, p, ReviewResult{Findings: []ResultFinding{}, StillPresent: []StillPresentEntry{}, ReviewedPaths: p.Request.MustReview}, Provenance{Session: "s-rev"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishApprove(d, PublishOpts{Ticket: fx.Ticket}, "person"); err != nil {
		t.Fatalf("PublishApprove: %v", err)
	}
	s, err = ReadStatus(d.Store, fx.Ticket)
	if err != nil || s.Open != nil || s.Rounds != 1 || s.Verdict != "clean" || s.Independent != IndependentYes || s.Reviewer == nil || s.Reviewer.Session != "s-rev" ||
		s.ReviewedSHA["fixture-repo"] != head || s.ApprovedFor != head || s.Published {
		t.Fatalf("ReadStatus after a clean round and an approval = %+v (%v)", s, err)
	}
}

// TestPointAtBranchCopyFallsBackToTheBuildLeaseWithoutAClone: with no clone
// holding the branch, the choice is the one the build lease and origin always
// had (pointAtTicketBranch); a branch no copy has is BRANCH_NOT_FOUND; and an
// adopted branch gone from origin is, too, whatever the build lease holds,
// since a branch nobody can open a pull request from is not one to review.
func TestPointAtBranchCopyFallsBackToTheBuildLeaseWithoutAClone(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	branch := ticketBranch(fx.Ticket)
	acquire := func(t *testing.T, branch string) string {
		t.Helper()
		lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Publish)
		if err != nil {
			t.Fatalf("acquire the publish lease: %v", err)
		}
		return lease.Dir
	}
	point := func(t *testing.T, adopted bool) (branchCopy, error) {
		t.Helper()
		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		return pointAtBranchCopy(d, acquire(t, branch), "fixture-repo", fx.Ticket, branch, adopted, journal.BuiltCommits(lines), "publish")
	}

	// A clone that is mapped but lacks the branch changes nothing.
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	if got, err := point(t, false); err != nil || got != buildLeasesCopy {
		t.Fatalf("copy = %v (%v), want the build lease's: the branch is built there and not pushed", got, err)
	}

	// Gone from the build lease and absent from origin: no copy.
	if err := os.RemoveAll(buildLeaseDir(t, fx)); err != nil {
		t.Fatal(err)
	}
	_, err := point(t, false)
	wantAxiCode(t, err, "BRANCH_NOT_FOUND")

	// An adopted branch whose only copy is the build lease's is refused too.
	buildLeaseDir(t, fx) // a fresh build lease, on the branch cut from main
	run(t, buildLeaseOf(t, fx, fx.Ticket), "checkout", "-B", branch, "origin/main")
	writeAndCommit(t, buildLeaseOf(t, fx, fx.Ticket), "built.txt", "built", "built")
	_, err = point(t, true)
	wantAxiCode(t, err, "BRANCH_NOT_FOUND")
}
