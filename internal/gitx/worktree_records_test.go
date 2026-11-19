package gitx

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// worktreeStream is one commit on main, the repository the removals below are
// made in.
const worktreeStream = `commit refs/heads/main
committer t <t@example.invalid> 1700000000 +0000
data 4
base
M 100644 inline f.txt
data 2
f

`

// recordIDs lists the names of repo's worktree records.
func recordIDs(t *testing.T, repo string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, ".git", "worktrees"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Name())
	}
	sort.Strings(ids)
	return ids
}

// recordOf is the name of the record of the worktree at dir.
func recordOf(t *testing.T, repo, dir string) string {
	t.Helper()
	for _, id := range recordIDs(t, repo) {
		b, err := os.ReadFile(filepath.Join(repo, ".git", "worktrees", id, "gitdir"))
		if err != nil {
			continue
		}
		target := filepath.FromSlash(strings.TrimSpace(string(b)))
		if !filepath.IsAbs(target) {
			target = filepath.Join(repo, ".git", "worktrees", id, target)
		}
		if SameDir(filepath.Dir(target), dir) {
			return id
		}
	}
	t.Fatalf("no worktree record names %s: %v", dir, recordIDs(t, repo))
	return ""
}
