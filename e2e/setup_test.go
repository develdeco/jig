package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// markProject writes a jig.yaml marker into the clone and commits it, so a
// git status afterwards shows only what a test adds.
func (r *hookRepo) markProject(marker string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.clone, "jig.yaml"), []byte(marker), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "mark the project")
}

func (r *hookRepo) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(r.clone, filepath.FromSlash(rel)))
	return err == nil
}

// TestAttachRefusesOutsideProject: jig attach is setup --here for a folder in
// a jig project, which has a jig.yaml marker at or above it. A repository
// without one, and a folder in no repository, are refused with NOT_IN_PROJECT
// and nothing is written; in a project it sets the project up.
func TestAttachRefusesOutsideProject(t *testing.T) {
	t.Parallel()
	r := newHookRepo(t)

	for _, dir := range []string{r.clone, r.work} {
		res := r.jigIn(dir, "attach", "--yes")
		if res.Code != 1 || !strings.Contains(res.Stdout, "code: NOT_IN_PROJECT") {
			t.Errorf("jig attach --yes in %s = exit %d, want 1 and NOT_IN_PROJECT\n%s%s", dir, res.Code, res.Stdout, res.Stderr)
		}
	}
	if r.exists(".claude") || r.exists("CLAUDE.local.md") {
		t.Fatal("a refused attach wrote into the repository")
	}
	if _, err := os.Stat(r.env.userHome + string(filepath.Separator) + ".claude"); !os.IsNotExist(err) {
		t.Fatalf("a refused attach wrote into the home directory: %v", err)
	}

	// The same repository once it carries the marker.
	r.markProject("project: demo\nkind: project\n")
	sub := filepath.Join(r.clone, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res := r.jigIn(sub, "attach", "--yes")
	if res.Code != 0 || !strings.Contains(res.Stdout, "result: installed") {
		t.Fatalf("jig attach in a project = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	if !r.exists(".claude/settings.local.json") || !r.exists(".claude/skills/jig/SKILL.md") {
		t.Error("attach did not set up the folder of the marker")
	}
	if _, err := os.Stat(filepath.Join(sub, ".claude")); !os.IsNotExist(err) {
		t.Error("attach wrote in the folder it was run from, not the project's")
	}
}

func (r *hookRepo) jigIn(dir string, args ...string) jigResult {
	r.t.Helper()
	return runJig(r.t, r.env, dir, args...)
}

// TestDoctorNamesMissingPieces: with only the skills installed, the report
// names the hooks, the git hooks and the hint as missing, each with the
// command that adds it; those commands, run as written, make the report clean.
func TestDoctorNamesMissingPieces(t *testing.T) {
	t.Parallel()
	r := newHookRepo(t)
	nowhere := r.work // in no project and no repository

	if res := r.jigIn(nowhere, "skills", "--global"); res.Code != 0 || !strings.Contains(res.Stdout, "result: installed") {
		t.Fatalf("jig skills --global = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	res := r.jigIn(nowhere, "doctor")
	if res.Code != 0 {
		t.Fatalf("jig doctor = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	rows := piecesTable(t, res.Stdout)
	want := map[string]string{
		"hooks,claude": "jig hooks --global --yes",
		"githook,":     "jig githook --global",
		"hint,claude":  "jig hint --global --yes",
	}
	for key, fix := range want {
		row, ok := rows[key]
		if !ok {
			t.Errorf("the report has no %s row:\n%s", key, res.Stdout)
			continue
		}
		if row.state != "missing" || row.fix != fix {
			t.Errorf("%s: state %q, fix %q, want missing and %q", key, row.state, row.fix, fix)
		}
	}
	for _, key := range []string{"skills,claude", "skills,agents"} {
		if row, ok := rows[key]; !ok || row.state != "installed" || row.fix != "" {
			t.Errorf("%s = %+v, want installed with nothing to fix", key, row)
		}
	}

	for _, fix := range want {
		args := strings.Fields(fix)[1:]
		if res := r.jigIn(nowhere, args...); res.Code != 0 {
			t.Fatalf("%s = exit %d\n%s%s", fix, res.Code, res.Stdout, res.Stderr)
		}
	}
	res = r.jigIn(nowhere, "doctor")
	for key, row := range piecesTable(t, res.Stdout) {
		if !strings.HasPrefix(key, "helpers") && row.state != "installed" {
			t.Errorf("after the fixes %s = %+v", key, row)
		}
	}
}

type pieceRow struct{ state, fix string }

// piecesTable reads the pieces table of a doctor report: row key is
// "piece,harness". The cells before state hold no comma; the ones after it
// may be quoted.
func piecesTable(t *testing.T, out string) map[string]pieceRow {
	t.Helper()
	rows := map[string]pieceRow{}
	in := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "pieces["):
			in = true
		case in && strings.HasPrefix(line, "  "):
			cells := splitRow(strings.TrimPrefix(line, "  "))
			if len(cells) != 7 {
				t.Fatalf("a pieces row of %d cells, want 7: %q", len(cells), line)
			}
			rows[cells[0]+","+cells[1]] = pieceRow{state: cells[3], fix: cells[6]}
		default:
			in = false
		}
	}
	return rows
}

func splitRow(line string) []string {
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

// TestSetupProjectUntrackedExcludes: jig setup in its untracked form puts the
// stubs, the project-local settings and CLAUDE.local.md in the folder and
// lists each in the repository's .git/info/exclude, so git status stays clean
// and git add -A takes none of them; --uninstall takes them and their lines
// away, and the person's own lines and files stay.
func TestSetupProjectUntrackedExcludes(t *testing.T) {
	t.Parallel()
	r := newHookRepo(t)
	r.markProject("project: demo\nkind: project\n")

	// The person's own exclude line and settings file.
	exclude := filepath.Join(r.clone, ".git", "info", "exclude")
	mine, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, append(mine, []byte("scratch/\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.clone, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.clone, ".claude", "settings.json"), []byte("{\"model\": \"opus\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "team settings")

	res := r.jigIn(r.clone, "setup", "--here", "--artifacts", "--yes")
	if res.Code != 0 || !strings.Contains(res.Stdout, "result: installed") {
		t.Fatalf("jig setup --here --artifacts --yes = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	files := []string{".claude/skills/jig/SKILL.md", ".agents/skills/jig/SKILL.md", ".claude/settings.local.json", "CLAUDE.local.md"}
	for _, f := range files {
		if !r.exists(f) {
			t.Errorf("%s was not written", f)
		}
		if out := r.git("check-ignore", "-v", "--", f); !strings.Contains(out, "info/exclude") {
			t.Errorf("%s is ignored by %q, want .git/info/exclude", f, out)
		}
	}
	if got := r.git("status", "--porcelain", "-uall"); got != "" {
		t.Errorf("git status after setup:\n%s\nwant a clean repository", got)
	}
	r.git("add", "-A")
	if got := r.git("diff", "--cached", "--name-only"); got != "" {
		t.Errorf("git add -A staged %q", got)
	}
	ex, _ := os.ReadFile(exclude)
	for _, f := range files {
		if !strings.Contains(string(ex), "/"+f+"\n") {
			t.Errorf(".git/info/exclude lacks /%s:\n%s", f, ex)
		}
	}
	if !strings.Contains(string(ex), "scratch/\n") {
		t.Errorf("the person's exclude line is gone:\n%s", ex)
	}
	// The team's settings.json is not jig's and is untouched.
	if got, _ := os.ReadFile(filepath.Join(r.clone, ".claude", "settings.json")); string(got) != "{\"model\": \"opus\"}\n" {
		t.Errorf("the team's settings.json changed: %q", got)
	}

	// Running it again changes nothing.
	if res := r.jigIn(r.clone, "setup", "--here", "--artifacts", "--yes"); res.Code != 0 || !strings.Contains(res.Stdout, "result: unchanged") {
		t.Errorf("second setup = exit %d\n%s", res.Code, res.Stdout)
	}

	res = r.jigIn(r.clone, "setup", "--here", "--artifacts", "--uninstall", "--yes")
	if res.Code != 0 || !strings.Contains(res.Stdout, "result: removed") {
		t.Fatalf("uninstall = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	for _, f := range files {
		if r.exists(f) {
			t.Errorf("%s is still there after the uninstall", f)
		}
	}
	if !r.exists(".claude/settings.json") {
		t.Error("the uninstall removed the team's settings.json")
	}
	ex, _ = os.ReadFile(exclude)
	if strings.Contains(string(ex), "jig") || !strings.Contains(string(ex), "scratch/\n") {
		t.Errorf(".git/info/exclude after the uninstall:\n%s", ex)
	}
	if got := r.git("status", "--porcelain", "-uall"); got != "" {
		t.Errorf("git status after the uninstall:\n%s", got)
	}

	// The tracked form is for the team: nothing is excluded.
	if res := r.jigIn(r.clone, "setup", "--here", "--skills", "--hint", "--tracked", "--yes"); res.Code != 0 {
		t.Fatalf("tracked setup = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	status := r.git("status", "--porcelain", "-uall")
	for _, f := range []string{".claude/skills/here/SKILL.md", ".agents/skills/here/SKILL.md", "AGENTS.md"} {
		if !strings.Contains(status, f) {
			t.Errorf("git status does not show the tracked file %s:\n%s", f, status)
		}
	}
	ex, _ = os.ReadFile(exclude)
	if strings.Contains(string(ex), "jig") {
		t.Errorf("the tracked form excluded something:\n%s", ex)
	}
}
