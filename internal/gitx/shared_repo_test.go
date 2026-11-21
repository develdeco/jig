package gitx

import (
	"os"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// sharedRepos holds the repositories built once for the whole test binary: a
// test that only reads one uses it as it is, and one that changes it takes a
// copy of the files (copyRepo), which costs no git process, so the suite does
// not start git again for each test's setup.
var sharedRepos struct {
	sync.Mutex
	dirs map[string]string
}

// sharedRepo returns the directory of the repository named key, building it
// with build (which is given the empty directory to make it in) on first use.
func sharedRepo(t *testing.T, key string, build func(t *testing.T, dir string)) string {
	t.Helper()
	sharedRepos.Lock()
	defer sharedRepos.Unlock()
	if dir, ok := sharedRepos.dirs[key]; ok {
		return dir
	}
	dir, err := os.MkdirTemp("", "jig-gitx-shared")
	if err != nil {
		t.Fatal(err)
	}
	gittest.AtExit(func() { os.RemoveAll(dir) })
	build(t, dir)
	if sharedRepos.dirs == nil {
		sharedRepos.dirs = map[string]string{}
	}
	sharedRepos.dirs[key] = dir
	return dir
}

// streamRepo is a repository on main holding the commits of a fast-import
// stream: one git init and one fast-import, once per binary.
func streamRepo(t *testing.T, key, stream string) string {
	t.Helper()
	return sharedRepo(t, key, func(t *testing.T, dir string) {
		if _, err := Run(dir, "init", "-q", "-b", "main"); err != nil {
			t.Fatal(err)
		}
		if _, err := RunInput(dir, []byte(stream), "fast-import", "--quiet"); err != nil {
			t.Fatalf("fast-import: %v", err)
		}
	})
}

// copyRepo is a copy of the repository at src in a directory of the test's
// own, made with no git process. A repository whose files name no absolute
// path (a plain init and a fast-import) copies whole.
func copyRepo(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy the repository %s: %v", src, err)
	}
	return dst
}
