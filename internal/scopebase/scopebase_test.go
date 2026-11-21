package scopebase

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=f@example.invalid", "-c", "user.name=f"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestBranchForTakesTheBaseOnlyWhenTheCloneHoldsIt: a container's branch is the
// base the change is measured from once origin/<base> is in the clone, and the
// target before, or with none named.
func TestBranchForTakesTheBaseOnlyWhenTheCloneHoldsIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	origin, clone := filepath.Join(root, "o.git"), filepath.Join(root, "c")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, root, "init", "-b", "main", clone)
	git(t, clone, "remote", "add", "origin", origin)
	git(t, clone, "commit", "--allow-empty", "-m", "base")
	git(t, clone, "push", "origin", "main", "main:refs/heads/I-1-issue")
	git(t, clone, "fetch", "origin")

	for _, c := range []struct{ name, dir, base, want string }{
		{"no base named", clone, "", "main"},
		{"the target itself", clone, "main", "main"},
		{"a base the clone holds", clone, "I-1-issue", "I-1-issue"},
		{"a base the clone lacks", clone, "I-2-issue", "main"},
		{"no clone", "", "I-1-issue", "main"},
	} {
		if got := BranchFor(c.dir, "main", c.base); got != c.want {
			t.Errorf("%s: BranchFor = %q, want %q", c.name, got, c.want)
		}
	}
}
