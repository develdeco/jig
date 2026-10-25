package correct

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func TestOwnersAreTheSlicesOfTheDeepestWorkspaceThatHoldsAFile(t *testing.T) {
	t.Parallel()
	ws := []SliceWorkspace{{"a", "alpha"}, {"b", "beta"}, {"a2", "alpha"}, {"deep", "alpha/sub"}, {"all", "."}}
	owners := func(files ...string) []string {
		var out []string
		for _, o := range Owners(files, ws) {
			out = append(out, o.Slice)
		}
		return out
	}
	for _, c := range []struct {
		name  string
		files []string
		want  []string
	}{
		{"one workspace", []string{"beta/b.go"}, []string{"b"}},
		{"slices that share a workspace share its files", []string{"alpha/a.go"}, []string{"a", "a2"}},
		{"several workspaces, in the order of the slices", []string{"beta/b.go", "alpha/a.go"}, []string{"a", "b", "a2"}},
		{"a workspace inside another owns its files", []string{"alpha/sub/x.go"}, []string{"deep"}},
		{"a slice of the whole repo owns what no workspace holds", []string{"docs/readme.md"}, []string{"all"}},
		{"a workspace is a directory, not a name prefix", []string{"alphabet/x.go"}, []string{"all"}},
		{"no file, no owner", nil, nil},
	} {
		if got := owners(c.files...); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Owners(%v) = %v, want %v", c.name, c.files, got, c.want)
		}
	}
	if got := Owners([]string{"docs/readme.md"}, ws[:2]); got != nil {
		t.Errorf("Owners of a file under no workspace = %v, want none", got)
	}
}

func TestUnslicedNamesTheSlicesTheFilesPointTo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	all, err := w.st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	guess := func(sha string) (SliceGuess, bool) {
		t.Helper()
		g, unsliced, err := Unsliced(w.clone, "", sha, all)
		if err != nil {
			t.Fatalf("Unsliced(%s): %v", sha, err)
		}
		return g, unsliced
	}
	slicesOf := func(g SliceGuess) []string {
		var out []string
		for _, o := range g.Owners {
			out = append(out, o.Slice)
		}
		return out
	}

	one := w.commitFiles("one", "alpha/a.go", "docs/readme.md")
	g, unsliced := guess(one)
	if !unsliced || !reflect.DeepEqual(slicesOf(g), []string{"a"}) || g.Trailer != "" || g.Commit != one || g.Ref() != "slice:"+one[:12] ||
		!reflect.DeepEqual(g.Files, []string{"alpha/a.go", "docs/readme.md"}) || !reflect.DeepEqual(g.Paths, []string{"alpha", "beta"}) {
		t.Errorf("a commit in the workspace of slice a = %+v, %v, want slice a alone, whatever else it touched", g, unsliced)
	}

	if g, unsliced := guess(w.commitFiles("several", "alpha/a2.go", "beta/b.go")); !unsliced || !reflect.DeepEqual(slicesOf(g), []string{"a", "b"}) {
		t.Errorf("a commit in two workspaces = %+v, %v, want both slices as the candidates", g, unsliced)
	}
	if g, unsliced := guess(w.commitFiles("none", "docs/more.md")); !unsliced || len(g.Owners) != 0 || !reflect.DeepEqual(g.Files, []string{"docs/more.md"}) {
		t.Errorf("a commit in no workspace = %+v, %v, want a guess with no candidate", g, unsliced)
	}

	// The trailer names the slice: nothing to infer. One that names no slice
	// of the ticket does not.
	w.write("alpha/t.go", "t")
	w.git("add", "--", "alpha/t.go")
	w.git("commit", "-m", "named", "-m", "Jig-Slice: b")
	if _, unsliced := guess(w.git("rev-parse", "HEAD")); unsliced {
		t.Error("a commit whose trailer names slice b = unsliced, want it left alone, whatever its files say")
	}
	w.write("alpha/u.go", "u")
	w.git("add", "--", "alpha/u.go")
	w.git("commit", "-m", "typo", "-m", "Jig-Slice: bee")
	if g, unsliced := guess(w.git("rev-parse", "HEAD")); !unsliced || g.Trailer != "bee" || !reflect.DeepEqual(slicesOf(g), []string{"a"}) {
		t.Errorf("a commit whose trailer names no slice = %+v, %v, want the trailer kept and slice a guessed", g, unsliced)
	}
}

