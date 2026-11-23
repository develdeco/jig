package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// stepsRow is the line of the Steps panel that names step, whole.
func stepsRow(g *rig, step int) string {
	g.t.Helper()
	for _, r := range panelLines(strings.Split(g.view(), "\n"), "Steps") {
		if strings.Contains(r, " "+stepNames[step]+" ") {
			return r
		}
	}
	g.t.Fatalf("no row of the %s in the Steps panel:\n%s", stepNames[step], g.view())
	return ""
}

// atClock sets the rig's clock to ts.
func atClock(g *rig, ts string) {
	g.t.Helper()
	now, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		g.t.Fatal(err)
	}
	g.m.now = func() time.Time { return now }
}

// approvedStatus is `jig status SHOP-1` once its brief is approved: the slice
// is queued and ready.
func approvedStatus() string {
	return strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "queued", "0", "-", "-", "Round to the cent"},
		}), "questions: none",
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Dana"}, {"provenance", "person"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{{"Goal", "round"}}),
	}, "\n") + "\n"
}

// TestAWaitHasItsOwnClockInTheNeedsYouColor: a brief that waits for its
// approval reads the sub-step's words and then how long it has waited, in the
// colour of a turn that is the person's, and the clock runs on its own: the
// work beside it stays the drafting time.
func TestAWaitHasItsOwnClockInTheNeedsYouColor(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	atClock(g, "2026-10-23T09:03:15Z") // 3m 08s after the draft's last call
	row := stepsRow(g, stepBrief)
	if !strings.Contains(row, "waiting for your approval 3m 08s") || !strings.Contains(row, " 6s ") {
		t.Errorf("3 minutes after the draft the brief reads\n%s\nwant its 6s of work and the wait at 3m 08s", row)
	}
	atClock(g, "2026-10-23T10:00:07Z")
	if row := stepsRow(g, stepBrief); !strings.Contains(row, "waiting for your approval 1h 00m") || !strings.Contains(row, " 6s ") {
		t.Errorf("an hour after the draft the brief reads\n%s", row)
	}

	g.m.st = styles{r: colored()}
	text := "drafted · 3 sections, 1 slice · waiting for your approval 1h 00m"
	if view := g.view(); !strings.Contains(view, g.m.st.state(StateNeedsInput, text)) {
		t.Errorf("the wait is not in the needs-you colour:\n%q\nwant %q in it", view, g.m.st.state(StateNeedsInput, text))
	}
}

// TestTheTotalSaysWorkedAndCountsNoWait: the Steps panel's title adds up work
// and says so, whatever a wait has stood.
func TestTheTotalSaysWorkedAndCountsNoWait(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	for _, now := range []string{"2026-10-23T09:05:00Z", "2026-10-23T10:00:07Z", "2026-10-24T10:00:07Z"} {
		atClock(g, now)
		title := briefWorkTitle(g)
		if !strings.Contains(title, "worked 6s") || strings.Contains(title, "total") {
			t.Errorf("at %s the Steps title reads %q, want the work of 6s", now, title)
		}
	}
}

// briefWorkTitle is the top edge of the Steps panel.
func briefWorkTitle(g *rig) string {
	g.t.Helper()
	panel := panelLines(strings.Split(g.view(), "\n"), "Steps")
	if len(panel) == 0 {
		g.t.Fatalf("no Steps panel:\n%s", g.view())
	}
	return panel[0]
}

