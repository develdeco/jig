package phase

import (
	"reflect"
	"strings"
	"testing"
)

// ticketUnlanded is a published ticket with a recorded pull request and no
// landed line: it is at Land. onHost says the host reports it merged.
func ticketUnlanded(id string, onHost bool) *Ticket {
	f, lines := publishedWithPR()
	f.Ticket, f.MergedOnHost = id, onHost
	t := Derive(f, lines)
	return &t
}

// ticketLanded is a published ticket whose landing is recorded.
func ticketLanded(id string) *Ticket {
	f, lines := publishedWithPR()
	f.Ticket, f.Landed = id, true
	t := Derive(f, lines)
	return &t
}

func model(entries ...ChartEntry) ChartFacts {
	return ChartFacts{Name: "c", Issue: "ISS-1", Entries: entries, Tail: &ChartTail{State: TailWaiting, Waits: []string{"x"}}}
}

func landIDs(c Chart) []string {
	var out []string
	for _, l := range c.Lands {
		out = append(out, l.ID)
	}
	return out
}

// TestIssueRunStopsAtEachLanding: an issue of two dependent tasks. The first
// published and unlanded: the run stops at Land A with B an end behind it, in
// the issue's own id; once A has landed B runs; once B is published it stops at
// Land B; then the suite, the publish, Land <issue>, and a landed issue is done.
func TestIssueRunStopsAtEachLanding(t *testing.T) {
	t.Parallel()
	a := func(tk *Ticket) ChartEntry { e := entry(1, "A-1", tk); e.Into = "iss-1"; return e }
	b := func(tk *Ticket, met bool) ChartEntry { return entry(2, "B-1", tk, mergedOn("A-1", met)) }

	c := DeriveChart(model(a(ticketUnlanded("A-1", false)), b(ticketAt("B-1"), false)))
	if c.Code != CodeChartWaitsLand || c.Ready || c.Prompt != "Continue ISS-1" {
		t.Fatalf("stop at A: code %s ready %v prompt %q", c.Code, c.Ready, c.Prompt)
	}
	if want := "A-1 waits for the person's word: Land A-1"; c.Reason != want {
		t.Errorf("reason = %q, want %q", c.Reason, want)
	}
	if len(c.Lands) != 1 || c.Lands[0].Prompt != "Land A-1" || c.Lands[0].State != "waits for a merge" || len(c.Lands[0].After) != 0 {
		t.Errorf("lands = %+v", c.Lands)
	}
	if len(c.Ends) != 1 || c.Ends[0].ID != "B-1" {
		t.Fatalf("ends = %+v, want B-1 behind the landing", c.Ends)
	}

	// A has landed: B is the run again.
	c = DeriveChart(model(a(ticketLanded("A-1")), b(ticketAt("B-1"), true)))
	if !c.Ready || !reflect.DeepEqual(c.Run, [][]string{{"B-1"}}) || c.Prompt != "Continue ISS-1" || len(c.Lands) != 0 {
		t.Fatalf("after Land A: %+v", c)
	}

	// B published, unlanded: stop at Land B.
	c = DeriveChart(model(a(ticketLanded("A-1")), b(ticketUnlanded("B-1", false), true)))
	if c.Code != CodeChartWaitsLand || !reflect.DeepEqual(landIDs(c), []string{"B-1"}) {
		t.Fatalf("stop at B: %s %v", c.Code, landIDs(c))
	}

	// Both landed, suite not run: the issue's tail is the run's own work.
	f := model(a(ticketLanded("A-1")), b(ticketLanded("B-1"), true))
	f.Tail = &ChartTail{State: TailWaiting, Unmet: []ChartUnmet{{Code: CodeSuiteNotRun, Why: "no run", Prompt: "Run `jig verify ISS-1 --final`"}}}
	c = DeriveChart(f)
	if !c.Ready || c.Issue == nil || c.Issue.Stage != IssueSuite || c.Issue.Prompt == "" || c.Code == CodeChartDone {
		t.Fatalf("suite: %+v", c.Issue)
	}

	f.Tail = &ChartTail{State: TailWaiting, Unmet: []ChartUnmet{{Code: CodeSuiteFailed, Why: "failed", Prompt: "Add a fix task for ISS-1"}}}
	if c = DeriveChart(f); !c.Ready || c.Issue.Stage != IssueFix || c.Issue.Code != CodeSuiteFailed {
		t.Fatalf("fix: %+v", c.Issue)
	}

	f.Tail = &ChartTail{State: TailWaiting, Unmet: []ChartUnmet{{Code: "LAND_OTHER", Why: "other", Prompt: "do it"}}}
	if c = DeriveChart(f); c.Ready || c.Issue.Stage != IssueUnmet || c.Issue.Why != "other" || c.Code != CodeChartBlocked {
		t.Fatalf("other precondition: %+v code %s", c.Issue, c.Code)
	}

	f.Tail = &ChartTail{State: TailReady, Prompt: "Publish ISS-1"}
	if c = DeriveChart(f); !c.Ready || c.Issue.Stage != IssuePublish || c.Issue.Prompt != "Publish ISS-1" {
		t.Fatalf("publish: %+v", c.Issue)
	}

	f.Tail = &ChartTail{State: TailPublished}
	c = DeriveChart(f)
	if c.Code != CodeChartWaitsLand || c.Issue.Stage != IssuePublished || !reflect.DeepEqual(landIDs(c), []string{"ISS-1"}) || c.Reason != "ISS-1 waits for the person's word: Land ISS-1" {
		t.Fatalf("published: %s %+v %v %q", c.Code, c.Issue, landIDs(c), c.Reason)
	}

	f.Tail = &ChartTail{State: TailLanded}
	if c = DeriveChart(f); c.Code != CodeChartDone || c.Issue.Stage != IssueLanded {
		t.Fatalf("landed: %s %+v", c.Code, c.Issue)
	}
}

