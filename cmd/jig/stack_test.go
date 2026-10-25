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
	rows := stackRows(plan)
	if len(rows) != 4 || !rows[0].Restack || rows[0].ParentHead != plan.Repos[0].ParentHead || !rows[2].Retarget || !rows[2].Merged || rows[1].Parent != "" {
		t.Fatalf("stackRows = %+v, want the four repos with their restack and retarget", rows)
	}
	got := stackBlocks("JIG-2", rows)
	want := []string{
		"stack[4]{repo,parent,start_from,pr_target}:\n  api,JIG-1,jig/JIG-1,jig/JIG-1\n  web,-,origin/main,main\n  docs,JIG-1 (merged),origin/trunk,trunk\n  ops,JIG-1,origin/jig/JIG-1,jig/JIG-1",
		"note: the branch of JIG-1 moved past JIG-2 in api: restack onto `jig/JIG-1` at `aaaaaaaaaaaa`: `git merge jig/JIG-1`; jig never rebases",
		"note: JIG-1 merged in docs: retarget the PR of JIG-2 to trunk and merge trunk (`git merge origin/trunk`)",
		"note: the clone of ops holds no copy of jig/JIG-1, the branch of JIG-1: fetch it (`git fetch origin`) before JIG-2 starts from it",
	}
	for i := range got {
		got[i] = strings.TrimRight(got[i], "\n")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stackBlocks =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if rows := stackRows(stack.Plan{Ticket: "JIG-3"}); rows != nil || stackBlocks("JIG-3", rows) != nil {
		t.Errorf("a ticket stacked on nothing has rows %+v, want none and no block", rows)
	}
}
