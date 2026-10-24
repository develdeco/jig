package tui

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// update rewrites the golden files: go test ./internal/tui -update.
var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares got with testdata/<name>.golden.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -update to write it)", err)
	}
	if got+"\n" != string(want) {
		t.Errorf("%s differs from the screen drawn\n--- want\n%s--- got\n%s\n", path, want, got)
	}
}

// at is the n-th second after 09:00 on a morning far from any real clock.
func at(min, sec int) string {
	return fmt.Sprintf("2026-10-23T09:%02d:%02dZ", min, sec)
}

// jl is a journal envelope of project demo.
func jl(ts, ticket, slice, event string, mod ...func(*journal.Line)) events.Envelope {
	l := journal.Line{TS: ts, Ticket: ticket, Slice: slice, Event: event}
	for _, f := range mod {
		f(&l)
	}
	return events.Envelope{Src: events.SrcJournal, Store: "demo", Line: l}
}

// ll is a ledger envelope.
func ll(ts, ticket, event string, mod ...func(*journal.Line)) events.Envelope {
	l := journal.Line{TS: ts, Ticket: ticket, Event: event}
	for _, f := range mod {
		f(&l)
	}
	return events.Envelope{Src: events.SrcLedger, Line: l}
}

func with(session, commit, outcome, summary, ref, prov string) func(*journal.Line) {
	return func(l *journal.Line) {
		l.Session, l.Commit, l.Outcome, l.Summary, l.Ref, l.Provenance = session, commit, outcome, summary, ref, prov
	}
}

// sha is a commit id of 40 hex digits starting with the given digit.
func sha(d byte) string { return strings.Repeat(string(d), 40) }

// jig1 is a ticket in the middle of its build: slice a green, b building after
// a failed oracle run, c parked on a question, d waiting for them.
func jig1() []events.Envelope {
	return []events.Envelope{
		jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(0, 5), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "", "", "")),
		jl(at(0, 20), "JIG-1", "a", journal.EventOracle, with("", sha('a'), "pass", "", "", "")),
		jl(at(0, 21), "JIG-1", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(1, 0), "JIG-1", "b", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(1, 10), "JIG-1", "b", journal.EventVerifyPending, with("s-builder", sha('b'), "", "", "", "")),
		jl(at(1, 40), "JIG-1", "b", journal.EventOracle, with("", sha('b'), "fail", "TestB: want 2, got 3", "", "")),
		jl(at(2, 0), "JIG-1", "c", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-001", "agent")),
		ll(at(2, 1), "JIG-1", journal.EventTool, func(l *journal.Line) { l.Session, l.Tool = "s-builder", "Bash" }),
	}
}

// statusJIG1 is `jig status JIG-1` for jig1, rendered by the same code the verb uses.
func statusJIG1() string {
	return strings.Join([]string{
		"ticket: JIG-1",
		"state: parked",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question"}, [][]string{
			{"a", "green", "1", "-", "-"},
			{"b", "building", "2", "-", "-"},
			{"c", "needs-input", "1", "-", "q-001"},
			{"d", "queued", "0", "b+c", "-"},
		}),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}}),
		axi.Table("parked", []string{"slice", "question", "resume"}, [][]string{{"c", "q-001", "jig answer JIG-1 q-001 '<text>'"}}),
		axi.Help("Answer q-001: `jig answer JIG-1 q-001 '<text>'`"),
	}, "\n") + "\n"
}

// statusJIG1Brief is statusJIG1 with the slices' goals and the brief block a
// status of this version prints.
func statusJIG1Brief() string {
	return strings.Join([]string{
		"ticket: JIG-1",
		"state: parked",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Greet by name"},
			{"b", "building", "2", "-", "-", "Say goodbye, politely"},
			{"c", "needs-input", "1", "-", "q-001", "Pick the tone"},
			{"d", "queued", "0", "b+c", "-", "Ship the copy"},
		}),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}}),
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Ada Lovelace"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "agent"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{
			{"Goal", "-"}, {"Greeting", "a"}, {"Farewell", "b"}, {"Tone", "c+d"},
		}),
		axi.Help("Answer q-001: `jig answer JIG-1 q-001 '<text>'`"),
	}, "\n") + "\n"
}

// jig2 is a ticket reviewed and published: a first round that found two asks,
// both decided by a person, a clean second round, and the publish.
func jig2() []events.Envelope {
	return []events.Envelope{
		jl(at(10, 0), "JIG-2", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(10, 30), "JIG-2", "a", journal.EventVerified, with("", sha('c'), "", "", "", "")),
		jl(at(11, 0), "JIG-2", "", journal.EventGateOpen),
		jl(at(11, 30), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
		jl(at(12, 0), "JIG-2", "", journal.EventTriage, with("s-person", "", "dismiss", "not a bug", "r1-f1", "person")),
		jl(at(12, 5), "JIG-2", "", journal.EventTriage, with("s-person", "", "keep", "", "r1-f2", "person")),
		jl(at(13, 0), "JIG-2", "", journal.EventGateOpen),
		jl(at(13, 30), "JIG-2", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
		jl(at(14, 0), "JIG-2", "", journal.EventPrBody, func(l *journal.Line) { l.Commit, l.Branch = sha('c'), "jig/JIG-2" }),
		jl(at(14, 10), "JIG-2", "", journal.EventPublishApproved, func(l *journal.Line) {
			l.Commit, l.Branch, l.Provenance = sha('c'), "jig/JIG-2", "person"
		}),
		jl(at(14, 20), "JIG-2", "", journal.EventPr, func(l *journal.Line) {
			l.Commit, l.Branch, l.Outcome, l.URL = sha('c'), "jig/JIG-2", "opened", "https://example.test/acme/demo/pull/7"
		}),
		jl(at(14, 21), "JIG-2", "", journal.EventPublishDone),
	}
}

// jig2Mid is JIG-2 as it stands after its first round: two asks waiting.
func jig2Mid() []events.Envelope { return jig2()[:4] }

func statusJIG2() string {
	return strings.Join([]string{
		"ticket: JIG-2",
		"state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question"}, [][]string{{"a", "green", "1", "-", "-"}}),
		"questions: none",
		axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, [][]string{
			{"r1-f1", "high", "alpha/a.go:3", "Unchecked error", "needs a human"},
			{"r1-f2", "low", "beta/b.go:9", "Name shadows import", "-"},
		}),
		axi.Help("Run `jig triage JIG-2 <id> keep|dismiss` to decide the outstanding asks, then `jig gate JIG-2` to apply the decisions"),
	}, "\n") + "\n"
}

