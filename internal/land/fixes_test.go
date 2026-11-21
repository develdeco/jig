package land_test

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// TestLandJudgeBehindIsNotLandable: a published ticket owed a restack (its
// parent's branch moved past it) or a retarget (its parent merged while its
// branch lacks the base's head) is refused LAND_NOT_PUBLISHED with no merge to
// name; once its landing is on record in that repo it is no refusal.
func TestLandJudgeBehindIsNotLandable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ticket("T-1", store.Ticket{})
	w.publish("T-1", "", prURL("1"), ready.PROpen, "main")
	for name, repo := range map[string]stack.Repo{
		"behind":   {Repo: "app", Behind: true, StartFrom: "T-9"},
		"retarget": {Repo: "app", Retarget: true, StartFrom: "origin/main"},
	} {
		w.d.Plan = func(string, string) (stack.Plan, error) { return stack.Plan{Repos: []stack.Repo{repo}}, nil }
		v := w.judge("T-1", land.Options{})
		wantRefusal(t, v, land.CodeNotPublished)
		if !strings.Contains(v.Refusal.Msg, "restack or retarget") || !strings.Contains(strings.Join(v.Refusal.Help, " "), repo.StartFrom) {
			t.Errorf("%s: refusal = %+v, want the merge to make named", name, v.Refusal)
		}
	}
	w.d.Plan = func(string, string) (stack.Plan, error) {
		return stack.Plan{Repos: []stack.Repo{{Repo: "app", Behind: false, Retarget: false}}}, nil
	}
	if v := w.judge("T-1", land.Options{}); v.Refusal != nil || len(v.Merge) != 1 {
		t.Errorf("not behind = %+v, want its merge named", v)
	}
	w.d.Plan = func(string, string) (stack.Plan, error) {
		return stack.Plan{Repos: []stack.Repo{{Repo: "app", Behind: true}}}, nil
	}
	if err := journal.Append(w.st, "T-1", land.Landing{PR: prURL("1"), Base: "main", Provenance: journal.ProvenanceAgent}.Line("T-1")); err != nil {
		t.Fatal(err)
	}
	if v := w.judge("T-1", land.Options{}); v.Refusal != nil || !v.Done {
		t.Errorf("landed in the repo, behind or not = %+v, want done", v)
	}
}

// freshReader is a merge reader that has asked the host nothing yet.
func freshReader(w *world) land.Merges {
	return &mergefacts.Reader{Store: w.st, Cfg: w.d.Cfg, PRs: w.h}
}

// sharedChild is two issues I-1 and I-2 that share the task T-1, published
// into both, with its pull request into I-1 merged and into I-2 open; both
// issues have published a pull request of their own, and T-9 is a task of I-1
// that waits for T-1 to merge.
func sharedChild(t *testing.T) *world {
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("I-2", store.Ticket{Kind: store.KindIssue})
	w.ticket("T-1", store.Ticket{})
	w.ticket("T-9", store.Ticket{BlockedBy: []store.TicketBlockedBy{{Ticket: "T-1", Kind: "merged"}}})
	w.chart("a", "I-1", "T-1", "T-9")
	w.chart("b", "I-2", "T-1")
	w.publish("I-1", "", prURL("10"), ready.PROpen, "main")
	w.publish("I-2", "", prURL("20"), ready.PROpen, "main")
	w.publish("T-1", "I-1", prURL("1"), ready.PRMerged, w.branch("I-1"))
	w.publish("T-1", "I-2", prURL("2"), ready.PROpen, w.branch("I-2"))
	w.publish("T-9", "I-1", prURL("9"), ready.PROpen, w.branch("I-1"))
	return w
}

// TestLandJudgeReadsASharedChildForTheContainer: an issue lands once its shared
// task merged into it, whatever the other issue did; a task of the issue that
// waits for the shared task reads it for the issue's own pull request too, and
// the same issue's Complete agrees with Judge.
func TestLandJudgeReadsASharedChildForTheContainer(t *testing.T) {
	t.Parallel()
	w := sharedChild(t)
	v := w.judge("I-1", land.Options{})
	wantRefusal(t, v, land.CodeWaits, "T-9")
	if strings.Contains(v.Refusal.Msg, "T-1:") {
		t.Errorf("I-1 waits on the shared task merged into it: %s", v.Refusal.Msg)
	}
	if v := w.judge("T-9", land.Options{}); v.Refusal != nil || len(v.Merge) != 1 {
		t.Errorf("T-9 waits for T-1, merged into I-1: %+v, want its merge named", v)
	}
	w.d.Merges = freshReader(w)
	if _, waiting, err := land.Complete(w.d, "I-1"); err != nil || strings.Join(waiting, ",") != "T-9" {
		t.Errorf("Complete(I-1) = waiting %v, %v, want T-9 alone", waiting, err)
	}

	// Merged into the other issue only, the shared task is no merge into I-1.
	w2 := sharedChild(t)
	w2.h.status[prURL("1")] = repohost.Status{State: repohost.StateOpen, Base: w2.branch("I-1")}
	w2.h.status[prURL("2")] = repohost.Status{State: repohost.StateMerged, Base: w2.branch("I-2")}
	wantRefusal(t, w2.judge("T-9", land.Options{}), land.CodeWaits, "T-1")
	v = w2.judge("I-1", land.Options{})
	wantRefusal(t, v, land.CodeWaits, "T-1", "T-9")
	if help := strings.Join(v.Refusal.Help, " "); !strings.Contains(help, "jig land T-1 --issue I-1") {
		t.Errorf("help = %q, want the shared task named with its issue", help)
	}
}

// TestLandCommandNamesTheIssueOfASharedTask: a task two issues share is landed
// with --issue, the one it lands into; any other ticket is landed bare.
func TestLandCommandNamesTheIssueOfASharedTask(t *testing.T) {
	t.Parallel()
	w := sharedChild(t)
	for _, tc := range []struct{ ticket, into, want string }{
		{"T-1", "I-2", "jig land T-1 --issue I-2"},
		{"T-1", "", "jig land T-1 --issue <ID>"},
		{"T-1", "I-7", "jig land T-1 --issue <ID>"},
		{"T-9", "I-1", "jig land T-9"},
		{"I-1", "", "jig land I-1"},
		{"T-404", "I-1", "jig land T-404"},
	} {
		if got := land.Command(w.st, tc.ticket, tc.into); got != tc.want {
			t.Errorf("Command(%s, %q) = %q, want %q", tc.ticket, tc.into, got, tc.want)
		}
	}
}
