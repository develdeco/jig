package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestSharedPublishNamesItsIssue: `jig publish` of a task two issues list
// refuses with SHARED_TASK_ISSUE_NEEDED, listing its issues, when it names none
// or one that is not the task's; a task of one issue takes no --issue.
func TestSharedPublishNamesItsIssue(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "fixture@example.invalid"}, {"config", "user.name", "fixture"}} {
		if _, err := gitx.Run(r.root, args...); err != nil {
			t.Fatal(err)
		}
	}
	r.ticket(t, "SHOP-1", "kind: issue\n", true)
	r.ticket(t, "SHOP-2", "kind: issue\n", true)
	r.ticket(t, "SHOP-3", "", true)
	r.ticket(t, "SHOP-4", "", true)
	r.chart(t, "one", "issue: SHOP-1\ntickets:\n  - id: SHOP-3\n    title: shared\n  - id: SHOP-4\n    title: alone\n", "")
	r.chart(t, "two", "issue: SHOP-2\ntickets:\n  - id: SHOP-3\n    title: shared\n", "")

	for _, args := range [][]string{{"publish", "SHOP-3"}, {"publish", "SHOP-3", "--issue", "SHOP-9"}} {
		out, code := r.run(t, args...)
		if code == 0 || !strings.Contains(out, "SHARED_TASK_ISSUE_NEEDED") || !strings.Contains(out, "SHOP-1: no pull request") || !strings.Contains(out, "SHOP-2: no pull request") {
			t.Errorf("jig %s: exit %d, want SHARED_TASK_ISSUE_NEEDED listing both issues:\n%s", strings.Join(args, " "), code, out)
		}
	}
	out, code := r.run(t, "publish", "SHOP-4", "--issue", "SHOP-2")
	if code == 0 || !strings.Contains(out, "SHARED_TASK_ISSUE_NEEDED") {
		t.Errorf("jig publish SHOP-4 --issue SHOP-2: exit %d, want a refusal:\n%s", code, out)
	}
}
