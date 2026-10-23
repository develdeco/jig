package setup

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// userSettings is a Claude Code settings file as a person keeps it: their keys
// in an order of their own, hooks of their own in two of the events jig also
// listens to, a number written 1.50 and a string with characters a careless
// JSON writer escapes.
const userSettings = `{
  "model": "opus",
  "env": {"B": "2", "A": "1", "url": "https://example.test/?a=1&b=<2>"},
  "permissions": {"allow": ["Bash(ls:*)"], "deny": []},
  "hooks": {
    "PreToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": "echo pre"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "echo stop-one"}, {"type": "command", "command": "echo stop-two"}]}],
    "PostToolUse": [{"matcher": "Write", "hooks": [{"type": "command", "command": "fmt --check"}]}]
  },
  "statusLine": {"type": "command", "command": "ccstatusline"},
  "cleanupPeriodDays": 30,
  "ratio": 1.50,
  "big": 1e3
}
`

func mustParse(t *testing.T, data []byte) *object {
	t.Helper()
	root, _, err := parseSettings(data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	return root
}

// hookCommands lists the commands of event's hooks, group by group.
func hookCommands(t *testing.T, root *object, event string) [][]string {
	t.Helper()
	hv, _ := root.get("hooks")
	hooks, ok := hv.(*object)
	if !ok {
		t.Fatalf("no hooks object in %v", root.keys)
	}
	v, ok := hooks.get(event)
	if !ok {
		return nil
	}
	var out [][]string
	for _, g := range v.([]any) {
		var cmds []string
		hv, _ := g.(*object).get("hooks")
		for _, e := range hv.([]any) {
			c, _ := e.(*object).get("command")
			cmds = append(cmds, c.(string))
		}
		out = append(out, cmds)
	}
	return out
}

func countOwned(t *testing.T, root *object, event string) int {
	t.Helper()
	n := 0
	for _, g := range hookCommands(t, root, event) {
		for _, c := range g {
			if OwnedCommand(c) {
				n++
			}
		}
	}
	return n
}

// canonical is data parsed and written back in its own layout: the form a
// merge followed by an unmerge must return a document to.
func canonical(t *testing.T, data []byte) string {
	t.Helper()
	out, err := marshalJSON(mustParse(t, data), detectLayout(data))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestClaudeHooksMergeIdempotent pins the Claude Code merge: a person's keys
// and hooks stay, in their order; two merges leave one jig entry per event,
// and the second changes nothing; an unmerge takes out jig's entries and
// nothing else.
func TestClaudeHooksMergeIdempotent(t *testing.T) {
	t.Parallel()
	const jig = "jig"
	command := ClaudeCommand(jig)

	once, changed, err := MergeClaude([]byte(userSettings), jig)
	if err != nil || !changed {
		t.Fatalf("first merge: changed=%v err=%v", changed, err)
	}
	twice, changed, err := MergeClaude(once, jig)
	if err != nil || changed {
		t.Fatalf("second merge: changed=%v err=%v, want it to find nothing to do", changed, err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("the second merge changed the document:\n%s\n---\n%s", once, twice)
	}

	root := mustParse(t, once)
	wantKeys := []string{"model", "env", "permissions", "hooks", "statusLine", "cleanupPeriodDays", "ratio", "big"}
	if !slices.Equal(root.keys, wantKeys) {
		t.Errorf("top-level keys = %v, want the person's order %v", root.keys, wantKeys)
	}
	hv, _ := root.get("hooks")
	wantEvents := []string{"PreToolUse", "Stop", "PostToolUse", "SessionStart", "UserPromptSubmit", "PostToolUseFailure", "SessionEnd"}
	if got := hv.(*object).keys; !slices.Equal(got, wantEvents) {
		t.Errorf("hook events = %v, want theirs first, then jig's new ones: %v", got, wantEvents)
	}

	for _, h := range ClaudeHooks() {
		if n := countOwned(t, root, h.Event); n != 1 {
			t.Errorf("%s has %d jig entries after two merges, want 1", h.Event, n)
		}
	}
	if got, want := hookCommands(t, root, "PreToolUse"), [][]string{{"echo pre"}}; !equalGroups(got, want) {
		t.Errorf("PreToolUse = %v, want the person's, untouched", got)
	}
	if got, want := hookCommands(t, root, "Stop"), [][]string{{"echo stop-one", "echo stop-two"}, {command}}; !equalGroups(got, want) {
		t.Errorf("Stop = %v, want the person's group then jig's: %v", got, want)
	}
	if got, want := hookCommands(t, root, "PostToolUse"), [][]string{{"fmt --check"}, {command}}; !equalGroups(got, want) {
		t.Errorf("PostToolUse = %v, want %v", got, want)
	}
	// The PostToolUse entry is for the Bash tool only; the others have no matcher.
	pv, _ := hv.(*object).get("PostToolUse")
	if m, _ := pv.([]any)[1].(*object).get("matcher"); m != "Bash" {
		t.Errorf("jig PostToolUse matcher = %v, want Bash", m)
	}
	sv, _ := hv.(*object).get("SessionStart")
	if _, has := sv.([]any)[0].(*object).get("matcher"); has {
		t.Errorf("jig SessionStart entry has a matcher")
	}

	// What the person wrote is kept as they wrote it.
	for _, want := range []string{`"ratio": 1.50`, `"big": 1e3`, `https://example.test/?a=1&b=<2>`} {
		if !strings.Contains(string(once), want) {
			t.Errorf("the merged document lost %q:\n%s", want, once)
		}
	}
	if !json.Valid(once) {
		t.Errorf("the merged document is not JSON:\n%s", once)
	}

	// Taking jig out leaves the person's file as it was.
	back, changed, err := UnmergeClaude(once)
	if err != nil || !changed {
		t.Fatalf("unmerge: changed=%v err=%v", changed, err)
	}
	if want := canonical(t, []byte(userSettings)); string(back) != want {
		t.Errorf("after the unmerge the document is not the person's:\n%s\n--- want ---\n%s", back, want)
	}
	again, changed, err := UnmergeClaude(back)
	if err != nil || changed || !bytes.Equal(again, back) {
		t.Errorf("a second unmerge: changed=%v err=%v, want nothing to do", changed, err)
	}
}

func equalGroups(a, b [][]string) bool {
	return slices.EqualFunc(a, b, func(x, y []string) bool { return slices.Equal(x, y) })
}

// TestClaudeMergeCreatesAndEmptiesAFile pins the shape of a fresh file and that
// an unmerge of a file that held only jig's entries leaves an empty object.
func TestClaudeMergeCreatesAndEmptiesAFile(t *testing.T) {
	t.Parallel()
	for _, in := range [][]byte{nil, {}, []byte("  \n")} {
		out, changed, err := MergeClaude(in, "jig")
		if err != nil || !changed {
			t.Fatalf("merge of %q: changed=%v err=%v", in, changed, err)
		}
		root := mustParse(t, out)
		if len(root.keys) != 1 || root.keys[0] != "hooks" {
			t.Errorf("a new file has keys %v, want only hooks", root.keys)
		}
		events, err := ClaudeEvents(out)
		if err != nil || !slices.Equal(events, []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "Stop", "SessionEnd"}) {
			t.Errorf("ClaudeEvents = %v, %v", events, err)
		}
		back, changed, err := UnmergeClaude(out)
		if err != nil || !changed || string(back) != "{}\n" {
			t.Errorf("unmerge of a file of only jig's = %q, changed=%v, err=%v, want {}", back, changed, err)
		}
	}
	// Nothing to uninstall from a file that is not there.
	if out, changed, err := UnmergeClaude(nil); err != nil || changed || out != nil {
		t.Errorf("unmerge of nothing = %q, %v, %v", out, changed, err)
	}
}

// TestClaudeMergeFollowsAMovedBinary pins that an entry whose command is out of
// date is brought up to date in its place, and that duplicates collapse to one.
func TestClaudeMergeFollowsAMovedBinary(t *testing.T) {
	t.Parallel()
	const moved = `"C:/tools/jig.exe"`
	start := `{"hooks": {
  "Stop": [
    {"hooks": [{"type": "command", "command": "echo mine"}]},
    {"hooks": [{"type": "command", "command": "jig observe --harness claude"}, {"type": "command", "command": "/old/bin/jig observe --harness claude"}]},
    {"hooks": [{"type": "command", "command": "\"/older/jig\" observe --harness claude"}]}
  ]
}}`
	out, changed, err := MergeClaude([]byte(start), moved)
	if err != nil || !changed {
		t.Fatalf("merge: changed=%v err=%v", changed, err)
	}
	root := mustParse(t, out)
	want := [][]string{{"echo mine"}, {ClaudeCommand(moved)}}
	if got := hookCommands(t, root, "Stop"); !equalGroups(got, want) {
		t.Errorf("Stop = %v, want one jig entry, in the place of the first, with the new command: %v", got, want)
	}
	for _, h := range ClaudeHooks() {
		if n := countOwned(t, root, h.Event); n != 1 {
			t.Errorf("%s has %d jig entries, want 1", h.Event, n)
		}
	}
}

// TestUnmergeClaudeKeepsWhatIsTheirs pins that an unmerge removes owned entries
// everywhere, a group only when jig emptied it, and what a person put next to
// jig's entry stays.
func TestUnmergeClaudeKeepsWhatIsTheirs(t *testing.T) {
	t.Parallel()
	start := `{
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "jig observe --harness claude"}, {"type": "command", "command": "echo mine"}]}],
    "PreToolUse": [{"hooks": [{"type": "command", "command": "jig observe --harness claude"}]}],
    "Notification": [{"hooks": []}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "echo not jig, only talks of jig observe"}]}]
  },
  "other": true
}
`
	out, changed, err := UnmergeClaude([]byte(start))
	if err != nil || !changed {
		t.Fatalf("unmerge: changed=%v err=%v", changed, err)
	}
	root := mustParse(t, out)
	hv, _ := root.get("hooks")
	if got, want := hv.(*object).keys, []string{"Stop", "Notification", "SessionEnd"}; !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v (PreToolUse held only jig's entry)", got, want)
	}
	if got, want := hookCommands(t, root, "Stop"), [][]string{{"echo mine"}}; !equalGroups(got, want) {
		t.Errorf("Stop = %v, want %v", got, want)
	}
	if got := hookCommands(t, root, "SessionEnd"); len(got) != 1 || got[0][0] != "echo not jig, only talks of jig observe" {
		t.Errorf("SessionEnd = %v, want the person's echo untouched", got)
	}
	if v, _ := root.get("other"); v != true {
		t.Errorf("a key outside hooks was lost")
	}
}

// TestClaudeMergeRefusesWhatItCannotRead pins that a file jig cannot make sense
// of is an error and never something to write over.
func TestClaudeMergeRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"comments":         "{\n// my settings\n\"a\": 1}",
		"trailing comma":   `{"a": 1,}`,
		"array":            `[1, 2]`,
		"hooks a string":   `{"hooks": "none"}`,
		"event an object":  `{"hooks": {"Stop": {}}}`,
		"two documents":    `{} {}`,
		"truncated":        `{"hooks": {`,
		"not json at all":  `hello`,
		"bare null number": `42`,
	} {
		if out, changed, err := MergeClaude([]byte(in), "jig"); err == nil || changed || out != nil {
			t.Errorf("%s: merge = %q, %v, %v, want an error and nothing to write", name, out, changed, err)
		}
	}
	// An unmerge reads the file too, but a hooks value that is not an object has
	// no entries of jig's to take out.
	if out, changed, err := UnmergeClaude([]byte(`{"hooks": "none"}`)); err != nil || changed || out == nil {
		t.Errorf("unmerge of a non-object hooks = %q, %v, %v, want it untouched", out, changed, err)
	}
	if _, _, err := UnmergeClaude([]byte(`{"hooks": `)); err == nil {
		t.Errorf("unmerge of a truncated file is not an error")
	}
}

