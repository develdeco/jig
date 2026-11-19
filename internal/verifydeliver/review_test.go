package verifydeliver

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
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

// requireIntentWireShape checks the intent binding's wire shape in the
// review.json bytes a reviewer is handed, decoding them into raw keys
// rather than into ReviewRequest: every other reader of review.json decodes
// into that struct, so a renamed json tag round-trips through it unnoticed
// while the reviewer looks for the name its prompt gave. The shape is one
// top-level "intent" object with exactly the keys "source" and "path", and
// no "brief_path" left beside it.
func requireIntentWireShape(t *testing.T, reviewJSON []byte) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(reviewJSON, &raw); err != nil {
		t.Fatalf("parse review.json as raw keys: %v", err)
	}
	if _, ok := raw["brief_path"]; ok {
		t.Errorf("review.json still carries brief_path, which intent replaced:\n%s", reviewJSON)
	}
	rawIntent, ok := raw["intent"]
	if !ok {
		t.Fatalf("review.json has no \"intent\" key:\n%s", reviewJSON)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawIntent, &fields); err != nil {
		t.Fatalf("review.json's \"intent\" is not an object: %v\n%s", err, rawIntent)
	}
	if got, want := slices.Sorted(maps.Keys(fields)), []string{"path", "source"}; !slices.Equal(got, want) {
		t.Errorf("review.json's intent keys = %v, want %v", got, want)
	}
}

// --- MarshalReviewRequest --------------------------------------------------

