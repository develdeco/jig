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

// briefHashRig is a store with one ticket whose brief.md holds brief, and the
// hash of whatever the ticket folder holds now.
func briefHashRig(t *testing.T, brief string) (hash func() string, writeIllustration func(string), removeIllustration func()) {
	t.Helper()
	st := &Store{Root: t.TempDir()}
	const id = "JIG-1"
	if err := st.CreateTicketRecord(id, Ticket{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	dir := st.TicketDir(id)
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	hash = func() string {
		t.Helper()
		sum, err := st.BriefSHA256(id)
		if err != nil {
			t.Fatalf("BriefSHA256: %v", err)
		}
		return sum
	}
	writeIllustration = func(html string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, IllustrationFile), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeIllustration = func() {
		t.Helper()
		if err := os.Remove(filepath.Join(dir, IllustrationFile)); err != nil {
			t.Fatal(err)
		}
	}
	return hash, writeIllustration, removeIllustration
}

// TestBriefSHA256WithoutAnIllustrationIsTheFilesHash: a brief with no
// illustration hashes to the sha256 of brief.md and nothing else, as it did
// before briefs had illustrations, so an approval recorded then stays valid.
// The digest below is that of these exact bytes, pinned as a literal so a
// change to the rule fails here whatever the code computes.
func TestBriefSHA256WithoutAnIllustrationIsTheFilesHash(t *testing.T) {
	hash, _, _ := briefHashRig(t, "hello\n")
	const sha256OfHelloNewline = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if got := hash(); got != sha256OfHelloNewline {
		t.Fatalf("BriefSHA256 of a brief with no illustration = %s, want the sha256 of brief.md, %s", got, sha256OfHelloNewline)
	}
}

// TestBriefSHA256CoversTheIllustration: adding an illustration, editing it and
// removing it each change the hash, and removing it gives the brief's own hash
// back.
func TestBriefSHA256CoversTheIllustration(t *testing.T) {
	hash, write, remove := briefHashRig(t, "# JIG-1\n\n## Goal\n\nShow it.\n")
	plain := hash()

	write("<p>one</p>")
	withOne := hash()
	if withOne == plain {
		t.Fatal("adding an illustration left the hash as it was")
	}
	if again := hash(); again != withOne {
		t.Fatalf("the same files hashed twice give %s and %s", withOne, again)
	}

	write("<p>two</p>")
	withTwo := hash()
	if withTwo == withOne || withTwo == plain {
		t.Fatalf("editing the illustration gave %s, which is the hash from before the edit (%s) or without it (%s)", withTwo, withOne, plain)
	}

	write("")
	if empty := hash(); empty == plain {
		t.Fatal("an empty illustration.html hashes as no illustration: the file's presence is part of the brief")
	}

	remove()
	if back := hash(); back != plain {
		t.Fatalf("removing the illustration gave %s, want the brief's own hash %s", back, plain)
	}
}

// TestBriefSHA256KeepsTheTwoFilesApart: the same bytes split differently
// between brief.md and illustration.html hash differently.
func TestBriefSHA256KeepsTheTwoFilesApart(t *testing.T) {
	first, write, _ := briefHashRig(t, "abcd")
	write("efgh")
	a := first()

	second, write2, _ := briefHashRig(t, "abcdef")
	write2("gh")
	if b := second(); a == b {
		t.Fatalf("moving bytes from the illustration to the brief left the hash as %s", a)
	}
}

// TestBriefSHA256RefusesAnIllustrationItCannotRead: an illustration.html that
// is not a readable file is an error, not a brief with none.
func TestBriefSHA256RefusesAnIllustrationItCannotRead(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	const id = "JIG-1"
	if err := st.CreateTicketRecord(id, Ticket{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	dir := st.TicketDir(id)
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, IllustrationFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if sum, err := st.BriefSHA256(id); err == nil {
		t.Fatalf("BriefSHA256 with a directory for an illustration = %q, nil; want an error", sum)
	}
}
