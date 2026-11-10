package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// refStore is a store of made-up tickets: ZQX-7 (earlier called ZQX-2), ZQX-9
// and another ticket QWV-3, with one earlier round of ZQX-7 that reported the
// finding zq-f1 and one of QWV-3 that reported qw-f1. No id here is a real
// store's.
func refStore(t *testing.T) *store.Store {
	t.Helper()
	st := &store.Store{Root: filepath.Join(t.TempDir(), "made-up-store")}
	for _, tk := range []struct {
		id      string
		aliases []string
	}{{"ZQX-7", []string{"ZQX-2"}}, {"ZQX-9", nil}, {"QWV-3", nil}} {
		if err := os.MkdirAll(st.TicketDir(tk.id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateTicketRecord(tk.id, store.Ticket{Slug: "made-up", Aliases: tk.aliases}); err != nil {
			t.Fatal(err)
		}
	}
	for ticket, id := range map[string]string{"ZQX-7": "zq-f1", "QWV-3": "qw-f1"} {
		dir := gateRoundDir(st, ticket, 1)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "findings:\n  - id: " + id + "\n    file: a.go\n"
		if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// TestStoreRefsMatchTheStoreOwnNamesAsWholeTokens: an id or alias of any
// ticket and a finding id of this ticket match as a whole token, case
// sensitively, and never inside a longer id or a word.
func TestStoreRefsMatchTheStoreOwnNamesAsWholeTokens(t *testing.T) {
	m, err := newStoreRefMatcher(refStore(t), "ZQX-7", 2)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		text string
		want []string
	}{
		{"// see ZQX-7 for why", []string{"ZQX-7"}},
		{"// fixed in ZQX-2, then ZQX-9.", []string{"ZQX-2", "ZQX-9"}},
		{"(QWV-3)", []string{"QWV-3"}},
		{"ZQX-7", []string{"ZQX-7"}},
		{"// ZQX-70 is longer", nil},
		{"// ZQX-7-b is longer", nil},
		{"// AZQX-7 and ZQX-7a and _ZQX-7 and ZQX-7_", nil},
		{"// zqx-7 is another case", nil},
		{"// finding zq-f1 came back", []string{"zq-f1"}},
		{"// zq-f12 is longer", nil},
		{"// the other ticket's qw-f1 is not this one's", nil},
		{"// ZQX-9 then ZQX-2", []string{"ZQX-9", "ZQX-2"}},
		{"no names here", nil},
	}
	for _, c := range cases {
		if got := m.find(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("find(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// TestStoreRefsMatchTheStorePath: the store's root path matches in any slash
// form, and a different path does not.
func TestStoreRefsMatchTheStorePath(t *testing.T) {
	st := refStore(t)
	m, err := newStoreRefMatcher(st, "ZQX-7", 1)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.ToSlash(absPath(st.Root))
	for _, text := range []string{
		"// kept in " + root + "/tickets",
		"path := `" + strings.ReplaceAll(root, "/", `\`) + `\tickets` + "`",
		`path := "` + strings.ReplaceAll(root, "/", `\`) + `\tickets"`,
		`path := "` + strings.ReplaceAll(root, "/", `\\`) + `\\tickets"`,
	} {
		if got := m.find(text); !reflect.DeepEqual(got, []string{StoreRefPath}) {
			t.Errorf("find(%q) = %v, want the store path", text, got)
		}
	}
	if got := m.find("// kept in /somewhere/else/entirely"); len(got) != 0 {
		t.Errorf("another path matched: %v", got)
	}
}

// TestStoreRefsOnlyCountAddedLines: a deleted line and an unchanged one that
// name the store are never noted, an added one is, with its repo, file and
// new-side line; and a delta round's later commit does not hide an earlier
// addition, since the range is the whole change.
func TestStoreRefsOnlyCountAddedLines(t *testing.T) {
	st := refStore(t)
	dir, _ := tinyRepo(t)
	base := run(t, dir, "rev-parse", "HEAD")
	writeAndCommit(t, dir, "untouched.txt", "// ZQX-9 was here before\nplain\n", "before")
	base = run(t, dir, "rev-parse", "HEAD")
	writeAndCommit(t, dir, "a.go", "package a\n\n// ZQX-7 explains this\nvar X = 1\n", "adds a note")
	writeAndCommit(t, dir, "untouched.txt", "plain\n", "deletes the line naming ZQX-9")
	head := writeAndCommit(t, dir, "b.go", "package b\n", "adds nothing that names the store")

	refs, err := storeRefsOf(st, "ZQX-7", 2, []storeRefRepo{{Name: "app", Dir: dir, Base: base, Head: head}})
	if err != nil {
		t.Fatal(err)
	}
	want := []StoreRef{{Repo: "app", File: "a.go", Line: 3, ID: "ZQX-7"}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("store refs = %+v, want %+v", refs, want)
	}
}

// TestStoreRefsAreQualifiedByRepo: a hit in any repo of the round is noted
// with that repo's name.
func TestStoreRefsAreQualifiedByRepo(t *testing.T) {
	st := refStore(t)
	one, _ := tinyRepo(t)
	two, _ := tinyRepo(t)
	b1, b2 := run(t, one, "rev-parse", "HEAD"), run(t, two, "rev-parse", "HEAD")
	h1 := writeAndCommit(t, one, "x.txt", "nothing\n", "plain")
	h2 := writeAndCommit(t, two, "y.txt", "ok\n// QWV-3 here\n", "names a ticket")

	refs, err := storeRefsOf(st, "ZQX-7", 1, []storeRefRepo{
		{Name: "one", Dir: one, Base: b1, Head: h1},
		{Name: "two", Dir: two, Base: b2, Head: h2},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []StoreRef{{Repo: "two", File: "y.txt", Line: 2, ID: "QWV-3"}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("store refs = %+v, want %+v", refs, want)
	}
}

// TestStoreRefsRowsSumUpTheRest: at most 20 rows, then one row saying how many
// more and in how many files.
func TestStoreRefsRowsSumUpTheRest(t *testing.T) {
	var refs []StoreRef
	for i := 1; i <= 20; i++ {
		refs = append(refs, StoreRef{Repo: "app", File: "a.go", Line: i, ID: "ZQX-7"})
	}
	if rows := StoreRefsRows(refs); len(rows) != 20 {
		t.Fatalf("20 refs gave %d rows, want 20", len(rows))
	}
	refs = append(refs,
		StoreRef{Repo: "app", File: "a.go", Line: 30, ID: "ZQX-7"},
		StoreRef{Repo: "app", File: "b.go", Line: 1, ID: "ZQX-7"},
		StoreRef{Repo: "lib", File: "b.go", Line: 1, ID: "ZQX-9"},
	)
	rows := StoreRefsRows(refs)
	if len(rows) != 21 {
		t.Fatalf("23 refs gave %d rows, want 21", len(rows))
	}
	if got, want := rows[20][0], "and 3 more in 3 files"; got != want {
		t.Errorf("last row = %q, want %q", got, want)
	}
	if rows[0][1] != "a.go:1" {
		t.Errorf("first row file:line = %q, want a.go:1", rows[0][1])
	}
}

// TestReviewNotesStoreRefsNameTheCountAndEachPlace: the notes say how many
// lines the change adds that name the store and where, without what they cite,
// and a change with none has the notes it always had.
func TestReviewNotesStoreRefsNameTheCountAndEachPlace(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	refs := []StoreRef{
		{Repo: "app", File: "a.go", Line: 3, ID: "ZQX-7"},
		{Repo: "app", File: "docs/b.md", Line: 12, ID: "zq-f1"},
	}
	if err := writeReviewNotes(st, "ZQX-7", "review-notes.md", "", 1, nil, "", nil, nil, nil, nil, nil, "", refs); err != nil {
		t.Fatal(err)
	}
	want := "# Review Notes\n\n" +
		"## Store references\n\nThe change adds 2 lines that name something only the jig store holds:\n\n- a.go:3\n- docs/b.md:12\n\n" +
		"## Coverage\n\n"
	got := readNotes(t, st, "ZQX-7", "review-notes.md")
	if got != want {
		t.Errorf("notes =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "ZQX-7") || strings.Contains(got, "zq-f1") {
		t.Errorf("the notes are public and name what a line cites:\n%s", got)
	}

	if err := writeReviewNotes(st, "ZQX-7", "review-notes.md", "", 1, nil, "", nil, nil, nil, nil, nil, "", refs[:1]); err != nil {
		t.Fatal(err)
	}
	if got := readNotes(t, st, "ZQX-7", "review-notes.md"); !strings.Contains(got, "adds 1 line that") {
		t.Errorf("one reference reads %q, want a singular", got)
	}
}

// TestReviewNotesStoreRefsOfTheReposPullRequest: a ticket that publishes in
// several repos names, in each repo's notes, the places in that repo alone.
func TestReviewNotesStoreRefsOfTheReposPullRequest(t *testing.T) {
	t.Parallel()
	pt := &publishTarget{
		repoName: "web",
		covered:  []string{"api", "web"},
		gateRep: reportYAML{StoreRefs: []StoreRef{
			{Repo: "api", File: "a.go", Line: 1, ID: "ZQX-7"},
			{Repo: "web", File: "w.ts", Line: 9, ID: "ZQX-7"},
		}},
	}
	got := pt.storeRefs()
	if want := []StoreRef{{Repo: "web", File: "w.ts", Line: 9, ID: "ZQX-7"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("store refs of web = %+v, want %+v", got, want)
	}
	one := &publishTarget{repoName: "app", covered: []string{"app"}, gateRep: reportYAML{StoreRefs: pt.gateRep.StoreRefs}}
	if got := one.storeRefs(); len(got) != 2 {
		t.Errorf("a ticket in one repo lists %d, want all 2", len(got))
	}
}

// TestReviewNotesStoreRefsCountLinesNotNames: one line that names two things is
// one line in the notes, listed once.
func TestReviewNotesStoreRefsCountLinesNotNames(t *testing.T) {
	t.Parallel()
	notes := storeRefsNotes([]StoreRef{
		{Repo: "app", File: "a.go", Line: 3, ID: "ZQX-7"},
		{Repo: "app", File: "a.go", Line: 3, ID: "ZQX-9"},
	})
	if want := "adds 1 line that name something only the jig store holds:\n\n- a.go:3\n\n"; !strings.HasSuffix(notes, want) {
		t.Errorf("notes =\n%q\nwant them to end with %q", notes, want)
	}
}