func TestMarshalReviewRequestEmptyListsAsBrackets(t *testing.T) {
	t.Parallel()

	data, err := MarshalReviewRequest(ReviewRequest{Ticket: "JIG-1", Round: 1})
	if err != nil {
		t.Fatalf("MarshalReviewRequest: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"oracles", "oracles_passed", "open", "dismissed", "must_review"} {
		got := strings.TrimSpace(string(raw[field]))
		if got != "[]" {
			t.Errorf("field %q = %s, want []", field, got)
		}
	}
}

func TestMarshalReviewRequestPreservesPopulatedLists(t *testing.T) {
	t.Parallel()

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
		StillPresent:  []StillPresentEntry{},
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

// validResultJSONRaw is validResultJSON decoded to a generic map so a test
// can add or rename a key that ResultFinding's own type could never
// express (e.g. a misnamed "prior_id"), then re-encoded.
func validResultJSONRaw(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(validResultJSON(t, nil), &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	mutate(m)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal mutated result: %v", err)
	}
	return data
}

func TestParseReviewResultValid(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
		{"directory file", validResultJSON(t, func(r *ReviewResult) { r.Findings[0].File = "alpha/" })},
		// A bare JSON null, misnamed top-level and finding keys, and any
		// non-object top level must all be rejected, never silently
		// accepted as a clean (zero-finding) result.
		{"bare null", []byte("null")},
		{"json array", []byte(`[{"findings": [], "reviewed_paths": []}]`)},
		{"json string", []byte(`"clean"`)},
		{"misnamed findings key", []byte(`{"finding": [], "reviewed_paths": []}`)},
		{"misnamed reviewed_paths key", []byte(`{"findings": [], "reviewedPaths": []}`)},
		{"missing findings key", []byte(`{"reviewed_paths": []}`)},
		{"missing reviewed_paths key", []byte(`{"findings": []}`)},
		{"null findings", []byte(`{"findings": null, "reviewed_paths": []}`)},
		{"null reviewed_paths", []byte(`{"findings": [], "reviewed_paths": null}`)},
		{"unknown top-level key", []byte(`{"findings": [], "reviewed_paths": [], "issues": []}`)},
		{"misnamed prior key", validResultJSONRaw(t, func(m map[string]any) {
			f := m["findings"].([]any)[0].(map[string]any)
			f["prior_id"] = "r1-f1"
		})},
		// A duplicate key - exact, or only a different case of a key already
		// used - repeats at the top level or inside a finding: encoding/json
		// would otherwise fold the case and let the last one silently win.
		{"duplicate findings key (case variant)", []byte(`{"findings": [], "reviewed_paths": [], "FINDINGS": []}`)},
		{"literal duplicate top-level key", []byte(`{"findings": [], "reviewed_paths": [], "reviewed_paths": ["x.go"]}`)},
		{"duplicate key inside a finding", validResultJSONRaw(t, func(m map[string]any) {
			f := m["findings"].([]any)[0].(map[string]any)
			f["Title"] = "a different title"
		})},
		// A lone case variant with no duplicate to catch (the correctly-cased
		// key is absent, not repeated) must still be rejected: exact key
		// names are required, not merely no-duplicates, since
		// encoding/json's own struct decode matches a key to a field
		// case-insensitively and would otherwise accept it silently.
		{"lone case-variant top-level key", []byte(`{"findings": [], "reviewed_paths": [], "Summary": "x"}`)},
		{"lone case-variant finding key", validResultJSONRaw(t, func(m map[string]any) {
			f := m["findings"].([]any)[0].(map[string]any)
			delete(f, "oracle")
			f["Oracle"] = "test"
		})},
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
	t.Parallel()

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
	t.Parallel()

	data := marshalReviewResult(t, ReviewResult{ReviewedPaths: []string{"a.go"}, Summary: "clean"})
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
	t.Parallel()

	leaseDir := t.TempDir()
	present := func(path string) (bool, error) { return true, nil }
	absent := func(path string) (bool, error) { return false, nil }
	noDeleted := map[string]bool{}

	t.Run("valid passes", func(t *testing.T) {
		req, result := validRequestAndResult()
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("file neither present nor deleted", func(t *testing.T) {
		req, result := validRequestAndResult()
		err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), absent, noDeleted)
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("deleted file accepted without an at-head check", func(t *testing.T) {
		req, result := validRequestAndResult()
		deleted := map[string]bool{"c.go": true}
		calledAtHead := false
		neverCalled := func(path string) (bool, error) { calledAtHead = true; return false, nil }
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), neverCalled, deleted); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
		if calledAtHead {
			t.Error("atHead was consulted for a file already known deleted in the scope diff")
		}
	})

	t.Run("any text is a command", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Oracle = "go test ./internal/x -run 'TestA'"
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v (a command need not be a manifest oracle)", err)
		}
	})

	t.Run("a fix names a command", func(t *testing.T) {
		_, result := validRequestAndResult()
		result.Findings[0].Oracle = ""
		result.Findings[0].Action = ActionFix
		err := checkResultFinding(0, result.Findings[0])
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
		result.Findings[0].Prior = "r1-f1"
		if err := checkResultFinding(0, result.Findings[0]); err != nil {
			t.Fatalf("a changed version keeps its prior's command: %v", err)
		}
	})

	t.Run("omitted oracle with more than one manifest oracle and action note", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Oracle = ""
		result.Findings[0].Action = ActionNote
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"lint", "test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v (a note never needs an oracle)", err)
		}
	})

	t.Run("omitted oracle with exactly one manifest oracle", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Oracle = ""
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v (no choice to make with one oracle)", err)
		}
	})

	t.Run("prior names no known id", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f9"
		err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted)
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("prior names an open id", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f1"
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("prior names a dismissed id", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f2"
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	t.Run("two findings share the same prior", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.Findings[0].Prior = "r1-f1"
		second := result.Findings[0]
		second.File = "a.go"
		result.Findings = append(result.Findings, second)
		err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted)
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("reviewed_paths missing a must_review path", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.ReviewedPaths = nil
		err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted)
		if err == nil || reviewInvalidCode(t, err) != "REVIEW_INVALID" {
			t.Fatalf("err = %v, want REVIEW_INVALID", err)
		}
	})

	t.Run("reviewed_paths covers must_review across a Windows separator", func(t *testing.T) {
		req, result := validRequestAndResult()
		req.MustReview = []string{"dir/c.go"}
		result.Findings[0].File = "dir/c.go"
		result.ReviewedPaths = []string{`dir\c.go`}
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v", err)
		}
	})

	// reviewed_paths absolute entries (review.json, the intent path,
	// slices_path, journal_path are all absolute, and the prompt asks for
	// "every file you read") never fail the round; an absolute path inside
	// the lease worktree still counts toward coverage.
	t.Run("absolute paths outside the worktree in reviewed_paths do not fail the round", func(t *testing.T) {
		req, result := validRequestAndResult()
		result.ReviewedPaths = append(result.ReviewedPaths,
			filepath.Join(leaseDir, "..", "brief.md"), // outside the worktree
			`C:\some\other\place\review.json`,
			"../sibling/x.go",
		)
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v, want these entries merely ignored", err)
		}
	})

	t.Run("an absolute path inside the worktree in reviewed_paths counts as coverage", func(t *testing.T) {
		req, result := validRequestAndResult()
		req.MustReview = []string{"c.go", "dir/d.go"}
		result.Findings[0].File = "c.go"
		result.ReviewedPaths = []string{
			filepath.Join(leaseDir, "c.go"),
			filepath.Join(leaseDir, "dir", "d.go"),
		}
		if err := validateReviewResult(req, result, oneRepoIn(leaseDir, []string{"test"}), present, noDeleted); err != nil {
			t.Fatalf("validateReviewResult: %v, want an absolute worktree path to cover must_review", err)
		}
	})
}

