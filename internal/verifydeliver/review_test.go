package verifydeliver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// reviewGitEnv pins the identity/date used for every commit these tests
// make directly, so shas stay comparable across runs.
var reviewGitEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// newReviewLease creates a tiny git repo standing in for a gate lease,
// seeded with one commit on target and a refs/remotes/origin/<target> ref
// pointing at it - no real remote is needed, since gitx.MergeBase and
// gitx.RevParse only resolve refs.
func newReviewLease(t *testing.T, target string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := gitx.RunEnv(dir, reviewGitEnv, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", target)
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	writeReviewFile(t, dir, "seed.txt", "seed\n")
	run("add", "-A")
	run("commit", "-m", "seed")
	sha := run("rev-parse", "HEAD")
	run("update-ref", "refs/remotes/origin/"+target, sha)
	return dir
}

func writeReviewFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// commitReviewLease stages everything in dir and commits it, returning the
// new HEAD sha.
func commitReviewLease(t *testing.T, dir, msg string) string {
	t.Helper()
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := gitx.RunEnv(dir, reviewGitEnv, "commit", "-m", msg); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	sha, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return sha
}

// newReviewStore creates a minimal store rooted at a fresh temp dir: just
// enough for store.Open (a project.yaml) and TicketDir-based paths.
func newReviewStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st
}

func reviewInvalidCode(t *testing.T, err error) string {
	t.Helper()
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error = %v (%T), want *axi.Error", err, err)
	}
	return ae.Code
}

// --- MarshalReviewRequest --------------------------------------------------

func TestMarshalReviewRequestEmptyListsAsBrackets(t *testing.T) {
	data, err := MarshalReviewRequest(ReviewRequest{Ticket: "JIG-1", Round: 1})
	if err != nil {
		t.Fatalf("MarshalReviewRequest: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"oracles", "open", "dismissed", "must_review"} {
		got := strings.TrimSpace(string(raw[field]))
		if got != "[]" {
			t.Errorf("field %q = %s, want []", field, got)
		}
	}
}

func TestMarshalReviewRequestPreservesPopulatedLists(t *testing.T) {
	req := ReviewRequest{
		Oracles:    []string{"test"},
		Open:       []OpenFinding{{ID: "r1-f1", File: "a.go", Title: "t"}},
		Dismissed:  []DismissedFinding{{ID: "r1-f2", File: "b.go", Title: "u"}},
		MustReview: []string{"a.go", "b.go"},
	}
	data, err := MarshalReviewRequest(req)
	if err != nil {
		t.Fatalf("MarshalReviewRequest: %v", err)
	}
	var got ReviewRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Open) != 1 || got.Open[0].ID != "r1-f1" {
		t.Errorf("Open = %+v, want one entry r1-f1", got.Open)
	}
	if len(got.Dismissed) != 1 || got.Dismissed[0].ID != "r1-f2" {
		t.Errorf("Dismissed = %+v, want one entry r1-f2", got.Dismissed)
	}
}

// --- RenderReviewPrompt -----------------------------------------------------

