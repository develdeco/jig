package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/setup"
)

// doctorRows parses the pieces table of a doctor report into its rows, each a
// map of column to cell. Cells with a comma or colon are quoted; the report is
// read with the same rule axi writes it by.
func doctorRows(t *testing.T, out string) []map[string]string {
	t.Helper()
	cols := []string{"piece", "harness", "where", "state", "detail", "cost", "fix"}
	var rows []map[string]string
	in := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "pieces["):
			in = true
		case in && strings.HasPrefix(line, "  "):
			cells := splitCSV(strings.TrimPrefix(line, "  "))
			if len(cells) != len(cols) {
				t.Fatalf("a row of %d cells, want %d: %q", len(cells), len(cols), line)
			}
			row := map[string]string{}
			for i, c := range cols {
				row[c] = cells[i]
			}
			rows = append(rows, row)
		default:
			in = false
		}
	}
	return rows
}

// splitCSV splits a line the way axi.Table writes a row: commas separate cells
// and a cell in double quotes may hold commas, with \" and \\ escaped.
func splitCSV(line string) []string {
	var cells []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quoted && c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == '"':
			quoted = !quoted
		case c == ',' && !quoted:
			cells = append(cells, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, cur.String())
}

func findRow(rows []map[string]string, piece, harness string) map[string]string {
	for _, r := range rows {
		if r["piece"] == piece && r["harness"] == harness {
			return r
		}
	}
	return nil
}

func TestDoctorNamesTheMissingPiecesAndTheirCommands(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	ie.env = ie.env.inDir(ie.root)
	if out, code := runMain(t, ie.env, "", "skills", "--global"); code != 0 {
		t.Fatalf("skills: exit %d\n%s", code, out)
	}
	out, code := runMain(t, ie.env, "", "doctor")
	if code != 0 {
		t.Fatalf("doctor: exit %d\n%s", code, out)
	}
	rows := doctorRows(t, out)
	for _, c := range []struct{ piece, harness, state, fix string }{
		{"skills", "claude", "installed", ""},
		{"skills", "agents", "installed", ""},
		{"hooks", "claude", "missing", "jig hooks --global --yes"},
		{"githook", "", "missing", "jig githook --global"},
		{"hint", "claude", "missing", "jig hint --global --yes"},
	} {
		r := findRow(rows, c.piece, c.harness)
		if r == nil {
			t.Errorf("no %s/%s row:\n%s", c.piece, c.harness, out)
			continue
		}
		if r["state"] != c.state || r["fix"] != c.fix {
			t.Errorf("%s/%s: state %q fix %q, want %q %q", c.piece, c.harness, r["state"], r["fix"], c.state, c.fix)
		}
		if c.state == "missing" && r["cost"] == "" {
			t.Errorf("%s/%s: no cost", c.piece, c.harness)
		}
	}
	if !strings.Contains(out, "jig: "+binaryVersion()) {
		t.Errorf("the report does not name the binary's version:\n%s", out)
	}
	if !strings.Contains(out, "jig setup --global --artifacts --yes") {
		t.Errorf("no command that installs it all:\n%s", out)
	}

	// Every fix is a command that works: run them, and the report is clean.
	for _, fix := range []string{"jig hooks --global --yes", "jig githook --global", "jig hint --global --yes"} {
		if out, code := runMain(t, ie.env, "", strings.Fields(fix)[1:]...); code != 0 {
			t.Fatalf("%s: exit %d\n%s", fix, code, out)
		}
	}
	out, _ = runMain(t, ie.env, "", "doctor")
	for _, r := range doctorRows(t, out) {
		if r["piece"] != "helpers" && r["state"] != "installed" {
			t.Errorf("after the fixes: %v", r)
		}
	}
	if !strings.Contains(out, "Every piece jig sets up is in place") {
		t.Errorf("no all-clear:\n%s", out)
	}
}

func TestDoctorStubVersionAgainstBinaryVersion(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	e := ie.env.inDir(repo)
	if out, code := runMain(t, e, "", "skills", "--here"); code != 0 {
		t.Fatalf("skills --here: exit %d\n%s", code, out)
	}
	out, _ := runMain(t, e, "", "doctor")
	r := findRow(doctorRows(t, out), "skills", "claude")
	if r == nil || r["state"] != "installed" || r["where"] != "here (stub)" {
		t.Fatalf("a fresh stub: %v\n%s", r, out)
	}

	// A stub an older jig wrote.
	stubDir := filepath.Join(repo, ".claude", "skills")
	data := readFileOrFail(t, filepath.Join(stubDir, "jig", "SKILL.md"))
	old := strings.Replace(data, "v="+binaryVersion(), "v=v0.0.1-old", 1)
	if old == data {
		t.Fatalf("the stub carries no stamp to age:\n%s", data)
	}
	writeTestFile(t, filepath.Join(stubDir, "jig", "SKILL.md"), old)
	out, _ = runMain(t, e, "", "doctor")
	r = findRow(doctorRows(t, out), "skills", "claude")
	if r == nil || r["state"] != "stale" || !strings.Contains(r["detail"], "v0.0.1-old") || !strings.Contains(r["detail"], binaryVersion()) || r["fix"] != "jig skills --here" {
		t.Errorf("an old stub: %v\n%s", r, out)
	}
	// The fix refreshes it.
	if out, code := runMain(t, e, "", "skills", "--here"); code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("refresh: exit %d\n%s", code, out)
	}
	out, _ = runMain(t, e, "", "doctor")
	if r := findRow(doctorRows(t, out), "skills", "claude"); r == nil || r["state"] != "installed" {
		t.Errorf("after the refresh: %v", r)
	}
}

