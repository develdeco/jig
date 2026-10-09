package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers Dispatch.ExtraReadFile on the backends that spell a
// dispatch's paths for their session: headless spells it long (and grants
// nothing for it), and herdr - which scopes no edits - names it to its WSL
// session by its mount on Windows and leaves the prompt as jig wrote it
// elsewhere.

// --- headless -------------------------------------------------------------------

// TestSessionViewSpellsTheExtraReadFileLikeTheOtherPaths: the file the prompt
// names for the session to read goes through the same long-spelling rewrite as
// the worktree and the result path, and grants nothing: the settings are the
// same with and without it. (longPath is the identity off Windows, so this
// pins the wiring; longpath_windows_test.go pins the spelling.)
func TestSessionViewSpellsTheExtraReadFileLikeTheOtherPaths(t *testing.T) {
	d := missingDispatch(t, true)
	d.ExtraReadFile = filepath.Join(t.TempDir(), "reviews", "round-1", "recordings.json")
	d.Prompt = "recordings.json at " + d.ExtraReadFile
	got := sessionView(d)
	if want := longPath(d.ExtraReadFile); got.ExtraReadFile != want || got.Prompt != "recordings.json at "+want {
		t.Errorf("sessionView left ExtraReadFile %q and prompt %q, want the long spelling %q", got.ExtraReadFile, got.Prompt, want)
	}

	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}
	with, err := b.settings(d)
	if err != nil {
		t.Fatal(err)
	}
	d.ExtraReadFile = ""
	without, err := b.settings(d)
	if err != nil {
		t.Fatal(err)
	}
	if with != without {
		t.Errorf("an ExtraReadFile changed the settings:\n%s\nwant\n%s", with, without)
	}
}

// --- herdr ----------------------------------------------------------------------

// herdrRunLog runs one dispatch through herdrBackend{goos} against the herdr
// stub, which also stands in for wsl (the stub logs its argv and answers
// every command it does not know with an empty object, which Run tolerates),
// and returns every command it was handed, argv whole.
func herdrRunLog(t *testing.T, goos string, d Dispatch) [][]string {
	t.Helper()
	stubDir := buildHerdrStub(t)
	wslDir := buildBinary(t, filepath.Join("testdata", "fixture", "herdrstub"), "wsl")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+wslDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(jigWSLDistroEnv, "")
	logFile := filepath.Join(t.TempDir(), "herdr.log")
	t.Setenv("HERDR_STUB_LOG", logFile)

	b := &herdrBackend{goos: goos}
	if err := b.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return readHerdrLoggedArgv(t, logFile)
}

// TestHerdrBackendSpellsEveryPathOfTheDispatchForWSLOnWindows: on Windows the
// session runs in WSL, so herdr hands it the prompt with the worktree, the
// input file, the result file and the file to read each spelled as their WSL
// mount, while the workspace is created at the worktree's mount. jig itself
// keeps reading the result at its host path. A path that another one contains
// is replaced whole, and the file to read takes no channel of its own: it is
// only named in the prompt.
func TestHerdrBackendSpellsEveryPathOfTheDispatchForWSLOnWindows(t *testing.T) {
	// Host-spelled paths herdr only passes on as text; the result path is the
	// one jig touches, so it is a real one.
	const (
		worktree = `C:\demo\pool\repo\T-1-gate`
		input    = `C:\demo\store\T-1\work\gate.round-1.review.json`
		readFile = `D:\jighome\evidence\id\T-1\reviews\round-1\recordings.json`
	)
	resultJSON := filepath.Join(t.TempDir(), "gate.round-1.result.json")
	prompt := fmt.Sprintf("inputs in %s, list at %s, result at %s, worktree %s", input, readFile, resultJSON, worktree)
	wantPrompt := fmt.Sprintf("inputs in %s, list at %s, result at %s, worktree %s",
		"/mnt/c/demo/store/T-1/work/gate.round-1.review.json", "/mnt/d/jighome/evidence/id/T-1/reviews/round-1/recordings.json", WSLPath(resultJSON), "/mnt/c/demo/pool/repo/T-1-gate")

	calls := herdrRunLog(t, "windows", Dispatch{
		Ticket: "T-1", Slice: "gate", Attempt: 1, Worktree: worktree,
		SliceJSON: input, ResultJSON: resultJSON, ExtraReadFile: readFile, Prompt: prompt,
	})

	wantCreate := herdrCmdline(t, "workspace", "create", "--cwd", "/mnt/c/demo/pool/repo/T-1-gate", "--label", "jig-T-1-gate", "--no-focus")
	wantPrompt1 := herdrCmdline(t, "agent", "prompt", "jig-T-1-gate-a1", wantPrompt, "--wait")
	var sawCreate, sawPrompt bool
	for _, call := range calls {
		if len(call) != 5 || call[0] != "wsl" || call[1] != "-e" || call[2] != "bash" || call[3] != "-lc" {
			t.Fatalf("a herdr command did not run in a WSL login shell: %v", call)
		}
		sawCreate = sawCreate || call[4] == wantCreate
		sawPrompt = sawPrompt || call[4] == wantPrompt1
	}
	if !sawCreate {
		t.Errorf("no workspace was created at the worktree's WSL mount; commands: %v", calls)
	}
	if !sawPrompt {
		t.Errorf("no prompt carried every path spelled for WSL (want %q); commands: %v", wantPrompt, calls)
	}
	if _, err := os.Stat(resultJSON); err != nil {
		t.Errorf("Run left no result at the host path jig reads: %v", err)
	}
}