// TestADoneWaitReadsWaited: once the approval is given the sub-step reads how
// long it waited in place of how long it took, from the draft's last line to the
// approval's, and the step's time is the drafting alone.
func TestADoneWaitReadsWaited(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	g.r.answer("status SHOP-1 --store /s", approvedStatus(), 0)
	atClock(g, "2026-10-23T10:05:00Z")
	g.feed(jl("2026-10-23T10:02:07Z", "SHOP-1", "", journal.EventApproval, with("", "", "", "Dana <dana@example.invalid>", "0123456789ab", "person")))
	tk := g.m.w.tickets["SHOP-1"]
	subs := g.m.phaseOf(tk).Of(phase.Brief).Subs
	p := g.m.substepsPanel(tk, stepBrief, subs, 10)
	var lines []string
	for _, l := range p.lines {
		lines = append(lines, l.text)
	}
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "brief approved") || !strings.Contains(text, "waited 1h 02m") || strings.Contains(text, "took 1h") {
		t.Errorf("the brief's sub-steps read\n%s\nwant the approval to have waited 1h 02m", text)
	}
	if got := g.m.steps(tk)[stepBrief].Took; got != "6s" {
		t.Errorf("the brief took %q once approved, want its 6s of drafting", got)
	}
	if got := briefWorkTitle(g); !strings.Contains(got, "worked") || strings.Contains(got, "1h") {
		t.Errorf("the Steps title reads %q, want only work", got)
	}
}

// TestTheHeaderFollowsTheOpenSubStep: a brief that is drafted and waits reads
// that it waits for approval; one being drafted keeps saying so.
func TestTheHeaderFollowsTheOpenSubStep(t *testing.T) {
	t.Parallel()
	g := draftedRig(t)
	if head := strings.Split(g.view(), "\n")[0]; !strings.HasSuffix(head, "brief waits for your approval") {
		t.Errorf("a drafted brief's header = %q, want it to end in \"brief waits for your approval\"", head)
	}
	drafting, _ := quietRig(t, draftStatus(nil))
	if head := strings.Split(drafting.view(), "\n")[0]; !strings.HasSuffix(head, "brief being drafted") {
		t.Errorf("a brief being drafted has the header %q, want it to end in \"brief being drafted\"", head)
	}
}

// TestALandingWaitCountsApart: a published ticket waits for its merge; the wait
// reads on the Land row from the publish's last line, the publish's own time
// is work, and the merge, once the landing is recorded, is a wait that ended.
func TestALandingWaitCountsApart(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	atClock(g, "2026-10-23T09:18:31Z") // 4m 10s after the publish
	g.feed(jig2()...)
	row := stepsRow(g, stepLand)
	if !strings.Contains(row, "waits for a merge 4m 10s") {
		t.Errorf("the Land row reads\n%s\nwant it to wait for a merge 4m 10s", row)
	}
	if got := g.m.steps(g.m.w.tickets["JIG-2"])[stepLand].Took; got != "" {
		t.Errorf("the land step has work %q, want none: it only waits", got)
	}
	if title := briefWorkTitle(g); strings.Contains(title, "4m 10s") {
		t.Errorf("the Steps title %q counts the wait", title)
	}

	// The person's word names the merge: the session merging it is at work, and
	// nobody waits until it records the landing.
	g.feed(ll("2026-10-23T09:18:45Z", "JIG-2", journal.EventLandNamed, func(l *journal.Line) { l.Session = "s-builder" }))
	if row := stepsRow(g, stepLand); strings.Contains(row, "waits for a merge 4") {
		t.Errorf("the Land row reads\n%s\nwant no wait once the merge is named", row)
	}
	g.feed(jl("2026-10-23T09:19:00Z", "JIG-2", "", journal.EventLanded, func(l *journal.Line) {
		l.URL, l.Session = "https://example.test/acme/demo/pull/7", "s-builder"
	}))
	tk := g.m.w.tickets["JIG-2"]
	d := g.m.phaseOf(tk)
	w, ok := g.m.clocks(tk, d).waitOf(stepLand, "merge")
	if !ok || w.standing() || w.end.Sub(w.start) != 4*time.Minute+24*time.Second || w.forWhom != phase.WaitsForHost {
		t.Errorf("the merge's wait = %+v (%v), want it ended after 4m 24s, at the merge's naming, for the host", w, ok)
	}
	if got := g.m.steps(tk)[stepLand].Took; got != "15s" {
		t.Errorf("the land step took %q, want the 15s from the naming to the record", got)
	}
}