func TestRenderReviewPrompt(t *testing.T) {
	req := ReviewRequest{Ticket: "JIG-1", Round: 2, Scope: "delta", BaseSHA: "aaa", HeadSHA: "bbb"}
	prompt := RenderReviewPrompt(req, "/abs/review.json", "/abs/result.json")

	for _, want := range []string{
		"round 2 of ticket JIG-1",
		"/abs/review.json",
		"delta diff aaa..bbb",
		"Do not edit files, commit, or push",
		`"fix"`, `"ask"`, `"note"`,
		"risk_rationale",
		"/abs/result.json",
		`"findings"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nprompt:\n%s", want, prompt)
		}
	}
	// The prompt must never enumerate kinds of problems or coach behavior
	// beyond the output contract (design 1, 4.3): it names no problem
	// categories like "typo" or "dead code".
	for _, forbidden := range []string{"typo", "dead code", "mechanical", "class"} {
		if strings.Contains(strings.ToLower(prompt), forbidden) {
			t.Errorf("prompt contains forbidden coaching text %q", forbidden)
		}
	}
}

// --- ParseReviewResult -------------------------------------------------------

func validResultJSON(t *testing.T, mutate func(*ReviewResult)) []byte {
	t.Helper()
	res := ReviewResult{
		Findings: []ResultFinding{{
			File:          "billing/invoices.go",
			Line:          42,
			Title:         "List returns every tenant's invoices",
			Detail:        "detail",
			Action:        ActionFix,
			Risk:          RiskHigh,
			RiskRationale: "cross-tenant data leak",
			Oracle:        "test",
		}},
		ReviewedPaths: []string{"billing/invoices.go"},
		Summary:       "summary",
	}
	if mutate != nil {
		mutate(&res)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal fixture result: %v", err)
	}
	return data
}

func TestParseReviewResultValid(t *testing.T) {
	data := validResultJSON(t, nil)
	res, err := ParseReviewResult(data)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Action != ActionFix {
		t.Fatalf("Findings = %+v", res.Findings)
	}
}

func TestParseReviewResultRejectsEveryInvalidRule(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"not json", []byte("not json")},
		{"two json objects", append(validResultJSON(t, nil), []byte("{}")...)},
		{"unknown action", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].Action = "maybe" })},
		{"unknown risk", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].Risk = "extreme" })},
		{"empty title", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].Title = "  " })},
		{"empty risk_rationale", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].RiskRationale = "" })},
		{"negative line", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].Line = -1 })},
		{"empty file", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].File = "" })},
		{"absolute unix file", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].File = "/etc/passwd" })},
		{"absolute windows file", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].File = `C:\etc\passwd` })},
		{"dot-dot segment", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].File = "../secret.go" })},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseReviewResult(c.data)
			if err == nil {
				t.Fatal("ParseReviewResult: expected an error, got nil")
			}
			if code := reviewInvalidCode(t, err); code != "REVIEW_INVALID" {
				t.Errorf("code = %q, want REVIEW_INVALID", code)
			}
		})
	}
}

func TestParseReviewResultNormalizesWindowsSeparatorsButKeepsFileVerbatim(t *testing.T) {
	data := validResultJSON(t, func(r *ReviewResult) { r.Findings[0].File = `billing\invoices.go` })
	res, err := ParseReviewResult(data)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if res.Findings[0].File != `billing\invoices.go` {
		t.Errorf("File = %q, want the reviewer's own spelling preserved", res.Findings[0].File)
	}
}

func TestParseReviewResultCleanNoFindings(t *testing.T) {
	data, err := json.Marshal(ReviewResult{ReviewedPaths: []string{"a.go"}, Summary: "clean"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := ParseReviewResult(data)
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none", res.Findings)
	}
}

// --- validateReviewResult ----------------------------------------------------

func validRequestAndResult() (ReviewRequest, ReviewResult) {
	req := ReviewRequest{
		Open:       []OpenFinding{{ID: "r1-f1", File: "a.go", Title: "open one"}},
		Dismissed:  []DismissedFinding{{ID: "r1-f2", File: "b.go", Title: "dismissed one"}},
		MustReview: []string{"c.go"},
	}
	result := ReviewResult{
		Findings: []ResultFinding{{
			File: "c.go", Line: 1, Title: "t", Detail: "d",
			Action: ActionFix, Risk: RiskLow, RiskRationale: "r",
			Oracle: "test",
		}},
		ReviewedPaths: []string{"c.go"},
	}
	return req, result
}

func TestValidateReviewResultRules(t *testing.T) {
	present := func(path string) (bool, error) { return true, nil }
	absent := func(path string) (bool, error) { return false, nil }
	noDeleted := map[string]bool{}

	t.Run("valid passes", func(t *testing.T) {
		req, result := validRequestAndResult()
		if err := validateReviewResult(req, result, present, noDeleted, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("file neither present nor deleted", func(t *testing.T) {
		req, result := validRequestAndResult()
		err := validateReviewResult(req, result, absent, noDeleted, []string{"test"})
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("deleted file accepted without an at-head check", func(t *testing.T) {
		req, result := validRequestAndResult()
		deleted := map[string]bool{"c.go": true}
		calledAtHead := false
		neverCalled := func(path string) (bool, error) { calledAtHead = true; return false, nil }
		if err := validateReviewResult(req, result, neverCalled, deleted, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
		if calledAtHead {
			t.Error("atHead was consulted for a file already known deleted in the scope diff")
		}
	})

	t.Run("oracle not a manifest oracle", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Oracle = "lint"
		err := validateReviewResult(req, result, present, noDeleted, []string{"test"})
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("omitted oracle with more than one manifest oracle and action fix", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Oracle = ""
		err := validateReviewResult(req, result, present, noDeleted, []string{"lint", "test"})
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("omitted oracle with more than one manifest oracle and action note", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Oracle = ""
		result.Findings[0].Action = ActionNote
		if err := validateReviewResult(req, result, present, noDeleted, []string{"lint", "test"}); err != nil {
			t.Fatalf("validateReviewResult: %v (a note never needs an oracle)", err)
		}
	})

	t.Run("omitted oracle with exactly one manifest oracle", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Oracle = ""
		if err := validateReviewResult(req, result, present, noDeleted, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v (no choice to make with one oracle)", err)
		}
	})

	t.Run("prior names no known id", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f9"
		err := validateReviewResult(req, result, present, noDeleted, []string{"test"})
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("prior names an open id", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f1"
		if err := validateReviewResult(req, result, present, noDeleted, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("prior names a dismissed id", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f2"
		if err := validateReviewResult(req, result, present, noDeleted, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("two findings share the same prior", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f1"
		second := result.Findings[0]
		second.File = "a.go"
		result.Findings = append(result.Findings, second)
		err := validateReviewResult(req, result, present, noDeleted, []string{"test"})
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("reviewed_paths missing a must_review path", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.ReviewedPaths = nil
		err := validateReviewResult(req, result, present, noDeleted, []string{"test"})
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("reviewed_paths covers must_review across a Windows separator", func(t *testing.T) {
		req, result := validRequestAndResult()
		req.MustReview = []string{"dir/c.go"}
		result.Findings[0].File = "dir/c.go"
		result.ReviewedPaths = []string{`dir\c.go`}
		if err := validateReviewResult(req, result, present, noDeleted, []string{"test"}); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})
}

// --- computeScopeDiff --------------------------------------------------------

func TestComputeScopeDiff(t *testing.T) {
	dir := newReviewLease(t, "main")
	writeReviewFile(t, dir, "keep.go", "1")
	writeReviewFile(t, dir, "old.go", "renamed")
	writeReviewFile(t, dir, "open-gone.go", "will be deleted")
	writeReviewFile(t, dir, "open-stays.go", "stays")
	base := commitReviewLease(t, dir, "base")

	if err := os.Rename(filepath.Join(dir, "old.go"), filepath.Join(dir, "new.go")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	writeReviewFile(t, dir, "keep.go", "2")
	writeReviewFile(t, dir, "added.go", "brand new")
	if err := os.Remove(filepath.Join(dir, "open-gone.go")); err != nil {
		t.Fatalf("remove open-gone.go: %v", err)
	}
	head := commitReviewLease(t, dir, "head")

	openFiles := []string{"open-gone.go", "open-stays.go", "keep.go"}
	diff, err := computeScopeDiff(dir, base, head, openFiles)
	if err != nil {
		t.Fatalf("computeScopeDiff: %v", err)
	}

	wantChanged := []string{"added.go", "keep.go", "new.go"}
	if !equalStrings(diff.Changed, wantChanged) {
		t.Errorf("Changed = %v, want %v", diff.Changed, wantChanged)
	}
	wantDeleted := []string{"old.go", "open-gone.go"}
	if !equalStrings(diff.Deleted, wantDeleted) {
		t.Errorf("Deleted = %v, want %v", diff.Deleted, wantDeleted)
	}
	// must_review = changed union {open files that still exist at head}:
	// open-gone.go was deleted (excluded), open-stays.go still exists
	// (included even though it is not part of the diff itself), keep.go is
	// already in changed (deduplicated, not doubled).
	wantMustReview := []string{"added.go", "keep.go", "new.go", "open-stays.go"}
	if !equalStrings(diff.MustReview, wantMustReview) {
		t.Errorf("MustReview = %v, want %v", diff.MustReview, wantMustReview)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// --- resolveScopeBase ---------------------------------------------------------

func TestResolveScopeBaseRound1IsFullFromMergeBase(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	head := commitReviewLease(t, dir, "head")
	baseWant, err := gitx.RevParse(dir, "origin/main")
	if err != nil {
		t.Fatalf("rev-parse origin/main: %v", err)
	}

	scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 1, head)
	if err != nil {
		t.Fatalf("resolveScopeBase: %v", err)
	}
	if scope != "full" {
		t.Errorf("scope = %q, want full", scope)
	}
	if base != baseWant {
		t.Errorf("base = %q, want merge-base %q", base, baseWant)
	}
}

func TestResolveScopeBaseDeltaWhenPriorReviewedSHAIsAncestor(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	round1Head := commitReviewLease(t, dir, "round 1")
	if err := os.MkdirAll(gateRoundDir(st, "JIG-1", 1), 0o755); err != nil {
		t.Fatalf("mkdir round 1 dir: %v", err)
	}
	if err := writeReportYAML(gateRoundDir(st, "JIG-1", 1), GateReport{Round: 1, ReviewedSHA: map[string]string{"fixture-repo": round1Head}}); err != nil {
		t.Fatalf("write round 1 report.yaml: %v", err)
	}
	writeReviewFile(t, dir, "b.go", "2")
	round2Head := commitReviewLease(t, dir, "round 2")

	scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, round2Head)
	if err != nil {
		t.Fatalf("resolveScopeBase: %v", err)
	}
	if scope != "delta" {
		t.Errorf("scope = %q, want delta", scope)
	}
	if base != round1Head {
		t.Errorf("base = %q, want round 1's reviewed sha %q", base, round1Head)
	}
}

func TestResolveScopeBaseFullWhenPriorReviewedSHAIsNotAncestor(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	commitReviewLease(t, dir, "shared base")
	// A reviewed_sha that never lands on this lease's history (as a rebase
	// would leave behind) must fall back to full, not error.
	stray := "0123456789abcdef0123456789abcdef01234567"
	if err := os.MkdirAll(gateRoundDir(st, "JIG-1", 1), 0o755); err != nil {
		t.Fatalf("mkdir round 1 dir: %v", err)
	}
	if err := writeReportYAML(gateRoundDir(st, "JIG-1", 1), GateReport{Round: 1, ReviewedSHA: map[string]string{"fixture-repo": stray}}); err != nil {
		t.Fatalf("write round 1 report.yaml: %v", err)
	}
	writeReviewFile(t, dir, "b.go", "2")
	head := commitReviewLease(t, dir, "round 2")
	mergeBaseWant, err := gitx.RevParse(dir, "origin/main")
	if err != nil {
		t.Fatalf("rev-parse origin/main: %v", err)
	}

	scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, head)
	if err != nil {
		t.Fatalf("resolveScopeBase: %v", err)
	}
	if scope != "full" {
		t.Errorf("scope = %q, want full", scope)
	}
	if base != mergeBaseWant {
		t.Errorf("base = %q, want merge-base %q", base, mergeBaseWant)
	}
}

func TestResolveScopeBaseFallsBackToStartSHAWhenMergeBaseFails(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := gitx.RunEnv(dir, reviewGitEnv, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	writeReviewFile(t, dir, "a.go", "1")
	run("add", "-A")
	run("commit", "-m", "c1")
	// No refs/remotes/origin/main at all: merge-base must fail.
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}

	st := newReviewStore(t)
	startPath := filepath.Join(st.TicketDir("JIG-1"), "start.fixture-repo.sha")
	if err := os.MkdirAll(filepath.Dir(startPath), 0o755); err != nil {
		t.Fatalf("mkdir ticket dir: %v", err)
	}
	if err := os.WriteFile(startPath, []byte(head), 0o644); err != nil {
		t.Fatalf("write start sha: %v", err)
	}

	scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 1, head)
	if err != nil {
		t.Fatalf("resolveScopeBase: %v", err)
	}
	if scope != "full" {
		t.Errorf("scope = %q, want full", scope)
	}
	if base != head {
		t.Errorf("base = %q, want the recorded start sha %q", base, head)
	}
}

func TestResolveScopeBaseErrorsWhenMergeBaseFailsAndNoStartSHA(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := gitx.RunEnv(dir, reviewGitEnv, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	writeReviewFile(t, dir, "a.go", "1")
	run("add", "-A")
	run("commit", "-m", "c1")
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}

	st := newReviewStore(t)
	if _, _, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 1, head); err == nil {
		t.Fatal("resolveScopeBase: expected an error, got nil")
	}
}

// --- reviewerGateSource.Round --------------------------------------------------

// stubBackend implements session.Backend with a plain function, letting
// each test script the reviewer's behavior directly.
type stubBackend struct {
	run func(d session.Dispatch) error
}

func (s stubBackend) Run(d session.Dispatch) error { return s.run(d) }

// oneOracleManifest is the manifest every reviewerGateSource.Round test
// uses unless it specifically wants to exercise the multi-oracle rule: a
// single oracle, with a root workspace (".") matching a real project's
// default manifest (Q1: "main's default manifest has a root workspace
// `.`") so every file a test hands it derives a non-empty workspace unless
// the test builds a narrower manifest itself.
func oneOracleManifest() manifest.Manifest {
	return manifest.Manifest{
		Oracles:    map[string]string{"test": "go test ./..."},
		Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
	}
}

// writeMustReviewResult writes a clean (no findings) result.json to
// d.ResultJSON, covering exactly review.json's must_review list - the
// minimal valid reviewer response.
func writeMustReviewResult(t *testing.T, d session.Dispatch) {
	t.Helper()
	reviewData, err := os.ReadFile(d.SliceJSON)
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var req ReviewRequest
	if err := json.Unmarshal(reviewData, &req); err != nil {
		t.Fatalf("parse review.json: %v", err)
	}
	result := ReviewResult{ReviewedPaths: req.MustReview, Summary: "clean"}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(d.ResultJSON), 0o755); err != nil {
		t.Fatalf("mkdir result dir: %v", err)
	}
	if err := os.WriteFile(d.ResultJSON, data, 0o644); err != nil {
		t.Fatalf("write result.json: %v", err)
	}
}

func TestReviewerGateSourceRoundHappyPath(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "brief.md", "brief")
	writeReviewFile(t, dir, "a.go", "changed")
	head := commitReviewLease(t, dir, "round 1 head")

	// Leftover dirt from an earlier, killed oracle run: the pre-dispatch
	// pristine restore must wipe it before review.json is even built.
	writeReviewFile(t, dir, "leftover.txt", "oracle dirt")

	var sawReviewPath string
	var stalePresentAtDispatch bool
	staleResultPath := reviewResultJSONPath(st, "JIG-1", 1)
	if err := os.MkdirAll(filepath.Dir(staleResultPath), 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	if err := os.WriteFile(staleResultPath, []byte("garbage from a failed attempt"), 0o644); err != nil {
		t.Fatalf("write stale result.json: %v", err)
	}

	backend := stubBackend{run: func(d session.Dispatch) error {
		sawReviewPath = d.SliceJSON
		if _, err := os.Stat(d.ResultJSON); err == nil {
			stalePresentAtDispatch = true
		}
		writeMustReviewResult(t, d)
		return nil
	}}

	src := NewReviewerGateSource(backend)
	rnd, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		BriefPath: filepath.Join(dir, "brief.md"), Manifest: oneOracleManifest(),
	})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok {
		t.Fatal("Round: ok = false, want true")
	}
	if stalePresentAtDispatch {
		t.Error("a stale result.json from an earlier attempt was still present when the backend ran")
	}
	if sawReviewPath != reviewJSONPath(st, "JIG-1", 1) {
		t.Errorf("dispatch SliceJSON = %q, want %q", sawReviewPath, reviewJSONPath(st, "JIG-1", 1))
	}
	if rnd.Review == nil {
		t.Fatal("Round.Review is nil")
	}
	if rnd.Review.Scope != "full" || rnd.Review.HeadSHA != head {
		t.Errorf("Review = %+v", rnd.Review)
	}
	if len(rnd.Review.MustReview) == 0 || rnd.Review.MustReview[0] != "a.go" {
		t.Errorf("MustReview = %v, want [a.go]", rnd.Review.MustReview)
	}

	// The leftover dirt must be gone: the pre-dispatch restore wiped it,
	// and it never re-appears because the backend never touches the tree.
	status, err := gitx.Run(dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status != "" {
		t.Errorf("lease dirty after Round: %q", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "leftover.txt")); err == nil {
		t.Error("leftover.txt survived the pre-dispatch pristine restore")
	}

	// review.json itself: spot-check the wire shape.
	reviewData, err := os.ReadFile(sawReviewPath)
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var req ReviewRequest
	if err := json.Unmarshal(reviewData, &req); err != nil {
		t.Fatalf("parse review.json: %v", err)
	}
	if req.Ticket != "JIG-1" || req.Round != 1 || req.Scope != "full" {
		t.Errorf("review.json = %+v", req)
	}
	if len(req.Oracles) != 1 || req.Oracles[0] != "test" {
		t.Errorf("review.json oracles = %v, want [test]", req.Oracles)
	}
}

func TestReviewerGateSourceRoundGuardRejectsACommit(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	headBefore := commitReviewLease(t, dir, "head")

	backend := stubBackend{run: func(d session.Dispatch) error {
		writeReviewFile(t, dir, "sneaky.go", "the reviewer edited")
		commitReviewLease(t, dir, "reviewer committed, which it must never do")
		writeMustReviewResult(t, d)
		return nil
	}}

	src := NewReviewerGateSource(backend)
	_, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err == nil {
		t.Fatal("Round: expected REVIEW_INVALID, got nil")
	}
	if ok {
		t.Error("Round: ok = true, want false")
	}
	if code := reviewInvalidCode(t, err); code != "REVIEW_INVALID" {
		t.Errorf("code = %q, want REVIEW_INVALID", code)
	}

	// The post-dispatch restore must still put the lease back exactly
	// where it was, even though the round failed.
	headAfter, rerr := gitx.RevParse(dir, "HEAD")
	if rerr != nil {
		t.Fatalf("rev-parse HEAD: %v", rerr)
	}
	if headAfter != headBefore {
		t.Errorf("HEAD after a failed round = %q, want restored to %q", headAfter, headBefore)
	}
}

func TestReviewerGateSourceRoundGuardRejectsDirtyTree(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	commitReviewLease(t, dir, "head")

	backend := stubBackend{run: func(d session.Dispatch) error {
		writeReviewFile(t, dir, "a.go", "2") // tracked, but never committed
		writeMustReviewResult(t, d)
		return nil
	}}

	src := NewReviewerGateSource(backend)
	_, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err == nil || ok {
		t.Fatalf("Round: err=%v ok=%v, want a REVIEW_INVALID error", err, ok)
	}
	if code := reviewInvalidCode(t, err); code != "REVIEW_INVALID" {
		t.Errorf("code = %q, want REVIEW_INVALID", code)
	}
	status, serr := gitx.Run(dir, "status", "--porcelain")
	if serr != nil {
		t.Fatalf("git status: %v", serr)
	}
	if status != "" {
		t.Errorf("lease not restored clean after a failed round: %q", status)
	}
}

func TestReviewerGateSourceRoundBackendFailure(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	commitReviewLease(t, dir, "head")

	backend := stubBackend{run: func(d session.Dispatch) error {
		return errors.New("boom")
	}}

	src := NewReviewerGateSource(backend)
	_, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err == nil || ok {
		t.Fatalf("Round: err=%v ok=%v, want a REVIEW_FAILED error", err, ok)
	}
	if code := reviewInvalidCode(t, err); code != "REVIEW_FAILED" {
		t.Errorf("code = %q, want REVIEW_FAILED", code)
	}
}

func TestReviewerGateSourceRoundNoResultWritten(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	commitReviewLease(t, dir, "head")

	backend := stubBackend{run: func(d session.Dispatch) error { return nil }}

	src := NewReviewerGateSource(backend)
	_, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err == nil || ok {
		t.Fatalf("Round: err=%v ok=%v, want a REVIEW_FAILED error", err, ok)
	}
	if code := reviewInvalidCode(t, err); code != "REVIEW_FAILED" {
		t.Errorf("code = %q, want REVIEW_FAILED", code)
	}
}

func TestReviewerGateSourceRoundMissingCoverageIsInvalid(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	writeReviewFile(t, dir, "b.go", "1")
	commitReviewLease(t, dir, "head")

	backend := stubBackend{run: func(d session.Dispatch) error {
		// Covers only a.go, skipping must_review's b.go.
		result := ReviewResult{ReviewedPaths: []string{"a.go"}}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return os.WriteFile(d.ResultJSON, data, 0o644)
	}}

	src := NewReviewerGateSource(backend)
	_, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err == nil || ok {
		t.Fatalf("Round: err=%v ok=%v, want a REVIEW_INVALID error", err, ok)
	}
	if code := reviewInvalidCode(t, err); code != "REVIEW_INVALID" {
		t.Errorf("code = %q, want REVIEW_INVALID", code)
	}
	if !strings.Contains(err.Error(), "b.go") {
		t.Errorf("error = %v, want it to name the missing path b.go", err)
	}

	// A corrected result then passes.
	backend2 := stubBackend{run: func(d session.Dispatch) error { writeMustReviewResult(t, d); return nil }}
	src2 := NewReviewerGateSource(backend2)
	_, ok2, err2 := src2.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err2 != nil {
		t.Fatalf("Round after fix: %v", err2)
	}
	if !ok2 {
		t.Fatal("Round after fix: ok = false, want true")
	}
}

func TestReviewerGateSourceRoundPriorNamingNoKnownIDIsInvalid(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	commitReviewLease(t, dir, "head")

	backend := stubBackend{run: func(d session.Dispatch) error {
		reviewData, err := os.ReadFile(d.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}
		result := ReviewResult{
			Findings: []ResultFinding{{
				File: "a.go", Title: "t", Detail: "d",
				Action: ActionNote, Risk: RiskLow, RiskRationale: "r",
				Prior: "r1-f99",
			}},
			ReviewedPaths: req.MustReview,
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return os.WriteFile(d.ResultJSON, data, 0o644)
	}}

	src := NewReviewerGateSource(backend)
	_, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err == nil || ok {
		t.Fatalf("Round: err=%v ok=%v, want a REVIEW_INVALID error", err, ok)
	}
	if code := reviewInvalidCode(t, err); code != "REVIEW_INVALID" {
		t.Errorf("code = %q, want REVIEW_INVALID", code)
	}
}

// TestReviewerGateSourceRoundDeltaScopeAcrossRounds drives two rounds
// directly (findings bookkeeping, which would normally persist
// reviewed_sha via Gate, is S2's job - this test writes round 1's
// report.yaml by hand) and checks round 2 resolves a delta scope anchored
// on round 1's reviewed_sha.
func TestReviewerGateSourceRoundDeltaScopeAcrossRounds(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	round1Head := commitReviewLease(t, dir, "round 1")

	backend := stubBackend{run: func(d session.Dispatch) error { writeMustReviewResult(t, d); return nil }}
	src := NewReviewerGateSource(backend)

	rnd1, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err != nil || !ok {
		t.Fatalf("round 1: err=%v ok=%v", err, ok)
	}
	if rnd1.Review.Scope != "full" {
		t.Fatalf("round 1 scope = %q, want full", rnd1.Review.Scope)
	}
	if rnd1.Review.HeadSHA != round1Head {
		t.Fatalf("round 1 HeadSHA = %q, want %q", rnd1.Review.HeadSHA, round1Head)
	}
	if err := os.MkdirAll(gateRoundDir(st, "JIG-1", 1), 0o755); err != nil {
		t.Fatalf("mkdir round 1 dir: %v", err)
	}
	if err := writeReportYAML(gateRoundDir(st, "JIG-1", 1), GateReport{
		Round: 1, ReviewedSHA: map[string]string{"fixture-repo": rnd1.Review.HeadSHA},
	}); err != nil {
		t.Fatalf("write round 1 report.yaml: %v", err)
	}

	writeReviewFile(t, dir, "b.go", "2")
	round2Head := commitReviewLease(t, dir, "round 2")

	rnd2, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 2, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err != nil || !ok {
		t.Fatalf("round 2: err=%v ok=%v", err, ok)
	}
	if rnd2.Review.Scope != "delta" {
		t.Fatalf("round 2 scope = %q, want delta", rnd2.Review.Scope)
	}
	if rnd2.Review.BaseSHA != round1Head {
		t.Fatalf("round 2 BaseSHA = %q, want round 1's head %q", rnd2.Review.BaseSHA, round1Head)
	}
	if rnd2.Review.HeadSHA != round2Head {
		t.Fatalf("round 2 HeadSHA = %q, want %q", rnd2.Review.HeadSHA, round2Head)
	}
	if !equalStrings(rnd2.Review.MustReview, []string{"b.go"}) {
		t.Fatalf("round 2 MustReview = %v, want [b.go]", rnd2.Review.MustReview)
	}
}

// TestReviewerGateSourceRoundWithFakeBackend ties review.go's dispatch
// together with session's fake backend (its gate playback path), the
// combination a scripted end-to-end run exercises.
func TestReviewerGateSourceRoundWithFakeBackend(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	writeReviewFile(t, dir, "a.go", "1")
	writeReviewFile(t, dir, "b.go", "1")
	commitReviewLease(t, dir, "head")

	scenarioDir := t.TempDir()
	roundDir := filepath.Join(scenarioDir, "gate", "round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	result := ReviewResult{
		Findings: []ResultFinding{{
			File: "a.go", Line: 1, Title: "t", Detail: "d",
			Action: ActionNote, Risk: RiskLow, RiskRationale: "r",
		}},
		ReviewedPaths: []string{"a.go", "b.go"},
		Summary:       "s",
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal scenario result: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roundDir, "review-result.json"), data, 0o644); err != nil {
		t.Fatalf("write review-result.json: %v", err)
	}

	backend, err := session.New("fake", session.Options{ScenarioDir: scenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	src := NewReviewerGateSource(backend)
	rnd, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok {
		t.Fatal("Round: ok = false, want true")
	}
	if len(rnd.Review.Result.Findings) != 1 || rnd.Review.Result.Findings[0].Action != ActionNote {
		t.Errorf("Result.Findings = %+v", rnd.Review.Result.Findings)
	}
}

// TestReviewerGateSourceRoundCleanWithoutDispatchWhenNothingOutstanding
// covers design 5.4/Q8: when the scope diff changes nothing and no
// finding is open, the previous review already covers head, so Round
// never dispatches a reviewer session at all.
func TestReviewerGateSourceRoundCleanWithoutDispatchWhenNothingOutstanding(t *testing.T) {
	dir := newReviewLease(t, "main") // HEAD already equals origin/main
	st := newReviewStore(t)
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}

	called := false
	backend := stubBackend{run: func(session.Dispatch) error {
		called = true
		return errors.New("must not dispatch")
	}}

	src := NewReviewerGateSource(backend)
	rnd, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
	})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok {
		t.Fatal("Round: ok = false, want true (a clean round)")
	}
	if called {
		t.Error("the backend was dispatched even though nothing was outstanding")
	}
	if rnd.Review == nil {
		t.Fatal("Round.Review is nil")
	}
	if rnd.Review.HeadSHA != head {
		t.Errorf("Review.HeadSHA = %q, want %q", rnd.Review.HeadSHA, head)
	}
	if len(rnd.Review.MustReview) != 0 {
		t.Errorf("Review.MustReview = %v, want none", rnd.Review.MustReview)
	}
	if len(rnd.Review.Result.Findings) != 0 {
		t.Errorf("Review.Result.Findings = %v, want none", rnd.Review.Result.Findings)
	}
	if rnd.Review.Result.ReviewedPaths == nil || len(rnd.Review.Result.ReviewedPaths) != 0 {
		t.Errorf("Review.Result.ReviewedPaths = %v, want an empty (non-nil) slice", rnd.Review.Result.ReviewedPaths)
	}
}

// TestReviewerGateSourceRoundDispatchesWhenOpenFindingsAreOutstanding
// covers the other half of Q8: even with an empty scope diff, an open
// finding still outstanding from an earlier round means the reviewer must
// look again, so Round dispatches as usual.
func TestReviewerGateSourceRoundDispatchesWhenOpenFindingsAreOutstanding(t *testing.T) {
	dir := newReviewLease(t, "main")
	st := newReviewStore(t)

	called := false
	backend := stubBackend{run: func(d session.Dispatch) error {
		called = true
		writeMustReviewResult(t, d)
		return nil
	}}

	src := NewReviewerGateSource(backend)
	_, ok, err := src.Round(RoundInput{
		Store: st, Ticket: "JIG-1", Round: 1, LeaseDir: dir,
		RepoName: "fixture-repo", Target: "main", Model: "rung-a",
		Manifest: oneOracleManifest(),
		Open:     []OpenFinding{{ID: "r1-f1", File: "a.go", Title: "t", Action: ActionFix}},
	})
	if err != nil {
		t.Fatalf("Round: %v", err)
	}
	if !ok {
		t.Fatal("Round: ok = false, want true")
	}
	if !called {
		t.Error("the backend was never dispatched despite an open finding")
	}
}
