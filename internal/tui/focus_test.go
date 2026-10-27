package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// TestShownCommandDropsTheCdBeforeIt: a harness runs a command in a folder
// with `cd <dir> &&` before it; a move shows the command that ran.
func TestShownCommandDropsTheCdBeforeIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{`cd "D:\work\shop" && go test ./cart/...`, "go test ./cart/..."},
		{`cd /w && cd sub && git   commit -m x`, "git commit -m x"},
		{"go vet ./...", "go vet ./..."},
		{`cd /w && jig claim SHOP-1 --slice round 2>&1`, "jig claim SHOP-1 --slice round"},
		{"cd /w", "cd /w"},
	} {
		if got := shownCommand(tc.in); got != tc.want {
			t.Errorf("shownCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFocusIndexOpensAPersonsTurnThenTheWorkUnderWay: the card is for a
// sub-step that waits for a person or failed, else the one under way; a step
// with neither folds every sub-step.
func TestFocusIndexOpensAPersonsTurnThenTheWorkUnderWay(t *testing.T) {
	t.Parallel()
	sub := func(state string) phase.Sub { return phase.Sub{State: state} }
	for _, tc := range []struct {
		name string
		subs []phase.Sub
		want int
	}{
		{"under way", []phase.Sub{sub(phase.SubDone), sub(phase.SubActive), sub(phase.SubPending)}, 1},
		{"a person's turn first", []phase.Sub{sub(phase.SubActive), sub(phase.SubNeedsYou)}, 1},
		{"a failure first", []phase.Sub{sub(phase.SubActive), sub(phase.SubFailed)}, 1},
		{"all done", []phase.Sub{sub(phase.SubDone), sub(phase.SubDone)}, -1},
		{"nothing started", []phase.Sub{sub(phase.SubPending), sub(phase.SubWaiting)}, -1},
	} {
		if got := focusIndex(tc.subs); got != tc.want {
			t.Errorf("%s: focusIndex = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// exit sets a ledger tool line's exit status.
func exit(code int) func(*journal.Line) { return func(l *journal.Line) { l.Exit = &code } }

// tool is a ledger tool line of session running command.
func tool(ts, session, command string, mod ...func(*journal.Line)) events.Envelope {
	return ll(ts, "", journal.EventTool, append([]func(*journal.Line){func(l *journal.Line) {
		l.Session, l.Tool, l.Command = session, "Bash", command
	}}, mod...)...)
}

// said gives a tool line the words the session gave its call.
func said(description string) func(*journal.Line) {
	return func(l *journal.Line) { l.Description = description }
}

// cardRig is SHOP-1 at width by height: slice round green, slice clamp claimed
// by s-b (Claude Code, Sonnet 5) and being verified, slice cart waiting, with
// the commands s-b and another session ran around it.
func cardRig(t *testing.T, width, height int) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round to the cent"},
			{"clamp", "building", "1", "round", "-", "Clamp negative quantities to zero before the total is taken, so a cart that holds a returned item never charges less than nothing; a quantity above the stock is left to the checkout, which knows the stock and says so to the person at the till before they pay for the order"},
			{"cart", "queued", "0", "clamp", "-", "Show the cart"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: width, Height: height})
	g.feed(
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-b", "claude", "claude-sonnet-5" }),
		jl(at(1, 0), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(1, 30), "SHOP-1", "round", journal.EventVerifyPending, with("s-b", sha('a'), "", "Round to the cent", "", "")),
		jl(at(1, 37), "SHOP-1", "round", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		tool(at(1, 50), "s-b", "go test ./...", exit(0)),
		jl(at(2, 0), "SHOP-1", "clamp", journal.EventClaim, with("s-b", "", "", "", "", "")),
		tool(at(2, 5), "s-b", `cd "D:\work\shop" && go test ./cart/...`, exit(1), said("Run the cart tests")),
		tool(at(2, 8), "s-b", `cd "D:\work\shop" && go test ./cart/...`, exit(0)),
		tool(at(2, 9), "s-other", "ls"),
		jl(at(2, 10), "SHOP-1", "clamp", journal.EventVerifyPending, with("s-b", sha('b'), "", "Clamp negative quantities", "", "")),
	)
	return g
}

// TestTheSliceUnderWayOpensIntoACard: on a screen with room, the slice being
// built opens into a card headed by how long it has run, with its goal, its
// builder by role, harness and model, and its moves: its own lines and the
// commands its builder ran since the claim, a failing one with its exit
// status. A command before the claim or from another session is not its move.
// The slice that is done folds to a line with the time it took.
func TestTheSliceUnderWayOpensIntoACard(t *testing.T) {
	t.Parallel()
	g := cardRig(t, 100, 40)
	v := g.view()
	golden(t, "ticket-card", v)
	block := strings.Join(panelLines(strings.Split(v, "\n"), "Build ─"), "\n")
	for _, want := range []string{
		"✓ slice round · verified aaaaaaa · 37s",
		"╭ ◐ slice clamp · verifying bbbbbbb", "57m 30s ╮",
		"│ goal   Clamp negative quantities",
		"│ who    builder (Claude Code · Sonnet 5)",
		"│        a returned item never charges less than nothing; a quantity above the stock is     │",
		"│        left to the checkout, which knows the stock and says so to the person at the till… │",
		"│ moves  09:02:05 ✗ Run the cart tests · exit 1",
		"09:02:08 · $ `go test ./cart/...`",
		"09:02:10 ◑ verifying bbbbbbb",
		"○ slice cart · waits for clamp",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the block lacks %q:\n%s", want, block)
		}
	}
	for _, not := range []string{"`ls`", "09:01:50", "cd \"D:"} {
		if strings.Contains(block, not) {
			t.Errorf("the card shows %q, which is not a move of slice clamp:\n%s", not, block)
		}
	}
	if strings.Contains(block, "took it") {
		t.Errorf("the card's moves repeat the claim its head already tells:\n%s", block)
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "  │  │") && !strings.HasSuffix(strings.TrimRight(line, " "), "│ │") {
			t.Errorf("a row of the card is open on the right: %q", line)
		}
	}
}

// TestTheCardGivesWayOnASmallScreen: at 80 by 24 the card keeps its moves and
// its facts give way; a screen too short for even that lists the sub-steps one
// line each, as before.
func TestTheCardGivesWayOnASmallScreen(t *testing.T) {
	t.Parallel()
	v := cardRig(t, 80, 24).view()
	block := strings.Join(panelLines(strings.Split(v, "\n"), "Build ─"), "\n")
	if !strings.Contains(block, "│ moves") || strings.Contains(block, "│ goal") {
		t.Errorf("the 80x24 card does not keep its moves over its facts:\n%s", v)
	}
	v = cardRig(t, 80, 22).view()
	block = strings.Join(panelLines(strings.Split(v, "\n"), "Build ─"), "\n")
	if strings.Contains(block, "│ moves") || !strings.Contains(block, "◐ slice clamp · verifying bbbbbbb") {
		t.Errorf("the 80x22 block is not one line per sub-step:\n%s", v)
	}
}

// TestAReviewRoundOpensWithItsReviewersCommands: an open round taken by a
// subagent of the session that built the ticket opens into a card that names
// it reviewer subagent, with the model the round's lines name, and lists the
// commands it ran in the round, not the build's.
func TestAReviewRoundOpensWithItsReviewersCommands(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round to the cent"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.feed(
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-b", "claude", "claude-sonnet-5" }),
		jl(at(1, 0), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
		tool(at(1, 20), "s-b", "git commit -m round"),
		jl(at(1, 30), "SHOP-1", "round", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(3, 0), "SHOP-1", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 1 }),
		jl(at(3, 10), "SHOP-1", "", journal.EventGateReviewer, func(l *journal.Line) { l.Attempt, l.Session, l.Model = 1, "s-b", "claude-opus-5" }),
		tool(at(3, 20), "s-b", "jig review context SHOP-1 --model claude-opus-5"),
		tool(at(4, 0), "s-b", `cd /w/pool/SHOP-1-gate && go test ./cart/...`, exit(1)),
	)
	v := g.view()
	block := strings.Join(panelLines(strings.Split(v, "\n"), "Review ─"), "\n")
	for _, want := range []string{
		"╭ ◎ round 1 · taken by reviewer subagent (Claude Code · Opus 5)",
		"09:03:20 · $ `jig review context SHOP-1 --model claude-opus-5`",
		"$ `go test ./cart/...` · exit 1",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the review block lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(block, "│ who") || strings.Contains(block, "took it") {
		t.Errorf("the card names its reviewer twice, in its head and in a who row or a move:\n%s", block)
	}
	if strings.Contains(block, "git commit -m round") {
		t.Errorf("the round's card shows a command of the build:\n%s", block)
	}
}

// TestADoneStepFoldsEverySubstep: the sub-steps of a step that is done all fold
// to a line, each with the time it took; no card opens.
func TestADoneStepFoldsEverySubstep(t *testing.T) {
	t.Parallel()
	g := cardRig(t, 100, 40)
	g.r.answer("status SHOP-1 --store /s", strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round to the cent"},
			{"clamp", "green", "1", "round", "-", "Clamp negative quantities"},
			{"cart", "queued", "0", "clamp", "-", "Show the cart"},
		}), "questions: none"}, "\n")+"\n", 0)
	g.feed(
		jl(at(2, 30), "SHOP-1", "clamp", journal.EventVerified, with("", sha('b'), "", "", "", "")),
	)
	tk := g.m.w.tickets["SHOP-1"]
	subs := g.m.phaseOf(tk).Of(phase.Build).Subs
	p := g.m.substepsPanel(tk, stepBuild, subs[:2], 10)
	var lines []string
	for _, l := range p.lines {
		lines = append(lines, l.text)
	}
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "│ moves") {
		t.Errorf("a done step opened a card:\n%s", text)
	}
	for _, want := range []string{"slice round · verified aaaaaaa · 37s", "slice clamp · verified bbbbbbb · 30s"} {
		if !strings.Contains(text, want) {
			t.Errorf("the folded block lacks %q:\n%s", want, text)
		}
	}
}

