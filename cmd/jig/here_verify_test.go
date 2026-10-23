package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
)

// hereVerifyCtx is what `jig here` hands its sections for a located ticket in
// the rig's clone.
func hereVerifyCtx(r *verifyRig, slice string) hereCtx {
	return hereCtx{
		e:       r.e,
		Store:   r.st,
		JigHome: r.fx.Home,
		Where:   locate.Where{Ticket: r.fx.Ticket, Slice: slice, Repo: "fixture-repo", RepoDir: r.fx.RepoDir},
	}
}

// verifySection is the section `jig verify` registers for `jig here`.
func verifySection(t *testing.T) hereSection {
	t.Helper()
	for _, s := range hereSections() {
		if s.Name == "verify" {
			if s.Blocks == nil || s.JSON == nil || s.Order != 320 {
				t.Fatalf("the verify section is not registered as expected: %+v", s)
			}
			return s
		}
	}
	t.Fatal("no verify section is registered")
	return hereSection{}
}

// TestHereVerifySectionShowsHowHeadStandsForTheLocatedSlice: the section says
// nothing where nothing was located or verified, and otherwise the state of
// the located slice's verification at the repo's HEAD, with what `jig here`
// itself cannot show: a failure, its summary and its time.
func TestHereVerifySectionShowsHowHeadStandsForTheLocatedSlice(t *testing.T) {
	t.Parallel()
	section := verifySection(t)
	r := newVerifyRig(t)
	sha := r.fixClamp()

	// Nothing located, no store, or nothing verified yet: no blocks, no JSON.
	noSlice := hereVerifyCtx(r, "")
	noRepo := hereVerifyCtx(r, "a")
	noRepo.Where.RepoDir = ""
	for name, c := range map[string]hereCtx{
		"no store":                       {e: r.e},
		"no ticket":                      {e: r.e, Store: r.st},
		"no slice located":               noSlice,
		"no repo located":                noRepo,
		"a slice never verified at HEAD": hereVerifyCtx(r, "a"),
	} {
		blocks, err := section.Blocks(c)
		if err != nil || len(blocks) != 0 {
			t.Errorf("%s: Blocks = %v, %v, want none", name, blocks, err)
		}
		if v, err := section.JSON(c); err != nil || v != nil {
			t.Errorf("%s: JSON = %v, %v, want none", name, v, err)
		}
	}

	// A worker has started on HEAD.
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	blocks, err := section.Blocks(hereVerifyCtx(r, "a"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(blocks, "\n")
	for _, want := range []string{"verification:", "slice: a", "commit: " + shortSHA(sha), "state: pending", "attempt: 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("section lacks %q:\n%s", want, text)
		}
	}
	// Another slice at the same HEAD is not that verification.
	if blocks, err := section.Blocks(hereVerifyCtx(r, "b")); err != nil || len(blocks) != 0 {
		t.Errorf("slice b at a's HEAD: Blocks = %v, %v, want none", blocks, err)
	}

	// The worker's failure, with its time and summary.
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "fail", Commit: sha, Attempt: 1, Seconds: 9, Summary: "FAIL TestClamp"}); err != nil {
		t.Fatal(err)
	}
	v, err := section.JSON(hereVerifyCtx(r, "a"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Slice, Commit, State, Summary string
		Attempt, Seconds              int
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("section JSON %s: %v", data, err)
	}
	if got.Slice != "a" || got.Commit != sha || got.State != "fail" || got.Summary != "FAIL TestClamp" || got.Attempt != 1 || got.Seconds != 9 {
		t.Errorf("section JSON = %s, want a's failure at %s", data, sha)
	}
	blocks, err = section.Blocks(hereVerifyCtx(r, "a"))
	if err != nil || !strings.Contains(strings.Join(blocks, "\n"), "summary: FAIL TestClamp") {
		t.Errorf("section after the failure: %v, %v, want the summary", blocks, err)
	}
}

// TestHereShowsTheVerifySection: through the verb, `jig here --slice` in a
// clone prints the section after its own pending_verifications table.
func TestHereShowsTheVerifySection(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	sha := r.fixClamp()
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: sha, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	out, code := r.run("here", "--slice", "a")
	if code != 0 {
		t.Fatalf("jig here exit = %d\n%s", code, out)
	}
	for _, want := range []string{"pending_verifications[1]", "verification:", "state: pending", "commit: " + shortSHA(sha)} {
		if !strings.Contains(out, want) {
			t.Errorf("jig here lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "pending_verifications") > strings.Index(out, "verification:") {
		t.Errorf("the verify section should follow here's own tables:\n%s", out)
	}
}
