package correct

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/stack"
)

// stackedOn is the plan of JIG-1 stacked on JIG-0 in the repo app: the
// parent's branch jig/JIG-0 at parentHead, the child's at c0ffee.
func stackedOn(parentHead string, behind bool) stack.Plan {
	return stack.Plan{Ticket: "JIG-1", Branch: "jig/JIG-1", Repos: []stack.Repo{{
		Repo: "app", Target: "main", StartFrom: "jig/JIG-0", PRTarget: "jig/JIG-0",
		Parent: "JIG-0", ParentBranch: "jig/JIG-0", ParentRef: "jig/JIG-0", ParentHead: parentHead,
		Head: "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00", Behind: behind,
	}}}
}

// TestCheckTellsARestackOncePerParentHead: a parent whose branch moved past the
// child's is a restack note naming the branch, its head and the merge that
// takes it in, recorded once for that head and offered while it holds; a new
// head is a new note, and a branch level with the parent's has none.
func TestCheckTellsARestackOncePerParentHead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	plan := stackedOn("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true)
	in := w.input()
	in.Stack = func() (stack.Plan, error) { return plan, nil }

	notes, err := Check(in)
	if err != nil || len(notes) != 1 || notes[0].Kind != KindRestack || notes[0].Ref != "restack:JIG-0:app:aaaaaaaaaaaa" {
		t.Fatalf("Check = %+v, %v, want the one restack onto JIG-0's head", notes, err)
	}
	for _, want := range []string{"JIG-1 is stacked on JIG-0", "restack onto `jig/JIG-0` at `aaaaaaaaaaaa`: `git merge jig/JIG-0` in app", "jig never rebases"} {
		if !strings.Contains(notes[0].Text, want) {
			t.Errorf("the restack text %q lacks %q", notes[0].Text, want)
		}
	}
	lines := w.corrections()
	if len(lines) != 1 || lines[0].Outcome != KindRestack || lines[0].Ref != notes[0].Ref || lines[0].Commit != plan.Repos[0].ParentHead || lines[0].Session != "s-1" {
		t.Fatalf("correction lines = %+v, want the restack, with the parent's head, by s-1", lines)
	}
	if again, err := Check(in); err != nil || len(again) != 1 || again[0].Ref != notes[0].Ref {
		t.Errorf("Check again = %+v, %v, want the same note while the child is behind", again, err)
	}
	if got := len(w.corrections()); got != 1 {
		t.Errorf("correction lines after the second Check = %d, want still one", got)
	}

	plan = stackedOn("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true)
	if notes, err := Check(in); err != nil || len(notes) != 1 || notes[0].Ref != "restack:JIG-0:app:bbbbbbbbbbbb" {
		t.Errorf("after the parent moved again: Check = %+v, %v, want the restack onto the new head", notes, err)
	}
	if got := len(w.corrections()); got != 2 {
		t.Errorf("correction lines after the parent moved again = %d, want two", got)
	}

	plan = stackedOn("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false)
	if notes, err := Check(in); err != nil || len(notes) != 0 {
		t.Errorf("after the child merged the parent: Check = %+v, %v, want nothing", notes, err)
	}
}

// TestCheckTellsARetargetOnlyToTheSessionThatFoundIt: a parent that merged is a
// retarget note, offered again to the session that found it and to no other.
func TestCheckTellsARetargetOnlyToTheSessionThatFoundIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	plan := stack.Plan{Ticket: "JIG-1", Repos: []stack.Repo{{
		Repo: "app", Target: "main", StartFrom: "origin/main", PRTarget: "main", Parent: "JIG-0", ParentBranch: "jig/JIG-0",
		Merged: true, Head: "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00", Retarget: true,
	}}}
	in := w.input()
	in.Stack = func() (stack.Plan, error) { return plan, nil }

	notes, err := Check(in)
	if err != nil || len(notes) != 1 || notes[0].Kind != KindRetarget || notes[0].Ref != "retarget:JIG-0:app" {
		t.Fatalf("Check = %+v, %v, want the retarget", notes, err)
	}
	if want := "the parent merged in app: retarget the PR to main and merge main (`git merge origin/main`)"; !strings.Contains(notes[0].Text, want) {
		t.Errorf("the retarget text %q lacks %q", notes[0].Text, want)
	}
	if again, err := Check(in); err != nil || len(again) != 1 {
		t.Errorf("Check again in the same session = %+v, %v, want the note again", again, err)
	}
	other := in
	other.Session = "s-2"
	if notes, err := Check(other); err != nil || len(notes) != 0 {
		t.Errorf("Check in another session = %+v, %v, want nothing: a parent merges once", notes, err)
	}
	if got := len(w.corrections()); got != 1 {
		t.Errorf("correction lines = %d, want one", got)
	}
}

// TestCheckKeepsTheOtherNotesWhenTheStackCannotBeRead: a stack that cannot be
// read is an error, and the notes of the other detectors still come back.
func TestCheckKeepsTheOtherNotesWhenTheStackCannotBeRead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fail("a", "sig", 1)
	w.fail("a", "sig", 2)
	in := w.input()
	broken := errors.New("no such ticket")
	in.Stack = func() (stack.Plan, error) { return stack.Plan{}, broken }
	notes, err := Check(in)
	if !errors.Is(err, broken) || len(notes) != 1 || notes[0].Kind != KindStall {
		t.Errorf("Check = %+v, %v, want the stall and the stack's error", notes, err)
	}
}

// TestScopeTakesNoMergeForADrift: a merge brings in what was made on the
// branch it merges, as the restack of a stacked ticket does, so the files it
// brings from outside the workspaces are no drift.
func TestScopeTakesNoMergeForADrift(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	all, err := w.st.ReadSlices("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	w.git("switch", "-q", "-c", "parent")
	w.commitFiles("the parent's work", "docs/parent.md")
	w.git("switch", "-q", "main")
	w.commitFiles("the child's work", "alpha/a.go")
	w.git("merge", "-q", "--no-ff", "--no-edit", "parent")
	merge := w.git("rev-parse", "HEAD")
	if _, drifted, err := Scope(w.clone, "", merge, all); err != nil || drifted {
		t.Errorf("a merge of a branch with files outside the workspaces = drifted %v, %v, want neither", drifted, err)
	}
}
