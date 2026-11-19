package journal

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnOldJournalWithSlotLinesStillReads: a journal written when runs waited
// for a suite slot holds slot-wait and slot-taken lines and an oracle line with
// a shared field; it reads without error and the oracle line keeps what it
// measured.
func TestAnOldJournalWithSlotLinesStillReads(t *testing.T) {
	st := newTestStore(t)
	if err := os.MkdirAll(st.TicketDir("JIG-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"ts":"2026-10-01T10:00:00Z","ticket":"JIG-1","slice":"a","event":"slot-wait","summary":"waiting for the suite slot"}
{"ts":"2026-10-01T10:00:05Z","ticket":"JIG-1","slice":"a","event":"slot-taken"}
{"ts":"2026-10-01T10:01:00Z","ticket":"JIG-1","slice":"a","event":"oracle","outcome":"pass","seconds":55,"cpu_ms":4200,"peak_bytes":314572800,"peak_basis":"sampled","shared":true}
`
	if err := os.WriteFile(filepath.Join(st.TicketDir("JIG-1"), "journal.ndjson"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(st, "JIG-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(lines) != 3 || lines[0].Event != "slot-wait" || lines[1].Event != "slot-taken" {
		t.Fatalf("lines = %+v, want the two slot lines and the oracle line", lines)
	}
	o := lines[2]
	if o.Event != EventOracle || o.Seconds != 55 || o.CPUMs != 4200 || o.PeakBytes != 300<<20 || o.PeakBasis != "sampled" {
		t.Errorf("oracle line = %+v, want its seconds and measurement", o)
	}
}

// TestReadKeepsALineOfAnEventThisVersionDoesNotKnow: a journal a newer version
// wrote holds a builder-probe line, whose event and keys this version does not
// know; it reads without error, keeps the line with its event, and the lines
// around it.
func TestReadKeepsALineOfAnEventThisVersionDoesNotKnow(t *testing.T) {
	st := newTestStore(t)
	if err := os.MkdirAll(st.TicketDir("JIG-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	newer := `{"ts":"2026-11-01T10:00:00Z","ticket":"JIG-1","slice":"a","event":"claim","session":"s"}
{"ts":"2026-11-01T10:01:00Z","ticket":"JIG-1","slice":"a","event":"builder-probe","session":"s","probe":{"seam":"x","file":"a.go","line":3,"mutation":"m","outcome":"caught","blob":"b"}}
{"ts":"2026-11-01T10:02:00Z","ticket":"JIG-1","slice":"a","event":"verified","commit":"abc"}
`
	if err := os.WriteFile(filepath.Join(st.TicketDir("JIG-1"), "journal.ndjson"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := Read(st, "JIG-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(lines) != 3 || lines[0].Event != "claim" || lines[1].Event != "builder-probe" || lines[2].Event != "verified" || lines[2].Commit != "abc" {
		t.Fatalf("lines = %+v, want the claim, the builder-probe and the verified line", lines)
	}
}
