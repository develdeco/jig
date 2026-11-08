package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// ownRepo makes a repo whose origin/main holds base files, then applies edit
// on a branch and commits it; it returns the repo and the branch head.
func ownRepo(t *testing.T, base map[string]string, edit func(dir string)) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	run(t, dir, "init", "-b", "main")
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range base {
		write(name, content)
	}
	run(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "--allow-empty", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	run(t, dir, "checkout", "-b", "work")
	edit(dir)
	run(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "--allow-empty", "-m", "work"); err != nil {
		t.Fatal(err)
	}
	return dir, run(t, dir, "rev-parse", "HEAD")
}

// TestOwnSourcesAreTheFilesTheTicketAddedChangedOrRetyped: the set is the
// files added, modified or type-changed from the merge base with the base
// branch; a deleted file is not in it, and a renamed one is there under its
// new path only. A commit on the base branch after the branch point adds
// nothing.
func TestOwnSourcesAreTheFilesTheTicketAddedChangedOrRetyped(t *testing.T) {
	t.Parallel()
	dir, head := ownRepo(t,
		map[string]string{"a_test.go": "a\n", "gone_test.go": "gone\n", "old_test.go": "a long enough body to rename\nline2\nline3\n", "same_test.go": "same\n"},
		func(dir string) {
			if err := os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("a2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "new_test.go"), []byte("new\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			run(t, dir, "rm", "-q", "gone_test.go")
			run(t, dir, "mv", "old_test.go", "moved_test.go")
		})
	run(t, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "later_test.go"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", "main moves on"); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")

	own, err := ownSources(dir, "main", head)
	if err != nil {
		t.Fatalf("ownSources: %v", err)
	}
	var got []string
	for f := range own {
		got = append(got, f)
	}
	sort.Strings(got)
	want := []string{"a_test.go", "moved_test.go", "new_test.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("own = %v, want %v (not the deleted, the unchanged, the old name or main's later file)", got, want)
	}
}

// TestOwnRecordingsDropWhatTheTicketDidNotMake: a recording is kept only when
// its source is a file in the set; one with no source (an old journal line) or
// a source the ticket did not touch is left out.
func TestOwnRecordingsDropWhatTheTicketDidNotMake(t *testing.T) {
	t.Parallel()
	mk := func(name, source string) pickCandidate {
		c := pickCandidate{}
		c.rec.File, c.rec.Source = name, source
		return c
	}
	all := []pickCandidate{
		mk("own.svg", "e2e/a_test.go"),
		mk("dotted.svg", "./e2e//a_test.go"),
		mk("old.svg", ""),
		mk("other.svg", "e2e/b_test.go"),
	}
	got := ownRecordings(all, map[string]bool{"e2e/a_test.go": true})
	var names []string
	for _, c := range got {
		names = append(names, c.rec.File)
	}
	if want := []string{"own.svg", "dotted.svg"}; !reflect.DeepEqual(names, want) {
		t.Errorf("kept = %v, want %v", names, want)
	}
	if got := ownRecordings(all, nil); len(got) != 0 {
		t.Errorf("with no changed file kept %v, want none", got)
	}
}
