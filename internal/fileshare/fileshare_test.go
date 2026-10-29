package fileshare_test

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/gitx"
)

// git runs the git program in dir, with an identity for its commits.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, append([]string{"-c", "user.name=fileshare-test", "-c", "user.email=test@example.invalid"}, args...)...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// TestGitReplacesAndRemovesARefHeldOpen holds two ref files open with Open
// while git moves a new value into place over one and deletes the other, as
// a user's git does beside a jig that reads the repository: both go ahead,
// and the holder still reads the bytes it opened. Held with os.Open on
// Windows, git fails both after its retries.
func TestGitReplacesAndRemovesARefHeldOpen(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	git(t, dir, "branch", "side")
	two, one := git(t, dir, "rev-parse", "HEAD"), git(t, dir, "rev-parse", "HEAD^")
	main := filepath.Join(dir, ".git", "refs", "heads", "main")
	held, err := fileshare.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	side, err := fileshare.Open(filepath.Join(dir, ".git", "refs", "heads", "side"))
	if err != nil {
		t.Fatal(err)
	}
	defer side.Close()

	git(t, dir, "update-ref", "refs/heads/main", one)
	git(t, dir, "branch", "-D", "side")

	if got, err := fileshare.ReadFile(main); err != nil || strings.TrimSpace(string(got)) != one {
		t.Errorf("ReadFile after the update = %q, %v, want %s", got, err, one)
	}
	if got, err := io.ReadAll(held); err != nil || strings.TrimSpace(string(got)) != two {
		t.Errorf("the holder read %q, %v, want the %s it opened", got, err, two)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "refs", "heads", "side")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the deleted branch's file: %v, want it gone", err)
	}
}

// TestAMissingFileIsNotExist checks that a missing file and a missing
// directory fail the way os.Open fails them, so callers tell absence apart.
func TestAMissingFileIsNotExist(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{filepath.Join(dir, "none"), filepath.Join(dir, "no", "such")} {
		if _, err := fileshare.ReadFile(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(%s) = %v, want fs.ErrNotExist", p, err)
		}
	}
}
