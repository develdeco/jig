package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// The sections of `jig status` that carry the records the screens show: the
// slices' workspace and oracle, the rounds, the latest round's findings and the
// publish. Each test writes the files a round or a publish leaves in the store
// and reads what status prints.

// roundFiles are the files a gate round leaves: its report.yaml, and its
// findings.yaml when a reviewer ran it (findings "" for a round with none).
type roundFiles struct {
	report, findings string
}

// writeRound records round n of the fixture's ticket as files.
func writeRound(t *testing.T, storeDir, ticket string, n int, f roundFiles) {
	t.Helper()
	dir := filepath.Join(storeDir, "tickets", ticket, "gate", fmt.Sprintf("round-%d", n))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte(f.report), 0o644); err != nil {
		t.Fatalf("write report.yaml: %v", err)
	}
	if f.findings != "" {
		if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), []byte(f.findings), 0o644); err != nil {
			t.Fatalf("write findings.yaml: %v", err)
		}
	}
}

// finding is one findings.yaml entry.
func finding(id, action, risk, file string, line int, title, status string) string {
	return fmt.Sprintf("  - id: %s\n    file: %s\n    line: %d\n    title: %s\n    detail: d\n    action: %s\n"+
		"    risk: %s\n    risk_rationale: why %s matters\n    status: %s\n    recurrences: 0\n", id, file, line, title, action, risk, id, status)
}

// sha40 is forty copies of one digit: a sha.
func sha40(d string) string { return strings.Repeat(d, 40) }

// statusOf renders the fixture ticket's status.
func statusOf(t *testing.T, fx *fixture.Fixture, st *store.Store) string {
	t.Helper()
	out, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	return out
}

// block is the lines of the block of out that starts with header, up to the
// next line that is not indented.
func block(out, header string) []string {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, header) {
			end := i + 1
			for end < len(lines) && strings.HasPrefix(lines[end], "  ") {
				end++
			}
			return lines[i:end]
		}
	}
	return nil
}

func openStore(t *testing.T, fx *fixture.Fixture) *store.Store {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st
}

