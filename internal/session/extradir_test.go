package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// This file covers Dispatch.ExtraWriteDir on every backend: headless grants
// the edit tools one more path-scoped tree, fake copies a scenario's demo
// media into it, and herdr - which scopes no edits - needs no grant for it and,
// on Windows, names it to its WSL session by its mount.

// --- headless -------------------------------------------------------------------

// TestHeadlessSettingsGrantsTheExtraWriteDir pins the one rule an
// ExtraWriteDir adds: edit tools inside that directory's tree, and nothing
// else - the worktree and result rules, the hook, and every other path stay
// exactly as they were.
func TestHeadlessSettingsGrantsTheExtraWriteDir(t *testing.T) {
	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}
	d := missingDispatch(t, true)
	plain, err := b.settings(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}

	d.ExtraWriteDir = filepath.Join(filepath.Dir(d.Worktree), "evidence", "id", "T-1", "abc123")
	raw, err := b.settings(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	type shape struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
		Hooks any `json:"hooks"`
	}
	var got, base shape
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	if err := json.Unmarshal([]byte(plain), &base); err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{}, base.Permissions.Allow...), "Edit("+rulePath("linux", d.ExtraWriteDir)+"/**)")
	if !reflect.DeepEqual(got.Permissions.Allow, want) {
		t.Errorf("allow rules =\n%q\nwant the dispatch's own rules plus the extra directory's tree:\n%q", got.Permissions.Allow, want)
	}
	if !reflect.DeepEqual(got.Hooks, base.Hooks) {
		t.Errorf("hooks changed with an ExtraWriteDir: %v, want %v", got.Hooks, base.Hooks)
	}
}

// TestHeadlessSettingsExtraWriteDirThroughASymlink: like the worktree and
// result paths, the extra directory is granted in its resolved spelling too.
func TestHeadlessSettingsExtraWriteDirThroughASymlink(t *testing.T) {
	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	d := missingDispatch(t, true)
	d.ExtraWriteDir = link
	raw, err := b.settings(d)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	var got struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, r := range got.Permissions.Allow {
		have[r] = true
	}
	for _, rule := range []string{
		"Edit(" + rulePath("linux", link) + "/**)",
		"Edit(" + rulePath("linux", resolved) + "/**)",
	} {
		if !have[rule] {
			t.Errorf("allow rules lack %q: %q", rule, got.Permissions.Allow)
		}
	}
}

