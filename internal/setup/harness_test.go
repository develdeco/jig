package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// goldenExt is the extension of each harness's golden file.
func goldenExt(name string) string {
	if name == "opencode" {
		return "js"
	}
	return "json"
}

func golden(t *testing.T, name string) string {
	t.Helper()
	return readOrFail(t, filepath.Join("testdata", name))
}

// TestHarnessGoldenFiles pins the file each harness gets when jig installs
// into nothing: the vendor's event names, the shape of its entries, and the
// command each one runs.
func TestHarnessGoldenFiles(t *testing.T) {
	t.Parallel()
	for _, h := range Harnesses() {
		t.Run(h.Name, func(t *testing.T) {
			t.Parallel()
			out, changed, err := h.Merge(nil, "jig")
			if err != nil || !changed {
				t.Fatalf("Merge(nil) = changed %v, err %v", changed, err)
			}
			if want := golden(t, h.Name+".golden."+goldenExt(h.Name)); string(out) != want {
				t.Errorf("%s:\n%s\nwant\n%s", h.Name, out, want)
			}
		})
	}
	// A program that is a path is written quoted, and Copilot's PowerShell
	// entry calls it with the call operator.
	for _, name := range []string{"copilot", "opencode"} {
		h, _ := HarnessNamed(name)
		out, _, err := h.Merge(nil, `"C:/tools/jig.exe"`)
		if err != nil {
			t.Fatal(err)
		}
		if want := golden(t, name+".quoted.golden."+goldenExt(name)); string(out) != want {
			t.Errorf("%s with a quoted program:\n%s\nwant\n%s", name, out, want)
		}
	}
}

// TestHarnessEventsMatchWhatTheyInstall: the events a harness advertises are
// the ones its entries are under, in order, and Installed reads them back.
func TestHarnessEventsMatchWhatTheyInstall(t *testing.T) {
	t.Parallel()
	for _, h := range Harnesses() {
		out, _, err := h.Merge(nil, "jig")
		if err != nil {
			t.Fatal(err)
		}
		got, err := h.Installed(out)
		if err != nil {
			t.Fatalf("%s: Installed: %v", h.Name, err)
		}
		want := h.Events
		if h.Name == "opencode" {
			want = OpenCodeEvents()
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: Installed = %v, want %v", h.Name, got, want)
		}
		if none, err := h.Installed(nil); err != nil || len(none) != 0 {
			t.Errorf("%s: Installed(nothing) = %v, %v", h.Name, none, err)
		}
	}
}

// theirFile is a person's own hook file for each harness: keys of their own,
// hooks of their own in an event jig also listens to.
var theirFile = map[string]string{
	"codex": `{
  "note": "mine",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "echo mine"}]}]
  }
}
`,
	"gemini": `{
  "theme": "dark",
  "hooks": {
    "AfterAgent": [{"hooks": [{"type": "command", "command": "echo mine"}]}]
  },
  "tools": {"sandbox": false}
}
`,
	"cursor": `{
  "version": 1,
  "hooks": {
    "stop": [{"command": "echo mine"}],
    "afterFileEdit": [{"command": "fmt"}]
  }
}
`,
	"copilot": `{
  "version": 1,
  "hooks": {
    "sessionEnd": [{"type": "command", "bash": "echo mine"}]
  }
}
`,
}

