package gitx

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// removedRepo is a repo with the files written, and helpers to change them and
// commit.
func removedRepo(t *testing.T, files map[string]string) (dir string, write func(rel, content string), commit func(msg string) string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := Run(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	write = func(rel, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit = func(msg string) string {
		t.Helper()
		git("add", "-A")
		git("commit", "-m", msg)
		sha, err := RevParse(dir, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	git("init", "-b", "main")
	git("config", "user.name", "jig-fixture")
	git("config", "user.email", "fixture@example.invalid")
	for rel, content := range files {
		write(rel, content)
	}
	return dir, write, commit
}

// TestRemovedSentences: a removed line gives each of its sentences of eight or
// more words that ends in a stop, a bang or a question mark; a short one, one
// with no final mark and text after the last mark are left; a deleted file
// gives its sentences too; an added line and a binary file give none.
func TestRemovedSentences(t *testing.T) {
	dir, write, commit := removedRepo(t, map[string]string{
		"a.txt": "The quick brown fox jumps over the lazy dog today. Short one here. Is this a long enough question to be taken?\n" +
			"This line has many words but no final mark at all\n" +
			"No stop inside.word mid and then eight words are here yes ok. tail words without a mark here\n" +
			"kept line stays put\n",
		"gone.txt": "A whole file that is deleted keeps its sentences as removed ones!\n",
		"blob.bin": "a\x00b. one two three four five six seven eight nine.\n",
	})
	base := commit("c1")
	write("a.txt", "kept line stays put\nA brand new line with plenty of words in it to count as added.\n")
	write("blob.bin", "a\x00c. one two three four five six seven eight nine.\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	head := commit("c2")

	got, err := RemovedSentences(dir, base, head, 8)
	if err != nil {
		t.Fatalf("RemovedSentences: %v", err)
	}
	want := []RemovedSentence{
		{Path: "a.txt", Text: "The quick brown fox jumps over the lazy dog today."},
		{Path: "a.txt", Text: "Is this a long enough question to be taken?"},
		{Path: "a.txt", Text: "No stop inside.word mid and then eight words are here yes ok."},
		{Path: "gone.txt", Text: "A whole file that is deleted keeps its sentences as removed ones!"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RemovedSentences = %+v, want %+v", got, want)
	}

	none, err := RemovedSentences(dir, head, head, 8)
	if err != nil || len(none) != 0 {
		t.Fatalf("an empty change removes %+v (err %v), want nothing", none, err)
	}
}

// TestGrepFixed: the lines of the tracked text files at a revision that hold
// any pattern verbatim, with path and line; regex characters are literal, a
// binary file, an untracked file and a later revision's text are not searched,
// and no match is an empty result.
func TestGrepFixed(t *testing.T) {
	dir, write, commit := removedRepo(t, map[string]string{
		"b/x.txt":        "one\nsee a.b (c) [d] * here.\ntwo\n",
		"y.txt":          "see a.b (c) [d] * here. and more\n",
		"z.txt":          "see aXb (c) [d] * here.\n",
		"blob.bin":       "see a.b (c) [d] * here.\x00\n",
		"with space.txt": "tail line\nsee a.b (c) [d] * here.\n",
	})
	rev := commit("c1")
	write("late.txt", "see a.b (c) [d] * here.\n")
	commit("c2")
	write("untracked.txt", "see a.b (c) [d] * here.\n")

	got, err := GrepFixed(dir, rev, []string{"see a.b (c) [d] * here.", ""})
	if err != nil {
		t.Fatalf("GrepFixed: %v", err)
	}
	want := []GrepHit{
		{Path: "b/x.txt", Line: 2, Text: "see a.b (c) [d] * here."},
		{Path: "with space.txt", Line: 2, Text: "see a.b (c) [d] * here."},
		{Path: "y.txt", Line: 1, Text: "see a.b (c) [d] * here. and more"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GrepFixed = %+v, want %+v", got, want)
	}

	for _, pats := range [][]string{{"not anywhere in this repository at all"}, nil, {""}} {
		got, err := GrepFixed(dir, rev, pats)
		if err != nil || len(got) != 0 {
			t.Fatalf("GrepFixed(%q) = %+v, %v; want empty and no error", pats, got, err)
		}
	}
	if _, err := GrepFixed(dir, "no-such-rev", []string{"x"}); err == nil {
		t.Fatal("GrepFixed at an unknown rev returned no error")
	}
}
