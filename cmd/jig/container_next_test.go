package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// containerNextOut is the part of `jig next <ticket> --json` the container's
// base shows in.
type containerNextOut struct {
	Branch string      `json:"branch"`
	Stack  []nextStack `json:"stack"`
}

func (r chartRig) containerNext(t *testing.T, id string) containerNextOut {
	t.Helper()
	out, code := r.run(t, "next", id, "--json")
	var n struct {
		containerNextOut
		// An issue that has a chart answers its run: its own branch and stack
		// are the issue block.
		Issue *containerNextOut `json:"issue"`
	}
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		t.Fatalf("jig next %s --json: exit %d, %v\n%s", id, code, err, out)
	}
	if n.Issue != nil {
		return *n.Issue
	}
	return n.containerNextOut
}

// containerRig is a store of one repo and a chart of the issue TK-1: the tasks
// TK-2 and TK-3 (which waits for TK-2 to merge), the multi-task TK-4 and its
// task TK-5; TK-6 is of a legacy chart (no issue).
func containerRig(t *testing.T) chartRig {
	t.Helper()
	r := newChartRig(t)
	r.put(t, "project.yaml", "schema_version: 2\nname: demo\nkeys:\n  TK: everything in demo\ntrackers: []\nrepos:\n  - remote: /srv/app.git\n    label: app\n")
	r.ticket(t, "TK-1", "kind: issue\n", false)
	r.ticket(t, "TK-2", "", false)
	r.ticket(t, "TK-3", "blocked_by:\n  - ticket: TK-2\n    kind: merged\n", false)
	r.ticket(t, "TK-4", "kind: multi-task\n", false)
	r.ticket(t, "TK-5", "parent: TK-4\n", false)
	r.ticket(t, "TK-6", "", false)
	r.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-3\n    title: Three\n  - id: TK-4\n    title: Four\n    kind: multi-task\n  - id: TK-5\n    title: Five\n", "")
	r.chart(t, "old", "tickets:\n  - id: TK-6\n    title: Six\n", "")
	return r
}

// TestContainerNextIssueAndTasks: a task of a chart with an issue starts from
// the target and its pull request goes into the issue's branch; the task
// waiting for it to merge starts from the issue's branch; the issue names its
// own branch, starting from the target and going into it.
func TestContainerNextIssueAndTasks(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	issue := r.containerNext(t, "TK-1")
	if issue.Branch == "" || len(issue.Stack) != 1 {
		t.Fatalf("jig next TK-1 = %+v, want its branch and one repo row", issue)
	}
	if row := issue.Stack[0]; row.Repo != "app" || row.StartFrom != "origin/main" || row.PRTarget != "main" || row.Container != "" {
		t.Errorf("the issue's row = %+v, want it to start from origin/main and go into main", row)
	}

	first := r.containerNext(t, "TK-2")
	if len(first.Stack) != 1 {
		t.Fatalf("jig next TK-2 = %+v, want one repo row", first)
	}
	if row := first.Stack[0]; row.StartFrom != "origin/main" || row.PRTarget != issue.Branch || row.Container != "TK-1" || row.ContainerBranch != issue.Branch {
		t.Errorf("TK-2's row = %+v, want origin/main and a pull request into %s", row, issue.Branch)
	}
	second := r.containerNext(t, "TK-3")
	if row := second.Stack[0]; row.StartFrom != "origin/"+issue.Branch || row.PRTarget != issue.Branch {
		t.Errorf("TK-3's row = %+v, want it to start from origin/%s", row, issue.Branch)
	}

	text, code := r.run(t, "next", "TK-2")
	if code != 0 || !strings.Contains(text, "stack[1]{repo,parent,start_from,pr_target}:\n  app,TK-1,origin/main,"+issue.Branch) {
		t.Errorf("jig next TK-2 (exit %d) lacks the stack table naming the container:\n%s", code, text)
	}
	if !strings.Contains(text, "holds no copy of "+issue.Branch) || !strings.Contains(text, "git push origin origin/main:refs/heads/"+issue.Branch) {
		t.Errorf("jig next TK-2 does not say how to create the issue's branch:\n%s", text)
	}
}

// TestContainerNextMultiTask: a task under a multi-task goes into the
// multi-task's branch, and the multi-task itself starts from, and would go
// into, the issue's.
func TestContainerNextMultiTask(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	issue := r.containerNext(t, "TK-1")
	multi := r.containerNext(t, "TK-4")
	if multi.Branch == "" || len(multi.Stack) != 1 {
		t.Fatalf("jig next TK-4 = %+v", multi)
	}
	if row := multi.Stack[0]; row.StartFrom != "origin/"+issue.Branch || row.PRTarget != issue.Branch || row.Container != "TK-1" {
		t.Errorf("the multi-task's row = %+v, want it to start from and go into %s", row, issue.Branch)
	}
	task := r.containerNext(t, "TK-5")
	if row := task.Stack[0]; row.PRTarget != multi.Branch || row.Container != "TK-4" || row.StartFrom != "origin/main" {
		t.Errorf("TK-5's row = %+v, want a pull request into %s", row, multi.Branch)
	}
	text, _ := r.run(t, "next", "TK-5")
	if want := "git push origin origin/" + issue.Branch + ":refs/heads/" + multi.Branch; !strings.Contains(text, want) {
		t.Errorf("jig next TK-5 does not name the push from the issue's branch (%s):\n%s", want, text)
	}
}

// TestContainerNextLegacyChart: a ticket of a chart with no issue, and a ticket
// no chart lists, read exactly as before: no stack.
func TestContainerNextLegacyChart(t *testing.T) {
	t.Parallel()
	r := containerRig(t)
	r.ticket(t, "TK-7", "", false)
	for _, id := range []string{"TK-6", "TK-7"} {
		if n := r.containerNext(t, id); len(n.Stack) != 0 {
			t.Errorf("jig next %s has a stack %+v, want none", id, n.Stack)
		}
		text, _ := r.run(t, "next", id)
		if strings.Contains(text, "stack[") || strings.Contains(text, "holds no copy") {
			t.Errorf("jig next %s names a stack:\n%s", id, text)
		}
	}
}
