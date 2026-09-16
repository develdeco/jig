// Package lint enforces the mechanical budgets on jig's agent-facing docs:
// each skills/<dir>/SKILL.md's size and frontmatter, the router's intent
// table, and (once it exists) ARCHITECTURE.md's module table against the
// real package layout.
package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the repository root, found relative to this file via
// runtime.Caller so the test works regardless of the working directory it
// is run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("lint: runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

// frontmatter splits a SKILL.md's leading "---\n...\n---" block from the
// rest of the file and returns its raw lines.
func frontmatter(t *testing.T, path string, data []byte) []string {
	t.Helper()
	text := string(normalizeEOL(data))
	if !strings.HasPrefix(text, "---\n") {
		t.Fatalf("%s: missing opening --- frontmatter fence", path)
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end == -1 {
		t.Fatalf("%s: missing closing --- frontmatter fence", path)
	}
	return strings.Split(rest[:end], "\n")
}

// normalizeEOL strips carriage returns so the lint checks measure the
// committed content, not the checkout's line-ending conversion (a CI
// runner may materialize CRLF).
func normalizeEOL(data []byte) []byte {
	return []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
}

func TestSkillsBudget(t *testing.T) {
	root := repoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob skills/*/SKILL.md: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("lint: no skills/*/SKILL.md found")
	}

	for _, path := range matches {
		path := path
		dir := filepath.Base(filepath.Dir(path))
		t.Run(dir, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			if lines := strings.Count(string(normalizeEOL(data)), "\n"); lines > 100 {
				t.Errorf("%s: %d lines, budget is <=100", path, lines)
			}
			if n := len(normalizeEOL(data)); n > 6000 {
				t.Errorf("%s: %d bytes, budget is <=6000", path, n)
			}

			fm := frontmatter(t, path, normalizeEOL(data))
			var name, description string
			for _, line := range fm {
				switch {
				case strings.HasPrefix(line, "name: "):
					name = strings.TrimPrefix(line, "name: ")
				case strings.HasPrefix(line, "description: "):
					description = strings.TrimPrefix(line, "description: ")
				}
			}
			if name == "" {
				t.Errorf("%s: frontmatter missing name:", path)
			}
			if name != dir {
				t.Errorf("%s: frontmatter name %q does not match directory %q", path, name, dir)
			}
			if description == "" {
				t.Errorf("%s: frontmatter missing description:", path)
			}
			if words := len(strings.Fields(description)); words > 50 {
				t.Errorf("%s: description is %d words, budget is <=50", path, words)
			}
		})
	}
}

func TestRouterTable(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "skills", "router", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(normalizeEOL(data))

	for _, phrase := range []string{
		"fresh work",
		"status ask",
		"external work product",
		"many tickets",
		"process defect",
		"recall",
		"validator side only",
	} {
		if !strings.Contains(text, phrase) {
			t.Errorf("%s: missing intent phrase %q", path, phrase)
		}
	}

	rows := map[string]string{
		"pr-mode": "here's the contractor's PR",
		"fleet":   "work the backlog today",
		"retro":   "it keeps making that same mistake",
	}
	for label, rowText := range rows {
		idx := strings.Index(text, rowText)
		if idx == -1 {
			t.Errorf("%s: missing %s row (looked for %q)", path, label, rowText)
			continue
		}
		lineEnd := strings.Index(text[idx:], "\n")
		if lineEnd == -1 {
			lineEnd = len(text) - idx
		}
		line := text[idx : idx+lineEnd]
		if !strings.Contains(line, "v0.2") {
			t.Errorf("%s: %s row missing v0.2 annotation:\n%s", path, label, line)
		}
	}
}

// moduleRowRE matches a "## Module responsibilities" table row whose first
// cell is a package dir like "store/".
var moduleRowRE = regexp.MustCompile(`^\|\s*` + "`" + `?([a-zA-Z0-9_]+)/` + "`" + `?\s*\|`)

func TestArchitectureDoc(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "ARCHITECTURE.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("ARCHITECTURE.md does not exist yet")
		}
		t.Fatalf("read %s: %v", path, err)
	}

	lines := strings.Split(string(normalizeEOL(data)), "\n")
	heading := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "## Module responsibilities" {
			heading = i
			break
		}
	}
	if heading == -1 {
		t.Fatalf("%s: no \"## Module responsibilities\" heading", path)
	}

	found := 0
	for _, line := range lines[heading+1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			break // next section
		}
		m := moduleRowRE.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		found++
		pkgDir := filepath.Join(root, m[1])
		if info, err := os.Stat(pkgDir); err != nil || !info.IsDir() {
			t.Errorf("%s: module row names %q, no such directory", path, m[1]+"/")
		}
	}
	if found == 0 {
		t.Fatalf("%s: \"## Module responsibilities\" table has no package-dir rows", path)
	}
}