// --- computeScopeDiff --------------------------------------------------------

func TestComputeScopeDiff(t *testing.T) {
	t.Parallel()

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

// TestReviewBaseOfNamesEachRepo: the base a round records for a repo is the one
// base of a round over one repo, whatever the repo's name, and for a round over
// several the base of that repo's own entry, never its head, nor another's.
func TestReviewBaseOfNamesEachRepo(t *testing.T) {
	t.Parallel()
	one := &Review{BaseSHA: "b-one", HeadSHA: "h-one"}
	if got := one.baseOf("any"); got != "b-one" {
		t.Errorf("one repo: baseOf = %q, want b-one", got)
	}
	several := &Review{Repos: []ReviewedRepo{{Repo: "api", BaseSHA: "b-api", HeadSHA: "h-api"}, {Repo: "web", BaseSHA: "b-web", HeadSHA: "h-web"}}}
	for repo, want := range map[string]string{"api": "b-api", "web": "b-web", "docs": ""} {
		if got := several.baseOf(repo); got != want {
			t.Errorf("several repos: baseOf(%q) = %q, want %q", repo, got, want)
		}
	}
}

// --- resolveScopeBase ---------------------------------------------------------

func TestResolveScopeBaseRound1IsFullFromMergeBase(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

// TestResolveScopeBasePassesOverABuildersRound: a round the records show one of
// the builders reviewed (independent: false) is not what a delta takes as
// reviewed. The round after it reviews from the latest earlier round that was
// not a builder's, or, when there is none, the whole change; an unknown
// reviewer's round still anchors the delta.
func TestResolveScopeBasePassesOverABuildersRound(t *testing.T) {
	t.Parallel()

	dir := newReviewLease(t, "main")
	st := newReviewStore(t)
	fullBase, err := gitx.RevParse(dir, "origin/main")
	if err != nil {
		t.Fatalf("rev-parse origin/main: %v", err)
	}
	record := func(n int, independent Independence, head string) {
		t.Helper()
		if err := os.MkdirAll(gateRoundDir(st, "JIG-1", n), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeReportYAML(gateRoundDir(st, "JIG-1", n), GateReport{Round: n, Independent: independent, ReviewedSHA: map[string]string{"fixture-repo": head}}); err != nil {
			t.Fatal(err)
		}
	}
	writeReviewFile(t, dir, "a.go", "1")
	round1Head := commitReviewLease(t, dir, "round 1")

	// Round 1 was a builder's: round 2, at the same head, is the whole change.
	record(1, IndependentNo, round1Head)
	if scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, round1Head); err != nil || scope != "full" || base != fullBase {
		t.Errorf("after a builder's round 1: scope %q, base %q (%v), want full from %s", scope, base, err, fullBase)
	}

	// Round 2 was an unknown reviewer's at round 1's head, round 3 a
	// builder's at a later head: round 4 reviews from round 2's head.
	record(2, IndependentUnknown, round1Head)
	writeReviewFile(t, dir, "b.go", "2")
	round3Head := commitReviewLease(t, dir, "round 3")
	record(3, IndependentNo, round3Head)
	if scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 4, round3Head); err != nil || scope != "delta" || base != round1Head {
		t.Errorf("after a builder's round 3: scope %q, base %q (%v), want a delta from round 2's head %s", scope, base, err, round1Head)
	}
}

// TestResolveScopeBaseAfterAMerge: a delta round after the session merged the
// target into the branch reviews the merge's resolution, not everything the
// target brought in. The base is the head itself for a merge git made on its own
// (the diff is empty), a tree - git's own merge of the tip's parents - for a tip
// resolved by hand (the diff is the hand edits), and the reviewed head for any
// other line. The scope is a delta in all of them.
func TestResolveScopeBaseAfterAMerge(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (dir string, st *store.Store, run func(args ...string) string, round1 string) {
		t.Helper()
		dir = newReviewLease(t, "main")
		run = func(args ...string) string {
			t.Helper()
			out, err := gitx.RunEnv(dir, reviewGitEnv, args...)
			if err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
			return out
		}
		run("config", "core.autocrlf", "false")
		run("switch", "-c", "work")
		writeReviewFile(t, dir, "work.go", "1")
		round1 = commitReviewLease(t, dir, "round 1")
		st = newReviewStore(t)
		if err := os.MkdirAll(gateRoundDir(st, "JIG-1", 1), 0o755); err != nil {
			t.Fatalf("mkdir round 1 dir: %v", err)
		}
		if err := writeReportYAML(gateRoundDir(st, "JIG-1", 1), GateReport{Round: 1, ReviewedSHA: map[string]string{"fixture-repo": round1}}); err != nil {
			t.Fatalf("write round 1 report.yaml: %v", err)
		}
		// The target moves, by files the branch never touched.
		run("switch", "main")
		writeReviewFile(t, dir, "target-a.go", "a")
		writeReviewFile(t, dir, "target-b.go", "b")
		moved := commitReviewLease(t, dir, "the target moves")
		run("update-ref", "refs/remotes/origin/main", moved)
		run("switch", "work")
		return dir, st, run, round1
	}
	changed := func(t *testing.T, dir, base, head string) []string {
		t.Helper()
		diff, err := computeScopeDiff(dir, base, head, nil)
		if err != nil {
			t.Fatalf("computeScopeDiff(%s): %v", base, err)
		}
		return diff.Changed
	}

	t.Run("a merge git made on its own", func(t *testing.T) {
		t.Parallel()
		dir, st, run, _ := setup(t)
		run("merge", "--no-ff", "--no-edit", "main")
		head := run("rev-parse", "HEAD")
		scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, head)
		if err != nil || scope != "delta" || base != head {
			t.Fatalf("resolveScopeBase = %q, %q, %v, want a delta from the head itself", scope, base, err)
		}
		if got := changed(t, dir, base, head); len(got) != 0 {
			t.Errorf("the scope diff = %v, want it empty", got)
		}
	})

	t.Run("the same head", func(t *testing.T) {
		t.Parallel()
		dir, st, run, round1 := setup(t)
		scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, run("rev-parse", "HEAD"))
		if err != nil || scope != "delta" || base != round1 {
			t.Fatalf("resolveScopeBase = %q, %q, %v, want a delta from the reviewed head", scope, base, err)
		}
	})

	t.Run("a merge resolved by hand", func(t *testing.T) {
		t.Parallel()
		dir, st, run, _ := setup(t)
		run("merge", "--no-ff", "--no-commit", "main")
		writeReviewFile(t, dir, "hand.go", "the hand edit")
		head := commitReviewLease(t, dir, "merge main, by hand")
		scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, head)
		if err != nil || scope != "delta" {
			t.Fatalf("resolveScopeBase = %q, %q, %v, want a delta", scope, base, err)
		}
		if kind := run("cat-file", "-t", base); kind != "tree" {
			t.Fatalf("the base %s is a %s, want a tree: git's own merge of the tip's parents", base, kind)
		}
		if got, want := changed(t, dir, base, head), []string{"hand.go"}; !reflect.DeepEqual(got, want) {
			t.Errorf("the scope diff = %v, want only the hand edit %v, none of the files the target brought in", got, want)
		}
	})

	t.Run("a commit of the session's own", func(t *testing.T) {
		t.Parallel()
		dir, st, run, _ := setup(t)
		run("merge", "--no-ff", "--no-edit", "main")
		merge := run("rev-parse", "HEAD")
		writeReviewFile(t, dir, "own.go", "2")
		head := commitReviewLease(t, dir, "a commit of its own")
		scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, head)
		if err != nil || scope != "delta" || base != merge {
			t.Fatalf("resolveScopeBase = %q, %q, %v, want a delta from the carried merge %s", scope, base, err, merge)
		}
		if got, want := changed(t, dir, base, head), []string{"own.go"}; !reflect.DeepEqual(got, want) {
			t.Errorf("the scope diff = %v, want only the commit's file %v", got, want)
		}
	})

	t.Run("a commit after a merge of a branch that is not the target", func(t *testing.T) {
		t.Parallel()
		dir, st, run, round1 := setup(t)
		run("switch", "-c", "other", round1)
		writeReviewFile(t, dir, "other.go", "x")
		commitReviewLease(t, dir, "other work")
		run("switch", "work")
		run("merge", "--no-ff", "--no-edit", "other")
		writeReviewFile(t, dir, "own.go", "2")
		head := commitReviewLease(t, dir, "a commit of its own")
		scope, base, err := resolveScopeBase(st, "JIG-1", dir, "fixture-repo", "main", 2, head)
		if err != nil || scope != "delta" || base != round1 {
			t.Fatalf("resolveScopeBase = %q, %q, %v, want a delta from the reviewed head: the reviewer sees more, never less", scope, base, err)
		}
	})
}

