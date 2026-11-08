package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// buildLogName is a build-log file name: a ticket id (letters and digits in
// the key, then a number) or a lowercase slug, then .md.
var buildLogName = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*-[0-9]+|[a-z0-9]+(-[a-z0-9]+)*)\.md$`)

// buildLogEntryProblem says what is wrong with one build-log entry, given its
// file name, whether it is a directory and its content, or "" when it is
// fine. The structure only, never the prose.
func buildLogEntryProblem(name string, isDir bool, content string) string {
	if isDir || !buildLogName.MatchString(name) {
		return "want a .md named by a ticket id or a lowercase slug"
	}
	if !strings.HasPrefix(content, "# ") {
		return `first line must start with "# "`
	}
	return ""
}

// buildLogCollisions names each pair of entries whose names are equal once
// case is folded: a ticket id and its lowercase slug are one path on Windows
// and macOS, though two on Linux.
func buildLogCollisions(names []string) []string {
	seen := map[string]string{}
	var out []string
	for _, n := range names {
		key := strings.ToLower(n)
		if first, ok := seen[key]; ok {
			out = append(out, first+" and "+n)
			continue
		}
		seen[key] = n
	}
	return out
}

// The build log is one file per entry under docs/build-log/, named by ticket
// id or a lowercase slug, so two branches never touch the same file. This
// checks structure only, never prose.
func TestBuildLogFolder(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	entries, err := os.ReadDir(filepath.Join(root, "docs", "build-log"))
	if err != nil {
		t.Fatalf("docs/build-log must exist: %v", err)
	}
	var names []string
	for _, e := range entries {
		var content string
		if !e.IsDir() {
			b, err := os.ReadFile(filepath.Join(root, "docs", "build-log", e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			content = string(b)
		}
		if p := buildLogEntryProblem(e.Name(), e.IsDir(), content); p != "" {
			t.Errorf("docs/build-log/%s: %s", e.Name(), p)
		}
		names = append(names, e.Name())
	}
	for _, c := range buildLogCollisions(names) {
		t.Errorf("docs/build-log: %s are the same file on a case-insensitive file system", c)
	}

	for _, f := range []string{"AGENTS.md", ".github/CONTRIBUTING.md", "README.md"} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "docs/build-log/") {
			t.Errorf("%s must name docs/build-log/", f)
		}
	}
}

// TestBuildLogEntryRules holds the rules to cases: each good entry passes and
// each bad one is rejected, so weakening a rule fails a case.
func TestBuildLogEntryRules(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		file    string
		isDir   bool
		content string
		ok      bool
	}{
		{"a ticket id", "DEV-9.md", false, "# DEV-9\n", true},
		{"a ticket id whose key has a digit", "I18N-1.md", false, "# I18N-1\n", true},
		{"a lowercase slug", "fix-the-thing.md", false, "# Fix\n", true},
		{"a slug with digits", "bump-2-go.md", false, "# Bump\n", true},
		{"no .md", "DEV-9", false, "# DEV-9\n", false},
		{"another extension", "DEV-9.txt", false, "# DEV-9\n", false},
		{"a subdirectory", "DEV-9.md", true, "", false},
		{"a space in the name", "dev 9.md", false, "# x\n", false},
		{"an underscore", "fix_it.md", false, "# x\n", false},
		{"a mixed-case slug", "Fix-It.md", false, "# x\n", false},
		{"a key starting with a digit", "9X-1.md", false, "# x\n", false},
		{"a ticket id with no number", "DEV-x.md", false, "# x\n", false},
		{"a first line that is not a heading", "DEV-9.md", false, "DEV-9\n", false},
		{"a heading with no space after the hash", "DEV-9.md", false, "#DEV-9\n", false},
		{"an empty file", "DEV-9.md", false, "", false},
	} {
		if got := buildLogEntryProblem(c.file, c.isDir, c.content); (got == "") != c.ok {
			t.Errorf("%s: problem = %q, want ok = %v", c.name, got, c.ok)
		}
	}
}

// TestBuildLogRejectsNamesEqualOnceCaseFolded: a ticket id and its lowercase
// slug are one path on two of the three platforms.
func TestBuildLogRejectsNamesEqualOnceCaseFolded(t *testing.T) {
	t.Parallel()
	if got := buildLogCollisions([]string{"DEV-9.md", "RUN-16.md", "dev-9.md"}); len(got) != 1 || !strings.Contains(got[0], "DEV-9.md") || !strings.Contains(got[0], "dev-9.md") {
		t.Errorf("collisions = %q, want DEV-9.md and dev-9.md", got)
	}
	if got := buildLogCollisions([]string{"DEV-9.md", "DEV-10.md", "fix-it.md"}); len(got) != 0 {
		t.Errorf("collisions = %q, want none", got)
	}
}
