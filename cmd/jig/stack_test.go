package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/stack"
)

// TestStackBlocksNameWhereTheBranchStartsAndWhereItsPRGoes: a stacked ticket's
// table names, for each repo, the parent it builds on, the start point and the
// pull request's target, and a note follows for a parent's branch that moved
// past it, a parent that merged, and a parent's branch the clone lacks.
func TestStackBlocksNameWhereTheBranchStartsAndWhereItsPRGoes(t *testing.T) {
	t.Parallel()
	plan := stack.Plan{Ticket: "JIG-2", Repos: []stack.Repo{
		{Repo: "api", Target: "main", StartFrom: "jig/JIG-1", PRTarget: "jig/JIG-1", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "jig/JIG-1",
			ParentHead: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Head: "c1", Behind: true},
		{Repo: "web", Target: "main", StartFrom: "origin/main", PRTarget: "main"},
		{Repo: "docs", Target: "trunk", StartFrom: "origin/trunk", PRTarget: "trunk", Parent: "JIG-1", ParentBranch: "jig/JIG-1", Merged: true, Head: "c2", Retarget: true},
		{Repo: "ops", Target: "main", StartFrom: "origin/jig/JIG-1", PRTarget: "jig/JIG-1", Parent: "JIG-1", ParentBranch: "jig/JIG-1"},
	}}
	rows := stackRows(plan, false)
	if len(rows) != 4 || !rows[0].Restack || rows[0].ParentHead != plan.Repos[0].ParentHead || !rows[2].Retarget || !rows[2].Merged || rows[1].Parent != "" {
		t.Fatalf("stackRows = %+v, want the four repos with their restack and retarget", rows)
	}
	got := stackBlocks("JIG-2", rows)
	want := []string{
		"stack[4]{repo,parent,start_from,pr_target}:\n  api,JIG-1,jig/JIG-1,jig/JIG-1\n  web,-,origin/main,main\n  docs,JIG-1 (merged),origin/trunk,trunk\n  ops,JIG-1,origin/jig/JIG-1,jig/JIG-1",
		"note: the branch of JIG-1 moved past JIG-2 in api: restack onto `jig/JIG-1` at `aaaaaaaaaaaa`: `git merge jig/JIG-1`; jig never rebases",
		"note: JIG-1 merged in docs: merge trunk (`git merge origin/trunk`); the publish of JIG-2 points its PR at trunk, but the hand-off (`--body`, then `--pr`) does not: run `gh pr edit <pr> --base trunk`",
		"note: the clone of ops holds no copy of jig/JIG-1, the branch of JIG-1: fetch it (`git fetch origin`) before JIG-2 starts from it",
	}
	for i := range got {
		got[i] = strings.TrimRight(got[i], "\n")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stackBlocks =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if rows := stackRows(stack.Plan{Ticket: "JIG-3"}, false); rows != nil || stackBlocks("JIG-3", rows) != nil {
		t.Errorf("a ticket stacked on nothing has rows %+v, want none and no block", rows)
	}
}

// TestStackBlocksNameThePublish: a row that asks for a merge names it (Merge),
// and, once the ticket published, the publish that follows (Publish, with
// --repo when the ticket has several rows); the restack and retarget notes of a
// published ticket end with it, and the text before it is the notes' own. A
// ticket that has not published names no publish and its notes are unchanged.
func TestStackBlocksNameThePublish(t *testing.T) {
	t.Parallel()
	one := stack.Plan{Ticket: "JIG-2", Repos: []stack.Repo{
		{Repo: "docs", Target: "trunk", StartFrom: "origin/trunk", PRTarget: "trunk", Parent: "JIG-1", ParentBranch: "jig/JIG-1", Merged: true, Head: "c2", Retarget: true},
	}}
	rows := stackRows(one, true)
	if len(rows) != 1 || rows[0].Merge != "git merge origin/trunk" || rows[0].Publish != "jig publish JIG-2" {
		t.Fatalf("one row = %+v, want the merge and a bare publish", rows)
	}
	got := stackBlocks("JIG-2", rows)
	if want := "note: JIG-1 merged in docs: merge trunk (`git merge origin/trunk`), then run `jig publish JIG-2`, which points the PR of JIG-2 at trunk, but the hand-off (`--body`, then `--pr`) does not: run `gh pr edit <pr> --base trunk`"; got[1] != want {
		t.Errorf("retarget note = %q, want %q", got[1], want)
	}
	if rows := stackRows(one, false); rows[0].Merge != "git merge origin/trunk" || rows[0].Publish != "" {
		t.Errorf("unpublished = %+v, want the merge and no publish", rows[0])
	}

	several := stack.Plan{Ticket: "JIG-2", Repos: []stack.Repo{
		{Repo: "api", Target: "main", StartFrom: "origin/jig/JIG-1", PRTarget: "jig/JIG-1", Parent: "JIG-1", ParentBranch: "jig/JIG-1", ParentRef: "origin/jig/JIG-1",
			ParentHead: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Head: "c1", Behind: true},
		{Repo: "web", Target: "main", StartFrom: "origin/main", PRTarget: "main"},
		{Repo: "docs", Target: "trunk", StartFrom: "origin/trunk", PRTarget: "trunk", Parent: "JIG-1", ParentBranch: "jig/JIG-1", Merged: true, Head: "c2", Retarget: true},
	}}
	rows = stackRows(several, true)
	if rows[0].Publish != "jig publish JIG-2 --repo api" || rows[0].Merge != "git merge origin/jig/JIG-1" || rows[1].Merge != "" || rows[1].Publish != "" ||
		rows[2].Publish != "jig publish JIG-2 --repo docs" || rows[2].Merge != "git merge origin/trunk" {
		t.Fatalf("several rows = %+v, want a publish for each row that asks for a merge", rows)
	}
	got = stackBlocks("JIG-2", rows)
	if want := "note: the branch of JIG-1 moved past JIG-2 in api: restack onto `jig/JIG-1` at `aaaaaaaaaaaa`: `git merge origin/jig/JIG-1`; jig never rebases; then run `jig publish JIG-2 --repo api`"; got[1] != want {
		t.Errorf("restack note = %q, want %q", got[1], want)
	}
	if want := "note: JIG-1 merged in docs: merge trunk (`git merge origin/trunk`), then run `jig publish JIG-2 --repo docs`, which points the PR of JIG-2 at trunk, but the hand-off (`--body`, then `--pr`) does not: run `gh pr edit <pr> --base trunk`"; got[2] != want {
		t.Errorf("retarget note = %q, want %q", got[2], want)
	}

	// The help of a ticket that is behind names, for each such row, the merge,
	// the publish and where the pull request goes, and the gate a hand-resolved
	// merge needs.
	n := nextTicket{Ticket: "JIG-2", State: stateBehind, Stack: rows}
	help := n.behindHelp()
	want := []string{
		"in api run `git merge origin/jig/JIG-1`, then `jig publish JIG-2 --repo api`, which pushes the merge and points the open pull request at jig/JIG-1; a merge resolved by hand needs `jig gate JIG-2` first",
		"in docs run `git merge origin/trunk`, then `jig publish JIG-2 --repo docs`, which pushes the merge and points the open pull request at trunk (the hand-off, `--body` then `--pr`, does not: run `gh pr edit <pr> --base trunk`); a merge resolved by hand needs `jig gate JIG-2` first",
	}
	if !reflect.DeepEqual(help, want) {
		t.Errorf("behind help =\n%s\nwant\n%s", strings.Join(help, "\n"), strings.Join(want, "\n"))
	}
}
