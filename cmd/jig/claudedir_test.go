package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// withVar returns e with the variable key answering value, and every other
// variable as e had it.
func (e env) withVar(key, value string) env {
	get := e.getenv
	e.getenv = func(k string) string {
		if k == key {
			return value
		}
		return get(k)
	}
	return e
}

// TestUserDirsResolvesClaudesDirectory: Claude Code's user directory is
// CLAUDE_CONFIG_DIR when it is set, made absolute against the env's working
// directory, and the home's .claude when it is unset or empty.
func TestUserDirsResolvesClaudesDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "wd")
	abs := filepath.Join(root, "claude-work")
	e := envFrom(map[string]string{"HOME": home, "USERPROFILE": home}).inDir(wd)
	for _, c := range []struct {
		name, value, claude string
	}{
		{"unset", "", filepath.Join(home, ".claude")},
		{"absolute", abs, abs},
		{"relative", filepath.Join("sub", "claude"), filepath.Join(wd, "sub", "claude")},
	} {
		got := e
		if c.value != "" {
			got = e.withVar(claudeConfigDirEnv, c.value)
		}
		u, err := got.userDirs()
		if err != nil {
			t.Fatalf("%s: userDirs: %v", c.name, err)
		}
		if u.Home != home || u.ClaudeDir() != c.claude {
			t.Errorf("%s: userDirs = %+v (Claude Code's %s), want home %s and %s", c.name, u, u.ClaudeDir(), home, c.claude)
		}
	}
	// An empty variable is the same as none.
	if u, err := e.withVar(claudeConfigDirEnv, "").userDirs(); err != nil || u.Claude != "" {
		t.Errorf("empty %s: userDirs = %+v, %v, want no directory of its own", claudeConfigDirEnv, u, err)
	}
}

// TestGlobalPiecesFollowClaudeConfigDir: with CLAUDE_CONFIG_DIR set to a
// directory outside the home, the global skills (the helpers too), hooks and
// hint for Claude Code go there and nothing goes in <home>/.claude, the shared
// agents skills stay in the home, and doctor finds each of them installed
// where it is and says which directory it checked. Doctor without the
// variable looks in the home and finds Claude Code's missing; each uninstall
// takes them out of the same directory.
func TestGlobalPiecesFollowClaudeConfigDir(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	claudeDir := filepath.Join(ie.root, "claude-work")
	plain := ie.env.inDir(ie.root)
	e := plain.withVar(claudeConfigDirEnv, claudeDir)
	homeClaude := filepath.Join(ie.userHome, ".claude")

	for _, args := range [][]string{{"skills", "--global"}, {"skills", "--global", "--helpers"}, {"hooks", "--global", "--yes"}, {"hint", "--global", "--yes"}} {
		if out, code := runMain(t, e, "", args...); code != 0 || !strings.Contains(out, "result: installed") {
			t.Fatalf("jig %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
	}
	for _, name := range []string{"here", "jig-init"} {
		if !exists(filepath.Join(claudeDir, "skills", name, "SKILL.md")) {
			t.Errorf("skill %s is not in Claude Code's directory", name)
		}
		if !exists(filepath.Join(ie.userHome, ".agents", "skills", name, "SKILL.md")) {
			t.Errorf("the shared agents copy of %s left the home", name)
		}
	}
	if !strings.Contains(readFileOrFail(t, filepath.Join(claudeDir, "settings.json")), "observe") {
		t.Error("the hooks are not in Claude Code's settings.json")
	}
	if !strings.Contains(readFileOrFail(t, filepath.Join(claudeDir, "CLAUDE.md")), "jig here") {
		t.Error("the hint is not in Claude Code's CLAUDE.md")
	}
	if exists(homeClaude) {
		t.Errorf("a global install wrote into %s, which Claude Code does not read", homeClaude)
	}

	out, code := runMain(t, e, "", "doctor")
	if code != 0 {
		t.Fatalf("doctor: exit %d\n%s", code, out)
	}
	if want := claudeConfigDirEnv + " is set: Claude Code's skills, hooks and hint are checked in " + claudeDir; !strings.Contains(out, want) {
		t.Errorf("doctor does not say which directory it checked (%q):\n%s", want, out)
	}
	rows := doctorRows(t, out)
	for _, key := range [][2]string{{"skills", "claude"}, {"skills", "agents"}, {"helpers", ""}, {"hooks", "claude"}, {"hint", "claude"}} {
		if r := findRow(rows, key[0], key[1]); r["state"] != "installed" || r["fix"] != "" {
			t.Errorf("doctor with %s set: %s,%s = %v, want installed\n%s", claudeConfigDirEnv, key[0], key[1], r, out)
		}
	}
	out, _ = runMain(t, plain, "", "doctor")
	if strings.Contains(out, claudeConfigDirEnv) {
		t.Errorf("doctor without the variable notes it:\n%s", out)
	}
	rows = doctorRows(t, out)
	for _, piece := range []string{"skills", "hooks", "hint"} {
		if r := findRow(rows, piece, "claude"); r["state"] != "missing" {
			t.Errorf("doctor without %s: %s,claude = %v, want missing in the home\n%s", claudeConfigDirEnv, piece, r, out)
		}
	}

	for _, args := range [][]string{{"skills", "--global", "--uninstall"}, {"hooks", "--global", "--uninstall", "--yes"}, {"hint", "--global", "--uninstall", "--yes"}} {
		if out, code := runMain(t, e, "", args...); code != 0 || !strings.Contains(out, "result: removed") {
			t.Fatalf("jig %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
	}
	if exists(filepath.Join(claudeDir, "skills", "here")) || exists(filepath.Join(claudeDir, "CLAUDE.md")) {
		t.Error("the uninstall left jig's skills or hint in Claude Code's directory")
	}
	if strings.Contains(readFileOrFail(t, filepath.Join(claudeDir, "settings.json")), "observe") {
		t.Error("the uninstall left jig's hooks in Claude Code's settings.json")
	}

	// The older install verb defaults to the same directory.
	if out, code := runMain(t, e, "", "skills", "install"); code != 0 || !exists(filepath.Join(claudeDir, "skills", "here", "SKILL.md")) || exists(homeClaude) {
		t.Errorf("skills install with %s set: exit %d, want the skills in %s\n%s", claudeConfigDirEnv, code, claudeDir, out)
	}
}