// statusJIG2Done is `jig status JIG-2` once the clean round has folded the
// asks away.
func statusJIG2Done() string {
	return strings.Join([]string{
		"ticket: JIG-2",
		"state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question"}, [][]string{{"a", "green", "1", "-", "-"}}),
		"questions: none",
		axi.Help("Run `jig status JIG-2` to confirm the ticket is fully green"),
	}, "\n") + "\n"
}

// rosterJSON is `jig next --json` for the store: JIG-3 is not started.
func rosterJSON() string {
	data, _ := json.Marshal(map[string]any{"tickets": []map[string]any{
		{"id": "JIG-1", "title": "Greeting copy", "state": "ready"},
		{"id": "JIG-2", "title": "Config loader", "state": "ready"},
		{"id": "JIG-3", "title": "Release notes", "state": "blocked"},
	}})
	return string(data) + "\n"
}

// call is one command the model ran.
type call struct {
	Args []string
	Line string
}

// fakeRunner answers commands from a table keyed by the first word (or by the
// whole line, which wins) and records them.
type fakeRunner struct {
	mu    sync.Mutex
	calls []call
	by    map[string]ranStep
}

func newRunner() *fakeRunner {
	return &fakeRunner{by: map[string]ranStep{
		"status JIG-1 --store /s": {Out: statusJIG1()},
		"status JIG-2 --store /s": {Out: statusJIG2()},
		"next --json --store /s":  {Out: rosterJSON()},
	}}
}

// answer sets what the command line prints and exits with.
func (f *fakeRunner) answer(line, out string, code int) *fakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.by[line] = ranStep{Out: out, Code: code}
	return f
}

func (f *fakeRunner) run(args []string) (string, int) {
	line := strings.Join(args, " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{Args: args, Line: line})
	if r, ok := f.by[line]; ok {
		return r.Out, r.Code
	}
	return "", 0
}

// lines are the command lines run so far, except the status and next reads.
func (f *fakeRunner) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.Args[0] == "status" || c.Args[0] == "next" {
			continue
		}
		out = append(out, c.Line)
	}
	return out
}

// rig is a model over a fake runner, driven synchronously.
type rig struct {
	t       *testing.T
	m       *Model
	r       *fakeRunner
	started bool
}

// newRig builds an 80 by 24 model on UTC, without color, in project demo.
func newRig(t *testing.T, ticket string) *rig {
	t.Helper()
	r := newRunner()
	m := New(Config{RetryDelay: time.Millisecond, Run: r.run, Ticket: ticket, Scope: []string{"--store", "/s"}, Project: "demo", Zone: time.UTC, Width: 80, Height: 24})
	return &rig{t: t, m: m, r: r}
}

// feed delivers a batch of the stream and settles what it starts.
func (g *rig) feed(envs ...events.Envelope) *rig {
	g.t.Helper()
	if !g.started {
		g.started = true
		g.settle(g.m.Init())
	}
	g.send(eventsMsg(envs))
	return g
}

// send delivers one message and settles the commands it starts.
func (g *rig) send(msg tea.Msg) {
	g.t.Helper()
	_, cmd := g.m.Update(msg)
	g.settle(cmd)
}

// settle runs cmd and every command its messages start, to the end.
func (g *rig) settle(cmd tea.Cmd) {
	g.t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			g.settle(c)
		}
	default:
		if _, ok := msg.(tea.QuitMsg); ok {
			return
		}
		g.send(msg)
	}
}

// press sends keys: a name like "enter" or "esc" is that key, anything else
// is typed a rune at a time.
func (g *rig) press(keys ...string) *rig {
	g.t.Helper()
	for _, k := range keys {
		switch k {
		case "enter":
			g.send(tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			g.send(tea.KeyMsg{Type: tea.KeyEsc})
		case "up":
			g.send(tea.KeyMsg{Type: tea.KeyUp})
		case "down":
			g.send(tea.KeyMsg{Type: tea.KeyDown})
		case "backspace":
			g.send(tea.KeyMsg{Type: tea.KeyBackspace})
		case "home":
			g.send(tea.KeyMsg{Type: tea.KeyHome})
		case "end":
			g.send(tea.KeyMsg{Type: tea.KeyEnd})
		case "space":
			g.send(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		case "ctrl+c":
			g.send(tea.KeyMsg{Type: tea.KeyCtrlC})
		default:
			for _, r := range k {
				g.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
		}
	}
	return g
}

func (g *rig) view() string { return g.m.View() }

// colored is a renderer that draws with the colors of a terminal, whatever the
// output is.
func colored() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(&strings.Builder{})
	r.SetColorProfile(termenv.ANSI256)
	return r
}

// contextWithTimeout is a context a test cannot outlive.
func contextWithTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