// TestATimePastAHundredHoursFitsItsColumn: a step that has run for 332 hours
// reads "332h" in its seven cells, with no ellipsis.
func TestATimePastAHundredHoursFitsItsColumn(t *testing.T) {
	t.Parallel()
	if got := span(332*time.Hour + 51*time.Minute); got != "332h" {
		t.Errorf("span(332h51m) = %q, want 332h", got)
	}
	if got := span(99*time.Hour + 59*time.Minute); got != "99h 59m" {
		t.Errorf("span(99h59m) = %q, want 99h 59m", got)
	}
	g := newRig(t, "SHOP-1")
	row := ansi.Strip(g.m.stepLine(stepRow{Step: stepBuild, State: StateBuilding, Text: "slice round is being built", Took: ago(332*time.Hour + 51*time.Minute)}, true))
	if !strings.Contains(row, " 332h    slice round") || strings.Contains(row, "…") {
		t.Errorf("the build row reads %q, want 332h in its seven cells", row)
	}
}

// TestAWaitStartsAtTheLineThatOpenedItNotAtItsOwn: the asks a round left wait
// from the round's close; a decision on one of them is a line of the asks
// sub-step, not the start of the wait for the others.
func TestAWaitStartsAtTheLineThatOpenedItNotAtItsOwn(t *testing.T) {
	t.Parallel()
	placed := []placedLine{
		{jl(at(1, 0), "JIG-1", "", journal.EventGateOpen), stepReview, "round:1"},
		{jl(at(2, 0), "JIG-1", "", journal.EventGateRound), stepReview, "round:1"},
		{jl(at(3, 0), "JIG-1", "", journal.EventTriage), stepReview, "asks"},
	}
	start, ok := waitStart(placed, stepReview, "asks", time.Time{}, nil)
	if want, _ := placed[1].l.At(); !ok || !start.Equal(want) {
		t.Errorf("the asks wait from %v (%v), want the round's close at %v", start, ok, want)
	}
}

// TestAnAnsweredQuestionIsAWaitNotWork: a slice's question stood unanswered for
// three minutes; what the builder's session did meanwhile and the time until the
// answer are the wait's, and the slice counts the minute before the question and
// the minute after the answer.
func TestAnAnsweredQuestionIsAWaitNotWork(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"c", "green", "1", "-", "-", "Pick the tone"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	atClock(g, "2026-10-23T09:06:00Z")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.feed(
		jl(at(1, 0), "SHOP-1", "c", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(2, 0), "SHOP-1", "c", journal.EventQuestion, with("s-b", "", "", "Formal or casual?", "q-001", "agent")),
		toolOn("SHOP-1", at(3, 0), "s-b", "go test ./...", said("Run the tests")),
		jl(at(5, 0), "SHOP-1", "c", journal.EventAnswer, with("", "", "", "Casual.", "q-001", "person")),
		jl(at(6, 0), "SHOP-1", "c", journal.EventVerified, with("", sha('a'), "", "", "", "")),
	)
	tk := g.m.w.tickets["SHOP-1"]
	if got := g.m.steps(tk)[stepBuild].Took; got != "2m 00s" {
		t.Errorf("the build took %q, want the two minutes before the question and after the answer", got)
	}
	for _, p := range g.m.placed(tk, g.m.phaseOf(tk)) {
		if p.l.Command != "" && p.key == "slice:c" {
			t.Errorf("the call made while the question stood is held by slice c")
		}
	}
	closed, open := tk.answered()
	if len(closed) != 1 || closed[0].end.Sub(closed[0].start) != 3*time.Minute || closed[0].key != "slice:c" || len(open) != 0 {
		t.Errorf("answered = %+v, %v, want one wait of three minutes on slice c", closed, open)
	}
}
