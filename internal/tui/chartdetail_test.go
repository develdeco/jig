package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// claimLine is the machine ledger's line for a tool call of session that ran
// command, made by agent ("" for the session itself).
func claimLine(ts, ticket, session, agent, command string) events.Envelope {
	return ll(ts, ticket, journal.EventTool, func(l *journal.Line) {
		l.Session, l.Agent, l.Tool, l.Command = session, agent, "Bash", command
	})
}

// heldChartRig is chartRig where the claim of JIG-5's slice s2 was made by a
// subagent: the machine ledger has the tool line of the call, with the agent.
func heldChartRig(t *testing.T) *rig {
	t.Helper()
	g := chartRig(t)
	return g.feed(claimLine(at(2, 1), "JIG-5", "s-builder", "agent-4f2a91c0e7", "jig claim JIG-5 --slice s2 --unattended"))
}

// onChart opens the chart screen of a rig that shows the project's list.
func onChart(g *rig) *rig {
	g.t.Helper()
	g.press("end", "enter")
	if g.m.Screen() != "chart" {
		g.t.Fatalf("on %s, want chart:\n%s", g.m.Screen(), g.view())
	}
	return g
}

// detailPanelOf is the lines of the Detail panel of a screen, the edges included.
func detailPanelOf(t *testing.T, view string) []string {
	t.Helper()
	panel := panelLines(strings.Split(view, "\n"), "Detail")
	if panel == nil {
		t.Fatalf("the screen has no Detail panel:\n%s", view)
	}
	return panel
}

// TestChartDetailNamesTheHolderTheAgentAndTheStage: the Detail panel of the
// ticket under the cursor says which slice is being built, how far it has come,
// who holds its claim and which agent made the call.
func TestChartDetailNamesTheHolderTheAgentAndTheStage(t *testing.T) {
	t.Parallel()
	g := onChart(heldChartRig(t))
	panel := detailPanelOf(t, g.view())
	if !strings.HasSuffix(panel[0], "JIG-5 ─╮") {
		t.Errorf("the top edge does not end with the ticket's id:\n%s", strings.Join(panel, "\n"))
	}
	if got := strings.Join(panel, "\n"); !strings.Contains(got, "slice    s2 · verifying · builder · agent 4f2a91c0") {
		t.Errorf("the panel does not name the slice, its stage, its holder and its agent:\n%s", got)
	}
}

// TestChartDetailOfASessionWithNoAgent: a session that builds by itself has no
// agent, and the row names the holder alone.
func TestChartDetailOfASessionWithNoAgent(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	got := strings.Join(detailPanelOf(t, g.view()), "\n")
	if !strings.Contains(got, "s2 · verifying · builder") {
		t.Errorf("the panel does not name the slice, its stage and its holder:\n%s", got)
	}
	if strings.Contains(got, "agent") {
		t.Errorf("the panel names an agent for a claim no tool line of an agent made:\n%s", got)
	}
}

// TestChartDetailFollowsTheCursorAndReadsNothingNew: the panel is the ticket
// under the cursor, drawn again from what the screens hold, with no verb run.
func TestChartDetailFollowsTheCursorAndReadsNothingNew(t *testing.T) {
	t.Parallel()
	g := onChart(heldChartRig(t))
	calls := len(g.r.calls)
	g.press("down")
	panel := detailPanelOf(t, g.view())
	if !strings.HasSuffix(panel[0], "JIG-8 ─╮") {
		t.Errorf("the panel is not the next ticket's:\n%s", strings.Join(panel, "\n"))
	}
	if len(panel) != 3 || !strings.HasPrefix(strings.TrimPrefix(panel[1], "  │  "), "state ") {
		t.Errorf("the panel of a ticket nothing builds is not its one state row:\n%s", strings.Join(panel, "\n"))
	}
	if len(g.r.calls) != calls {
		t.Errorf("moving the cursor ran %v", g.r.calls[calls:])
	}
	g.press("up")
	if got := strings.Join(detailPanelOf(t, g.view()), "\n"); !strings.Contains(got, "agent 4f2a91c0") {
		t.Errorf("the cursor back on JIG-5 does not bring its detail back:\n%s", got)
	}
	if len(g.r.calls) != calls {
		t.Errorf("moving the cursor ran %v", g.r.calls[calls:])
	}
}