func TestResolveScopeBaseFullWhenPriorReviewedSHAIsNotAncestor(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

// oneOracleManifest is the manifest every reviewerGateSource.Round test
// uses unless it specifically wants to exercise the multi-oracle rule: a
// single oracle, with a root workspace (".") matching a real project's
// default manifest (main's default manifest has a root workspace
// `.`) so every file a test hands it derives a non-empty workspace unless
// the test builds a narrower manifest itself.
func oneOracleManifest() manifest.Manifest {
	return manifest.Manifest{
		Oracles:    map[string]string{"test": "go test ./..."},
		Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
	}
}

// TestPrepareReviewRequestAfterACleanMergeListsOnlyWhatFollowsIt: a merge of
// the target that carries over, then a commit of the branch's own, reviews the
// commit alone - not every file the target brought in.
func TestPrepareReviewRequestAfterACleanMergeListsOnlyWhatFollowsIt(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (in RoundInput, run func(args ...string) string) {
		t.Helper()
		dir := newReviewLease(t, "main")
		run = func(args ...string) string {
			t.Helper()
			out, err := gitx.RunEnv(dir, reviewGitEnv, args...)
			if err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
			return out
		}
		run("config", "core.autocrlf", "false")
		run("switch", "-c", "work")
		writeReviewFile(t, dir, "work.go", "1")
		round1 := commitReviewLease(t, dir, "round 1")
		st := newReviewStore(t)
		if err := os.MkdirAll(gateRoundDir(st, "JIG-1", 1), 0o755); err != nil {
			t.Fatalf("mkdir round 1 dir: %v", err)
		}
		if err := writeReportYAML(gateRoundDir(st, "JIG-1", 1), GateReport{Round: 1, ReviewedSHA: map[string]string{"fixture-repo": round1}}); err != nil {
			t.Fatalf("write round 1 report.yaml: %v", err)
		}
		// The target moves, by several files the branch never touched.
		run("switch", "main")
		for _, f := range []string{"target-a.go", "target-b.go", "target-c.go", "target-d.go"} {
			writeReviewFile(t, dir, f, f)
		}
		moved := commitReviewLease(t, dir, "the target moves")
		run("update-ref", "refs/remotes/origin/main", moved)
		run("switch", "work")
		in = RoundInput{
			Store: st, Ticket: "JIG-1", Round: 2, LeaseDir: dir,
			RepoName: "fixture-repo", Target: "main", Intent: Intent{Source: IntentSourceNone},
		}
		return in, run
	}
	prepare := func(t *testing.T, in RoundInput) RepoPrep {
		t.Helper()
		prep, err := PrepareReviewRequest(in)
		if err != nil {
			t.Fatalf("PrepareReviewRequest: %v", err)
		}
		if len(prep.Repos) != 1 {
			t.Fatalf("Repos = %d, want 1", len(prep.Repos))
		}
		return prep.Repos[0]
	}

	t.Run("a clean merge then a commit", func(t *testing.T) {
		t.Parallel()
		in, run := setup(t)
		run("merge", "--no-ff", "--no-edit", "main")
		merge := run("rev-parse", "HEAD")
		writeReviewFile(t, in.LeaseDir, "work.go", "2")
		writeReviewFile(t, in.LeaseDir, "target-a.go", "edited")
		commitReviewLease(t, in.LeaseDir, "a commit of its own")
		rp := prepare(t, in)
		if rp.Base != merge {
			t.Errorf("Base = %s, want the merge commit %s", rp.Base, merge)
		}
		if want := []string{"target-a.go", "work.go"}; !reflect.DeepEqual(rp.MustReview, want) {
			t.Errorf("MustReview = %v, want only the commit's files %v", rp.MustReview, want)
		}
	})

	t.Run("a merge resolved by hand then a commit", func(t *testing.T) {
		t.Parallel()
		in, run := setup(t)
		run("merge", "--no-ff", "--no-commit", "main")
		writeReviewFile(t, in.LeaseDir, "hand.go", "the hand edit")
		commitReviewLease(t, in.LeaseDir, "merge main, by hand")
		writeReviewFile(t, in.LeaseDir, "work.go", "2")
		commitReviewLease(t, in.LeaseDir, "a commit of its own")
		rp := prepare(t, in)
		if kind := run("cat-file", "-t", rp.Base); kind != "tree" {
			t.Errorf("Base %s is a %s, want a tree", rp.Base, kind)
		}
		if want := []string{"hand.go", "work.go"}; !reflect.DeepEqual(rp.MustReview, want) {
			t.Errorf("MustReview = %v, want the hand edit and the commit's file %v", rp.MustReview, want)
		}
	})
}
