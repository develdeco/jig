package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The build log is one file per entry under docs/build-log/, named by ticket
// id or a lowercase slug, so two branches never touch the same file. This
// checks structure only, never prose.
func TestBuildLogFolder(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	name := regexp.MustCompile(`^([A-Za-z]+-[0-9]+|[a-z0-9]+(-[a-z0-9]+)*)\.md$`)

	entries, err := os.ReadDir(filepath.Join(root, "docs", "build-log"))
	if err != nil {
		t.Fatalf("docs/build-log must exist: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !name.MatchString(e.Name()) {
			t.Errorf("docs/build-log/%s: want a .md named by a ticket id or a lowercase slug", e.Name())
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, "docs", "build-log", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(b), "# ") {
			t.Errorf("docs/build-log/%s: first line must start with \"# \"", e.Name())
		}
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
