package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
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
		if got := WSLPath(c.in); got != c.want {
			t.Errorf("WSLPath(%q) = %q, want %q", c.in, got, c.want)
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

// buildHerdrStub returns the directory holding the once-built
// testdata/fixture/herdrstub binary, named herdr (or herdr.exe on Windows),
// so it can be prepended to PATH. Mirrors fixture.GhStub's build-a-fake-CLI
// shape, but caches the build across every test in this binary instead of
// rebuilding per test (see buildBinary).
func buildHerdrStub(t *testing.T) string {
	t.Helper()
	return buildBinary(t, filepath.Join("testdata", "fixture", "herdrstub"), "herdr")
}

// Helper binaries are built once per test binary and shared by every test
// that runs them (the pattern of fixture's envtool helper).
var (
	binMu    sync.Mutex
	binBuilt = map[string]binBuild{}
)

type binBuild struct {
	dir string
	err error
}

// buildBinary compiles the Go package at pkg (relative to the repo root) into
// a binary named name (plus .exe on Windows) inside its own directory, and
// returns that directory. The build happens once per test binary: the
// directory is an os.MkdirTemp dir (a t.TempDir would vanish with the first
// test that used it), removed through gittest.AtExit.
func buildBinary(t *testing.T, pkg, name string) string {
	t.Helper()
	binMu.Lock()
	defer binMu.Unlock()
	key := pkg + "|" + name
	b, ok := binBuilt[key]
	if !ok {
		b = compileBinary(fixture.RepoRoot(t), pkg, name)
		binBuilt[key] = b
	}
	if b.err != nil {
		t.Fatalf("%v", b.err)
	}
	return b.dir
}

func compileBinary(root, pkg, name string) binBuild {
	dir, err := os.MkdirTemp("", "jig-session-bin")
	if err != nil {
		return binBuild{err: fmt.Errorf("create build dir: %w", err)}
	}
	gittest.AtExit(func() { os.RemoveAll(dir) })
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", filepath.Join(dir, name), filepath.Join(root, pkg))
	if output, err := cmd.CombinedOutput(); err != nil {
		return binBuild{err: fmt.Errorf("build %s: %v\n%s", pkg, err, output)}
	}
	return binBuild{dir: dir}
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
		// herdr accepts the field and ignores it: the agent it starts takes
		// no such flag, so the calls below are what any dispatch makes.
		NoSessionPersistence: true,
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

// TestHerdrErrorsNameTheCommandAndNotItsOperands: a failed herdr command is
// named by its subcommand ("herdr agent prompt") and never by its operands,
// whether it exited non-zero or answered with something that is not JSON. The
// operands of `agent prompt` are the whole prompt and every path of the
// dispatch in it (spelled as WSL mounts on Windows), and the operands of
// `workspace create` are the worktree; an error carries them to the terminal
// and to whatever records it. herdr's own stderr, which says why, is kept.
func TestHerdrErrorsNameTheCommandAndNotItsOperands(t *testing.T) {
	const (
		worktree = `C:\demo\pool\repo\T-1-gate`
		input    = `C:\demo\store\T-1\work\gate.round-1.demo.json`
		extra    = `D:\jighome\evidence\id\T-1\abc`
		marker   = "OPERAND-WORDS-OF-THE-PROMPT"
	)
	resultJSON := filepath.Join(t.TempDir(), "gate.round-1.demo.result.json")
	prompt := fmt.Sprintf("%s: inputs in %s, media into %s, result at %s, worktree %s", marker, input, extra, resultJSON, worktree)

	cases := []struct {
		name   string
		env    string
		sub    string
		stderr bool
	}{
		{"workspace create exits non-zero", "HERDR_STUB_FAIL_CMD", "workspace create", true},
		{"agent start exits non-zero", "HERDR_STUB_FAIL_CMD", "agent start", true},
		{"agent prompt exits non-zero", "HERDR_STUB_FAIL_CMD", "agent prompt", true},
		{"agent read exits non-zero", "HERDR_STUB_FAIL_CMD", "agent read", true},
		{"agent prompt exits non-zero without a word", "HERDR_STUB_SILENT_FAIL_CMD", "agent prompt", false},
		{"workspace create exits non-zero without a word", "HERDR_STUB_SILENT_FAIL_CMD", "workspace create", false},
		{"workspace create answers with something that is not JSON", "HERDR_STUB_GARBAGE_CMD", "workspace create", false},
		{"agent prompt answers with something that is not JSON", "HERDR_STUB_GARBAGE_CMD", "agent prompt", false},
	}
	for _, goos := range []string{"linux", "windows"} {
		for _, c := range cases {
			c := c
			goos := goos
			t.Run(goos+"/"+c.name, func(t *testing.T) {
				stubDir := buildHerdrStub(t)
				wslDir := buildBinary(t, filepath.Join("testdata", "fixture", "herdrstub"), "wsl")
				t.Setenv("PATH", stubDir+string(os.PathListSeparator)+wslDir+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv(jigWSLDistroEnv, "")
				t.Setenv(c.env, c.sub)

				err := (&herdrBackend{goos: goos}).Run(Dispatch{
					Ticket: "T-1", Slice: GateDemoSlice, Attempt: 1, Worktree: worktree,
					SliceJSON: input, ResultJSON: resultJSON, ExtraWriteDir: extra, Prompt: prompt,
				})
				if err == nil {
					t.Fatal("Run succeeded, want the failing command's error")
				}
				got := err.Error()
				if !strings.Contains(got, "herdr "+c.sub) {
					t.Errorf("error %q does not name the command %q", got, "herdr "+c.sub)
				}
				if c.stderr && !strings.Contains(got, "the command was refused") {
					t.Errorf("error %q dropped herdr's own stderr", got)
				}
				for _, operand := range []string{
					marker, worktree, input, extra, resultJSON,
					WSLPath(worktree), WSLPath(input), WSLPath(extra), WSLPath(resultJSON),
					"jig-T-1-gate-demo", "--cwd", "--wait",
				} {
					if strings.Contains(got, operand) {
						t.Errorf("error %q echoes the operand %q", got, operand)
					}
				}
			})
		}
	}
}
