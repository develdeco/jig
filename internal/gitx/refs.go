package gitx

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RefExists reports whether the fully spelled ref (refs/heads/claims,
// refs/remotes/origin/claims) exists in the repository whose work tree is
// dir. A directory with no repository of its own has no refs and reports
// false.
//
// A store asks this on every Sync, Push and Claim, which cannot each cost a
// git process, so for an ordinary clone it reads the ref's loose file and then
// packed-refs. A linked worktree or submodule (.git is a file) and a
// repository that keeps its refs in the reftable format are asked of git.
func RefExists(dir, ref string) (bool, error) {
	gitDir := filepath.Join(dir, ".git")
	info, err := os.Stat(gitDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case !info.IsDir() || exists(filepath.Join(gitDir, "reftable")):
		return refExistsByGit(dir, ref)
	}
	if fi, err := os.Stat(filepath.Join(gitDir, filepath.FromSlash(ref))); err == nil && !fi.IsDir() {
		return true, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	f, err := os.Open(filepath.Join(gitDir, "packed-refs"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		if _, name, ok := strings.Cut(line, " "); ok && name == ref {
			return true, nil
		}
	}
	return false, sc.Err()
}

// refExistsByGit asks git whether ref exists: `show-ref --verify --quiet`
// exits 1 for a ref that does not.
func refExistsByGit(dir, ref string) (bool, error) {
	_, err := Run(dir, "show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