// TestIssueRunEveryTicketPublishedIsNotDone: a legacy chart with every ticket
// published is done; a model chart is done only when the issue landed.
func TestIssueRunEveryTicketPublishedIsNotDone(t *testing.T) {
	t.Parallel()
	e := entry(1, "A-1", ticketPublished("A-1"))
	if c := DeriveChart(ChartFacts{Name: "c", Entries: []ChartEntry{e}}); c.Code != CodeChartDone || c.Model || c.Issue != nil || len(c.Lands) != 0 {
		t.Errorf("legacy chart = %s model %v", c.Code, c.Model)
	}
	f := ChartFacts{Name: "c", Issue: "ISS-1", Entries: []ChartEntry{e}, Tail: &ChartTail{State: TailWaiting, Waits: []string{"A-1"}}}
	c := DeriveChart(f)
	if c.Code == CodeChartDone || c.Issue.Stage != IssueLanding || !strings.Contains(c.Reason, "ISS-1 waits for A-1 to land") {
		t.Errorf("model chart with every ticket published: %s %q", c.Code, c.Reason)
	}
}

// TestIssueRunHeldLandingHoldsOnlyItsDependents: a task that does not depend on
// the unlanded one is still run.
func TestIssueRunHeldLandingHoldsOnlyItsDependents(t *testing.T) {
	t.Parallel()
	c := DeriveChart(model(
		entry(1, "A-1", ticketUnlanded("A-1", false)),
		entry(2, "B-1", ticketAt("B-1"), mergedOn("A-1", false)),
		entry(3, "C-1", ticketAt("C-1")),
	))
	if !c.Ready || !reflect.DeepEqual(c.Run, [][]string{{"C-1"}}) || c.Prompt != "Continue ISS-1" && c.Prompt != "Take ISS-1 to PR" {
		t.Fatalf("run = %v ready %v prompt %q", c.Run, c.Ready, c.Prompt)
	}
	if len(c.Ends) != 1 || c.Ends[0].ID != "B-1" || len(c.Lands) != 1 {
		t.Errorf("ends %v lands %v", c.Ends, c.Lands)
	}
}

