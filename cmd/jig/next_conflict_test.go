package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestTestPair: two branches that change the same lines of a file conflict in
// it; two that change different parts of one file merge cleanly; a ref git
// cannot resolve leaves the pair untested, never an error.
func TestTestPair(t *testing.T) {
	t.Parallel()
	dir := cmdGitRepo(t, filepath.Join(t.TempDir(), "repo"))
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("f.txt", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "base")
	branch := func(name, body string) {
		gitIn(t, dir, "switch", "-c", name, "main")
		write("f.txt", body)
		gitIn(t, dir, "commit", "-am", name)
	}
	branch("top", "TOP\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("bottom", "1\n2\n3\n4\n5\n6\n7\n8\n9\nBOTTOM\n")
	branch("same-top", "OTHER\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")

	if state, files := testPair(dir, "top", "bottom"); state != pairClean || len(files) != 0 {
		t.Errorf("different parts of one file: %s %v, want clean", state, files)
	}
	if state, files := testPair(dir, "top", "same-top"); state != pairConflict || !reflect.DeepEqual(files, []string{"f.txt"}) {
		t.Errorf("the same lines: %s %v, want a conflict in f.txt", state, files)
	}
	if state, files := testPair(dir, "top", "nosuchref"); state != pairUntested || len(files) != 0 {
		t.Errorf("a bad ref: %s %v, want untested", state, files)
	}
}

// TestConflictAdviceForTheTicketInHand: a conflicting pair gives the ticket in
// hand one row for each conflicted file, naming the other ticket and its
// branch, and the ticket that comes first in next order (rank, then id); a
// clean sharing pair gives an info line only where asked; a pair of other
// tickets or an untested pair gives nothing; the help stacks the later ticket
// on the first.
func TestConflictAdviceForTheTicketInHand(t *testing.T) {
	_, storeRoot := setupGraduateStore(t)
	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	one, two := 1, 2
	for id, rank := range map[string]*int{"T-1": &two, "T-2": &one, "T-3": nil, "T-4": nil} {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: id, Rank: rank}); err != nil {
			t.Fatal(err)
		}
	}
	pairs := []testedPair{
		{Repo: "app", A: "T-1", B: "T-2", BranchA: "b1", BranchB: "b2", State: pairConflict, Files: []string{"a.go", "b.go"}, Shared: []string{"a.go", "b.go", "c.go"}},
		{Repo: "app", A: "T-1", B: "T-3", BranchA: "b1", BranchB: "b3", State: pairClean, Shared: []string{"d.go"}},
		{Repo: "app", A: "T-1", B: "T-4", BranchA: "b1", BranchB: "b4", State: pairUntested, Shared: []string{"e.go"}},
		{Repo: "app", A: "T-2", B: "T-3", BranchA: "b2", BranchB: "b3", State: pairConflict, Files: []string{"z.go"}},
	}
	a := adviceFromPairs(st, pairs, "T-1")
	want := []ticketConflict{
		{Repo: "app", Ticket: "T-2", File: "a.go", Branch: "b2", First: "T-2"},
		{Repo: "app", Ticket: "T-2", File: "b.go", Branch: "b2", First: "T-2"},
	}
	if !reflect.DeepEqual(a.Conflicts, want) {
		t.Errorf("conflicts = %+v, want %+v", a.Conflicts, want)
	}
	if !reflect.DeepEqual(a.Clean, []cleanShare{{Repo: "app", Ticket: "T-3", File: "d.go"}}) {
		t.Errorf("clean = %+v, want the one clean share", a.Clean)
	}
	blocks := strings.Join(conflictBlocks(a, true), "\n")
	for _, w := range []string{"warning:", "conflict[2]{repo,ticket,file,branch}:", "app,T-2,a.go,b2", "info: T-1 shares d.go with T-3; merges cleanly"} {
		if !strings.Contains(blocks, w) {
			t.Errorf("blocks lack %q:\n%s", w, blocks)
		}
	}
	if quiet := strings.Join(conflictBlocks(a, false), "\n"); strings.Contains(quiet, "info:") {
		t.Errorf("a clean pair printed where it should not:\n%s", quiet)
	}
	help := conflictHelp("T-1", a.Conflicts)
	if len(help) != 1 || help[0] != "Stack it with `jig depend T-1 --on T-2 --kind stacked`, or wait for T-2 to merge" {
		t.Errorf("help = %q", help)
	}
	// Seen from T-2, which comes first, the other is the one to stack.
	b := adviceFromPairs(st, pairs, "T-2")
	if help := conflictHelp("T-2", b.Conflicts); len(help) != 2 || !strings.Contains(help[0], "jig depend T-1 --on T-2 --kind stacked") {
		t.Errorf("help from the first ticket = %q", help)
	}
	if none := adviceFromPairs(st, pairs, "T-9"); len(none.Conflicts)+len(none.Clean) != 0 || len(conflictBlocks(none, true)) != 0 {
		t.Errorf("a ticket in no pair got advice: %+v", none)
	}
}

// TestPrintConflictsNamesARealConflictOnly: before a gate or a publish, two
// open tickets whose branches change the same lines of a file are told, with
// the file and the other branch; branches that change different parts of one
// file print nothing; the other ticket is told the same from its side.
func TestPrintConflictsNamesARealConflictOnly(t *testing.T) {
	_, storeRoot := setupGraduateStore(t)
	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"T-1", "T-2", "T-3"} {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	dir := cmdGitRepo(t, filepath.Join(t.TempDir(), "app"))
	body := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(body)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "base")
	branch := func(name, s string) {
		gitIn(t, dir, "switch", "-c", name, "main")
		write(s)
		gitIn(t, dir, "commit", "-am", name)
	}
	branch("T-1-one", "ONE\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("T-2-two", "TWO\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("T-3-three", "1\n2\n3\n4\n5\n6\n7\n8\n9\nTHREE\n")
	gitIn(t, dir, "switch", "main")

	cfg := project.Config{Name: "demo", Repos: []project.Repo{{Remote: "git@example.invalid:demo/app.git"}}}
	mp := project.MachineProject{Clones: map[string]string{"app": dir}}

	var out bytes.Buffer
	printConflicts(&out, st, cfg, mp, nil, "T-2")
	for _, w := range []string{"conflict[1]{repo,ticket,file,branch}:", "app,T-1,f.txt,T-1-one", "jig depend T-2 --on T-1 --kind stacked"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
	out.Reset()
	printConflicts(&out, st, cfg, mp, nil, "T-1")
	if !strings.Contains(out.String(), "jig depend T-2 --on T-1 --kind stacked") {
		t.Errorf("T-1 is not told to be stacked on:\n%s", out.String())
	}
	out.Reset()
	printConflicts(&out, st, cfg, mp, nil, "T-3")
	if out.Len() != 0 {
		t.Errorf("a clean pair printed:\n%s", out.String())
	}
}