// laidOut is a document as jig writes one: a compact list is spread over lines,
// which is the one change a merge makes to the person's own layout.
func laidOut(t *testing.T, doc string) string {
	t.Helper()
	root, l, err := parseSettings([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	out, err := marshalJSON(root, l)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestHarnessMergeIsIdempotentAndKeepsTheirs: a second merge changes nothing,
// the person's keys and hooks stay in their order, and an uninstall puts the
// file back as it was.
func TestHarnessMergeIsIdempotentAndKeepsTheirs(t *testing.T) {
	t.Parallel()
	for name, raw := range theirFile {
		h, _ := HarnessNamed(name)
		mine := laidOut(t, raw)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			once, changed, err := h.Merge([]byte(mine), "jig")
			if err != nil || !changed {
				t.Fatalf("first merge: changed %v, err %v", changed, err)
			}
			twice, changed, err := h.Merge(once, "jig")
			if err != nil || changed || string(twice) != string(once) {
				t.Fatalf("second merge: changed %v, err %v, same bytes %v", changed, err, string(twice) == string(once))
			}
			if !strings.Contains(string(once), "echo mine") {
				t.Errorf("the person's hook is gone:\n%s", once)
			}
			if jigEntries(string(once), name) != len(h.Events) {
				t.Errorf("want one jig entry for each of %d events:\n%s", len(h.Events), once)
			}
			// The person's keys keep their order: first key first.
			if first := strings.Index(string(once), `"`+firstKey(mine)+`"`); first < 0 || first > strings.Index(string(once), `"hooks"`) {
				t.Errorf("the person's first key moved:\n%s", once)
			}
			back, changed, err := h.Unmerge(once)
			if err != nil || !changed {
				t.Fatalf("unmerge: changed %v, err %v", changed, err)
			}
			if string(back) != mine {
				t.Errorf("after install and uninstall:\n%s\nwant the person's own\n%s", back, mine)
			}
			if again, changed, err := h.Unmerge(back); err != nil || changed || string(again) != string(back) {
				t.Errorf("second unmerge: changed %v, err %v", changed, err)
			}
		})
	}
}

// jigEntries counts the entries of jig's in doc: Copilot's names the command
// twice, for bash and for PowerShell.
func jigEntries(doc, harness string) int {
	n := strings.Count(doc, "observe --harness "+harness)
	if harness == "copilot" {
		n /= 2
	}
	return n
}

func firstKey(doc string) string {
	_, rest, _ := strings.Cut(doc, `"`)
	key, _, _ := strings.Cut(rest, `"`)
	return key
}

// TestHarnessMergeFollowsAMovedBinaryAndDropsDuplicates: an entry already
// there is brought up to date, once per event, whatever program wrote it.
func TestHarnessMergeFollowsAMovedBinaryAndDropsDuplicates(t *testing.T) {
	t.Parallel()
	for _, h := range Harnesses() {
		if h.Name == "opencode" {
			continue
		}
		old, _, err := h.Merge(nil, `"/old/place/jig"`)
		if err != nil {
			t.Fatal(err)
		}
		moved, changed, err := h.Merge(old, "jig")
		if err != nil || !changed {
			t.Fatalf("%s: merge after a move: changed %v, err %v", h.Name, changed, err)
		}
		if strings.Contains(string(moved), "/old/place") {
			t.Errorf("%s: the old program is still there:\n%s", h.Name, moved)
		}
		if n := jigEntries(string(moved), h.Name); n != len(h.Events) {
			t.Errorf("%s: %d entries after the move, want %d", h.Name, n, len(h.Events))
		}
	}
	// A duplicate entry in one event goes.
	h, _ := HarnessNamed("cursor")
	dup := `{"version":1,"hooks":{"stop":[{"command":"jig observe --harness cursor"},{"command":"echo mine"},{"command":"jig observe --harness cursor"}]}}`
	out, changed, err := h.Merge([]byte(dup), "jig")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if n := strings.Count(string(out), `"command": "jig observe --harness cursor"`); n != len(h.Events) {
		t.Errorf("cursor: %d jig entries, want one per event (%d):\n%s", n, len(h.Events), out)
	}
	if !strings.Contains(string(out), "echo mine") {
		t.Errorf("cursor: the person's entry went with the duplicate:\n%s", out)
	}
}

// TestHarnessMergeRefusesWhatItCannotRead: a file that is not the shape the
// harness reads is an error and nothing is written.
func TestHarnessMergeRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	for _, h := range Harnesses() {
		if h.Name == "opencode" {
			continue
		}
		for _, bad := range []string{"[]", `{"hooks": []}`, `{"hooks": {"` + h.Events[0] + `": {}}}`, "{\n // comment\n}"} {
			if out, _, err := h.Merge([]byte(bad), "jig"); err == nil {
				t.Errorf("%s: merged into %q: %s", h.Name, bad, out)
			}
		}
	}
}

// TestHarnessPlanDeletesAFileThatHeldOnlyJigs: a hook file made for hooks, with
// nothing left once jig's are out, is deleted; one with anything of the
// person's is kept.
func TestHarnessPlanDeletesAFileThatHeldOnlyJigs(t *testing.T) {
	t.Parallel()
	for _, h := range Harnesses() {
		if !h.Dedicated {
			continue
		}
		t.Run(h.Name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "sub", filepath.Base(h.GlobalFile("x")))
			plan, err := h.Plan(path, "jig", false)
			if err != nil || !plan.Changed() || plan.Delete {
				t.Fatalf("install plan: %+v, %v", plan, err)
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}
			if again, err := h.Plan(path, "jig", false); err != nil || again.Changed() {
				t.Fatalf("second install plan changes something: %+v, %v", again, err)
			}
			rm, err := h.Plan(path, "jig", true)
			if err != nil || !rm.Changed() || !rm.Delete {
				t.Fatalf("uninstall plan: %+v, %v", rm, err)
			}
			if err := rm.Apply(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the file is still there: %v", err)
			}
			if none, err := h.Plan(path, "jig", true); err != nil || none.Changed() {
				t.Errorf("uninstall of nothing: %+v, %v", none, err)
			}
		})
	}
	// With the person's hook next to it, the file stays.
	h, _ := HarnessNamed("cursor")
	path := filepath.Join(t.TempDir(), "hooks.json")
	mine := laidOut(t, theirFile["cursor"])
	writeOrFail(t, path, mine, 0o644)
	plan, err := h.Plan(path, "jig", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	rm, err := h.Plan(path, "jig", true)
	if err != nil || rm.Delete {
		t.Fatalf("uninstall plan deletes the person's file: %+v, %v", rm, err)
	}
	if err := rm.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := readOrFail(t, path); got != mine {
		t.Errorf("the person's file after install and uninstall:\n%s", got)
	}
}