// TestHeadlessArgsCarryTheExtraWriteDirRule: the rule reaches the session as
// part of the one inline --settings object, and the rest of the argv is the
// dispatch's own.
func TestHeadlessArgsCarryTheExtraWriteDirRule(t *testing.T) {
	b := &headlessBackend{goos: "linux", screenBinary: "/opt/jig/bin/jig"}
	d := missingDispatch(t, true)
	without, cleanup0, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	defer cleanup0()

	d.ExtraWriteDir = filepath.Join(filepath.Dir(d.Worktree), "media")
	with, cleanup1, err := b.args(d)
	if err != nil {
		t.Fatalf("args: %v", err)
	}
	defer cleanup1()
	if len(with) != len(without) {
		t.Fatalf("argv length changed with an ExtraWriteDir: %d, want %d", len(with), len(without))
	}
	changed := 0
	for i := range with {
		if with[i] == without[i] {
			continue
		}
		changed++
		if i == 0 || with[i-1] != "--settings" {
			t.Errorf("argv[%d] changed outside --settings: %q, want %q", i, with[i], without[i])
			continue
		}
		var settings struct {
			Permissions struct {
				Allow []string `json:"allow"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(with[i]), &settings); err != nil {
			t.Fatalf("--settings is not JSON: %v\n%s", err, with[i])
		}
		found := false
		for _, r := range settings.Permissions.Allow {
			found = found || r == "Edit("+rulePath("linux", d.ExtraWriteDir)+"/**)"
		}
		if !found {
			t.Errorf("--settings lacks the extra directory's rule: %q", settings.Permissions.Allow)
		}
	}
	if changed != 1 {
		t.Errorf("%d argv entries changed, want only --settings' value", changed)
	}
}

// TestSessionViewSpellsTheExtraWriteDirLikeTheOtherPaths: the extra directory
// goes through the same long-spelling rewrite as the worktree and the result
// path, prompt mentions included. (longPath is the identity off Windows, so
// this pins the wiring; longpath_windows_test.go pins the spelling.)
func TestSessionViewSpellsTheExtraWriteDirLikeTheOtherPaths(t *testing.T) {
	d := missingDispatch(t, true)
	d.ExtraWriteDir = filepath.Join(t.TempDir(), "media")
	d.Prompt = "write media into " + d.ExtraWriteDir
	got := sessionView(d)
	if want := longPath(d.ExtraWriteDir); got.ExtraWriteDir != want || got.Prompt != "write media into "+want {
		t.Errorf("sessionView left ExtraWriteDir %q and prompt %q, want the long spelling %q", got.ExtraWriteDir, got.Prompt, want)
	}
}

// TestSessionViewSpellsTheExtraReadFileLikeTheOtherPaths: the file the prompt
// names for the session to read goes through the same long-spelling rewrite,
// and, like the extra directory, grants nothing: the settings are the same
// with and without it.
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

// --- fake -----------------------------------------------------------------------

// writeDemoScenario writes <scenario>/gate/round-<n>/demo-result.json and the
// given demo-media files.
func writeDemoScenario(t *testing.T, scenarioDir string, round int, result []byte, media map[string][]byte) {
	t.Helper()
	roundDir := filepath.Join(scenarioDir, "gate", fmt.Sprintf("round-%d", round))
	if err := os.MkdirAll(filepath.Join(roundDir, "demo-media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roundDir, "demo-result.json"), result, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, data := range media {
		if err := os.WriteFile(filepath.Join(roundDir, "demo-media", name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFakeBackendGateDemoPlayback checks the gate-demo dispatch path: the
// scenario's demo-result.json is copied verbatim into ResultJSON, each
// demo-media file lands byte for byte in the dispatch's extra directory, as
// a real session writes its media there, and the worktree is never touched.
func TestFakeBackendGateDemoPlayback(t *testing.T) {
	worktree := newWorktree(t)
	before, err := gitx.Run(worktree, "log", "--format=%H")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}

	scenarioDir := t.TempDir()
	want := []byte(`{"media":[{"file":"a.png","caption":"one"},{"file":"b.svg","caption":"two"}],"summary":"s"}`)
	media := map[string][]byte{"a.png": {0x89, 'P', 'N', 'G', 0, 1, 2, 3}, "b.svg": []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>\n")}
	writeDemoScenario(t, scenarioDir, 2, want, media)

	backend := newFakeBackend(Options{ScenarioDir: scenarioDir})
	resultPath := filepath.Join(t.TempDir(), "work", "demo.result.json")
	mediaDir := filepath.Join(t.TempDir(), "evidence", "head")
	d := Dispatch{Ticket: "JIG-1", Slice: GateDemoSlice, Attempt: 2, Worktree: worktree, ResultJSON: resultPath, ExtraWriteDir: mediaDir}
	if err := backend.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read demo result: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("demo result = %s, want verbatim %s", got, want)
	}
	for name, data := range media {
		copied, err := os.ReadFile(filepath.Join(mediaDir, name))
		if err != nil {
			t.Errorf("media %s was not copied: %v", name, err)
			continue
		}
		if string(copied) != string(data) {
			t.Errorf("media %s = %q, want %q", name, copied, data)
		}
	}
	entries, err := os.ReadDir(mediaDir)
	if err != nil || len(entries) != len(media) {
		t.Errorf("the media dir holds %d entries (%v), want %d", len(entries), err, len(media))
	}

	after, err := gitx.Run(worktree, "log", "--format=%H")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if before != after {
		t.Errorf("worktree history changed by a demo dispatch: before %q after %q", before, after)
	}
}

// TestFakeBackendGateDemoWithNoMediaIsAValidScenario: a scenario that shows
// nothing has a result and no demo-media directory.
func TestFakeBackendGateDemoWithNoMediaIsAValidScenario(t *testing.T) {
	scenarioDir := t.TempDir()
	roundDir := filepath.Join(scenarioDir, "gate", "round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"media":[],"summary":"nothing to show"}`)
	if err := os.WriteFile(filepath.Join(roundDir, "demo-result.json"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	mediaDir := filepath.Join(t.TempDir(), "media")
	resultPath := filepath.Join(t.TempDir(), "demo.result.json")
	d := Dispatch{Ticket: "JIG-1", Slice: GateDemoSlice, Attempt: 1, Worktree: newWorktree(t), ResultJSON: resultPath, ExtraWriteDir: mediaDir}
	if err := newFakeBackend(Options{ScenarioDir: scenarioDir}).Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, err := os.ReadFile(resultPath); err != nil || string(got) != string(want) {
		t.Errorf("demo result = %q, %v; want %q", got, err, want)
	}
	if _, err := os.Stat(mediaDir); err == nil {
		t.Error("a media dir was created for a scenario with no media")
	}
}

// TestFakeBackendGateDemoFailsLoudly: what the scenario forgot to script, or
// scripted in a way a session could not honor, is an error and writes no
// result - never a silent "nothing to show".
func TestFakeBackendGateDemoFailsLoudly(t *testing.T) {
	result := []byte(`{"media":[],"summary":"s"}`)
	cases := []struct {
		name  string
		setup func(t *testing.T, scenarioDir string)
		d     func(d Dispatch) Dispatch
		want  string
	}{
		{"no coverage for the round", func(t *testing.T, s string) {}, func(d Dispatch) Dispatch { return d },
			"scenario has no gate round 2 demo-result.json"},
		{"a review result but no demo result", func(t *testing.T, s string) {
			dir := filepath.Join(s, "gate", "round-2")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "review-result.json"), []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}, func(d Dispatch) Dispatch { return d }, "scenario has no gate round 2 demo-result.json"},
		{"media but no directory to write them in", func(t *testing.T, s string) {
			writeDemoScenario(t, s, 2, result, map[string][]byte{"a.png": {1}})
		}, func(d Dispatch) Dispatch { d.ExtraWriteDir = ""; return d }, "names no directory to write them in"},
		{"a media entry that is not a file", func(t *testing.T, s string) {
			writeDemoScenario(t, s, 2, result, nil)
			if err := os.Mkdir(filepath.Join(s, "gate", "round-2", "demo-media", "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, func(d Dispatch) Dispatch { return d }, "demo-media/sub is not a regular file"},
	}
	for _, c := range cases {
		scenarioDir := t.TempDir()
		c.setup(t, scenarioDir)
		resultPath := filepath.Join(t.TempDir(), "demo.result.json")
		d := c.d(Dispatch{Ticket: "JIG-1", Slice: GateDemoSlice, Attempt: 2, Worktree: newWorktree(t), ResultJSON: resultPath, ExtraWriteDir: filepath.Join(t.TempDir(), "media")})
		err := newFakeBackend(Options{ScenarioDir: scenarioDir}).Run(d)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Run = %v, want an error containing %q", c.name, err, c.want)
		}
		if _, statErr := os.Stat(resultPath); statErr == nil {
			t.Errorf("%s: ResultJSON was written despite the error", c.name)
		}
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
// input file, the result file, the extra directory and the file to read each spelled as their
// WSL mount, while the workspace is created at the worktree's mount. jig
// itself keeps reading the result at its host path. A path that another one
// contains is replaced whole, and the extra directory takes no channel of its
// own: it is only named in the prompt.
func TestHerdrBackendSpellsEveryPathOfTheDispatchForWSLOnWindows(t *testing.T) {
	// Host-spelled paths herdr only passes on as text; the result path is the
	// one jig touches, so it is a real one.
	const (
		worktree = `C:\demo\pool\repo\T-1-gate`
		input    = `C:\demo\store\T-1\work\gate.round-1.demo.json`
		extra    = `D:\jighome\evidence\id\T-1\abc`
		readFile = `D:\jighome\evidence\id\T-1\reviews\round-1\recordings.json`
	)
	resultJSON := filepath.Join(t.TempDir(), "gate.round-1.demo.result.json")
	prompt := fmt.Sprintf("inputs in %s, media into %s, list at %s, result at %s, worktree %s", input, extra, readFile, resultJSON, worktree)
	wantPrompt := fmt.Sprintf("inputs in %s, media into %s, list at %s, result at %s, worktree %s",
		"/mnt/c/demo/store/T-1/work/gate.round-1.demo.json", "/mnt/d/jighome/evidence/id/T-1/abc", "/mnt/d/jighome/evidence/id/T-1/reviews/round-1/recordings.json", WSLPath(resultJSON), "/mnt/c/demo/pool/repo/T-1-gate")

	calls := herdrRunLog(t, "windows", Dispatch{
		Ticket: "T-1", Slice: GateDemoSlice, Attempt: 1, Worktree: worktree,
		SliceJSON: input, ResultJSON: resultJSON, ExtraWriteDir: extra, ExtraReadFile: readFile, Prompt: prompt,
	})

	wantCreate := herdrCmdline(t, "workspace", "create", "--cwd", "/mnt/c/demo/pool/repo/T-1-gate", "--label", "jig-T-1-gate-demo", "--no-focus")
	wantPrompt1 := herdrCmdline(t, "agent", "prompt", "jig-T-1-gate-demo-a1", wantPrompt, "--wait")
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

// TestHerdrBackendPassesAnExtraDirThroughOffWindows: herdr scopes no edits, so
// off Windows the field needs no grant and the same herdr commands run as for
// any dispatch. The directory is not passed to herdr as an argument of its
// own, and the prompt reads exactly as jig wrote it.
func TestHerdrBackendPassesAnExtraDirThroughOffWindows(t *testing.T) {
	// Windows-spelled paths, so a rewrite that reached this branch would show
	// on any host; herdr only passes them on as text.
	const (
		worktree = `C:\demo\pool\repo\JIG-1-gate`
		extra    = `C:\demo\jighome\evidence\id\JIG-1\abc`
	)
	prompt := "media into " + extra
	calls := herdrRunLog(t, "linux", Dispatch{
		Ticket: "JIG-1", Slice: GateDemoSlice, Attempt: 1, Worktree: worktree,
		ResultJSON: filepath.Join(t.TempDir(), "result.json"), ExtraWriteDir: extra, Prompt: prompt,
	})
	if len(calls) == 0 {
		t.Fatal("no herdr command ran")
	}
	sawPrompt := false
	for _, call := range calls {
		for i, a := range call {
			if strings.Contains(a, extra) && !(i == 4 && call[1] == "agent" && call[2] == "prompt" && a == prompt) {
				t.Errorf("herdr was handed the extra directory outside the prompt: %v", call)
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
