package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

func TestWSLPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`C:\Users\x`, "/mnt/c/Users/x"},
		{`D:\work\repos\jig`, "/mnt/d/work/repos/jig"},
		{`c:\already\lower`, "/mnt/c/already/lower"},
		{"/mnt/c/already/posix", "/mnt/c/already/posix"},
	}
	for _, c := range cases {
		if got := wslPath(c.in); got != c.want {
			t.Errorf("wslPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHerdrCommand covers herdrCommand's off-Windows (direct exec) and
// Windows (WSL) branches as a pure function, independent of the host OS
// actually running the test.
func TestHerdrCommand(t *testing.T) {
	args := []string{"workspace", "create", "--cwd", "/mnt/c/work", "--label", "l"}

	t.Run("off windows execs herdr directly", func(t *testing.T) {
		for _, goos := range []string{"linux", "darwin"} {
			name, argv := herdrCommand(goos, "", args)
			if name != "herdr" {
				t.Errorf("goos=%s: name = %q, want herdr", goos, name)
			}
			if !equalArgs(argv, args) {
				t.Errorf("goos=%s: argv = %v, want %v unchanged", goos, argv, args)
			}
		}
	})

	t.Run("windows default distro", func(t *testing.T) {
		name, argv := herdrCommand("windows", "", args)
		if name != "wsl" {
			t.Fatalf("name = %q, want wsl", name)
		}
		want := []string{"-e", "bash", "-lc", "herdr " + strings.Join(quoteHerdrArgs(args), " ")}
		if !equalArgs(argv, want) {
			t.Errorf("argv = %v, want %v", argv, want)
		}
	})

	t.Run("windows JIG_WSL_DISTRO", func(t *testing.T) {
		name, argv := herdrCommand("windows", "Ubuntu-24.04", args)
		if name != "wsl" {
			t.Fatalf("name = %q, want wsl", name)
		}
		want := []string{"-d", "Ubuntu-24.04", "-e", "bash", "-lc", "herdr " + strings.Join(quoteHerdrArgs(args), " ")}
		if !equalArgs(argv, want) {
			t.Errorf("argv = %v, want %v", argv, want)
		}
	})
}

// TestQuoteHerdrArgsEscaping checks quoteHerdrArgs against a literal
// expected command line for the shapes that actually break naive quoting:
// an arg with a space, one with an embedded single quote, and an empty
// arg.
func TestQuoteHerdrArgsEscaping(t *testing.T) {
	got := quoteHerdrArgs([]string{"hello world", "it's", ""})
	want := []string{`'hello world'`, `'it'\''s'`, `''`}
	if !equalArgs(got, want) {
		t.Fatalf("quoteHerdrArgs = %v, want %v", got, want)
	}
	joined := strings.Join(got, " ")
	wantJoined := `'hello world' 'it'\''s' ''`
	if joined != wantJoined {
		t.Fatalf("joined = %q, want %q", joined, wantJoined)
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildHerdrStub compiles testdata/fixture/herdrstub into a binary named
// herdr (or herdr.exe on Windows) inside a fresh directory, returning that
// directory so it can be prepended to PATH. Mirrors tracker's buildGhStub.
func buildHerdrStub(t *testing.T) string {
	t.Helper()
	src := filepath.Join(fixture.RepoRoot(t), "testdata", "fixture", "herdrstub")

	dir := t.TempDir()
	name := "herdr"
	if runtime.GOOS == "windows" {
		name = "herdr.exe"
	}
	out := filepath.Join(dir, name)

	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, src)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build herdrstub: %v\n%s", err, output)
	}
	return dir
}

// TestHerdrBackendRunDirectExec drives herdrBackend.Run against the herdr
// stub through the off-Windows (direct-exec) branch, forced via the
// injectable goos field so this test runs the same way on every host OS -
// including on Windows itself, where it exercises the non-WSL path rather
// than the (unexecutable in CI) real wsl.exe.
func TestHerdrBackendRunDirectExec(t *testing.T) {
	stubDir := buildHerdrStub(t)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logFile := filepath.Join(t.TempDir(), "herdr.log")
	t.Setenv("HERDR_STUB_LOG", logFile)

	worktree := t.TempDir()
	resultJSON := filepath.Join(t.TempDir(), "result.json")

	b := &herdrBackend{goos: "linux"}
	d := Dispatch{
		Ticket:     "JIG-1",
		Slice:      "s1",
		Attempt:    1,
		Worktree:   worktree,
		ResultJSON: resultJSON,
		Prompt:     "do the thing",
	}
	if err := b.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(resultJSON)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("parse result.json: %v", err)
	}
	if res["outcome"] != "green" {
		t.Errorf("outcome = %v, want green", res["outcome"])
	}

	// Every call's full argv, exactly: the pane id and workspace id are
	// taken from the stub's own "workspace create" response ("pane-1" /
	// "ws-1" - see testdata/fixture/herdrstub), not just spot-checked, and
	// --no-focus/--timeout and the prompt text must be present and in
	// place, not merely "somewhere in the log".
	lines := readHerdrLoggedArgv(t, logFile)
	want := [][]string{
		{"herdr", "workspace", "create", "--cwd", worktree, "--label", "jig-JIG-1-s1", "--no-focus"},
		{"herdr", "agent", "start", "jig-JIG-1-s1-a1", "--kind", "claude", "--pane", "pane-1", "--timeout", "60000"},
		{"herdr", "agent", "prompt", "jig-JIG-1-s1-a1", "do the thing", "--wait"},
		{"herdr", "agent", "read", "jig-JIG-1-s1-a1"},
		{"herdr", "workspace", "close", "ws-1"},
	}
	if len(lines) != len(want) {
		t.Fatalf("logged calls = %v, want %v", lines, want)
	}
	for i, w := range want {
		if !equalArgs(lines[i], w) {
			t.Errorf("call %d argv = %v, want %v", i, lines[i], w)
		}
	}
}

func readHerdrLoggedArgv(t *testing.T, logFile string) [][]string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read herdr stub log: %v", err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var argv []string
		if err := json.Unmarshal([]byte(line), &argv); err != nil {
			t.Fatalf("parse log line %q: %v", line, err)
		}
		out = append(out, argv)
	}
	return out
}
