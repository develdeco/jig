package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestApprovalRecordsEachSectionAndTheIllustration: an approval holds the
// heading and hash of each section it covered and the illustration's hash,
// they round-trip, an approval with none leaves the keys out of the file, and
// a file written before they existed reads with none.
func TestApprovalRecordsEachSectionAndTheIllustration(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	const id = "JIG-1"
	if err := st.CreateTicketRecord(id, Ticket{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	dir := st.TicketDir(id)
	brief := "# JIG-1\n\n## Context\n\nhi\n\n## Goal\n\nthere\n"
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}

	sections, ill, err := st.BriefRecord(id)
	if err != nil || ill != "" {
		t.Fatalf("BriefRecord without an illustration = %v, %q, %v; want its sections and no hash", sections, ill, err)
	}
	var want []ApprovedSection
	for _, sec := range BriefSections([]byte(brief)) {
		want = append(want, ApprovedSection{Heading: sec.Heading, Hash: sec.Hash})
	}
	if len(want) != 2 || !reflect.DeepEqual(sections, want) {
		t.Fatalf("BriefRecord sections = %+v, want %+v", sections, want)
	}

	html := []byte("<p>picture</p>")
	if err := os.WriteFile(filepath.Join(dir, IllustrationFile), html, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ill, err = st.BriefRecord(id); err != nil || ill != sha256Hex(html) {
		t.Fatalf("BriefRecord illustration = %q, %v; want %q", ill, err, sha256Hex(html))
	}

	a := Approval{By: "Pat Example", At: "2026-10-23T08:00:00Z", BriefSHA256: "abc", Provenance: "person", Sections: sections, IllustrationSHA256: ill}
	if err := st.WriteApproval(id, a); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.ReadApproval(id)
	if err != nil || !ok || !reflect.DeepEqual(got, a) {
		t.Fatalf("ReadApproval = %+v, %v, %v; want %+v", got, ok, err, a)
	}

	bare := Approval{By: "Pat Example", At: "2026-10-23T08:00:00Z", BriefSHA256: "abc"}
	if err := st.WriteApproval(id, bare); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "approval.yaml"))
	for _, key := range []string{"sections", "illustration_sha256"} {
		if strings.Contains(string(raw), key) {
			t.Errorf("an approval with none writes %q:\n%s", key, raw)
		}
	}

	older := "by: Pat Example\nat: \"2026-10-23T08:00:00Z\"\nbrief_sha256: abc\nprovenance: person\n"
	if err := os.WriteFile(filepath.Join(dir, "approval.yaml"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok, err = st.ReadApproval(id)
	if err != nil || !ok || len(got.Sections) != 0 || got.IllustrationSHA256 != "" {
		t.Fatalf("an older approval reads %+v, %v, %v; want none of the record", got, ok, err)
	}
}
