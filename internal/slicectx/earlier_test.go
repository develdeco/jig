package slicectx

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestSliceTrailer reads the Jig-Slice trailer of a message from its last
// paragraph, and nothing else.
func TestSliceTrailer(t *testing.T) {
	for _, tc := range []struct{ name, msg, want string }{
		{"trailer after a blank line", "JIG-1 a: fix\n\nJig-Slice: a\n", "a"},
		{"with other trailers", "fix\n\nbody\n\nSigned-off-by: x\nJig-Slice: b-2\n", "b-2"},
		{"crlf", "fix\r\n\r\nJig-Slice: c\r\n", "c"},
		{"no trailer", "fix\n\nbody text\n", ""},
		{"in the body, not the last paragraph", "fix\n\nJig-Slice: a\n\nmore text\n", ""},
		{"only the subject", "Jig-Slice: d", "d"},
		{"a word after the id is not a trailer", "fix\n\nJig-Slice: a b\n", ""},
	} {
		if got := sliceTrailer(tc.msg); got != tc.want {
			t.Errorf("%s: sliceTrailer(%q) = %q, want %q", tc.name, tc.msg, got, tc.want)
		}
	}
}

// TestEarlierSlicesAreFoundByTheirTrailer: the slices that verified are listed
// in the order they did, each with the commits of the clone that carry its
// trailer and the files they changed; a commit with no trailer, or for a slice
// that did not verify, is not in it.
func TestEarlierSlicesAreFoundByTheirTrailer(t *testing.T) {
	w := newWorld(t)
	w.write("alpha/clamp.go", "package alpha\n")
	first := w.commit("JIG-1 a: first", "a", "alpha/clamp.go")
	w.write("alpha/clamp_test.go", "package alpha\n")
	w.write("alpha/clamp.go", "package alpha // fixed\n")
	second := w.commit("JIG-1 a: second", "a", "alpha/clamp.go", "alpha/clamp_test.go")
	w.write("notes.txt", "x")
	w.commit("a hand edit with no trailer", "", "notes.txt")
	w.write("beta/other.go", "package beta\n")
	w.commit("JIG-1 c: not verified", "c", "beta/other.go")
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: second, Attempt: 1})

	c, err := Build(w.input("b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.EarlierSlices) != 1 {
		t.Fatalf("earlier slices = %+v, want only a", c.EarlierSlices)
	}
	a := c.EarlierSlices[0]
	if a.ID != "a" || len(a.Commits) != 2 || a.Commits[0].SHA != first || a.Commits[1].SHA != second ||
		a.Commits[0].Subject != "JIG-1 a: first" {
		t.Errorf("a commits = %+v, want %s then %s with subjects", a.Commits, first, second)
	}
	if strings.Join(a.Files, ",") != "alpha/clamp.go,alpha/clamp_test.go" {
		t.Errorf("a files = %v, want the union, each once", a.Files)
	}

	// Asked from a's own context, a is not its own earlier slice.
	if own, _ := Build(w.input("a")); len(own.EarlierSlices) != 0 {
		t.Errorf("a earlier slices = %+v, want none", own.EarlierSlices)
	}
	text := strings.Join(c.Blocks(), "\n")
	if !strings.Contains(text, "earlier_slices[1]{slice,commits,files}:") || !strings.Contains(text, "alpha/clamp.go+alpha/clamp_test.go") {
		t.Errorf("blocks lack the earlier slice row:\n%s", text)
	}
}

// TestEarlierSlicesStartAtTheStartSha: with a start sha recorded for the repo,
// commits before it are not read, so a trailer on an older commit of the
// clone belongs to another ticket.
func TestEarlierSlicesStartAtTheStartSha(t *testing.T) {
	w := newWorld(t)
	w.write("old.txt", "x")
	w.commit("older work, same slice name", "a", "old.txt")
	start := w.git("rev-parse", "HEAD")
	w.write("new.txt", "y")
	fresh := w.commit("this ticket a", "a", "new.txt")
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: fresh, Attempt: 1})

	in := w.input("b")
	if err := os.WriteFile(w.st.StartSHAPath("JIG-1", "repo"), []byte(start+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.EarlierSlices) != 1 || len(c.EarlierSlices[0].Commits) != 1 || c.EarlierSlices[0].Commits[0].SHA != fresh {
		t.Errorf("earlier = %+v, want only the commit after the start sha", c.EarlierSlices)
	}
}

// TestEarlierSlicesFallBackToTheVerifiedCommit: a verified slice whose commits
// the clone does not hold - or with no clone at all - is listed with the
// commits its verified lines name and no files.
func TestEarlierSlicesFallBackToTheVerifiedCommit(t *testing.T) {
	w := newWorld(t)
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: "abc123abc123abc123", Attempt: 1})
	in := w.input("b")
	in.RepoDir = ""
	c, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.EarlierSlices) != 1 || len(c.EarlierSlices[0].Commits) != 1 || c.EarlierSlices[0].Commits[0].SHA != "abc123abc123abc123" ||
		len(c.EarlierSlices[0].Files) != 0 {
		t.Errorf("earlier = %+v, want a with the verified commit and no files", c.EarlierSlices)
	}
}

// TestEarlierSlicesCapTheFiles: a slice that changed more files than
// MaxEarlierFiles lists the first ones and counts the rest.
func TestEarlierSlicesCapTheFiles(t *testing.T) {
	w := newWorld(t)
	var files []string
	for i := 0; i < MaxEarlierFiles+3; i++ {
		name := "f" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".txt"
		w.write(name, "x")
		files = append(files, name)
	}
	sha := w.commit("many files", "a", files...)
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: sha, Attempt: 1})
	c, err := Build(w.input("b"))
	if err != nil {
		t.Fatal(err)
	}
	e := c.EarlierSlices[0]
	if len(e.Files) != MaxEarlierFiles || e.FilesOmitted != 3 {
		t.Errorf("files %d, omitted %d, want %d and 3", len(e.Files), e.FilesOmitted, MaxEarlierFiles)
	}
	if !strings.Contains(e.filesCell(), "(+3 more)") {
		t.Errorf("files cell %q lacks the omitted count", e.filesCell())
	}
}