// prOpeningRig is SHOP-1 built and reviewed clean, its body written by session
// s-b, which then pushes and opens the pull request: commands that come before
// any line of the pull request sub-step.
func prOpeningRig(t *testing.T, extra ...events.Envelope) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round to the cent"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.feed(append([]events.Envelope{
		jl(at(1, 0), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(1, 30), "SHOP-1", "round", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(3, 0), "SHOP-1", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 1 }),
		jl(at(4, 0), "SHOP-1", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-b" }),
		jl(at(5, 0), "SHOP-1", "", journal.EventPrBody, func(l *journal.Line) { l.Commit = sha('a') }),
	}, extra...)...)
	return g
}

// TestAPullRequestBeingOpenedShowsTheSessionsCommands: the pull request
// sub-step writes no line until the pull request is recorded, so its card holds
// the commands the session that wrote the ticket's latest line ran since that
// line: the push and the `gh pr create` that open it.
func TestAPullRequestBeingOpenedShowsTheSessionsCommands(t *testing.T) {
	t.Parallel()
	g := prOpeningRig(t,
		tool(at(4, 30), "s-b", "jig publish SHOP-1 --body"),
		tool(at(5, 10), "s-b", "git push origin SHOP-1-round"),
		tool(at(5, 20), "s-b", `cd /w && gh pr create --head SHOP-1-round 2>&1`),
	)
	v := g.view()
	block := strings.Join(panelLines(strings.Split(v, "\n"), "Publish ─"), "\n")
	for _, want := range []string{"pull request", "$ `git push origin SHOP-1-round`", "$ `gh pr create --head SHOP-1-round`"} {
		if !strings.Contains(block, want) {
			t.Errorf("the publish block lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(block, "jig publish SHOP-1 --body") {
		t.Errorf("the card shows a command from before the body was written:\n%s", block)
	}
}

// TestACardWithNothingToShowDoesNotOpen: the pull request sub-step under way
// with no command run since the body has no moves and no facts, so the block
// lists it as a line.
func TestACardWithNothingToShowDoesNotOpen(t *testing.T) {
	t.Parallel()
	v := prOpeningRig(t).view()
	block := strings.Join(panelLines(strings.Split(v, "\n"), "Publish ─"), "\n")
	if strings.Contains(block, "│ moves") || strings.Contains(block, "nothing yet") {
		t.Errorf("an empty card opened:\n%s", block)
	}
}

// TestAHomeFolderIsShownAsATilde: a command that names the home folder, in
// any spelling a harness gives it (as Windows writes it, with forward slashes,
// as Git Bash names the drive, in any case), shows it as ~ in a card; a folder
// that only starts like it keeps its name.
func TestAHomeFolderIsShownAsATilde(t *testing.T) {
	t.Parallel()
	g := newRig(t, "SHOP-1")
	for _, tc := range []struct{ in, want string }{
		{`cp a "C:/Users/Ada/AppData/x"`, `cp a "~/AppData/x"`},
		{`cp a /c/Users/Ada/AppData/y`, `cp a ~/AppData/y`},
		{`type C:\Users\Ada\z.txt`, `type ~\z.txt`},
		{`ls c:/users/ada`, `ls ~`},
		{`ls C:/Users/Adam/x`, `ls C:/Users/Adam/x`},
	} {
		if got := g.m.tildeHome(tc.in); got != tc.want {
			t.Errorf("tildeHome(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if homePattern("") != nil {
		t.Error("no home has a pattern")
	}
}
