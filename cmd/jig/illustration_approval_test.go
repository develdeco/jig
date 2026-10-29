package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestIllustrationEditWithdrawsApproval: an approval covers the illustration
// as it covers the brief, so editing, adding or removing illustration.html
// leaves the brief as changed since it was approved, and an unattended claim
// is refused until the brief is approved again. A brief with no illustration
// is hashed as it always was, so an approval recorded before there were
// illustrations is still current. The hash is internal/store's
// (TestBriefSHA256CoversTheIllustration); this is the check that `jig
// validate` records it, `jig status` compares it and `jig claim` refuses on it.
func TestIllustrationEditWithdrawsApproval(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	briefPath := filepath.Join(st.TicketDir(T), "brief.md")
	illPath := filepath.Join(st.TicketDir(T), store.IllustrationFile)
	body, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	plain := sha256.Sum256(body)
	plainHex := hex.EncodeToString(plain[:])
	const illustration = "<!doctype html><h1>The picture</h1><p>a mock of the clamp</p>"

	approvalState := func() string {
		t.Helper()
		out, code := runMain(t, e, "", "status", T)
		if code != 0 {
			t.Fatalf("jig status: exit %d\n%s", code, out)
		}
		for _, state := range []string{"none", "current", "changed"} {
			if strings.Contains(out, "  approval: "+state+"\n") {
				return state
			}
		}
		t.Fatalf("jig status names no approval state:\n%s", out)
		return ""
	}
	validate := func() {
		t.Helper()
		if out, code := runMain(t, e, "", "validate", T); code != 0 || !strings.Contains(out, "  result: recorded\n") {
			t.Fatalf("validate: exit %d, want the approval recorded\n%s", code, out)
		}
	}
	claimRefused := func(label string) {
		t.Helper()
		out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--unattended", "--session", "s-1")
		if code != 1 || !strings.Contains(out, "code: CLAIM_NOT_APPROVED") || !strings.Contains(out, "changed after it was approved") {
			t.Fatalf("claim %s: exit %d, want 1 with CLAIM_NOT_APPROVED\n%s", label, code, out)
		}
	}
	write := func(html string) {
		t.Helper()
		if err := os.WriteFile(illPath, []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// With no illustration the approval is of the brief's own sha256: the value
	// an older jig wrote, so the approvals already recorded stay valid.
	validate()
	if a, ok, err := st.ReadApproval(T); err != nil || !ok || a.BriefSHA256 != plainHex {
		t.Fatalf("approval = %+v, %v, %v, want the sha256 of brief.md, %s", a, ok, err, plainHex)
	}
	if got := approvalState(); got != "current" {
		t.Fatalf("after validate the approval is %s, want current", got)
	}
	if err := st.WriteApproval(T, store.Approval{By: "Older Jig", At: "2026-10-01T00:00:00Z", BriefSHA256: plainHex, Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
	if got := approvalState(); got != "current" {
		t.Fatalf("an approval recorded before illustrations is %s, want current", got)
	}

	// An illustration added after the approval withdraws it.
	write(illustration)
	if got := approvalState(); got != "changed" {
		t.Fatalf("after an illustration was added the approval is %s, want changed", got)
	}
	claimRefused("after the illustration was added")

	// Approving again covers the illustration; editing it withdraws that.
	validate()
	if approved, _, _ := st.ReadApproval(T); approved.BriefSHA256 == plainHex {
		t.Fatal("the approval of a brief with an illustration is the brief's own hash: it does not cover the illustration")
	}
	if got := approvalState(); got != "current" {
		t.Fatalf("after validating with an illustration the approval is %s, want current", got)
	}
	write(illustration + "<p>redrawn</p>")
	if got := approvalState(); got != "changed" {
		t.Fatalf("after the illustration was edited the approval is %s, want changed", got)
	}
	claimRefused("after the illustration was edited")

	// Removing it changes the brief again, and the next approval is the
	// brief's own hash once more.
	validate()
	if err := os.Remove(illPath); err != nil {
		t.Fatal(err)
	}
	if got := approvalState(); got != "changed" {
		t.Fatalf("after the illustration was removed the approval is %s, want changed", got)
	}
	validate()
	if a, _, _ := st.ReadApproval(T); a.BriefSHA256 != plainHex {
		t.Errorf("approval after the illustration was removed = %s, want the sha256 of brief.md, %s", a.BriefSHA256, plainHex)
	}
}
