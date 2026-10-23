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

// TestHereVerifySectionShowsPendingAndHeadsVerification: the section is
// registered, says nothing where nothing was located, lists the verifications
// still pending, and shows how HEAD stands for the located slice.
func TestHereVerifySectionShowsPendingAndHeadsVerification(t *testing.T) {
	t.Parallel()
	var section hereSection
	for _, s := range hereSections() {
		if s.Name == "verify" {
			section = s
		}
	}
	if section.Blocks == nil || section.JSON == nil || section.Order != 320 {
		t.Fatalf("the verify section is not registered as expected: %+v", section)
	}

	r := newVerifyRig(t)
	sha := r.fixClamp()

	// Nothing located, no store, or nothing to say: no blocks.
	for name, c := range map[string]hereCtx{
		"no store":                      {e: r.e},
		"no ticket":                     {e: r.e, Store: r.st},
		"a ticket with no verification": hereVerifyCtx(r, ""),
	} {
		blocks, err := section.Blocks(c)
		if err != nil || len(blocks) != 0 {
			t.Errorf("%s: Blocks = %v, %v, want none", name, blocks, err)
		}
		if v, err := section.JSON(c); err != nil || v != nil {
			t.Errorf("%s: JSON = %v, %v, want none", name, v, err)
		}
	}

	// A worker is verifying the commit.
	if out, code := r.run("verify", "JIG-1", sha, "--wait"); code != 0 {
		t.Fatalf("setup: verify --wait exit %d\n%s", code, out)
	}
	pendingRun := r.commit("b", "b: start", map[string]string{"b.txt": "1"})
	if err := appendPending(r, "b", pendingRun); err != nil {
		t.Fatal(err)
	}

	blocks, err := section.Blocks(hereVerifyCtx(r, "b"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(blocks, "\n")
	for _, want := range []string{"verifying[1]{slice,commit,attempt,since}:", "b," + shortSHA(pendingRun) + ",1,", "verification:", "slice: b", "state: pending"} {
		if !strings.Contains(text, want) {
			t.Errorf("section lacks %q:\n%s", want, text)
		}
	}
	// HEAD is b's commit, still pending; a's verified commit is not HEAD.
	blocks, err = section.Blocks(hereVerifyCtx(r, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(blocks, "\n"); strings.Contains(text, "verification:") || !strings.Contains(text, "verifying[1]") {
		t.Errorf("slice a at a HEAD it was not verified at: %s", text)
	}

	v, err := section.JSON(hereVerifyCtx(r, "b"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Pending []struct{ Slice, Commit string }
		Head    struct{ Slice, Commit, State string }
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("section JSON %s: %v", data, err)
	}
	if len(got.Pending) != 1 || got.Pending[0].Commit != pendingRun || got.Head.State != "pending" || got.Head.Slice != "b" || got.Head.Commit != pendingRun {
		t.Errorf("section JSON = %s", data)
	}
}

// appendPending journals a verify-pending line for slice at sha: a worker
// that has started.
func appendPending(r *verifyRig, slice, sha string) error {
	return journal.Append(r.st, r.fx.Ticket, journal.Line{Slice: slice, Event: journal.EventVerifyPending, Commit: sha, Attempt: 1})
}