// TestIssueRunLandsNameWhatMustLandFirstAndHostMerges: lands are in row order,
// each with what must land first; a merge made on the host is a row whose
// state says it only needs recording, and the reason names the first landable
// and the others after it.
func TestIssueRunLandsNameWhatMustLandFirstAndHostMerges(t *testing.T) {
	t.Parallel()
	a := entry(1, "A-1", ticketUnlanded("A-1", true))
	b := entry(2, "B-1", ticketUnlanded("B-1", false), mergedOn("A-1", true))
	b.After = []string{"A-1"}
	c := DeriveChart(model(a, b))
	if c.Code != CodeChartWaitsLand {
		t.Fatalf("code = %s", c.Code)
	}
	if c.Lands[0].State != "merged on the host, not recorded" || c.Lands[1].State != "waits for a merge" || !reflect.DeepEqual(c.Lands[1].After, []string{"A-1"}) {
		t.Errorf("lands = %+v", c.Lands)
	}
	if want := "A-1 waits for the person's word: Land A-1; then Land B-1, after A-1"; c.Reason != want {
		t.Errorf("reason = %q, want %q", c.Reason, want)
	}
}

// TestIssueRunMultiTaskIsPublishedAndLanded: a multi-task is never briefed or
// run; ready it is in publish, published it is in lands and gates a dependent.
func TestIssueRunMultiTaskIsPublishedAndLanded(t *testing.T) {
	t.Parallel()
	m := entry(1, "M-1", ticketBriefing("M-1"))
	m.Kind, m.Container = "multi-task", &ChartContainer{State: TailReady, Prompt: "Publish M-1"}
	task := entry(2, "TASK-1", ticketLanded("TASK-1"))
	c := DeriveChart(model(m, task))
	if len(c.Briefs) != 0 || len(c.Run) != 0 || !c.Ready || !reflect.DeepEqual(c.Publish, []ChartPublish{{ID: "M-1", Prompt: "Publish M-1"}}) {
		t.Fatalf("ready multi-task: briefs %v run %v publish %v", c.Briefs, c.Run, c.Publish)
	}
	m.Container = &ChartContainer{State: TailPublished, Prompt: "Land M-1"}
	dep := entry(3, "D-1", ticketAt("D-1"), mergedOn("M-1", false))
	c = DeriveChart(model(m, task, dep))
	if len(c.Publish) != 0 || !reflect.DeepEqual(landIDs(c), []string{"M-1"}) || c.Code != CodeChartWaitsLand || len(c.Ends) != 1 || c.Ends[0].ID != "D-1" {
		t.Fatalf("published multi-task: publish %v lands %v code %s ends %v", c.Publish, landIDs(c), c.Code, c.Ends)
	}
}

// TestIssueRunEndReasonNamesTheBranch: a dependent of an unlanded ticket waits
// for it to land into the branch it lands into.
func TestIssueRunEndReasonNamesTheBranch(t *testing.T) {
	t.Parallel()
	a := entry(1, "A-1", ticketAt("A-1"))
	a.Into = "iss-1"
	b := entry(2, "B-1", ticketAt("B-1"), mergedOn("A-1", false))
	f := model(a, b)
	f.Named = nil
	d := &chartDeriver{f: f, byID: map[string]int{"A-1": 0, "B-1": 1}}
	if got, want := d.waitsReason(ChartEnd{Pos: 2, ID: "B-1", Because: b.Needs[0]}), "B-1 waits for A-1 to land into iss-1"; got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
	legacy := &chartDeriver{f: ChartFacts{Entries: f.Entries}, byID: d.byID}
	if got := legacy.waitsReason(ChartEnd{Pos: 2, ID: "B-1", Because: b.Needs[0]}); !strings.Contains(got, "to merge") {
		t.Errorf("legacy reason = %q", got)
	}
}

// TestIssueRunNamedPromptKeepsTheTickets: a run named over tickets keeps its
// own prompt, not the issue's.
func TestIssueRunNamedPromptKeepsTheTickets(t *testing.T) {
	t.Parallel()
	f := model(entry(1, "A-1", ticketBriefing("A-1")), entry(2, "B-1", ticketBriefing("B-1")))
	f.Named, f.Tail = []string{"A-1", "B-1"}, nil
	if c := DeriveChart(f); c.Prompt != "Take A-1 and B-1 to PR" {
		t.Errorf("prompt = %q", c.Prompt)
	}
	f.Named = nil
	if c := DeriveChart(f); c.Prompt != "Take ISS-1 to PR" {
		t.Errorf("prompt = %q", c.Prompt)
	}
}