// twoRounds records a round 1 that raised a fix, an ask and a note and a delta
// round 2 that raised one more ask, both reviewed by s-rev. Round 1's request is
// still in the work folder (its base is in it); round 2's is gone.
func twoRounds(t *testing.T, fx *fixture.Fixture, st *store.Store) {
	t.Helper()
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{
		report: "round: 1\nverdict: fix-slices\nmodel: claude-opus-5\ntarget_sha:\n  fixture-repo: " + sha40("1") + "\n" +
			"reviewed_sha:\n  fixture-repo: " + sha40("2") + "\nindependent: true\nreviewer:\n  session: s-rev\n  how: env\nfix_slices:\n  - fix-1\n",
		findings: "scope: full\nreviewed_paths:\n  - a.go\n  - b.go\n  - c.go\nfindings:\n" +
			finding("r1-f1", "fix", "high", "a.go", 3, "Unchecked error", "open") +
			finding("r1-f2", "ask", "medium", "b.go", 9, "Which greeting?", "asked") +
			finding("r1-f3", "note", "low", "c.go", 1, "Rename later", "noted") + "summary: round 1\n",
	})
	review := filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "work", "gate.round-1.review.json")
	if err := os.MkdirAll(filepath.Dir(review), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(review, []byte(`{"base_sha":"`+sha40("0")+`","head_sha":"`+sha40("2")+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRound(t, fx.StoreDir, fx.Ticket, 2, roundFiles{
		report: "round: 2\nverdict: fix-slices\ntarget_sha:\n  fixture-repo: " + sha40("3") + "\n" +
			"reviewed_sha:\n  fixture-repo: " + sha40("4") + "\nindependent: unknown\nreviewer:\n  session: s-rev\n  how: env\n",
		findings: "scope: delta\nreviewed_paths:\n  - a.go\nfindings:\n" +
			finding("r1-f2", "ask", "medium", "b.go", 9, "Which greeting?", "asked") +
			finding("r2-f1", "ask", "high", "a.go", 5, "Still unchecked", "asked") + "summary: round 2\n",
	})
	if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventGateReviewer, Attempt: 1, Session: "s-rev", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
}

// TestStatusSlicesNameTheirWorkspaceOracleAndRound: the slices table keeps its
// columns and their order and ends in the workspace and oracle each slice
// names and, for a fix slice, the round it came from.
func TestStatusSlicesNameTheirWorkspaceOracleAndRound(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	fix := store.Slice{ID: "fix-1", Workspace: "alpha", Goal: "Fix the finding", Oracle: "go test ./...", BlockedBy: []string{"a"}, FromGate: 2}
	if err := st.AppendSlices(fx.Ticket, []store.Slice{fix}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState(fx.Ticket, "fix-1", store.SliceState{State: "queued"}); err != nil {
		t.Fatal(err)
	}
	got := block(statusOf(t, fx, st), "slices[")
	want := []string{
		"slices[5]{id,state,attempts,blocked_by,question,goal,workspace,oracle,from_gate}:",
		"  a,queued,0,-,-,Fix Clamp so TestClamp passes.,alpha,test,-",
		"  fix-1,queued,0,a,-,Fix the finding,alpha,go test ./...,2",
	}
	if len(got) != 6 || got[0] != want[0] || got[1] != want[1] || got[5] != want[2] {
		t.Errorf("slices block:\n%s\nwant it to start %q and end %q", strings.Join(got, "\n"), want[:2], want[2])
	}
}

// TestStatusRoundsListsEachRecordedRound: one row per round, with who reviewed
// it, whether they were independent, what it raised by the reviewer's label,
// and the range and the paths it reviewed.
func TestStatusRoundsListsEachRecordedRound(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	if got := statusOf(t, fx, st); strings.Contains(got, "rounds[") || strings.Contains(got, "findings[") || strings.Contains(got, "publish:") {
		t.Errorf("a ticket with no round or publish has their blocks:\n%s", got)
	}
	twoRounds(t, fx, st)

	got := block(statusOf(t, fx, st), "rounds[")
	want := []string{
		"rounds[2]{round,verdict,reviewer,harness,model,independent,fix,ask,note,scope,base,head,files}:",
		// Round 1: the request is in the work folder, so its base is there.
		"  1,findings,s-rev,claude,claude-opus-5,true,1,1,1,full," + sha40("0") + "," + sha40("2") + ",3",
		// Round 2: only r2-f1 is its own (r1-f2 is round 1's, reported again),
		// the scope is a delta, so the base is the head round 1 reviewed. The
		// same session reviewed it, so the harness the journal names for it
		// stands; the report names no model.
		"  2,findings,s-rev,claude,-,unknown,0,1,0,delta," + sha40("2") + "," + sha40("4") + ",1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rounds block:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestStatusRoundsNamesARoundNoReviewerRan: a scripted round has no reviewer,
// scope or paths, and a clean round and one the reviewer built are told apart.
func TestStatusRoundsNamesARoundNoReviewerRan(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{report: "round: 1\nverdict: fix-slices\ntarget_sha:\n  fixture-repo: " + sha40("5") + "\n"})
	writeRound(t, fx.StoreDir, fx.Ticket, 2, roundFiles{
		report:   "round: 2\nverdict: not-independent\nreviewed_sha:\n  fixture-repo: " + sha40("6") + "\nindependent: false\nreviewer:\n  session: s-builder\n",
		findings: "scope: full\nreviewed_paths: []\nfindings: []\n",
	})
	writeRound(t, fx.StoreDir, fx.Ticket, 3, roundFiles{
		report:   "round: 3\nverdict: clean\nreviewed_sha:\n  fixture-repo: " + sha40("7") + "\nindependent: true\nreviewer:\n  session: s-rev\n",
		findings: "scope: delta\nreviewed_paths:\n  - a.go\nfindings: []\n",
	})
	got := block(statusOf(t, fx, st), "rounds[")
	want := []string{
		"rounds[3]{round,verdict,reviewer,harness,model,independent,fix,ask,note,scope,base,head,files}:",
		"  1,findings,-,-,-,-,0,0,0,-,-," + sha40("5") + ",-",
		"  2,not-independent,s-builder,-,-,false,0,0,0,full,-," + sha40("6") + ",0",
		"  3,clean,s-rev,-,-,true,0,0,0,delta," + sha40("6") + "," + sha40("7") + ",1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rounds block:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(statusOf(t, fx, st), "findings[") {
		t.Errorf("a latest round that raised nothing has a findings block")
	}
}

// TestStatusFindingsListsTheLatestRoundsFindings: the findings the latest round
// raised, high risk first, with the decision a person or an agent has pending
// for each and nothing of an earlier round's.
func TestStatusFindingsListsTheLatestRoundsFindings(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st := greenTicket(t, fx)
	twoRounds(t, fx, st)
	// Round 2 also raised a note, and its file carries r1-f2.
	writeRound(t, fx.StoreDir, fx.Ticket, 2, roundFiles{
		report: "round: 2\nverdict: fix-slices\nreviewed_sha:\n  fixture-repo: " + sha40("4") + "\nindependent: true\nreviewer:\n  session: s-rev\n",
		findings: "scope: delta\nreviewed_paths:\n  - a.go\nfindings:\n" +
			finding("r1-f2", "ask", "medium", "b.go", 9, "Which greeting?", "asked") +
			finding("r2-f2", "note", "low", "c.go", 2, "Tidy, later", "noted") +
			finding("r2-f1", "ask", "high", "a.go", 5, "Still unchecked", "asked") + "summary: round 2\n",
	})
	if code := triageAs(t, fx, true, "r2-f1", "dismiss", "--reason", "fine"); code != 0 {
		t.Fatalf("jig triage exit = %d", code)
	}
	got := block(statusOf(t, fx, st), "findings[")
	want := []string{
		"findings[2]{id,round,action,risk,file:line,title,status,rationale,decision,decided_by}:",
		`  r2-f1,2,ask,high,"a.go:5",Still unchecked,asked,why r2-f1 matters,dismiss,person`,
		`  r2-f2,2,note,low,"c.go:2","Tidy, later",noted,why r2-f2 matters,,`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings block:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestStatusFindingsDropsADecisionARoundHasTaken: a decision recorded before the
// latest round closed is spent, and its row says nothing of it.
func TestStatusFindingsDropsADecisionARoundHasTaken(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st := greenTicket(t, fx)
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{
		report: "round: 1\nverdict: fix-slices\nindependent: true\nreviewer:\n  session: s-rev\n",
		findings: "scope: full\nreviewed_paths:\n  - a.go\nfindings:\n" +
			finding("r1-f1", "ask", "high", "a.go", 5, "Unchecked", "asked") + "summary: round 1\n",
	})
	if code := triageAs(t, fx, true, "r1-f1", "keep", "--workspace", "alpha", "--oracle", "test"); code != 0 {
		t.Fatalf("jig triage exit = %d", code)
	}
	if got := block(statusOf(t, fx, st), "findings[")[1]; !strings.HasSuffix(got, ",keep,person") {
		t.Errorf("row of the pending decision = %q, want it to end in the keep", got)
	}
	if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventGateRound, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if got := block(statusOf(t, fx, st), "findings[")[1]; !strings.HasSuffix(got, ",,") {
		t.Errorf("row of the spent decision = %q, want no decision", got)
	}
}

// TestStatusPublishReadsNoFileOutsideTheStore: a pr-body line is the store's
// own, shared by every writer, and a path in it that leaves the store reads as
// no body.
func TestStatusPublishReadsNoFileOutsideTheStore(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	outside := filepath.Join(filepath.Dir(fx.StoreDir), "outside.md")
	if err := os.WriteFile(outside, []byte("not the store's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"../outside.md", "tickets/../../outside.md", filepath.ToSlash(outside), "/etc/hosts"} {
		line := journal.Line{Event: journal.EventPrBody, Commit: sha40("c"), Branch: "jig/" + fx.Ticket, Ref: ref}
		if err := journal.Append(st, fx.Ticket, line); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(block(statusOf(t, fx, st), "publish:"), "\n")
		if strings.Contains(got, "body") || strings.Contains(got, "not the store") {
			t.Errorf("a body named %q is read:\n%s", ref, got)
		}
	}
}

// TestStatusPublishNamesThePullRequestAndTheFirstLinesOfItsBody: the block
// grows as the publish does, from the body to the pull request to the changelog.
func TestStatusPublishNamesThePullRequestAndTheFirstLinesOfItsBody(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	head := sha40("c")
	bodyRel := "tickets/" + fx.Ticket + "/pr/fixture-repo.md"
	body := "## Intent\n\nShip the greeting copy.\n\n## What changed\n\n- a: " + strings.Repeat("long ", 60) + "\n- b\n- c\n- d\n- e\n"
	if err := os.MkdirAll(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "pr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.StoreDir, filepath.FromSlash(bodyRel)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventPrBody, Commit: head, Branch: "jig/" + fx.Ticket, Ref: bodyRel}); err != nil {
		t.Fatal(err)
	}

	pairs := func() map[string]string {
		out := map[string]string{}
		for _, l := range block(statusOf(t, fx, st), "publish:")[1:] {
			k, v, _ := strings.Cut(strings.TrimPrefix(l, "  "), ": ")
			out[k] = v
		}
		return out
	}
	got := pairs()
	if got["ticket"] != fx.Ticket || got["head"] != head || got["branch"] != "jig/"+fx.Ticket || got["body_file"] != bodyRel {
		t.Errorf("publish after the body: %v", got)
	}
	if _, ok := got["pr"]; ok {
		t.Errorf("publish names a pull request before one was opened: %v", got)
	}
	if _, ok := got["changelog"]; ok {
		t.Errorf("publish has a changelog before the publish finished: %v", got)
	}
	// The first eight lines that say something, the long one cut.
	long := "- a: " + strings.Repeat("long ", 60)
	wantBody := `"## Intent\nShip the greeting copy.\n## What changed\n` + strings.TrimRight(string([]rune(long)[:157]), " ") + `...\n- b\n- c\n- d\n- e"`
	if got["body"] != wantBody {
		t.Errorf("body = %q\nwant %q", got["body"], wantBody)
	}

	pr := journal.Line{Event: journal.EventPr, Commit: head, Branch: "jig/" + fx.Ticket, Outcome: "opened", URL: "https://example.test/acme/demo/pull/7"}
	if err := journal.Append(st, fx.Ticket, pr); err != nil {
		t.Fatal(err)
	}
	changelogRel := "tickets/" + fx.Ticket + "/changelog/consolidated.md"
	if err := os.MkdirAll(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "changelog"), 0o755); err != nil {
		t.Fatal(err)
	}
	changelog := "# JIG-1 - consolidated changelog\n\n## Slices\n- a: aaaaaaa\n\n## Fix rounds\n- none\n"
	if err := os.WriteFile(filepath.Join(fx.StoreDir, filepath.FromSlash(changelogRel)), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}
	got = pairs()
	if got["pr"] != `"https://example.test/acme/demo/pull/7"` || got["pr_state"] != "opened" {
		t.Errorf("publish after the pull request: %v", got)
	}
	if got["changelog_file"] != changelogRel || got["changelog"] != `"## Slices\n- a: aaaaaaa\n## Fix rounds\n- none"` {
		t.Errorf("changelog = %q (%q), want the consolidated changelog's lines without its title", got["changelog"], got["changelog_file"])
	}
}

// TestStatusPublishListsThePullRequestOfEachRepo: a publish whose lines name two
// repos has one pull request for each. The block keeps what is the ticket's (its
// branch) and a table has a row for each repo, with a dash for what the repo has
// not done yet; a ticket whose lines name one repo reads as it always did.
func TestStatusPublishListsThePullRequestOfEachRepo(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	branch := "jig/" + fx.Ticket
	api, web := sha40("a"), sha40("b")
	lines := []journal.Line{
		{Event: journal.EventPrBody, Commit: api, Branch: branch, Repo: "api", Ref: "tickets/" + fx.Ticket + "/pr/api.md"},
		{Event: journal.EventPrBody, Commit: web, Branch: branch, Repo: "web", Ref: "tickets/" + fx.Ticket + "/pr/web.md"},
		{Event: journal.EventPublishApproved, Commit: web, Branch: branch, Repo: "web", Provenance: "person"},
		{Event: journal.EventPr, Commit: api, Branch: branch, Repo: "api", Outcome: "opened", URL: "https://example.test/acme/api/pull/7"},
	}
	for _, l := range lines {
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	out := statusOf(t, fx, st)
	got := strings.Join(block(out, "publish:"), "\n")
	for _, bad := range []string{"  pr:", "  head:", "  body_file:"} {
		if strings.Contains(got, bad) {
			t.Errorf("the publish block of a ticket in two repos has %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "branch: "+branch) || !strings.Contains(got, "ticket: "+fx.Ticket) {
		t.Errorf("publish block = %s, want the ticket and its branch", got)
	}
	want := []string{
		"publish_prs[2]{repo,pr,state,head,body_file}:",
		`  api,"https://example.test/acme/api/pull/7",opened,` + api + ",tickets/" + fx.Ticket + "/pr/api.md",
		"  web,-,-," + web + ",tickets/" + fx.Ticket + "/pr/web.md",
	}
	if table := block(out, "publish_prs["); strings.Join(table, "\n") != strings.Join(want, "\n") {
		t.Errorf("publish_prs:\n%s\nwant\n%s", strings.Join(table, "\n"), strings.Join(want, "\n"))
	}

	// The second pull request is recorded: both rows have theirs.
	if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventPr, Commit: web, Branch: branch, Repo: "web", Outcome: "recorded", URL: "https://example.test/acme/web/pull/3"}); err != nil {
		t.Fatal(err)
	}
	if table := block(statusOf(t, fx, st), "publish_prs["); table[2] != `  web,"https://example.test/acme/web/pull/3",recorded,`+web+",tickets/"+fx.Ticket+"/pr/web.md" {
		t.Errorf("the web's row after its pull request was recorded = %q", table[2])
	}
}
