package journal

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime/debug"
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

// TestFormatStamp pins the stamp's shape: version, short revision, -dirty.
func TestFormatStamp(t *testing.T) {
	t.Parallel()
	mk := func(version string, settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: version}, Settings: settings}
	}
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "232c0161234567890"}
	mod := debug.BuildSetting{Key: "vcs.modified", Value: "true"}
	clean := debug.BuildSetting{Key: "vcs.modified", Value: "false"}
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"no build info", nil, "unknown"},
		{"nothing stamped", mk("(devel)"), "unknown"},
		{"clean", mk("v0.1.3", rev, clean), "v0.1.3+232c016"},
		{"dirty by vcs", mk("v0.1.3", rev, mod), "v0.1.3+232c016-dirty"},
		{"dirty by version", mk("v0.1.3-0.2026-232c016+dirty", rev), "v0.1.3-0.2026-232c016+232c016-dirty"},
		{"devel with revision", mk("(devel)", rev), "232c016"},
	} {
		if got := FormatStamp(tc.info); got != tc.want {
			t.Errorf("%s: FormatStamp = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestStampIsStable: the stamp is computed once and never empty.
func TestStampIsStable(t *testing.T) {
	t.Parallel()
	if Stamp() == "" || Stamp() != Stamp() {
		t.Fatalf("Stamp() = %q, want a stable non-empty string", Stamp())
	}
}