// herdrCmdline is the bash command line herdr's own arguments make inside a
// WSL login shell, as the backend builds it.
func herdrCmdline(t *testing.T, args ...string) string {
	t.Helper()
	_, argv := herdrCommand("windows", "", args)
	return argv[len(argv)-1]
}

// TestHerdrBackendPassesAnExtraReadFileThroughOffWindows: off Windows the same
// herdr commands run as for any dispatch. The file is not passed to herdr as
// an argument of its own, and the prompt reads exactly as jig wrote it.
func TestHerdrBackendPassesAnExtraReadFileThroughOffWindows(t *testing.T) {
	// Windows-spelled paths, so a rewrite that reached this branch would show
	// on any host; herdr only passes them on as text.
	const (
		worktree = `C:\demo\pool\repo\JIG-1-gate`
		readFile = `C:\demo\jighome\evidence\id\JIG-1\reviews\round-1\recordings.json`
	)
	prompt := "recordings at " + readFile
	calls := herdrRunLog(t, "linux", Dispatch{
		Ticket: "JIG-1", Slice: "gate", Attempt: 1, Worktree: worktree,
		ResultJSON: filepath.Join(t.TempDir(), "result.json"), ExtraReadFile: readFile, Prompt: prompt,
	})
	if len(calls) == 0 {
		t.Fatal("no herdr command ran")
	}
	sawPrompt := false
	for _, call := range calls {
		for i, a := range call {
			if strings.Contains(a, readFile) && !(i == 4 && call[1] == "agent" && call[2] == "prompt" && a == prompt) {
				t.Errorf("herdr was handed the file to read outside the prompt: %v", call)
			}
		}
		sawPrompt = sawPrompt || (len(call) == 6 && call[1] == "agent" && call[2] == "prompt" && call[4] == prompt)
	}
	if !sawPrompt {
		t.Errorf("the prompt did not reach herdr unchanged: %v", calls)
	}
	if calls[0][1] != "workspace" || calls[0][2] != "create" || calls[0][4] != worktree {
		t.Errorf("first call = %v, want the workspace created in the worktree %s", calls[0], worktree)
	}
}

// TestRespellMentionsReplacesTheLongestPathFirst: a path that contains
// another one is replaced whole, not by its shorter prefix, and an empty path
// or one the spelling leaves alone is skipped.
func TestRespellMentionsReplacesTheLongestPathFirst(t *testing.T) {
	t.Parallel()
	got := respellMentions(`run C:\a\b\c.json in C:\a\b, then C:\a`, []string{`C:\a`, "", `C:\a\b\c.json`, `C:\a\b`, "/mnt/c/z"}, WSLPath)
	if want := `run /mnt/c/a/b/c.json in /mnt/c/a/b, then /mnt/c/a`; got != want {
		t.Errorf("respellMentions = %q, want %q", got, want)
	}
}
