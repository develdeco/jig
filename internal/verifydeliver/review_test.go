package verifydeliver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// TestParseReviewResult covers every invalid case ParseReviewResult must
// catch, plus valid clean/findings results.
func TestParseReviewResult(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid clean", `{"verdict":"clean","findings":[],"closures":[],"summary":"ok"}`, false},
		{"valid findings", `{"verdict":"findings","findings":[{"id":"f1","class":"intent","title":"t","detail":"d","workspace":"alpha","oracle":"test"}],"closures":[{"id":"r1-f1","status":"closed","note":"n"}],"summary":"ok"}`, false},
		{"not json", `not json`, true},
		{"second json value", `{"verdict":"clean","findings":[],"closures":[],"summary":"ok"}{"x":1}`, true},
		{"trailing garbage", `{"verdict":"clean","findings":[],"closures":[],"summary":"ok"} garbage`, true},
		{"missing verdict", `{"findings":[],"closures":[],"summary":"ok"}`, true},
		{"headless fallback shape has no verdict", `{"outcome":"failed","summary":"x"}`, true},
		{"bad verdict", `{"verdict":"maybe","findings":[],"closures":[],"summary":"ok"}`, true},
		{"clean with findings", `{"verdict":"clean","findings":[{"id":"f1","class":"intent","title":"t","workspace":"a"}],"closures":[],"summary":"ok"}`, true},
		{"findings verdict with none", `{"verdict":"findings","findings":[],"closures":[],"summary":"ok"}`, true},
		{"bad finding class", `{"verdict":"findings","findings":[{"id":"f1","class":"weird","title":"t","workspace":"a"}],"closures":[],"summary":"ok"}`, true},
		{"empty finding title", `{"verdict":"findings","findings":[{"id":"f1","class":"intent","title":"   ","workspace":"a"}],"closures":[],"summary":"ok"}`, true},
		{"empty closure id", `{"verdict":"findings","findings":[{"id":"f1","class":"intent","title":"t","workspace":"a"}],"closures":[{"id":"","status":"closed"}],"summary":"ok"}`, true},
		{"bad closure status", `{"verdict":"findings","findings":[{"id":"f1","class":"intent","title":"t","workspace":"a"}],"closures":[{"id":"r1-f1","status":"maybe"}],"summary":"ok"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseReviewResult([]byte(c.body))
			if c.wantErr {
				var ae *axi.Error
				if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
					t.Fatalf("ParseReviewResult(%s) = %v, want *axi.Error REVIEW_INVALID", c.body, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReviewResult(%s): %v", c.body, err)
			}
		})
	}
}

// TestSynthesizeFixSlicesBundlingAndOrder checks D4's synthesis rules: one
// slice per intent finding, mechanical findings bundled per workspace,
// intent slices before mechanical bundles, and mechanical bundles pinned
// to the cheapest rung.
func TestSynthesizeFixSlicesBundlingAndOrder(t *testing.T) {
	man := manifest.Manifest{
		Workspaces: []manifest.Workspace{{ID: "alpha"}, {ID: "beta"}},
		Oracles:    map[string]string{"test": "go test"},
	}
	kept := []Finding{
		{ID: "r2-f1", Class: ClassMechanical, Title: "typo", Detail: "fix typo", Workspace: "alpha", Oracle: "test"},
		{ID: "r2-f2", Class: ClassIntent, Title: "bug", Detail: "nil deref", Workspace: "beta", Oracle: "test"},
		{ID: "r2-f3", Class: ClassMechanical, Title: "dead code", Workspace: "beta", Oracle: "test"},
	}
	slices, err := synthesizeFixSlices(2, kept, man)
	if err != nil {
		t.Fatalf("synthesizeFixSlices: %v", err)
	}
	if len(slices) != 3 {
		t.Fatalf("got %d slices, want 3: %+v", len(slices), slices)
	}
	if slices[0].ID != "fix-2-2" || slices[0].Goal != "bug\n\nnil deref" || slices[0].Rung != "" {
		t.Errorf("intent slice = %+v", slices[0])
	}
	if slices[1].ID != "fix-2-mech-alpha" || slices[1].Goal != "Fix these mechanical gate findings:\n- typo: fix typo" || slices[1].Rung != staircase.RungCheapest {
		t.Errorf("mechanical alpha bundle = %+v", slices[1])
	}
	if slices[2].ID != "fix-2-mech-beta" || slices[2].Goal != "Fix these mechanical gate findings:\n- dead code" || slices[2].Rung != staircase.RungCheapest {
		t.Errorf("mechanical beta bundle = %+v", slices[2])
	}
	for _, s := range slices {
		if s.FromGate != 2 {
			t.Errorf("slice %s FromGate = %d, want 2", s.ID, s.FromGate)
		}
	}
}

// TestSynthesizeFixSlicesSingleWorkspaceMechanicalID checks that a single
// shared workspace among mechanical findings gets the unsuffixed
// "fix-<n>-mech" id.
func TestSynthesizeFixSlicesSingleWorkspaceMechanicalID(t *testing.T) {
	man := manifest.Manifest{Oracles: map[string]string{"test": "go test"}}
	kept := []Finding{
		{ID: "r1-f1", Class: ClassMechanical, Title: "a", Workspace: "alpha", Oracle: "test"},
		{ID: "r1-f2", Class: ClassMechanical, Title: "b", Workspace: "alpha", Oracle: "test"},
	}
	slices, err := synthesizeFixSlices(1, kept, man)
	if err != nil {
		t.Fatalf("synthesizeFixSlices: %v", err)
	}
	if len(slices) != 1 || slices[0].ID != "fix-1-mech" {
		t.Fatalf("slices = %+v, want a single fix-1-mech", slices)
	}
	want := "Fix these mechanical gate findings:\n- a\n- b"
	if slices[0].Goal != want {
		t.Errorf("Goal = %q, want %q", slices[0].Goal, want)
	}
}

// TestSynthesizeFixSlicesOracleFallback checks the oracle resolution rule:
// an unknown oracle name falls back to the first manifest oracle name in
// sorted order, and a manifest with no oracles is GATE_NO_ORACLE.
func TestSynthesizeFixSlicesOracleFallback(t *testing.T) {
	man := manifest.Manifest{Oracles: map[string]string{"b": "cmd-b", "a": "cmd-a"}}
	kept := []Finding{{ID: "r1-f1", Class: ClassIntent, Title: "x", Workspace: "root", Oracle: "nonexistent"}}
	slices, err := synthesizeFixSlices(1, kept, man)
	if err != nil {
		t.Fatalf("synthesizeFixSlices: %v", err)
	}
	if len(slices) != 1 || slices[0].Oracle != "a" {
		t.Fatalf("slices = %+v, want oracle fallback to sorted-first %q", slices, "a")
	}

	noOracles := manifest.Manifest{}
	if _, err := synthesizeFixSlices(1, kept, noOracles); err == nil {
		t.Fatal("synthesizeFixSlices with no manifest oracles: want an error, got nil")
	} else {
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "GATE_NO_ORACLE" {
			t.Fatalf("err = %v, want *axi.Error GATE_NO_ORACLE", err)
		}
	}
}

// TestTriageOpenFindings exercises the Triage hook contract: only open
// findings are offered, an already-dismissed finding is never touched,
// nil keeps everything, and the hook is never called with zero open
// findings.
func TestTriageOpenFindings(t *testing.T) {
	findings := []Finding{
		{ID: "r1-f1", Status: StatusOpen},
		{ID: "r1-f2", Status: StatusDismissed},
		{ID: "r1-f3", Status: StatusOpen},
	}

	t.Run("nil triage keeps all open findings", func(t *testing.T) {
		final, kept := triageOpenFindings(findings, nil)
		if len(kept) != 2 {
			t.Fatalf("kept = %v, want the 2 open findings", kept)
		}
		for _, f := range final {
			if f.ID == "r1-f2" && f.Status != StatusDismissed {
				t.Errorf("already-dismissed finding changed: %+v", f)
			}
		}
	})

	t.Run("hook sees only open findings; unreturned ones become dismissed", func(t *testing.T) {
		var seen []string
		hook := func(open []Finding) []Finding {
			for _, f := range open {
				seen = append(seen, f.ID)
			}
			var kept []Finding
			for _, f := range open {
				if f.ID == "r1-f1" {
					kept = append(kept, f)
				}
			}
			return kept
		}
		final, kept := triageOpenFindings(findings, hook)
		if len(seen) != 2 || seen[0] != "r1-f1" || seen[1] != "r1-f3" {
			t.Fatalf("hook saw %v, want exactly the open findings [r1-f1 r1-f3]", seen)
		}
		if len(kept) != 1 || kept[0].ID != "r1-f1" {
			t.Fatalf("kept = %v, want only r1-f1", kept)
		}
		for _, f := range final {
			if f.ID == "r1-f3" && f.Status != StatusDismissed {
				t.Errorf("r1-f3 not dismissed by triage: %+v", f)
			}
			if f.ID == "r1-f2" && f.Status != StatusDismissed {
				t.Errorf("already-dismissed r1-f2 changed: %+v", f)
			}
		}
	})

	t.Run("zero open findings never calls the hook", func(t *testing.T) {
		called := false
		hook := func([]Finding) []Finding { called = true; return nil }
		allDismissed := []Finding{{ID: "x", Status: StatusDismissed}}
		_, kept := triageOpenFindings(allDismissed, hook)
		if called {
			t.Fatal("triage hook was called with zero open findings")
		}
		if len(kept) != 0 {
			t.Fatalf("kept = %v, want none", kept)
		}
	})
}

// TestPriorFindingsAndDismissed checks the cumulative computation: an open
// finding closed by a later round's closure drops out; a dismissed finding
// stays dismissed across rounds.
func TestPriorFindingsAndDismissed(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	ticket := "T-1"

	round1 := findingsFile{
		Findings: []Finding{
			{ID: "r1-f1", Class: ClassIntent, Title: "A", Status: StatusOpen},
			{ID: "r1-f2", Class: ClassMechanical, Title: "B", Status: StatusDismissed},
		},
	}
	round2 := findingsFile{
		Findings: []Finding{
			{ID: "r2-f1", Class: ClassIntent, Title: "C", Status: StatusOpen},
		},
		Closures: []Closure{{ID: "r1-f1", Status: "closed", Note: "fixed"}},
	}
	writeFindingsFile(t, st, ticket, 1, round1)
	writeFindingsFile(t, st, ticket, 2, round2)

	prior, dismissed, err := priorFindingsAndDismissed(st, ticket, 3)
	if err != nil {
		t.Fatalf("priorFindingsAndDismissed: %v", err)
	}
	if len(prior) != 1 || prior[0].ID != "r2-f1" {
		t.Fatalf("prior = %+v, want only r2-f1 (r1-f1 was closed)", prior)
	}
	if len(dismissed) != 1 || dismissed[0].ID != "r1-f2" || dismissed[0].Title != "B" {
		t.Fatalf("dismissed = %+v, want [{r1-f2 B}]", dismissed)
	}
}

// writeFindingsFile writes ff as round n's findings.yaml under st.
func writeFindingsFile(t *testing.T, st *store.Store, ticket string, n int, ff findingsFile) {
	t.Helper()
	dir := gateRoundDir(st, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	out, err := yaml.Marshal(ff)
	if err != nil {
		t.Fatalf("marshal findings.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), out, 0o644); err != nil {
		t.Fatalf("write findings.yaml: %v", err)
	}
}

// TestRenderFindingsMD checks the deterministic render's exact shape: no
// findings renders "none", and the Closures/Summary sections appear only
// when non-empty.
func TestRenderFindingsMD(t *testing.T) {
	t.Run("no findings, no closures, no summary", func(t *testing.T) {
		md := renderFindingsMD(2, "delta", "clean", nil, nil, "")
		want := "# Gate round 2\n\nscope: delta\nverdict: clean\n\n## Findings\n\nnone\n"
		if md != want {
			t.Fatalf("md = %q, want %q", md, want)
		}
	})

	t.Run("findings, closures, and summary all present", func(t *testing.T) {
		findings := []Finding{
			{ID: "r2-f1", Class: "mechanical", Workspace: "alpha", Status: "open", Title: "Fix the typo in Clamp's doc comment", Detail: "line 1\nline 2"},
			{ID: "r2-f2", Class: "intent", Workspace: "beta", Status: "dismissed", Title: "some title"},
		}
		closures := []Closure{{ID: "r1-f2", Status: "closed", Note: "resolved"}}
		md := renderFindingsMD(2, "delta", "fix-slices", findings, closures, "overall summary")
		want := "# Gate round 2\n\n" +
			"scope: delta\n" +
			"verdict: fix-slices\n\n" +
			"## Findings\n\n" +
			"- r2-f1 (mechanical, alpha) open: Fix the typo in Clamp's doc comment\n" +
			"  line 1\n" +
			"  line 2\n" +
			"- r2-f2 (intent, beta) dismissed: some title\n" +
			"\n## Closures\n\n" +
			"- r1-f2 closed: resolved\n" +
			"\n## Summary\n\n" +
			"overall summary\n"
		if md != want {
			t.Fatalf("md =\n%q\nwant\n%q", md, want)
		}
	})
}

// TestReviewScope covers D3's scope/anchor resolution: round 1's start-sha
// anchor and merge-base fallback, a later round's delta scope off an
// ancestor reviewed_sha, and the full-scope fallback when that
// reviewed_sha is not an ancestor (an unknown object after a rebase).
func TestReviewScope(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := gitx.Run(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "c1")
	c1 := run("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("2"), 0o644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "c2")
	c2 := run("rev-parse", "HEAD")
	run("update-ref", "refs/remotes/origin/main", c1)

	t.Run("round 1 uses start sha when it is an ancestor", func(t *testing.T) {
		st := &store.Store{Root: t.TempDir()}
		ticket := "T-1"
		if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
			t.Fatalf("mkdir ticket dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "start.repo.sha"), []byte(c1+"\n"), 0o644); err != nil {
			t.Fatalf("write start sha: %v", err)
		}
		scope, base, err := reviewScope(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main"}, c2)
		if err != nil {
			t.Fatalf("reviewScope: %v", err)
		}
		if scope != "full" || base != c1 {
			t.Fatalf("scope=%q base=%q, want full/%s", scope, base, c1)
		}
	})

	t.Run("round 1 falls back to merge-base with no start sha", func(t *testing.T) {
		st := &store.Store{Root: t.TempDir()}
		ticket := "T-1"
		if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
			t.Fatalf("mkdir ticket dir: %v", err)
		}
		scope, base, err := reviewScope(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main"}, c2)
		if err != nil {
			t.Fatalf("reviewScope: %v", err)
		}
		if scope != "full" || base != c1 {
			t.Fatalf("scope=%q base=%q, want full/%s (merge-base fallback)", scope, base, c1)
		}
	})

	t.Run("round >1 with an ancestor prior reviewed_sha is delta", func(t *testing.T) {
		st := &store.Store{Root: t.TempDir()}
		ticket := "T-1"
		writeReportYAMLDirect(t, st, ticket, 1, reportYAML{Round: 1, ReviewedSHA: map[string]string{"repo": c1}})
		scope, base, err := reviewScope(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main"}, c2)
		if err != nil {
			t.Fatalf("reviewScope: %v", err)
		}
		if scope != "delta" || base != c1 {
			t.Fatalf("scope=%q base=%q, want delta/%s", scope, base, c1)
		}
	})

	t.Run("round >1 with a non-ancestor prior reviewed_sha falls back to full", func(t *testing.T) {
		st := &store.Store{Root: t.TempDir()}
		ticket := "T-1"
		if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
			t.Fatalf("mkdir ticket dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "start.repo.sha"), []byte(c1+"\n"), 0o644); err != nil {
			t.Fatalf("write start sha: %v", err)
		}
		writeReportYAMLDirect(t, st, ticket, 1, reportYAML{Round: 1, ReviewedSHA: map[string]string{"repo": strings.Repeat("0", 40)}})
		scope, base, err := reviewScope(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main"}, c2)
		if err != nil {
			t.Fatalf("reviewScope: %v", err)
		}
		if scope != "full" || base != c1 {
			t.Fatalf("scope=%q base=%q, want full/%s (fallback after unknown object)", scope, base, c1)
		}
	})

	t.Run("full scope base is the merge base after a rebase onto an advanced target, not the stale start sha", func(t *testing.T) {
		// The ticket starts at T0, is cut and worked on, then main advances
		// to T1 (an unrelated commit landed after the ticket started) and
		// the ticket branch is rebased onto it. T0 is still technically an
		// ancestor of the rebased head, so the old bug picked it as the
		// base; the real fork point, and the correct base, is T1.
		rdir := t.TempDir()
		rrun := func(args ...string) string {
			t.Helper()
			out, err := gitx.Run(rdir, args...)
			if err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
			return out
		}
		write := func(name, content string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(rdir, name), []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		rrun("init", "-b", "main")
		rrun("config", "user.name", "jig-fixture")
		rrun("config", "user.email", "fixture@example.invalid")
		write("f.txt", "0")
		rrun("add", "-A")
		rrun("commit", "-m", "T0")
		t0 := rrun("rev-parse", "HEAD")
		rrun("update-ref", "refs/remotes/origin/main", t0)

		rrun("checkout", "-b", "jig/T-1")
		write("ticket.txt", "work")
		rrun("add", "-A")
		rrun("commit", "-m", "ticket work")

		rrun("checkout", "main")
		write("main.txt", "unrelated")
		rrun("add", "-A")
		rrun("commit", "-m", "T1")
		t1 := rrun("rev-parse", "HEAD")
		rrun("update-ref", "refs/remotes/origin/main", t1)

		rrun("checkout", "jig/T-1")
		rrun("rebase", "main")
		head := rrun("rev-parse", "HEAD")

		st := &store.Store{Root: t.TempDir()}
		ticket := "T-1"
		if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
			t.Fatalf("mkdir ticket dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "start.repo.sha"), []byte(t0+"\n"), 0o644); err != nil {
			t.Fatalf("write start sha: %v", err)
		}

		scope, base, err := reviewScope(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: rdir, RepoName: "repo", Target: "main"}, head)
		if err != nil {
			t.Fatalf("reviewScope: %v", err)
		}
		if scope != "full" || base != t1 {
			t.Fatalf("scope=%q base=%q, want full/%s (merge-base, not the stale start sha %s)", scope, base, t1, t0)
		}
	})
}

// writeReportYAMLDirect writes rep as round n's report.yaml under st,
// bypassing writeReportYAML's GateReport shape for tests that need to set
// ReviewedSHA directly.
func writeReportYAMLDirect(t *testing.T, st *store.Store, ticket string, n int, rep reportYAML) {
	t.Helper()
	dir := gateRoundDir(st, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	out, err := yaml.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal report.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), out, 0o644); err != nil {
		t.Fatalf("write report.yaml: %v", err)
	}
}

// scriptedReviewBackend is a session.Backend stub that reads review.json
// and hands it to a per-attempt scripted function, writing its
// ReviewResult back to ResultJSON. commitFirst, when set, makes it commit
// a trivial change in the worktree before writing the result (to exercise
// the forward-only guard); dirtyFirst leaves an uncommitted edit to a
// tracked file (seed.txt); untrackedFirst writes an untracked scratch file;
// writeNothing, when set, skips the write entirely (to exercise the
// missing-result-json path without touching a stale file).
type scriptedReviewBackend struct {
	t              *testing.T
	byAttempt      map[int]func(ReviewRequest) ReviewResult
	failErr        error
	commitFirst    bool
	dirtyFirst     bool
	untrackedFirst bool
	writeNothing   bool
}

func (b *scriptedReviewBackend) Run(d session.Dispatch) error {
	if b.failErr != nil {
		return b.failErr
	}
	if b.commitFirst {
		if err := os.WriteFile(filepath.Join(d.Worktree, "reviewer-edit.txt"), []byte("nope"), 0o644); err != nil {
			b.t.Fatalf("stub: write reviewer edit: %v", err)
		}
		if _, err := gitx.Run(d.Worktree, "add", "-A"); err != nil {
			b.t.Fatalf("stub: git add: %v", err)
		}
		if _, err := gitx.RunEnv(d.Worktree, buildGitEnv, "commit", "-m", "reviewer should never do this"); err != nil {
			b.t.Fatalf("stub: git commit: %v", err)
		}
	}
	if b.dirtyFirst {
		if err := os.WriteFile(filepath.Join(d.Worktree, "seed.txt"), []byte("reviewer edit\n"), 0o644); err != nil {
			b.t.Fatalf("stub: dirty tracked file: %v", err)
		}
	}
	if b.untrackedFirst {
		if err := os.WriteFile(filepath.Join(d.Worktree, "reviewer-scratch.txt"), []byte("scratch\n"), 0o644); err != nil {
			b.t.Fatalf("stub: write untracked file: %v", err)
		}
	}
	if b.writeNothing {
		return nil
	}
	data, err := os.ReadFile(d.SliceJSON)
	if err != nil {
		b.t.Fatalf("stub: read review.json: %v", err)
	}
	var req ReviewRequest
	if err := json.Unmarshal(data, &req); err != nil {
		b.t.Fatalf("stub: parse review.json: %v", err)
	}
	fn, ok := b.byAttempt[d.Attempt]
	if !ok {
		return fmt.Errorf("stub: no scripted result for attempt %d", d.Attempt)
	}
	res := fn(req)
	out, err := json.Marshal(res)
	if err != nil {
		b.t.Fatalf("stub: marshal result: %v", err)
	}
	return os.WriteFile(d.ResultJSON, out, 0o644)
}

// newRoundInputRepo creates a small git repo with one commit, standing in
// for a gate lease, and a fresh store with ticket's dir created.
func newRoundInputRepo(t *testing.T) (dir string, st *store.Store, ticket string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "seed")
	run("update-ref", "refs/remotes/origin/main", "HEAD")

	st = &store.Store{Root: t.TempDir()}
	ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatalf("mkdir ticket dir: %v", err)
	}
	return dir, st, ticket
}

var oneWorkspaceManifest = manifest.Manifest{
	Workspaces: []manifest.Workspace{{ID: "root"}},
	Oracles:    map[string]string{"test": "go test"},
}

// TestReviewerGateSourceHappyPath checks id assignment, empty-workspace
// resolution against a single-workspace manifest, and reviewed_sha.
func TestReviewerGateSourceHappyPath(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult {
			return ReviewResult{
				Verdict: "findings",
				Findings: []ResultFinding{
					{Class: ClassMechanical, Title: "typo", Detail: "d1", Workspace: "", Oracle: "test"},
					{Class: ClassIntent, Title: "bug", Detail: "d2", Workspace: "root", Oracle: "test"},
				},
				Summary: "s1",
			}
		},
	}}
	src := NewReviewerGateSource(backend)
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	round, ok, err := src.Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok || round.Review == nil {
		t.Fatalf("Round: ok=%v review=%v, want a review", ok, round.Review)
	}
	rv := round.Review
	if len(rv.Findings) != 2 {
		t.Fatalf("findings = %+v, want 2", rv.Findings)
	}
	if rv.Findings[0].ID != "r1-f1" || rv.Findings[0].Workspace != "root" {
		t.Errorf("finding 0 = %+v, want id r1-f1, workspace resolved to root", rv.Findings[0])
	}
	if rv.Findings[1].ID != "r1-f2" {
		t.Errorf("finding 1 = %+v, want id r1-f2", rv.Findings[1])
	}
	if rv.ReviewedSHA["repo"] != head {
		t.Errorf("ReviewedSHA = %v, want repo -> %s", rv.ReviewedSHA, head)
	}
	if rv.Scope != "full" {
		t.Errorf("Scope = %q, want full", rv.Scope)
	}
}

// TestReviewerGateSourceUnknownWorkspace checks that a finding naming a
// workspace the manifest does not have is REVIEW_INVALID.
func TestReviewerGateSourceUnknownWorkspace(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult {
			return ReviewResult{Verdict: "findings", Findings: []ResultFinding{
				{Class: ClassIntent, Title: "bug", Workspace: "nope", Oracle: "test"},
			}, Summary: "s"}
		},
	}}
	_, _, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID", err)
	}
}

// TestReviewerGateSourceUnknownClosureID checks that a closure naming an id
// outside the request's prior_findings is REVIEW_INVALID.
func TestReviewerGateSourceUnknownClosureID(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	writeFindingsFile(t, st, ticket, 1, findingsFile{Findings: []Finding{{ID: "r1-f1", Class: ClassIntent, Title: "A", Status: StatusOpen}}})
	writeReportYAMLDirect(t, st, ticket, 1, reportYAML{Round: 1})

	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		2: func(ReviewRequest) ReviewResult {
			return ReviewResult{Verdict: "clean", Closures: []Closure{{ID: "not-a-prior-id", Status: "closed", Note: "n"}}}
		},
	}}
	_, _, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID", err)
	}
}

// TestReviewerGateSourceMissingClosureInvalid checks A4: a result that
// leaves a prior open finding without any closure at all is REVIEW_INVALID,
// not silently accepted (which used to let an unclosed id linger in
// prior_findings forever).
func TestReviewerGateSourceMissingClosureInvalid(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	writeFindingsFile(t, st, ticket, 1, findingsFile{Findings: []Finding{
		{ID: "r1-f1", Class: ClassIntent, Title: "A", Status: StatusOpen},
		{ID: "r1-f2", Class: ClassIntent, Title: "B", Status: StatusOpen},
	}})
	writeReportYAMLDirect(t, st, ticket, 1, reportYAML{Round: 1})

	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		2: func(ReviewRequest) ReviewResult {
			// Only closes r1-f1; r1-f2 gets no closure at all.
			return ReviewResult{Verdict: "clean", Closures: []Closure{{ID: "r1-f1", Status: "closed", Note: "n"}}}
		},
	}}
	_, _, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID (missing closure for r1-f2)", err)
	}
}

// TestReviewerGateSourceDuplicateClosureInvalid checks A4: two closures for
// the same prior finding id is REVIEW_INVALID.
func TestReviewerGateSourceDuplicateClosureInvalid(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	writeFindingsFile(t, st, ticket, 1, findingsFile{Findings: []Finding{
		{ID: "r1-f1", Class: ClassIntent, Title: "A", Status: StatusOpen},
	}})
	writeReportYAMLDirect(t, st, ticket, 1, reportYAML{Round: 1})

	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		2: func(ReviewRequest) ReviewResult {
			return ReviewResult{Verdict: "clean", Closures: []Closure{
				{ID: "r1-f1", Status: "closed", Note: "n"},
				{ID: "r1-f1", Status: "still-open", Note: "actually not"},
			}}
		},
	}}
	_, _, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID (duplicate closure for r1-f1)", err)
	}
}

// TestReviewerGateSourceStillOpenClosureCarriesForward checks A4's
// carry-forward: a "still-open" closure produces a jig-side finding in this
// round (not a re-raise from the reviewer), copying class/title/workspace
// from the prior finding, extending detail with the closure's note, and
// inheriting the oracle of the fix slice the prior finding produced.
func TestReviewerGateSourceStillOpenClosureCarriesForward(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	writeFindingsFile(t, st, ticket, 1, findingsFile{Findings: []Finding{
		{ID: "r1-f1", Class: ClassIntent, Title: "Fix the leak", Detail: "detail here", Workspace: "root", Status: StatusOpen},
	}})
	writeReportYAMLDirect(t, st, ticket, 1, reportYAML{Round: 1, ReviewedSHA: map[string]string{}})
	if err := st.AppendSlices(ticket, []store.Slice{{ID: "fix-1-1", Workspace: "root", Goal: "Fix the leak", Oracle: "special-oracle"}}); err != nil {
		t.Fatalf("AppendSlices: %v", err)
	}

	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		2: func(ReviewRequest) ReviewResult {
			return ReviewResult{Verdict: "clean", Closures: []Closure{{ID: "r1-f1", Status: "still-open", Note: "still leaking"}}}
		},
	}}
	round, ok, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok || round.Review == nil {
		t.Fatalf("round = %+v, want an ok review", round)
	}
	findings := round.Review.Findings
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want 1 carried finding", findings)
	}
	f := findings[0]
	if f.ID != "r2-f1" || f.Class != ClassIntent || f.Title != "Fix the leak" || f.Workspace != "root" || f.Status != StatusOpen {
		t.Fatalf("carried finding = %+v, want id r2-f1, intent, \"Fix the leak\", root, open", f)
	}
	wantDetail := "detail here\n\nStill open in round 2: still leaking"
	if f.Detail != wantDetail {
		t.Fatalf("carried finding detail = %q, want %q", f.Detail, wantDetail)
	}
	if f.Oracle != "special-oracle" {
		t.Fatalf("carried finding oracle = %q, want %q (inherited from fix-1-1)", f.Oracle, "special-oracle")
	}
}

// TestReviewerGateSourceStaleResultDeletedBeforeDispatch checks that a
// stale result.json left by a previous attempt is removed before dispatch,
// so a backend that writes nothing this attempt fails with REVIEW_FAILED
// rather than the parsed stale file.
func TestReviewerGateSourceStaleResultDeletedBeforeDispatch(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	resultPath := reviewResultJSONPath(st, ticket, 1)
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	if err := os.WriteFile(resultPath, []byte(`{"verdict":"clean","findings":[],"closures":[],"summary":"stale"}`), 0o644); err != nil {
		t.Fatalf("write stale result.json: %v", err)
	}

	backend := &scriptedReviewBackend{t: t, writeNothing: true}
	_, _, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_FAILED" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_FAILED (not a parse of the stale file)", err)
	}
}

// TestReviewerGateSourceBackendError checks that a backend infrastructure
// failure is REVIEW_FAILED.
func TestReviewerGateSourceBackendError(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	backend := &scriptedReviewBackend{t: t, failErr: fmt.Errorf("boom")}
	_, _, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_FAILED" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_FAILED", err)
	}
}

// TestReviewerGateSourceForwardOnlyGuard checks that a reviewer committing
// in the lease is REVIEW_INVALID.
func TestReviewerGateSourceForwardOnlyGuard(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	backend := &scriptedReviewBackend{t: t, commitFirst: true, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult { return ReviewResult{Verdict: "clean"} },
	}}
	_, _, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID", err)
	}
}

// TestReviewerGateSourceForwardOnlyGuardRestoresLease checks that a
// reviewer committing in the lease is REVIEW_INVALID (as
// TestReviewerGateSourceForwardOnlyGuard already checks) AND that the lease
// is restored to head afterward: --branch mode's local branch ref must move
// back too, since HEAD following the branch is what makes reset --hard do
// that.
func TestReviewerGateSourceForwardOnlyGuardRestoresLease(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	backend := &scriptedReviewBackend{t: t, commitFirst: true, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult { return ReviewResult{Verdict: "clean"} },
	}}
	_, _, err = NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID", err)
	}
	after, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD after Round: %v", err)
	}
	if after != head {
		t.Fatalf("lease HEAD = %s after a rejected round, want restored to %s", after, head)
	}
	if status, err := gitx.Run(dir, "status", "--porcelain"); err != nil || status != "" {
		t.Fatalf("lease not clean after a rejected round: status=%q err=%v", status, err)
	}
}

// TestReviewerGateSourceUntrackedReviewerFileWiped checks that an untracked
// file a reviewer leaves behind fails the round (the guard already used
// --untracked-files=no before this fix, so this alone would not have caught
// it) and is gone from the lease afterward, so it does not poison a later,
// well-behaved round on the same lease.
func TestReviewerGateSourceUntrackedReviewerFileWiped(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	backend := &scriptedReviewBackend{t: t, untrackedFirst: true, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult { return ReviewResult{Verdict: "clean"} },
	}}
	round, ok, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	if err != nil {
		t.Fatalf("Round with an untracked reviewer file: %v", err)
	}
	if !ok || round.Review == nil || round.Review.Scope == "" {
		t.Fatalf("round = %+v, want an ok clean review", round)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviewer-scratch.txt")); !os.IsNotExist(err) {
		t.Fatalf("reviewer's untracked file survived Round: err=%v", err)
	}
}

// TestReviewerGateSourceUncommittedTrackedEditThenRetry checks scenario (1)
// from A2: a reviewer that leaves an uncommitted tracked edit gets
// REVIEW_INVALID, and a following well-behaved round on the same lease
// succeeds (the edit does not survive to poison it).
func TestReviewerGateSourceUncommittedTrackedEditThenRetry(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	bad := &scriptedReviewBackend{t: t, dirtyFirst: true, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult { return ReviewResult{Verdict: "clean"} },
	}}
	_, _, err := NewReviewerGateSource(bad).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID", err)
	}

	good := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		2: func(ReviewRequest) ReviewResult { return ReviewResult{Verdict: "clean"} },
	}}
	round, ok, err := NewReviewerGateSource(good).Round(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	if err != nil {
		t.Fatalf("well-behaved retry after a poisoned lease: %v", err)
	}
	if !ok || round.Review == nil {
		t.Fatalf("round = %+v, want an ok review", round)
	}
}

// TestReviewerGateSourcePreDispatchDirtNotBlamedOnReviewer checks scenario
// (4) from A2 / conformance M3: a tracked file dirtied before Round is
// called (as a gate oracle rewriting a file would) must not fail a
// well-behaved reviewer round with REVIEW_INVALID, since the pre-dispatch
// pristine restore wipes it before the reviewer ever runs.
func TestReviewerGateSourcePreDispatchDirtNotBlamedOnReviewer(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("oracle rewrote this\n"), 0o644); err != nil {
		t.Fatalf("dirty seed.txt before Round: %v", err)
	}
	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult { return ReviewResult{Verdict: "clean"} },
	}}
	round, ok, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	if err != nil {
		t.Fatalf("Round with pre-existing oracle dirt: %v, want no error (dirt wiped before dispatch)", err)
	}
	if !ok || round.Review == nil {
		t.Fatalf("round = %+v, want an ok clean review", round)
	}
}

// TestReviewerGateSourceAutoDismiss checks the mechanical dismissal
// guarantee: a finding whose normalized title matches a cumulative
// dismissed finding's normalized title is dismissed automatically, before
// jig ever offers it to Triage.
func TestReviewerGateSourceAutoDismiss(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	writeFindingsFile(t, st, ticket, 1, findingsFile{Findings: []Finding{
		{ID: "r1-f1", Class: ClassIntent, Title: "Fix the widget", Status: StatusDismissed},
	}})
	writeReportYAMLDirect(t, st, ticket, 1, reportYAML{Round: 1, ReviewedSHA: map[string]string{}})

	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		2: func(req ReviewRequest) ReviewResult {
			if len(req.Dismissed) != 1 || req.Dismissed[0].Title != "Fix the widget" {
				t.Fatalf("review.json dismissed = %+v, want [{r1-f1 Fix the widget}]", req.Dismissed)
			}
			return ReviewResult{Verdict: "findings", Findings: []ResultFinding{
				{Class: ClassIntent, Title: "  FIX the   Widget  ", Workspace: "root", Oracle: "test"},
				{Class: ClassIntent, Title: "A different bug", Workspace: "root", Oracle: "test"},
			}, Summary: "s2"}
		},
	}}
	round, ok, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 2, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok {
		t.Fatal("Round: ok = false, want true")
	}
	findings := round.Review.Findings
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want 2", findings)
	}
	if findings[0].Status != StatusDismissed {
		t.Errorf("re-raised dismissed finding = %+v, want Status dismissed", findings[0])
	}
	if findings[1].Status != StatusOpen {
		t.Errorf("distinct finding = %+v, want Status open", findings[1])
	}

	// Triage must never even see the auto-dismissed finding.
	var triageSaw []string
	triage := func(open []Finding) []Finding {
		for _, f := range open {
			triageSaw = append(triageSaw, f.ID)
		}
		return open
	}
	final, kept := triageOpenFindings(findings, triage)
	if len(triageSaw) != 1 || triageSaw[0] != findings[1].ID {
		t.Fatalf("triage saw %v, want only the non-dismissed finding", triageSaw)
	}
	if len(kept) != 1 {
		t.Fatalf("kept = %v, want 1", kept)
	}
	if final[0].Status != StatusDismissed {
		t.Errorf("auto-dismissed finding's status changed by triage: %+v", final[0])
	}
}

// landFixSliceGreen commits a trivial marker for slice in buildDir and
// records it as a green result, mirroring driveAttempt for a fix slice
// synthesized by the reviewer (so there is no scripted scenario attempt
// to replay).
func landFixSliceGreen(t *testing.T, st *store.Store, ticket, buildDir, slice string) {
	t.Helper()
	if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "dispatch", Model: "rung-a", Attempt: 1}); err != nil {
		t.Fatalf("journal dispatch %s: %v", slice, err)
	}
	marker := filepath.Join(buildDir, slice+"-marker.txt")
	if err := os.WriteFile(marker, []byte("fixed\n"), 0o644); err != nil {
		t.Fatalf("write marker for %s: %v", slice, err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add for %s: %v", slice, err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", ticket+" "+slice+": landed"); err != nil {
		t.Fatalf("git commit for %s: %v", slice, err)
	}
	sha, err := gitx.RevParse(buildDir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD after %s: %v", slice, err)
	}
	if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "result", Outcome: "green", Commit: sha, Attempt: 1}); err != nil {
		t.Fatalf("journal result %s: %v", slice, err)
	}
	if err := st.WriteSliceState(ticket, slice, store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state %s: %v", slice, err)
	}
	if err := st.Push(ticket + ": slice " + slice + " green"); err != nil {
		t.Fatalf("push after %s green: %v", slice, err)
	}
}

// TestGateReviewerTwoRounds is the S2 flagship: a full round-1 (full
// scope, mechanical bundle + kept/dismissed intent findings) into round-2
// (delta scope, closures, clean verdict) flow, driven through Gate with a
// scripted reviewer backend and an interactive-shaped Triage hook.
func TestGateReviewerTwoRounds(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{}}
	backend.byAttempt[1] = func(ReviewRequest) ReviewResult {
		return ReviewResult{
			Verdict: "findings",
			Findings: []ResultFinding{
				{Class: ClassMechanical, Title: "Add a doc comment", Detail: "alpha.Add lacks a doc comment", Workspace: "alpha", Oracle: "test"},
				{Class: ClassIntent, Title: "Greet ignores locale", Detail: "beta.Greet hardcodes English", Workspace: "beta", Oracle: "test"},
				{Class: ClassIntent, Title: "Clamp off by one", Detail: "alpha.Clamp misses the upper bound", Workspace: "alpha", Oracle: "test"},
			},
			Summary: "round 1 findings",
		}
	}

	var triageInput []Finding
	triageCallCount := 0
	triage := func(open []Finding) []Finding {
		triageCallCount++
		triageInput = append([]Finding{}, open...)
		var kept []Finding
		for _, f := range open {
			if f.Title == "Clamp off by one" {
				continue
			}
			kept = append(kept, f)
		}
		return kept
	}

	src := NewReviewerGateSource(backend)
	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Triage: triage})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Round != 1 || report1.Verdict != "fix-slices" {
		t.Fatalf("report1 = %+v, want round 1 fix-slices", report1)
	}
	if len(triageInput) != 3 {
		t.Fatalf("triage saw %d open findings, want 3", len(triageInput))
	}

	slices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	byID := map[string]store.Slice{}
	for _, s := range slices {
		byID[s.ID] = s
	}
	mech, ok := byID["fix-1-mech"]
	if !ok {
		t.Fatal("fix-1-mech was not appended")
	}
	if mech.Rung != staircase.RungCheapest || mech.FromGate != 1 {
		t.Fatalf("fix-1-mech = %+v, want rung cheapest, from_gate 1", mech)
	}
	if _, ok := byID["fix-1-2"]; !ok {
		t.Fatal("fix-1-2 (the kept intent finding) was not appended")
	}
	if _, ok := byID["fix-1-3"]; ok {
		t.Fatal("fix-1-3 (the dismissed finding) should not have been appended")
	}

	ff, ok, err := readFindingsFile(d.Store, fx.Ticket, 1)
	if err != nil || !ok {
		t.Fatalf("readFindingsFile round 1: ok=%v err=%v", ok, err)
	}
	statuses := map[string]string{}
	for _, f := range ff.Findings {
		statuses[f.ID] = f.Status
	}
	if statuses["r1-f1"] != StatusOpen || statuses["r1-f2"] != StatusOpen || statuses["r1-f3"] != StatusDismissed {
		t.Fatalf("statuses = %v, want r1-f1/r1-f2 open, r1-f3 dismissed", statuses)
	}

	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket+"-gate")
	if err != nil {
		t.Fatalf("reacquire gate lease: %v", err)
	}
	head1, err := gitx.RevParse(gateLease.Dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse gate lease HEAD: %v", err)
	}
	if report1.ReviewedSHA["fixture-repo"] != head1 {
		t.Fatalf("reviewed_sha = %v, want fixture-repo -> %s", report1.ReviewedSHA, head1)
	}

	reqData, err := os.ReadFile(reviewJSONPath(d.Store, fx.Ticket, 1))
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var req1 ReviewRequest
	if err := json.Unmarshal(reqData, &req1); err != nil {
		t.Fatalf("parse review.json: %v", err)
	}
	if req1.Scope != "full" {
		t.Fatalf("scope = %q, want full", req1.Scope)
	}
	startData, err := os.ReadFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "start.fixture-repo.sha"))
	if err != nil {
		t.Fatalf("read start sha: %v", err)
	}
	if req1.BaseSHA != strings.TrimSpace(string(startData)) {
		t.Fatalf("base_sha = %q, want start sha %q", req1.BaseSHA, startData)
	}
	if len(req1.PriorFindings) != 0 || len(req1.Dismissed) != 0 {
		t.Fatalf("round 1 prior/dismissed not empty: %+v", req1)
	}

	// Land both fix slices green in the build lease (like driveFix1, but for
	// these synthesized ids rather than a scripted scenario attempt), so
	// round 2's frontier check passes and it reviews a real advance.
	buildDir := buildLeaseDir(t, fx)
	landFixSliceGreen(t, d.Store, fx.Ticket, buildDir, "fix-1-mech")
	landFixSliceGreen(t, d.Store, fx.Ticket, buildDir, "fix-1-2")

	// A5: round 2 re-raises the round-1-dismissed "Clamp off by one" with
	// case/spacing drift, alongside closing every prior finding. Jig's
	// mechanical dismissal must catch the drifted title before Triage ever
	// sees it, so the round still ends up clean with no fix-2-* slice.
	backend.byAttempt[2] = func(req ReviewRequest) ReviewResult {
		var closures []Closure
		for _, pf := range req.PriorFindings {
			closures = append(closures, Closure{ID: pf.ID, Status: "closed", Note: "resolved"})
		}
		return ReviewResult{
			Verdict: "findings",
			Findings: []ResultFinding{
				{Class: ClassIntent, Title: "  clamp OFF by   one ", Workspace: "alpha", Oracle: "test"},
			},
			Closures: closures,
			Summary:  "round 2 clean",
		}
	}

	triageCallCountBeforeRound2 := triageCallCount
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Triage: triage})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Round != 2 || report2.Verdict != "clean" {
		t.Fatalf("report2 = %+v, want round 2 clean (the re-raised dismissed title must not produce a fix slice)", report2)
	}
	if triageCallCount != triageCallCountBeforeRound2 {
		t.Fatalf("Triage was called in round 2 (count %d -> %d): the re-raised dismissed finding must be auto-dismissed before Triage, never offered to it", triageCallCountBeforeRound2, triageCallCount)
	}

	ff2, ok, err := readFindingsFile(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("readFindingsFile round 2: ok=%v err=%v", ok, err)
	}
	if len(ff2.Findings) != 1 || ff2.Findings[0].Status != StatusDismissed {
		t.Fatalf("round 2 findings.yaml = %+v, want exactly 1 finding, auto-dismissed", ff2.Findings)
	}

	slicesAfter2, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices after round 2: %v", err)
	}
	for _, s := range slicesAfter2 {
		if strings.HasPrefix(s.ID, "fix-2-") {
			t.Fatalf("fix slice %s appended for round 2, want none (the only finding was auto-dismissed)", s.ID)
		}
	}

	reqData2, err := os.ReadFile(reviewJSONPath(d.Store, fx.Ticket, 2))
	if err != nil {
		t.Fatalf("read round 2 review.json: %v", err)
	}
	var req2 ReviewRequest
	if err := json.Unmarshal(reqData2, &req2); err != nil {
		t.Fatalf("parse round 2 review.json: %v", err)
	}
	if req2.Scope != "delta" || req2.BaseSHA != head1 {
		t.Fatalf("round 2 scope/base = %q/%q, want delta/%s", req2.Scope, req2.BaseSHA, head1)
	}
	if len(req2.PriorFindings) != 2 {
		t.Fatalf("round 2 prior_findings = %+v, want the 2 kept round-1 findings", req2.PriorFindings)
	}
	if len(req2.Dismissed) != 1 || req2.Dismissed[0].ID != "r1-f3" || req2.Dismissed[0].Title != "Clamp off by one" {
		t.Fatalf("round 2 review.json dismissed = %+v, want exactly [{r1-f3 Clamp off by one}]", req2.Dismissed)
	}
}

// invalidJSONBackend is a session.Backend stub that writes garbage to
// result.json, standing in for a reviewer session that crashed or produced
// unparseable output.
type invalidJSONBackend struct{}

func (invalidJSONBackend) Run(d session.Dispatch) error {
	return os.WriteFile(d.ResultJSON, []byte("not json"), 0o644)
}

// TestGateRetryAfterFailedReviewerAttempt reproduces the store wedge a
// failed reviewer attempt used to leave behind (A1): round 1's reviewer
// writes invalid JSON, so Gate journals gate-open and returns
// REVIEW_INVALID without ever reaching Store.Push. A second Gate call on the
// same ticket must not fail in Store.Sync on the leftover uncommitted
// journal/work files, and must retry as round 1 with the second attempt's
// valid result, never reading anything from the first, failed attempt.
func TestGateRetryAfterFailedReviewerAttempt(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	if !d.Store.HasRemote() {
		t.Fatal("fixture store has no remote; test needs one to reproduce the Sync wedge")
	}

	_, err := Gate(d, NewReviewerGateSource(invalidJSONBackend{}), GateOpts{Ticket: fx.Ticket})
	if err == nil {
		t.Fatal("Gate round 1 with invalid reviewer JSON: want an error")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("Gate round 1 error = %v, want *axi.Error REVIEW_INVALID", err)
	}

	// The failed attempt must have left the store dirty (gate-open journal
	// line, work/gate.round-1.review.json), uncommitted: that is the wedge
	// this fix removes.
	if status, serr := gitx.Run(fx.StoreDir, "status", "--porcelain"); serr != nil || status == "" {
		t.Fatalf("expected the store to be left dirty after the failed attempt; status=%q err=%v", status, serr)
	}

	retryBackend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult {
			return ReviewResult{Verdict: "clean", Summary: "round 1 clean on retry"}
		},
	}}
	report, err := Gate(d, NewReviewerGateSource(retryBackend), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate retry after a failed reviewer attempt: %v", err)
	}
	if report.Round != 1 || report.Verdict != "clean" {
		t.Fatalf("retry report = %+v, want round 1 clean (never reading the failed attempt's stale state)", report)
	}
	if status, serr := gitx.Run(fx.StoreDir, "status", "--porcelain"); serr != nil || status != "" {
		t.Fatalf("store left dirty after a successful retry: status=%q err=%v", status, serr)
	}
}

// TestGateReviewerStillOpenClosureCarriesForwardAcrossRounds is A4's
// Gate-level flagship: round 1 raises an intent finding and its fix slice
// lands; round 2 reports it "still-open" instead of re-raising it, so jig
// carries it forward as r2-f1 (inheriting the fix-1-1 oracle) with its own
// fix-2-1 slice, and round 2's verdict is fix-slices despite the reviewer
// saying "clean"; once fix-2-1 lands, round 3's prior_findings names only
// the carried r2-f1, never the original r1-f1.
func TestGateReviewerStillOpenClosureCarriesForwardAcrossRounds(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{}}
	backend.byAttempt[1] = func(ReviewRequest) ReviewResult {
		return ReviewResult{
			Verdict: "findings",
			Findings: []ResultFinding{
				{Class: ClassIntent, Title: "Leak across tenants", Detail: "detail", Workspace: "alpha", Oracle: "test"},
			},
			Summary: "round 1",
		}
	}
	src := NewReviewerGateSource(backend)
	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Verdict != "fix-slices" {
		t.Fatalf("report1 = %+v, want fix-slices", report1)
	}
	slices1, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices after round 1: %v", err)
	}
	byID1 := map[string]store.Slice{}
	for _, s := range slices1 {
		byID1[s.ID] = s
	}
	fix11, ok := byID1["fix-1-1"]
	if !ok {
		t.Fatal("fix-1-1 not appended")
	}

	buildDir := buildLeaseDir(t, fx)
	landFixSliceGreen(t, d.Store, fx.Ticket, buildDir, "fix-1-1")

	backend.byAttempt[2] = func(req ReviewRequest) ReviewResult {
		if len(req.PriorFindings) != 1 || req.PriorFindings[0].ID != "r1-f1" {
			t.Fatalf("round 2 prior_findings = %+v, want only r1-f1", req.PriorFindings)
		}
		return ReviewResult{Verdict: "clean", Closures: []Closure{{ID: "r1-f1", Status: "still-open", Note: "still leaks in beta"}}}
	}
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "fix-slices" {
		t.Fatalf("report2 = %+v, want fix-slices (the still-open closure must be carried forward as a kept finding)", report2)
	}
	slices2, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices after round 2: %v", err)
	}
	byID2 := map[string]store.Slice{}
	for _, s := range slices2 {
		byID2[s.ID] = s
	}
	carried, ok := byID2["fix-2-1"]
	if !ok {
		t.Fatal("fix-2-1 (the carried finding's fix slice) not appended")
	}
	if carried.Oracle != fix11.Oracle {
		t.Fatalf("carried slice oracle = %q, want %q (inherited from fix-1-1)", carried.Oracle, fix11.Oracle)
	}

	landFixSliceGreen(t, d.Store, fx.Ticket, buildDir, "fix-2-1")

	backend.byAttempt[3] = func(req ReviewRequest) ReviewResult {
		if len(req.PriorFindings) != 1 || req.PriorFindings[0].ID != "r2-f1" {
			t.Fatalf("round 3 prior_findings = %+v, want only the carried r2-f1, not the original r1-f1", req.PriorFindings)
		}
		return ReviewResult{Verdict: "clean", Closures: []Closure{{ID: "r2-f1", Status: "closed", Note: "fixed"}}}
	}
	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if report3.Verdict != "clean" {
		t.Fatalf("report3 = %+v, want clean", report3)
	}
}

// TestReviewerGateSourceCollapsesTitleWhitespace checks A6: a
// reviewer-supplied title with embedded newlines is collapsed to single
// spaces, so it cannot break the rendered findings.md list or a mechanical
// bundle's goal bullet.
func TestReviewerGateSourceCollapsesTitleWhitespace(t *testing.T) {
	dir, st, ticket := newRoundInputRepo(t)
	backend := &scriptedReviewBackend{t: t, byAttempt: map[int]func(ReviewRequest) ReviewResult{
		1: func(ReviewRequest) ReviewResult {
			return ReviewResult{Verdict: "findings", Findings: []ResultFinding{
				{Class: ClassIntent, Title: "Fix the\nwidget   over\n\nthere", Workspace: "root", Oracle: "test"},
			}, Summary: "s"}
		},
	}}
	round, ok, err := NewReviewerGateSource(backend).Round(RoundInput{Store: st, Ticket: ticket, Round: 1, LeaseDir: dir, RepoName: "repo", Target: "main", Manifest: oneWorkspaceManifest})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok || round.Review == nil || len(round.Review.Findings) != 1 {
		t.Fatalf("round = %+v, want 1 finding", round)
	}
	want := "Fix the widget over there"
	if got := round.Review.Findings[0].Title; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

// TestSynthesizeFixSlicesSanitizesWorkspaceID checks A7: a workspace id
// containing characters unsafe in a path segment ("/", "\", ":") is
// sanitized in a mechanical bundle's id, since that id becomes part of
// slices/<id>.state and work/<id>.attempt-N.* paths.
func TestSynthesizeFixSlicesSanitizesWorkspaceID(t *testing.T) {
	man := manifest.Manifest{Oracles: map[string]string{"test": "go test"}}
	kept := []Finding{
		{ID: "r1-f1", Class: ClassMechanical, Title: "a", Workspace: "svc/a:b\\c", Oracle: "test"},
		{ID: "r1-f2", Class: ClassMechanical, Title: "b", Workspace: "other", Oracle: "test"},
	}
	slices, err := synthesizeFixSlices(1, kept, man)
	if err != nil {
		t.Fatalf("synthesizeFixSlices: %v", err)
	}
	var got []string
	for _, s := range slices {
		got = append(got, s.ID)
	}
	want := "fix-1-mech-svc-a-b-c"
	found := false
	for _, id := range got {
		if id == want {
			found = true
		}
		if strings.ContainsAny(id, "/\\:") {
			t.Fatalf("slice id %q contains an unsafe path character", id)
		}
	}
	if !found {
		t.Fatalf("slice ids = %v, want one sanitized to %q", got, want)
	}
}

// TestSynthesizeFixSlicesDisambiguatesCollidingIDs checks D4: two mechanical
// findings in different workspaces whose sanitized ids collide ("svc/a" and
// "svc:a" both give "fix-1-mech-svc-a") get distinct slice ids instead of
// AppendSlices later rejecting the duplicate and leaving the round
// half-applied.
func TestSynthesizeFixSlicesDisambiguatesCollidingIDs(t *testing.T) {
	man := manifest.Manifest{Oracles: map[string]string{"test": "go test"}}
	kept := []Finding{
		{ID: "r1-f1", Class: ClassMechanical, Title: "a", Workspace: "svc/a", Oracle: "test"},
		{ID: "r1-f2", Class: ClassMechanical, Title: "b", Workspace: "svc:a", Oracle: "test"},
	}
	slices, err := synthesizeFixSlices(1, kept, man)
	if err != nil {
		t.Fatalf("synthesizeFixSlices: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("len(slices) = %d, want 2", len(slices))
	}
	ids := []string{slices[0].ID, slices[1].ID}
	if ids[0] == ids[1] {
		t.Fatalf("colliding slice ids not disambiguated: %v", ids)
	}
	if ids[0] != "fix-1-mech-svc-a" {
		t.Fatalf("first slice id = %q, want %q (first occurrence keeps the plain id)", ids[0], "fix-1-mech-svc-a")
	}
	if ids[1] != "fix-1-mech-svc-a-2" {
		t.Fatalf("second slice id = %q, want %q", ids[1], "fix-1-mech-svc-a-2")
	}
}

// TestDisambiguateFixSliceIDsSkipsTakenSuffixes checks that the suffix
// search does not stop at the first "-2" if a slice already legitimately
// has that id: it keeps counting until it finds a suffix nothing in the
// batch already uses.
func TestDisambiguateFixSliceIDsSkipsTakenSuffixes(t *testing.T) {
	slices := []store.Slice{
		{ID: "fix-1-mech-x"},
		{ID: "fix-1-mech-x-2"},
		{ID: "fix-1-mech-x"},
	}
	disambiguateFixSliceIDs(slices)
	got := []string{slices[0].ID, slices[1].ID, slices[2].ID}
	want := []string{"fix-1-mech-x", "fix-1-mech-x-2", "fix-1-mech-x-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slice[%d].ID = %q, want %q (got %v)", i, got[i], want[i], got)
		}
	}
}

// TestCarriedFindingOracleMatchesByWorkspaceNotUndisambiguatedID checks F4:
// when two mechanical bundles in different workspaces collide on the
// sanitized id ("svc/a" and "svc:a" both give "fix-1-mech-svc-a") and
// disambiguateFixSliceIDs pushes the second to "fix-1-mech-svc-a-2",
// carriedFindingOracle for a still-open finding from the second workspace
// must resolve the second bundle's own oracle, not the first workspace's
// bundle it would get by rebuilding "fix-1-mech-svc-a" from the workspace
// id alone.
func TestCarriedFindingOracleMatchesByWorkspaceNotUndisambiguatedID(t *testing.T) {
	_, st, ticket := newRoundInputRepo(t)
	slices := []store.Slice{
		{ID: "fix-1-mech-svc-a", Workspace: "svc/a", Oracle: "oracle-a", FromGate: 1, Rung: staircase.RungCheapest},
		{ID: "fix-1-mech-svc-a-2", Workspace: "svc:a", Oracle: "oracle-b", FromGate: 1, Rung: staircase.RungCheapest},
	}
	if err := st.AppendSlices(ticket, slices); err != nil {
		t.Fatalf("AppendSlices: %v", err)
	}

	prior := Finding{ID: "r1-f2", Class: ClassMechanical, Workspace: "svc:a"}
	oracle, err := carriedFindingOracle(st, ticket, "r1-f2", prior)
	if err != nil {
		t.Fatalf("carriedFindingOracle: %v", err)
	}
	if oracle != "oracle-b" {
		t.Fatalf("oracle = %q, want %q (the second, disambiguated workspace's own bundle)", oracle, "oracle-b")
	}

	priorFirst := Finding{ID: "r1-f1", Class: ClassMechanical, Workspace: "svc/a"}
	oracleFirst, err := carriedFindingOracle(st, ticket, "r1-f1", priorFirst)
	if err != nil {
		t.Fatalf("carriedFindingOracle: %v", err)
	}
	if oracleFirst != "oracle-a" {
		t.Fatalf("oracle = %q, want %q (the first workspace's own bundle)", oracleFirst, "oracle-a")
	}
}