func TestDoctorSeesAPartialAndAMovedBinaryAndAStaleWrapper(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	ie.env = ie.env.inDir(ie.root)
	if out, code := runMain(t, ie.env, "", "setup", "--global", "--hooks", "--githook", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, out)
	}
	// One of the five events loses its entry.
	data := readFileOrFail(t, ie.settings())
	cut, _, err := setup.UnmergeClaude([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	partial, _, err := setup.MergeClaude(cut, ie.quotedExe())
	if err != nil {
		t.Fatal(err)
	}
	partial = []byte(strings.Replace(string(partial), `"SessionEnd"`, `"NotJigs"`, 1))
	writeTestFile(t, ie.settings(), string(partial))
	out, _ := runMain(t, ie.env, "", "doctor")
	r := findRow(doctorRows(t, out), "hooks", "claude")
	if r == nil || r["state"] != "partial" || !strings.Contains(r["detail"], "SessionEnd") {
		t.Errorf("a missing event: %v\n%s", r, out)
	}

	// An install from before subagents had a stop entry of their own.
	older := strings.Replace(string(partial), `"NotJigs"`, `"SessionEnd"`, 1)
	older = strings.Replace(older, `"SubagentStop"`, `"NotJigsEither"`, 1)
	writeTestFile(t, ie.settings(), older)
	out, _ = runMain(t, ie.env, "", "doctor")
	r = findRow(doctorRows(t, out), "hooks", "claude")
	if r == nil || r["state"] != "partial" || !strings.Contains(r["detail"], "SubagentStop") {
		t.Errorf("an install without SubagentStop: %v\n%s", r, out)
	}

	// Entries that run another jig.
	moved, _, _ := setup.MergeClaude(nil, `"/old/place/jig"`)
	writeTestFile(t, ie.settings(), string(moved))
	out, _ = runMain(t, ie.env, "", "doctor")
	if r := findRow(doctorRows(t, out), "hooks", "claude"); r == nil || r["state"] != "stale" || r["fix"] != "jig hooks --global --yes" {
		t.Errorf("a moved binary: %v\n%s", r, out)
	}

	// A wrapper that is not what this jig writes.
	hooksDir := home.HooksDir(ie.jigHome)
	post := filepath.Join(hooksDir, "post-commit")
	writeTestFile(t, post, readFileOrFail(t, post)+"# edited\n")
	out, _ = runMain(t, ie.env, "", "doctor")
	if r := findRow(doctorRows(t, out), "githook", ""); r == nil || r["state"] != "stale" || !strings.Contains(r["detail"], "post-commit") || r["fix"] != "jig githook --global" {
		t.Errorf("a stale wrapper: %v\n%s", r, out)
	}
}

func TestDoctorHooksNotSeen(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	ie.env = ie.env.inDir(ie.root)
	if out, code := runMain(t, ie.env, "", "hooks", "--global", "--yes"); code != 0 {
		t.Fatalf("hooks: exit %d\n%s", code, out)
	}
	out, _ := runMain(t, ie.env, "", "doctor")
	if !strings.Contains(out, "hooks not seen: no hook or tool event from claude") {
		t.Errorf("hooks installed and never seen:\n%s", out)
	}
	// Another harness's event does not count.
	if err := ledger.Append(ie.jigHome, journal.Line{Event: journal.EventTool, Harness: "codex", Tool: "Bash"}); err != nil {
		t.Fatal(err)
	}
	out, _ = runMain(t, ie.env, "", "doctor")
	if !strings.Contains(out, "hooks not seen: no hook or tool event from claude") {
		t.Errorf("a codex event counted for claude:\n%s", out)
	}
	if err := ledger.Append(ie.jigHome, journal.Line{Event: journal.EventTool, Harness: "claude", Tool: "Bash"}); err != nil {
		t.Fatal(err)
	}
	out, _ = runMain(t, ie.env, "", "doctor")
	if strings.Contains(out, "hooks not seen") {
		t.Errorf("hooks seen, still reported:\n%s", out)
	}
}

func TestDoctorFindsOtherHarnessesByTheirDirectory(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	ie.env = ie.env.inDir(ie.root)
	out, _ := runMain(t, ie.env, "", "doctor")
	rows := doctorRows(t, out)
	if findRow(rows, "hooks", "codex") != nil || findRow(rows, "hint", "codex") != nil {
		t.Errorf("codex is reported though its directory is not there:\n%s", out)
	}
	if err := os.MkdirAll(filepath.Join(ie.userHome, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _ = runMain(t, ie.env, "", "doctor")
	rows = doctorRows(t, out)
	if r := findRow(rows, "hooks", "codex"); r == nil || r["fix"] != "jig hooks --global --harness codex --yes" {
		t.Errorf("codex hooks: %v\n%s", r, out)
	}
	if r := findRow(rows, "hint", "codex"); r == nil || r["fix"] != "jig hint --global --yes --harness codex" {
		t.Errorf("codex hint: %v\n%s", r, out)
	}
	// --harness names them whatever the directories say.
	out, _ = runMain(t, ie.env, "", "doctor", "--harness", "cursor,opencode")
	rows = doctorRows(t, out)
	if findRow(rows, "hooks", "cursor") == nil || findRow(rows, "hooks", "opencode") == nil || findRow(rows, "hooks", "claude") != nil {
		t.Errorf("--harness cursor,opencode:\n%s", out)
	}
	if out, code := runMain(t, ie.env, "", "doctor", "--harness", "emacs"); code != 2 {
		t.Errorf("an unknown harness: exit %d\n%s", code, out)
	}
}

func TestDoctorInAProject(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\nexpects:\n  hooks: true\n")
	e := ie.env.inDir(repo)

	out, _ := runMain(t, e, "", "doctor")
	if !strings.Contains(out, "scope: global + here") {
		t.Errorf("scope:\n%s", out)
	}
	r := findRow(doctorRows(t, out), "hooks", "claude")
	if r == nil || r["state"] != "missing" || !strings.Contains(r["detail"], "jig.yaml expects hooks") {
		t.Errorf("hooks the marker expects: %v\n%s", r, out)
	}

	if out, code := runMain(t, e, "", "attach", "--yes"); code != 0 {
		t.Fatalf("attach: exit %d\n%s", code, out)
	}
	out, _ = runMain(t, e, "", "doctor")
	rows := doctorRows(t, out)
	for _, c := range []struct{ piece, harness, where string }{
		{"skills", "claude", "here (stub)"},
		{"skills", "agents", "here (stub)"},
		{"hooks", "claude", "here"},
		{"githook", "", "here"},
		{"hint", "claude", "here"},
	} {
		r := findRow(rows, c.piece, c.harness)
		if r == nil || r["state"] != "installed" || r["where"] != c.where {
			t.Errorf("%s/%s after attach: %v\n%s", c.piece, c.harness, r, out)
		}
	}
}

func TestDoctorWarnsOfHooksTheWrappersDoNotRun(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	e := ie.env.inDir(repo)
	if out, code := runMain(t, ie.env, "", "githook", "--global"); code != 0 {
		t.Fatalf("githook: exit %d\n%s", code, out)
	}
	writeTestFile(t, filepath.Join(repo, ".git", "hooks", "reference-transaction"), "#!/bin/sh\n")
	out, _ := runMain(t, e, "", "doctor")
	if !strings.Contains(out, "githook:") || !strings.Contains(out, "reference-transaction") || !strings.Contains(out, "stop running") {
		t.Errorf("no warning:\n%s", out)
	}
	if _, err := gitx.RunEnv(repo, ie.gitEnv(), "rev-parse", "--git-dir"); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorWritesNothing(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	before, err := gitx.Run(repo, "status", "--porcelain", "-uall", "--ignored")
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, ie.env.inDir(repo), "", "doctor"); code != 0 {
		t.Fatalf("doctor: exit %d\n%s", code, out)
	}
	after, _ := gitx.Run(repo, "status", "--porcelain", "-uall", "--ignored")
	if before != after || exists(ie.userHome) || exists(ie.jigHome) {
		t.Errorf("doctor wrote something: git status %q -> %q", before, after)
	}
}

// TestDoctorSaysWhenClaudeReadsAnotherDirectory: jig writes to ~/.claude; a
// Claude Code started with CLAUDE_CONFIG_DIR reads somewhere else, and doctor
// says so.
func TestDoctorSaysWhenClaudeReadsAnotherDirectory(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	ie.env = ie.env.inDir(ie.root)
	out, _ := runMain(t, ie.env, "", "doctor")
	if strings.Contains(out, "CLAUDE_CONFIG_DIR") {
		t.Errorf("a note about a variable that is not set:\n%s", out)
	}
	other := filepath.Join(ie.root, "claude-elsewhere")
	e := ie.env
	get := e.getenv
	e.getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return other
		}
		return get(k)
	}
	out, _ = runMain(t, e, "", "doctor")
	if !strings.Contains(out, "CLAUDE_CONFIG_DIR is set to") || !strings.Contains(out, other) || !strings.Contains(out, filepath.Join(ie.userHome, ".claude")) {
		t.Errorf("no note about CLAUDE_CONFIG_DIR:\n%s", out)
	}
}
