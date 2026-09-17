package tracker_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/tracker"
)

// writeStubScript writes a platform-shell script at dir/name that copies
// its stdin verbatim to logFile, then prints stdout to canned JSON, and
// returns the path TrackerCmd should point to.
func writeStubScript(t *testing.T, dir, logFile, stdout string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "trackercmd.cmd")
		content := "@echo off\r\n" +
			"findstr \"^\" > \"" + logFile + "\"\r\n" +
			"echo " + stdout + "\r\n"
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write stub script: %v", err)
		}
		return path
	}
	path := filepath.Join(dir, "trackercmd.sh")
	content := "#!/bin/sh\n" +
		"cat > \"" + logFile + "\"\n" +
		"echo '" + stdout + "'\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write stub script: %v", err)
	}
	return path
}

func TestCommandAdapter(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "stdin.log")
	script := writeStubScript(t, dir, logFile, `{"id": "CMD-1"}`)

	cfg := project.Config{
		SchemaVersion: 1,
		Name:          "fixture",
		TicketFormat:  "CMD-{n}",
		Tracker:       "command",
		TrackerCmd:    script,
	}
	st, cfg := newTestStore(t, cfg)

	a, err := tracker.New(cfg, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	id, err := a.Mint(tracker.Draft{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "CMD-1" {
		t.Fatalf("id = %q, want CMD-1", id)
	}

	logged, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	var msg struct {
		Action  string `json:"action"`
		Ticket  string `json:"ticket"`
		Payload struct {
			Title string `json:"Title"`
			Body  string `json:"Body"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(bytesTrim(logged), &msg); err != nil {
		t.Fatalf("parse logged stdin %q: %v", logged, err)
	}
	if msg.Action != "mint" {
		t.Fatalf("logged action = %q, want mint", msg.Action)
	}
	if msg.Payload.Title != "T" || msg.Payload.Body != "B" {
		t.Fatalf("logged payload = %+v", msg.Payload)
	}
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