func TestHarnessFiles(t *testing.T) {
	t.Parallel()
	home := filepath.Join("h", "me")
	dir := filepath.Join("w", "proj")
	cases := []struct {
		name, global, project, tracked string
	}{
		{"claude", ".claude/settings.json", ".claude/settings.local.json", ".claude/settings.json"},
		{"codex", ".codex/hooks.json", ".codex/hooks.json", ".codex/hooks.json"},
		{"gemini", ".gemini/settings.json", ".gemini/settings.json", ".gemini/settings.json"},
		{"cursor", ".cursor/hooks.json", ".cursor/hooks.json", ".cursor/hooks.json"},
		{"copilot", ".copilot/hooks/jig.json", ".github/hooks/jig.json", ".github/hooks/jig.json"},
		{"opencode", ".config/opencode/plugins/jig.js", ".opencode/plugins/jig.js", ".opencode/plugins/jig.js"},
	}
	for _, c := range cases {
		h, ok := HarnessNamed(c.name)
		if !ok {
			t.Fatalf("no harness %s", c.name)
		}
		if got := filepath.ToSlash(h.GlobalFile(home)); got != "h/me/"+c.global {
			t.Errorf("%s global = %s", c.name, got)
		}
		if got := filepath.ToSlash(h.ProjectFile(dir, false)); got != "w/proj/"+c.project {
			t.Errorf("%s project = %s", c.name, got)
		}
		if got := filepath.ToSlash(h.ProjectFile(dir, true)); got != "w/proj/"+c.tracked {
			t.Errorf("%s tracked = %s", c.name, got)
		}
	}
	if got, want := HarnessNames(), []string{"claude", "codex", "gemini", "cursor", "copilot", "opencode"}; !slices.Equal(got, want) {
		t.Errorf("HarnessNames = %v", got)
	}
	if _, ok := HarnessNamed("emacs"); ok {
		t.Error("HarnessNamed(emacs) found one")
	}
}

// TestOpenCodeShim: the plugin carries jig's mark, runs the command it was
// given, is replaced by a later install, is refused over a file that is not
// jig's, and is valid JavaScript.
func TestOpenCodeShim(t *testing.T) {
	t.Parallel()
	h, _ := HarnessNamed("opencode")
	shim := string(OpenCodeShim("jig"))
	if !strings.HasPrefix(shim, openCodeMarker) || !strings.Contains(shim, `const COMMAND = "jig observe --harness opencode"`) {
		t.Fatalf("shim:\n%s", shim)
	}
	if !OwnedCommand("jig observe --harness opencode") {
		t.Error("the shim's command is not one OwnedCommand recognizes")
	}

	moved, changed, err := h.Merge([]byte(shim), `"/new/jig"`)
	if err != nil || !changed || !strings.Contains(string(moved), `\"/new/jig\" observe --harness opencode`) {
		t.Errorf("a moved binary: changed %v, err %v\n%s", changed, err, moved)
	}
	if _, _, err := h.Merge([]byte("export const Mine = async () => ({})\n"), "jig"); err == nil || !strings.Contains(err.Error(), "not one jig wrote") {
		t.Errorf("a plugin that is not jig's: %v", err)
	}
	if out, changed, err := h.Unmerge([]byte("export const Mine = async () => ({})\n")); err != nil || changed || out == nil {
		t.Errorf("uninstall over a plugin that is not jig's: changed %v, err %v", changed, err)
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("node is not on PATH; the shim's syntax is not checked")
		return
	}
	file := filepath.Join(t.TempDir(), "jig.mjs")
	writeOrFail(t, file, shim, 0o644)
	if out, err := exec.Command(node, "--check", file).CombinedOutput(); err != nil {
		t.Errorf("node --check: %v\n%s", err, out)
	}
}