// TestChartDetailOfATicketWithNothingBuilding: a published ticket, a ticket
// nothing has started and a ticket the screens do not know each have the one
// state row, so the layout does not move with the cursor.
func TestChartDetailOfATicketWithNothingBuilding(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	g.press("up")
	panel := detailPanelOf(t, g.view())
	if !strings.HasSuffix(panel[0], "JIG-4 ─╮") || len(panel) != 3 || !strings.Contains(panel[1], "state    published") {
		t.Errorf("the panel of a published ticket does not say so in its one row:\n%s", strings.Join(panel, "\n"))
	}
	g.press("down", "down")
	panel = detailPanelOf(t, g.view())
	if !strings.HasSuffix(panel[0], "JIG-8 ─╮") || len(panel) != 3 {
		t.Errorf("the panel of a ticket nothing builds is not one row:\n%s", strings.Join(panel, "\n"))
	}
}

// TestChartDetailOfATicketTheScreensDoNotKnowSaysSo: a ticket the chart lists
// that no line or status has reached has the row "not read yet".
func TestChartDetailOfATicketTheScreensDoNotKnowSaysSo(t *testing.T) {
	t.Parallel()
	rows := chartRig(t).m.chartDetailRows(chartRow{id: "JIG-9"})
	if len(rows) != 1 || rows[0].label != "state" || !strings.Contains(rows[0].text, "not read yet") {
		t.Errorf("got %v, want the one state row, not read yet", rows)
	}
}

// TestChartDetailTakesItsRowsFromTheMapAndTheRunPanelFirst: a short screen gives
// up the Map, then the Run panel's rows, and keeps the Detail panel; a tall one
// has room for all of them.
func TestChartDetailTakesItsRowsFromTheMapAndTheRunPanelFirst(t *testing.T) {
	t.Parallel()
	g := chartScreenRig(t)
	v := g.view()
	if strings.Contains(v, "map:") || strings.Contains(v, "╭─ Map") {
		t.Errorf("the 80 by 24 screen still has the map:\n%s", v)
	}
	run := strings.Join(panelLines(strings.Split(v, "\n"), "Run"), "\n")
	if !strings.Contains(run, "next ") || !strings.Contains(run, "ends at") || strings.Contains(run, "wave") {
		t.Errorf("the Run panel did not give up its waves and keep next and ends at:\n%s", run)
	}
	if got := detailPanelOf(t, v); len(got) != 3 {
		t.Errorf("the Detail panel has %d rows with its frame, want 3:\n%s", len(got), strings.Join(got, "\n"))
	}

	tall := chartRig(t)
	tall.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	onChart(tall)
	v = tall.view()
	if !strings.Contains(v, "╭─ Map") {
		t.Errorf("the 100 by 40 screen has no Map panel:\n%s", v)
	}
	run = strings.Join(panelLines(strings.Split(v, "\n"), "Run"), "\n")
	for _, want := range []string{"next ", "wave 1", "wave 2", "ends at"} {
		if !strings.Contains(run, want) {
			t.Errorf("the Run panel of the tall screen has lost %q:\n%s", want, run)
		}
	}
	if len(detailPanelOf(t, v)) != 3 {
		t.Errorf("the Detail panel of the tall screen is not its row:\n%s", v)
	}
}

// TestChartDetailIsAbsentForAChartOfFogOnly: a chart with no ticket has none to
// detail.
func TestChartDetailIsAbsentForAChartOfFogOnly(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	roster := jsonLine(map[string]any{
		"tickets": []map[string]any{},
		"charts":  []map[string]any{{"name": "ideas", "title": "Ideas", "tickets": []string{}, "ungraduated": 2}},
	})
	verb := jsonLine(map[string]any{
		"chart": "ideas", "title": "Ideas", "ready": false, "code": "CHART_NOT_GRADUATED",
		"reason": "run `jig graduate ideas`", "prompt": "", "started": false, "ungraduated": 2,
		"tickets": []map[string]any{{"id": "", "title": "Gift cards"}, {"id": "", "title": "Loyalty points"}},
		"map":     checkoutMap(),
	})
	answerChart(g, roster, "ideas", verb, nil)
	g.feed().press("enter")
	if g.m.Screen() != "chart" {
		t.Fatalf("on %s, want chart:\n%s", g.m.Screen(), g.view())
	}
	if v := g.view(); strings.Contains(v, "Detail") {
		t.Errorf("a chart of fog entries has a Detail panel:\n%s", v)
	}
}

