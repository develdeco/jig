package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// TestOwnSourcesAreTheFilesTheTicketAddedChangedOrRetyped: the set is the
// files added, modified or type-changed from the merge base of the head with
// origin/<base>. A deleted file is not in it and a renamed one is there under
// its new path only. Three things are pinned by history the plain answers
// would get wrong:
//   - main moves on after the branch point and modifies same_test.go, which
//     the branch never touched: a diff from origin/main itself would list it,
//     only the merge base keeps it out;
//   - the local main is left behind the branch point, while c1_test.go was
//     added on origin/main before it: only origin/<base> keeps it out;
//   - link_test.go is a regular file that the branch turns into a symlink (a
//     type change, written to the index directly so it needs no symlink
//     support): the T of the filter holds it.
func TestOwnSourcesAreTheFilesTheTicketAddedChangedOrRetyped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
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
	commit := func(msg string) {
		t.Helper()
		run(t, dir, "add", "-A")
		if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "--allow-empty", "-m", msg); err != nil {
			t.Fatal(err)
		}
	}
	write("a_test.go", "a\n")
	write("gone_test.go", "gone\n")
	write("old_test.go", "a long enough body to rename\nline2\nline3\n")
	write("same_test.go", "same\n")
	write("link_test.go", "regular\n")
	commit("c0")
	// The local main stays here.
	run(t, dir, "checkout", "-q", "-b", "work")
	write("c1_test.go", "added on main before the branch point\n")
	commit("c1")
	run(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")

	write("a_test.go", "a2\n")
	write("new_test.go", "new\n")
	run(t, dir, "rm", "-q", "gone_test.go")
	run(t, dir, "mv", "old_test.go", "moved_test.go")
	commit("work")
	// Turn link_test.go into a symlink entry without touching the filesystem.
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("a_test.go"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha := run(t, dir, "hash-object", "-w", target)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "update-index", "--add", "--cacheinfo", "120000,"+sha+",link_test.go")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", "link"); err != nil {
		t.Fatal(err)
	}
	head := run(t, dir, "rev-parse", "HEAD")
	if got := run(t, dir, "diff", "--name-only", "--diff-filter=T", "origin/main", head); strings.TrimSpace(got) != "link_test.go" {
		t.Fatalf("link_test.go is not a type change on the branch: %q", got)
	}

	// Main moves on after the branch point; the local main does not follow.
	run(t, dir, "checkout", "-q", "-f", "-b", "later", "origin/main")
	write("same_test.go", "same, changed on main\n")
	write("later_test.go", "later\n")
	commit("main moves on")
	run(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	run(t, dir, "checkout", "-q", "-f", "work")

	own, err := ownSources(dir, "main", head)
	if err != nil {
		t.Fatalf("ownSources: %v", err)
	}
	var got []string
	for f := range own {
		got = append(got, f)
	}
	sort.Strings(got)
	want := []string{"a_test.go", "link_test.go", "moved_test.go", "new_test.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("own = %v, want %v (not the deleted, the unchanged, the old name, c1_test.go, or what main changed after the branch point)", got, want)
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

// TestHasRecordingsIsTrueOnlyForARecordedLineThatWroteFiles: a ticket with
// nothing recorded (or only refused or empty lines) has no pick to make, so
// publish does not compute its own files at all.
func TestHasRecordingsIsTrueOnlyForARecordedLineThatWroteFiles(t *testing.T) {
	t.Parallel()
	rec := journal.Line{Event: "recorded", Recordings: []journal.Recording{{File: "a.svg"}}}
	cases := []struct {
		name  string
		lines []journal.Line
		want  bool
	}{
		{"no lines", nil, false},
		{"an oracle line", []journal.Line{{Event: "oracle"}}, false},
		{"a recorded line with no files", []journal.Line{{Event: "recorded", Outcome: "refused: x"}}, false},
		{"a recorded line with files", []journal.Line{{Event: "oracle"}, rec}, true},
	}
	for _, c := range cases {
		if got := hasRecordings(c.lines); got != c.want {
			t.Errorf("%s: hasRecordings = %v, want %v", c.name, got, c.want)
		}
	}
}