// TestClaudeMergeKeepsTheFilesLayout pins that a file indented with tabs, ended
// with CRLF and started with a byte order mark is written back that way.
func TestClaudeMergeKeepsTheFilesLayout(t *testing.T) {
	t.Parallel()
	in := "\xef\xbb\xbf{\r\n\t\"model\": \"opus\"\r\n}\r\n"
	out, changed, err := MergeClaude([]byte(in), "jig")
	if err != nil || !changed {
		t.Fatalf("merge: changed=%v err=%v", changed, err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "\xef\xbb\xbf{\r\n\t\"model\": \"opus\",\r\n\t\"hooks\": {\r\n\t\t\"SessionStart\"") {
		t.Errorf("layout lost:\n%q", s)
	}
	if !strings.HasSuffix(s, "}\r\n") {
		t.Errorf("final newline lost: %q", s[len(s)-6:])
	}
	back, _, err := UnmergeClaude(out)
	if err != nil || string(back) != in {
		t.Errorf("unmerge = %q, %v, want the original %q", back, err, in)
	}

	// No final newline stays none; a single-line file gets the default layout.
	noNL, _, err := MergeClaude([]byte(`{"a": 1}`), "jig")
	if err != nil || strings.HasSuffix(string(noNL), "\n") {
		t.Errorf("a file with no final newline got one: %q, %v", noNL, err)
	}
}

func TestPlanClaude(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := dir + "/sub/settings.local.json"

	plan, err := PlanClaude(path, "jig", false)
	if err != nil || plan.Exists || !plan.Changed() {
		t.Fatalf("plan for a new file: %+v, %v", plan, err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	again, err := PlanClaude(path, "jig", false)
	if err != nil || !again.Exists || again.Changed() {
		t.Errorf("plan after applying: %+v, %v, want nothing to do", again, err)
	}
	rm, err := PlanClaude(path, "", true)
	if err != nil || !rm.Changed() || !rm.LeavesEmptyObject() {
		t.Errorf("uninstall plan: %+v, %v, want a change that leaves an empty object", rm, err)
	}
	missing, err := PlanClaude(dir+"/none.json", "", true)
	if err != nil || missing.Changed() {
		t.Errorf("uninstall plan for a missing file: %+v, %v, want nothing to do", missing, err)
	}
	bad := dir + "/bad.json"
	if err := writeFile(bad, []byte("{oops"), 0o644, false); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanClaude(bad, "jig", false); err == nil || !strings.Contains(err.Error(), bad) {
		t.Errorf("plan for an unreadable file: %v, want an error naming it", err)
	}
}