// buildingRig is a chart "checkout" whose ticket JIG-5 has slices s1 to sN all
// being built, each claimed by session s-builder two minutes after the last
// (the claim of the first by the agent a1, the others by the session itself),
// at the given width and height, on its chart screen.
func buildingRig(t *testing.T, width, height, n int) *rig {
	t.Helper()
	var rows [][]string
	var lines []events.Envelope
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s%d", i)
		rows = append(rows, []string{id, "building", "1", "-", "-", "Slice " + id})
		lines = append(lines, jl(at(2*i, 0), "JIG-5", id, journal.EventClaim, with("s-builder", "", "", "", "", "")))
	}
	status := strings.Join([]string{"ticket: JIG-5", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, rows), "questions: none",
		axi.Help("Run `jig next JIG-5` to see what is ready, then `jig claim JIG-5 --slice <slice>` to take one; ask: Build JIG-5"),
	}, "\n") + "\n"
	g := newRig(t, "")
	_, all := chartTickets()
	all["JIG-5"] = status
	answerChart(g, chartRoster(checkoutListing()), "checkout", checkoutJSON(), all)
	g.send(tea.WindowSizeMsg{Width: width, Height: height})
	g.feed(asTicket(jig2(), "JIG-4")...)
	g.feed(lines...)
	g.feed(claimLine(at(2, 1), "JIG-5", "s-builder", "agent-a1", "jig claim JIG-5 --slice s1"))
	return onChart(g)
}

// TestChartDetailGivesUpRowsDownToOneOnAShortScreen: the Detail panel has the
// row of each slice being built, at most detailMax, and a short screen cuts
// them, the last first, after the Map and the Run panel went, down to one.
func TestChartDetailGivesUpRowsDownToOneOnAShortScreen(t *testing.T) {
	t.Parallel()
	tall := buildingRig(t, 100, 50, 3)
	panel := detailPanelOf(t, tall.view())
	if len(panel) != 2+3 {
		t.Fatalf("three slices are being built and the Detail panel has %d rows:\n%s", len(panel)-2, strings.Join(panel, "\n"))
	}
	if got := strings.Join(panel, "\n"); !strings.Contains(got, "slice    s1 · claimed · builder · agent a1") ||
		!strings.Contains(got, "         s2 · claimed · builder") || !strings.Contains(got, "         s3 · claimed · builder") {
		t.Errorf("the rows do not name each slice, its stage and its holder, the label on the first only:\n%s", got)
	}

	for _, c := range []struct{ height, rows int }{{24, 2}, {23, 1}} {
		v := buildingRig(t, 80, c.height, 3).view()
		panel = detailPanelOf(t, v)
		if len(panel)-2 != c.rows || !strings.Contains(strings.Join(panel, "\n"), "slice    s1 · claimed") || strings.Contains(strings.Join(panel, "\n"), "s3") {
			t.Errorf("the 80 by %d screen has %d Detail rows for three slices, want %d, s3 gone first:\n%s", c.height, len(panel)-2, c.rows, v)
		}
		if !strings.Contains(v, "╭─ Activity") || len(strings.Split(v, "\n")) != c.height {
			t.Errorf("the 80 by %d screen lost its Activity panel or its height:\n%s", c.height, v)
		}
		run := strings.Join(panelLines(strings.Split(v, "\n"), "Run"), "\n")
		if strings.Contains(run, "wave") {
			t.Errorf("the Detail panel was cut before the Run panel:\n%s", v)
		}
	}
}

