//go:build windows

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// longNamedDir creates a directory with a name too long for 8.3 under a
// fresh temp dir and returns its long spelling and its 8.3 short spelling,
// skipping t on a volume that makes no short names.
func longNamedDir(t *testing.T) (long, short string) {
	t.Helper()
	long = filepath.Join(longPath(t.TempDir()), "a directory with a long name")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := syscall.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, syscall.MAX_PATH)
	n, err := syscall.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		t.Fatalf("GetShortPathName(%s): %d, %v", long, n, err)
	}
	short = syscall.UTF16ToString(buf[:n])
	if short == long {
		t.Skipf("the volume holding %s makes no 8.3 short names", long)
	}
	return long, short
}

func TestLongPath(t *testing.T) {
	long, short := longNamedDir(t)
	if got := longPath(short); got != long {
		t.Errorf("longPath(%s) = %s, want %s", short, got, long)
	}
	missing := filepath.Join("not-yet", "a.attempt-1.result.json")
	if got, want := longPath(filepath.Join(short, missing)), filepath.Join(long, missing); got != want {
		t.Errorf("longPath of a path whose tail does not exist yet = %s, want %s", got, want)
	}
	if got := longPath(long); got != long {
		t.Errorf("longPath(%s) = %s, want it unchanged", long, got)
	}
}

// TestHeadlessRunSpellsShortPathsLong pins what the CLI's rule matching
// needs: a dispatch whose paths come spelled through an 8.3 short name (a
// runner's RUNNER~1 temp dir, say) reaches the session spelled
// long - its working directory, the prompt's result path, and the edit
// rules - since the CLI denies every edit to a short spelling.
func TestHeadlessRunSpellsShortPathsLong(t *testing.T) {
	stubDir := buildBinary(t, filepath.Join("testdata", "fixture", "claudestub"), "claude")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "claude.log")
	t.Setenv("CLAUDE_STUB_LOG", logFile)
	t.Setenv("CLAUDE_STUB_STDOUT", strings.TrimSuffix(cliResultJSON(t, false, "done", nil), "\n"))
	for _, k := range []string{"CLAUDE_STUB_WRITE_PATH", "CLAUDE_STUB_STDERR", "CLAUDE_STUB_EXIT"} {
		t.Setenv(k, "")
	}

	long, short := longNamedDir(t)
	for _, dir := range []string{"lease", "work"} {
		if err := os.Mkdir(filepath.Join(long, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	slice := filepath.Join(short, "work", "a.attempt-1.slice.json")
	if err := os.WriteFile(slice, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(short, "work", "a.attempt-1.result.json")
	d := Dispatch{
		Ticket:     "T-1",
		Slice:      "a",
		Attempt:    1,
		Worktree:   filepath.Join(short, "lease"),
		SliceJSON:  slice,
		ResultJSON: result,
		Model:      "claude-haiku-4-5",
		Prompt:     "Read slice.json at " + slice + ". When finished write result.json at " + result + " with exactly one JSON object.",
		Screen:     true,
	}
	b := &headlessBackend{goos: "windows", screenBinary: builtJigBinary(t)}
	if err := b.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read claude stub log: %v", err)
	}
	var call struct {
		Argv []string `json:"argv"`
		Cwd  string   `json:"cwd"`
	}
	if err := json.Unmarshal(data, &call); err != nil {
		t.Fatalf("parse claude stub log: %v\n%s", err, data)
	}
	if want := filepath.Join(long, "lease"); call.Cwd != want {
		t.Errorf("claude ran in %s, want the lease spelled long, %s", call.Cwd, want)
	}
	prompt := call.Argv[len(call.Argv)-1]
	wantPrompt := "Read slice.json at " + filepath.Join(long, "work", "a.attempt-1.slice.json") +
		". When finished write result.json at " + filepath.Join(long, "work", "a.attempt-1.result.json") +
		" with exactly one JSON object."
	if prompt != wantPrompt {
		t.Errorf("prompt =\n%s\nwant\n%s", prompt, wantPrompt)
	}
	var settings string
	for i, a := range call.Argv {
		if a == "--settings" && i+1 < len(call.Argv) {
			settings = call.Argv[i+1]
		}
	}
	if strings.Contains(settings, filepath.Base(short)) {
		t.Errorf("settings still name the short spelling %s:\n%s", short, settings)
	}
	for _, rule := range []string{
		"Edit(" + rulePath("windows", filepath.Join(long, "lease")) + "/**)",
		"Edit(" + rulePath("windows", filepath.Join(long, "work", "a.attempt-1.result.json")) + ")",
	} {
		if !strings.Contains(settings, rule) {
			t.Errorf("settings grant no %s:\n%s", rule, settings)
		}
	}
}