func TestUnslicedPassesOverMergesEmptyAndRootCommits(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	all, err := w.st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	unsliced := func(sha string) bool {
		t.Helper()
		_, ok, err := Unsliced(w.clone, "", sha, all)
		if err != nil {
			t.Fatalf("Unsliced(%s): %v", sha, err)
		}
		return ok
	}

	w.git("checkout", "-q", "-b", "side")
	w.commitFiles("side", "alpha/s.go")
	w.git("checkout", "-q", "main")
	w.commitFiles("main", "beta/m.go")
	w.git("merge", "--no-ff", "-m", "merge side", "side")
	if unsliced(w.git("rev-parse", "HEAD")) {
		t.Error("a merge commit = unsliced, want no guess: it is nobody's slice work")
	}
	w.git("commit", "--allow-empty", "-m", "chore: release")
	if unsliced(w.git("rev-parse", "HEAD")) {
		t.Error("a commit with no file = unsliced, want no guess")
	}
	if unsliced(w.git("rev-list", "--max-parents=0", "HEAD")) {
		t.Error("a root commit = unsliced, want no guess: there is no parent to compare with")
	}
	if !unsliced(w.commitFiles("plain", "alpha/p.go")) {
		t.Error("a plain commit in a workspace = not unsliced, want a guess")
	}
	// Slices that build in another repo say nothing of this one.
	elsewhere := []store.Slice{{ID: "g", Workspace: "gamma"}}
	if _, ok, err := Unsliced(w.clone, "", w.git("rev-parse", "HEAD"), elsewhere); err != nil || ok {
		t.Errorf("slices of another repo = unsliced %v, %v, want neither", ok, err)
	}
	// A manifest that does not parse is an error, not a verdict.
	w.write(".claude/jig.yaml", "workspaces: [not, a, list of maps\n")
	sha := w.commit("break the manifest", ".claude/jig.yaml")
	if _, _, err := Unsliced(w.clone, "", sha, all); err == nil {
		t.Error("a manifest that does not parse = no error")
	}
}

func TestSliceTextNamesTheSliceTheCandidatesOrNone(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	for _, c := range []struct {
		name  string
		guess SliceGuess
		want  []string
		not   []string
	}{
		{"one", SliceGuess{Commit: sha, Owners: []SliceWorkspace{{"clamp", "cart"}}, Paths: []string{"cart", "ui"}},
			[]string{"commit 0123456789ab on SHOP-1 names no slice", "jig verifies nothing for it", "It touched cart/ (slice clamp's workspace)", "amend it with `Jig-Slice: clamp`", "`jig verify SHOP-1 0123456789ab --slice <slice>`"},
			[]string{"no slice's workspace"}},
		{"several", SliceGuess{Commit: sha, Owners: []SliceWorkspace{{"clamp", "cart"}, {"view", "ui"}}, Paths: []string{"cart", "ui"}},
			[]string{"names no slice", "the workspaces of slices clamp (cart/), view (ui/)", "amend it with `Jig-Slice: <slice>`"},
			[]string{"workspace):"}},
		{"none", SliceGuess{Commit: sha, Paths: []string{"cart", "ui"}},
			[]string{"names no slice", "It touched no slice's workspace (cart/, ui/): if it is work for a slice, amend it with `Jig-Slice: <slice>`"},
			[]string{"It touched cart"}},
		{"a trailer that names no slice", SliceGuess{Commit: sha, Trailer: "clamps", Owners: []SliceWorkspace{{"clamp", "cart"}}, Paths: []string{"cart"}},
			[]string{"carries `Jig-Slice: clamps`, but SHOP-1 has no slice clamps", "slice clamp's workspace", "amend it with `Jig-Slice: clamp`"},
			[]string{"names no slice,"}},
	} {
		text := sliceText("SHOP-1", c.guess)
		for _, want := range c.want {
			if !strings.Contains(text, want) {
				t.Errorf("%s: text %q lacks %q", c.name, text, want)
			}
		}
		for _, not := range c.not {
			if strings.Contains(text, not) {
				t.Errorf("%s: text %q has %q", c.name, text, not)
			}
		}
		if strings.Contains(text, "\n") {
			t.Errorf("%s: text %q is not one line", c.name, text)
		}
	}
}