// TestChartDetailReadsTheAgentFromTheNearestClaimCall: the agent is the one of
// the claim call nearest to the claim line, within a minute before it, and a
// release is not a claim.
func TestChartDetailReadsTheAgentFromTheNearestClaimCall(t *testing.T) {
	t.Parallel()
	claim := jl(at(5, 0), "JIG-5", "s2", journal.EventClaim, with("s-builder", "", "", "", "", ""))
	tk := &ticket{id: "JIG-5", lines: []events.Envelope{claim}}
	if got := tk.claimAgent(claim); got != "" {
		t.Errorf("no tool line is kept and the agent is %q", got)
	}
	tk.ledger = []events.Envelope{
		claimLine(at(3, 0), "JIG-5", "s-builder", "agent-far", "jig claim JIG-5 --slice s2"),
		claimLine(at(4, 30), "JIG-5", "s-builder", "agent-release", "jig claim JIG-5 --slice s2 --release"),
		claimLine(at(4, 40), "JIG-5", "s-other", "agent-other-session", "jig claim JIG-5 --slice s2"),
		claimLine(at(4, 50), "JIG-5", "s-builder", "agent-early", "jig claim JIG-5 --slice s2"),
		claimLine(at(5, 1), "JIG-5", "s-builder", "agent-near", "jig claim JIG-5 --slice s2"),
		claimLine(at(5, 30), "JIG-5", "s-builder", "agent-late", "jig claim JIG-5 --slice s3"),
		claimLine(at(5, 0), "JIG-5", "s-builder", "agent-not-a-claim", "go test ./..."),
	}
	if got := tk.claimAgent(claim); got != "agent-near" {
		t.Errorf("the agent is %q, want the one whose call was nearest the claim", got)
	}
	if _, ok := tk.liveClaim("s2"); !ok {
		t.Error("a claimed slice has no live claim")
	}
	tk.lines = append(tk.lines, jl(at(6, 0), "JIG-5", "s2", journal.EventRelease, with("s-builder", "", "", "", "", "")))
	if _, ok := tk.liveClaim("s2"); ok {
		t.Error("a released slice has a live claim")
	}
	tk.lines = append(tk.lines, jl(at(7, 0), "JIG-5", "s2", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	if _, ok := tk.liveClaim("s2"); !ok {
		t.Error("a slice claimed again after its release has no live claim")
	}
}

// TestClaimCallAgreesWithTheLedger: the screens may not import the ledger, so
// claimCall is a copy of what it and internal/observe ask of a command; the
// two agree on every command here, and on what each is.
func TestClaimCallAgreesWithTheLedger(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		cmd  string
		want bool
	}{
		{"jig claim JIG-5 --slice s2", true},
		{"jig claim JIG-5 --slice s2 --unattended", true},
		{"jig claim", true},
		{"jig  claim JIG-5", true},
		{"JIG claim JIG-5", true},
		{"./jig claim JIG-5", true},
		{"jig.exe claim JIG-5", true},
		{"jig.EXE claim JIG-5", true},
		{`C:\tools\jig.exe claim JIG-5 --slice a`, true},
		{`"C:\Program Files\jig\jig.exe" claim JIG-5`, true}, // the space splits the path, the last piece still names jig
		{`'jig' claim JIG-5`, true},
		{"/usr/local/bin/jig claim JIG-5", true},
		{"cd /repo && jig claim JIG-5 --slice a", true},
		{"jig claim JIG-5 --slice a && jig context JIG-5 --slice a", true},
		{"jig claim JIG-5 --slice s2 --release", false},
		{"jig claim --release", false},
		{"jig claim JIG-5 --slice s2 --session x --release", false},
		{"jig", false},
		{"jig context JIG-5 --slice s2", false},
		{"jig note claim", false},
		{"jig-23f732c claim JIG-5", false},
		{"mjig claim JIG-5", false},
		{"claim jig", false},
		{"go test ./...", false},
		{"", false},
	} {
		if got := claimCall(c.cmd); got != c.want {
			t.Errorf("claimCall(%q) = %v, want %v", c.cmd, got, c.want)
		}
		if ledgerSays := ledger.RunsJig(c.cmd, "", "claim") && !strings.Contains(c.cmd, "--release"); claimCall(c.cmd) != ledgerSays {
			t.Errorf("claimCall(%q) = %v, but the ledger and the --release refusal say %v", c.cmd, claimCall(c.cmd), ledgerSays)
		}
	}
}

// TestChartScreenDetailsAHeldAgent: a ticket a subagent builds, on a screen of
// 100 by 36.
func TestChartScreenDetailsAHeldAgent(t *testing.T) {
	t.Parallel()
	g := heldChartRig(t)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 36})
	onChart(g)
	golden(t, "chart-detail", g.view())
}
