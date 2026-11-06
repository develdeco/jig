package journal

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// TestAppendNamesTheBuildOnEveryLine: a line jig appends carries the build
// stamp of the jig that wrote it, as a jig field on the wire.
func TestAppendNamesTheBuildOnEveryLine(t *testing.T) {
	st := newTestStore(t)
	for _, event := range []string{"oracle", "verified", "gate-round", "pr", "publish-done", "review-notes", "recorded"} {
		if err := Append(st, "JIG-1", Line{Event: event}); err != nil {
			t.Fatalf("Append %s: %v", event, err)
		}
	}
	data, err := os.ReadFile(journalPath(st, "JIG-1"))
	if err != nil {
		t.Fatal(err)
	}
	rows := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(rows) != 7 {
		t.Fatalf("journal has %d lines, want 7", len(rows))
	}
	for _, row := range rows {
		var raw map[string]any
		if err := json.Unmarshal(row, &raw); err != nil {
			t.Fatal(err)
		}
		if got, _ := raw["jig"].(string); got == "" {
			t.Errorf("%s line has no jig field: %s", raw["event"], row)
		}
	}
}