func TestCheckTellsOnceOfACommitThatNamesNoSlice(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	in := w.input()

	in.Commit = w.commitFiles("work", "alpha/a.go")
	notes, err := Check(in)
	if err != nil || len(notes) != 1 || notes[0].Kind != KindSlice || notes[0].Ref != "slice:"+in.Commit[:12] ||
		!strings.Contains(notes[0].Text, "slice a's workspace") || !strings.Contains(notes[0].Text, "`Jig-Slice: a`") {
		t.Fatalf("Check = %+v, %v, want the note naming slice a", notes, err)
	}
	lines := w.corrections()
	if len(lines) != 1 || lines[0].Outcome != KindSlice || lines[0].Commit != in.Commit || lines[0].Slice != "" || lines[0].Session != "s-1" ||
		lines[0].Provenance != "jig" || lines[0].Ref != notes[0].Ref || lines[0].Summary != notes[0].Text || !reflect.DeepEqual(lines[0].Files, []string{"alpha/a.go"}) {
		t.Errorf("correction lines = %+v, want the one, on the commit, naming no slice as nothing is known", lines)
	}
	if got := w.sliceState("a"); got.State != "queued" {
		t.Errorf("slice a = %+v, want it left as it was: the guess marks nothing", got)
	}
	if verified := Verified(w.journalLines()); len(verified) != 0 {
		t.Errorf("verified slices = %v after a guess, want none: an inference is never a green", verified)
	}

	// At a later event, with no commit of its own, the session is offered it
	// again, and still no second line is written.
	again, err := Check(w.input())
	if err != nil || !reflect.DeepEqual(again, notes) || len(w.corrections()) != 1 {
		t.Errorf("a later event: %+v, %v with %d lines, want the same note and no new line", again, err, len(w.corrections()))
	}
	// Another session did not make the commit, and a session that began after
	// it was not there.
	other := w.input()
	other.Session = "s-2"
	if got, err := Check(other); err != nil || len(got) != 0 {
		t.Errorf("another session: %+v, %v, want nothing", got, err)
	}
	late := w.input()
	late.Since = time.Now().Add(time.Hour)
	if got, err := Check(late); err != nil || len(got) != 0 {
		t.Errorf("a session that began later: %+v, %v, want nothing", got, err)
	}
}

func TestCheckLeavesACommitThatNamesASlice(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	in := w.input()

	w.write("alpha/a.go", "a")
	w.git("add", "--", "alpha/a.go")
	w.git("commit", "-m", "named", "-m", "Jig-Slice: a")
	in.Commit = w.git("rev-parse", "HEAD")
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Errorf("a commit with the trailer: %+v, %v, want nothing", notes, err)
	}

	// A session that claimed a slice names it for every commit it makes.
	claimed := w.commitFiles("claimed", "beta/b.go")
	in.Commit, in.Slice = claimed, "a"
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Errorf("a commit of a session that claimed slice a: %+v, %v, want nothing", notes, err)
	}
	// A claim the ticket has no slice of names nothing.
	in.Slice = "zzz"
	if notes, err := Check(in); err != nil || len(notes) != 1 || notes[0].Ref != "slice:"+claimed[:12] {
		t.Errorf("a commit of a session whose claim names no slice: %+v, %v, want the note", notes, err)
	}

	// A commit that is not slice work is no fact: the session hears only of
	// the commit before it.
	w.git("commit", "--allow-empty", "-m", "chore: release")
	in.Slice, in.Commit = "", w.git("rev-parse", "HEAD")
	if notes, err := Check(in); err != nil || len(notes) != 1 || notes[0].Ref != "slice:"+claimed[:12] {
		t.Errorf("a commit with no file: %+v, %v, want the earlier note alone", notes, err)
	}
	if got := len(w.corrections()); got != 1 {
		t.Errorf("correction lines = %d, want the one note's", got)
	}
}

// TestCheckStopsOfferingACommitOnceItIsPutRight: the remedy the note names,
// amending the commit with the trailer or saying its slice to jig verify, ends
// the note, so that an unattended session which did as told is not held back
// for the commit it has since replaced.
func TestCheckStopsOfferingACommitOnceItIsPutRight(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	in := w.input()

	in.Commit = w.commitFiles("work", "alpha/a.go")
	if notes, err := Check(in); err != nil || len(notes) != 1 {
		t.Fatalf("Check = %+v, %v, want the note", notes, err)
	}
	if notes, err := Check(w.input()); err != nil || len(notes) != 1 {
		t.Fatalf("a later event = %+v, %v, want the note again", notes, err)
	}

	// Said to jig verify: a verification of the commit is in the journal.
	w.line(journal.Line{Slice: "a", Event: journal.EventVerifyPending, Commit: in.Commit})
	if notes, err := Check(w.input()); err != nil || len(notes) != 0 {
		t.Errorf("after the commit was verified for slice a: %+v, %v, want nothing", notes, err)
	}

	// Amended: the commit the note named is no longer on the branch.
	w2 := newWorld(t)
	in2 := w2.input()
	in2.Commit = w2.commitFiles("work", "alpha/a.go")
	if notes, err := Check(in2); err != nil || len(notes) != 1 {
		t.Fatalf("Check = %+v, %v, want the note", notes, err)
	}
	w2.git("commit", "--amend", "-m", "work", "-m", "Jig-Slice: a")
	if notes, err := Check(w2.input()); err != nil || len(notes) != 0 {
		t.Errorf("after the commit was amended with the trailer: %+v, %v, want nothing", notes, err)
	}
	// The amended commit names its slice, so it is no new fact either.
	in2.Commit = w2.git("rev-parse", "HEAD")
	if notes, err := Check(in2); err != nil || len(notes) != 0 {
		t.Errorf("the amended commit: %+v, %v, want nothing", notes, err)
	}
	if got := len(w2.corrections()); got != 1 {
		t.Errorf("correction lines = %d, want only the first note's", got)
	}
}
