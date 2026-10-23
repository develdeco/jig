package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestApprovalRoundTrip: an approval is read back as written from
// tickets/<id>/approval.yaml, an absent one reads as none, a second write
// replaces the first, and the brief's hash tracks the brief on disk.
func TestApprovalRoundTrip(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	const id = "JIG-1"
	if err := st.CreateTicketRecord(id, Ticket{Title: "x"}); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := st.ReadApproval(id); err != nil || ok {
		t.Fatalf("ReadApproval before any write = %v, %v; want none", ok, err)
	}

	brief := filepath.Join(st.TicketDir(id), "brief.md")
	if err := os.WriteFile(brief, []byte("# JIG-1\n\n## Context\n\nhi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := []byte("# JIG-1\n\n## Context\n\nhi\n")
	sum, err := st.BriefSHA256(id)
	if raw := sha256.Sum256(body); err != nil || sum != hex.EncodeToString(raw[:]) {
		t.Fatalf("BriefSHA256 = %q, %v; want the sha256 of the brief's bytes", sum, err)
	}

	want := Approval{By: "Pat Example", Email: "pat@example.invalid", At: "2026-10-23T08:00:00Z", BriefSHA256: sum, Provenance: "person"}
	if err := st.WriteApproval(id, want); err != nil {
		t.Fatalf("WriteApproval: %v", err)
	}
	got, ok, err := st.ReadApproval(id)
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadApproval = %+v, %v, %v; want %+v", got, ok, err, want)
	}
	raw, _ := os.ReadFile(filepath.Join(st.TicketDir(id), "approval.yaml"))
	for _, key := range []string{"by:", "email:", "at:", "brief_sha256:", "provenance:"} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("approval.yaml lacks the key %q:\n%s", key, raw)
		}
	}

	if err := os.WriteFile(brief, []byte("# JIG-1\n\n## Context\n\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after, err := st.BriefSHA256(id); err != nil || after == sum {
		t.Fatalf("BriefSHA256 after an edit = %q, %v; want a different hash", after, err)
	}

	second := Approval{By: "Sam Example", At: "2026-10-24T08:00:00Z", BriefSHA256: "abc", Provenance: "agent"}
	if err := st.WriteApproval(id, second); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.ReadApproval(id); !reflect.DeepEqual(got, second) {
		t.Errorf("after a second write the approval = %+v, want %+v", got, second)
	}
}

// TestBriefSHA256NeedsABrief: nothing to approve is an error, not a hash of
// nothing.
func TestBriefSHA256NeedsABrief(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if sum, err := st.BriefSHA256("JIG-1"); err == nil {
		t.Fatalf("BriefSHA256 of a ticket with no brief = %q, nil; want an error", sum)
	}
}
