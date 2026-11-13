package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestTriageKeepsANoteTheFloorRoutedAndTheNextRoundQueuesItsSlice is the path
// of a note a person takes up, through real jig subprocesses: a round whose
// only finding is a low fix is clean (the risk floor routes it to a note),
// `jig triage keep` takes the note, and the next `jig gate` queues the fix
// slice at once and opens no round.
func TestTriageKeepsANoteTheFloorRoutedAndTheNextGateQueuesItsSlice(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	ticket := fx.Ticket
	buildTicketOn(t, fx, env, true)

	// The scenario's round 1 report, with its findings replaced by one low
	// fix: same reviewed paths, so the report covers what the round names.
	data, err := os.ReadFile(filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	result["findings"] = []map[string]any{{
		"file": "beta/beta.go", "line": 6, "title": "Greet does not trim surrounding whitespace",
		"detail": "A padded name renders padded.", "action": "fix", "risk": "low",
		"risk_rationale": "cosmetic only", "oracle": "test",
	}}
	round1 := filepath.Join(t.TempDir(), "round-1.json")
	writeJSON(t, round1, result)

	if open := runJig(t, env, fx.StoreDir, "gate", ticket); open.Code != 0 {
		t.Fatalf("gate (round 1) exit = %d, want 0\n%s%s", open.Code, open.Stdout, open.Stderr)
	}
	done := runJig(t, env, fx.StoreDir, "review", "done", ticket, "--result", round1)
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") || !strings.Contains(done.Stdout, "r1-f1,noted,") {
		t.Fatalf("review done (round 1) exit = %d, want 0, a clean verdict and r1-f1 noted\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}

	keep := runJig(t, env, fx.StoreDir, "triage", ticket, "r1-f1", "keep", "--reason", "Worth doing now.")
	if keep.Code != 0 || !strings.Contains(keep.Stdout, "decision: keep") {
		t.Fatalf("triage keep exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", keep.Code, keep.Stdout, keep.Stderr)
	}
	status := runJig(t, env, fx.StoreDir, "status", ticket)
	if status.Code != 0 || !strings.Contains(status.Stdout, "decided_notes[1]") {
		t.Fatalf("status exit = %d, want decided_notes listing the kept note\n%s", status.Code, status.Stdout)
	}

	queue := runJig(t, env, fx.StoreDir, "gate", ticket)
	if queue.Code != 0 || !strings.Contains(queue.Stdout, "fix_slices") || !strings.Contains(queue.Stdout, "fix-2-q-beta-test") {
		t.Fatalf("gate after the keep exit = %d, want 0 and the slice fix-2-q-beta-test in fix_slices\nstdout:\n%s\nstderr:\n%s", queue.Code, queue.Stdout, queue.Stderr)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2")); err == nil {
		t.Fatal("the gate that queued the keep recorded a round")
	}
	if again := runJig(t, env, fx.StoreDir, "gate", ticket); again.Code == 0 || !strings.Contains(again.Stdout, "GATE_NOT_GREEN") {
		t.Fatalf("gate with the kept slice unbuilt exit = %d, want GATE_NOT_GREEN\n%s", again.Code, again.Stdout)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSlice(slices, "fix-2-q-beta-test") {
		t.Fatalf("slices = %+v, want the fix slice of the kept note", slices)
	}
}

// writeJSON writes v as JSON to path.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
