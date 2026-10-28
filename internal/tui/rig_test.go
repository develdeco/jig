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

// statusJIG1Brief is statusJIG1 with the slices' goals, workspaces and oracles
// and the brief block a status of this version prints.
func statusJIG1Brief() string {
	return strings.Join([]string{
		"ticket: JIG-1",
		"state: parked",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Greet by name", "app", "unit", "-"},
			{"b", "building", "2", "-", "-", "Say goodbye, politely", "app", "unit", "-"},
			{"c", "needs-input", "1", "-", "q-001", "Pick the tone", "copy", "go test ./...", "-"},
			{"d", "queued", "0", "b+c", "-", "Ship the copy", "copy", "go test ./...", "-"},
		}),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}}),
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Ada Lovelace"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "agent"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{
			{"Goal", "-"}, {"Greeting", "a"}, {"Farewell", "b"}, {"Tone", "c+d"},
		}),
		axi.Help("Answer q-001: `jig answer JIG-1 q-001 '<text>'`"),
	}, "\n") + "\n"
}

// briefTextJIG1 is `jig brief show JIG-1 --text`: the brief's title and its four
// sections, each with the first lines of its body, then the bodies the screens
// do not read.
func briefTextJIG1() string {
	return strings.Join([]string{
		axi.KV("brief", [][2]string{{"ticket", "JIG-1"}, {"title", "Greeting copy"}, {"illustration", "no"}}),
		axi.Table("sections", []string{"heading", "summary"}, [][]string{
			{"Goal", "Say hello and goodbye to a person by name, in the tone the product has."},
			{"Greeting", "Use the first name. Fall back to \"friend\" when there is none."},
			{"Farewell", "Say goodbye politely, and never twice."},
			{"Tone", "Casual or formal: the owner decides. Until then neither is shipped, so the copy waits for the answer."},
		}),
		"section: Goal\n  Say hello and goodbye to a person by name, in the tone the product has.",
		"section: Greeting\n  Use the first name. Fall back to \"friend\" when there is none.\n\n  A name is trimmed.",
		"section: Farewell\n  Say goodbye politely, and never twice.",
		"section: Tone\n  Casual or formal: the owner decides.",
		axi.Help("`jig brief show JIG-1` serves the brief and its illustration on 127.0.0.1 for a browser"),
	}, "\n") + "\n"
}

// jig1Records is jig1 as the build screen shows its records: the commits carry
// their subjects, the oracle lines their command, time and who ran them, slice
// b is checked twice and fails both times, and slice a once had a question that
// was answered.
func jig1Records() []events.Envelope {
	oracle := func(command string, seconds int, source string) func(*journal.Line) {
		return func(l *journal.Line) { l.Command, l.Seconds, l.Source = command, seconds, source }
	}
	return []events.Envelope{
		jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(0, 2), "JIG-1", "a", journal.EventQuestion, with("s-builder", "", "", "Trim the name?", "q-000", "agent")),
		jl(at(0, 3), "JIG-1", "a", journal.EventAnswer, with("s-person", "", "", "Yes, trim it.", "q-000", "person")),
		jl(at(0, 5), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "Greet the person by name", "", "")),
		jl(at(0, 20), "JIG-1", "a", journal.EventOracle, with("", sha('a'), "pass", "", "", ""), oracle("go test ./...", 15, "jig")),
		jl(at(0, 21), "JIG-1", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(1, 0), "JIG-1", "b", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(1, 10), "JIG-1", "b", journal.EventVerifyPending, with("s-builder", sha('b'), "", "Say goodbye", "", "")),
		jl(at(1, 40), "JIG-1", "b", journal.EventOracle, with("", sha('b'), "fail", "TestB: want 2, got 3", "", ""), oracle("go test ./...", 30, "jig")),
		jl(at(1, 50), "JIG-1", "b", journal.EventVerifyPending, with("s-builder", sha('e'), "", "Say goodbye, politely", "", "")),
		jl(at(2, 15), "JIG-1", "b", journal.EventOracle, with("", sha('e'), "fail", "--- FAIL: TestB (0.00s) b_test.go:12: want 2, got 3 FAIL exit status 1", "", ""), oracle("go test ./...", 25, "jig")),
		jl(at(2, 30), "JIG-1", "c", journal.EventQuestion, with("s-builder", "", "", "Formal or casual?", "q-001", "agent")),
		ll(at(2, 31), "JIG-1", journal.EventTool, func(l *journal.Line) { l.Session, l.Tool = "s-builder", "Bash" }),
	}
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
		"fix_budget: 1 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "alpha", "unit", "-"},
		}),
		"questions: none",
		axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, [][]string{
			{"r1-f1", "high", "alpha/a.go:3", "Unchecked error", "needs a human"},
			{"r1-f2", "low", "beta/b.go:9", "Name shadows import", "-"},
		}),
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "2", "1", "full", sha('0'), sha('c'), "4"},
		}),
		axi.Table("findings", findingColumns, [][]string{
			{"r1-f1", "1", "ask", "high", "alpha/a.go:3", "Unchecked error", "asked", "a failed write would pass silently", "", ""},
			{"r1-f2", "1", "ask", "low", "beta/b.go:9", "Name shadows import", "asked", "the import is hidden in this scope", "", ""},
			{"r1-f3", "1", "fix", "medium", "alpha/a.go:12", "Close the file", "open", "a leaked handle fails the next open", "", ""},
			{"r1-f4", "1", "note", "low", "beta/b.go:2", "Rename the helper", "noted", "a clearer name, whenever the file is next touched", "", ""},
		}),
		axi.Help("Run `jig triage JIG-2 <id> keep|dismiss` to decide the outstanding asks, then `jig gate JIG-2` to apply the decisions"),
	}, "\n") + "\n"
}

// roundColumns and findingColumns are the columns of `jig status`'s rounds and
// findings tables.
var (
	roundColumns   = []string{"round", "verdict", "reviewer", "harness", "model", "independent", "fix", "ask", "note", "scope", "base", "head", "files"}
	findingColumns = []string{"id", "round", "action", "risk", "file:line", "title", "status", "rationale", "decision", "decided_by"}
)

// statusJIG2Done is `jig status JIG-2` once the clean round has folded the
// asks away and the ticket is published.
func statusJIG2Done() string {
	return statusJIG2DoneWith(
		"## Intent\nLoad the config from the file the person names.\n## What changed\n- a: load the config (ccccccc)\n- fix-1: check the error\n## Verification\n- unit passed",
		"## Slices\n- a: ccccccc\n- fix-1: ddddddd\n## Fix rounds\n- round 1: fix-slice fix-1",
	)
}

// statusJIG2DoneWith is statusJIG2Done with the first lines of the pull request
// body and of the changelog the status carries.
func statusJIG2DoneWith(body, changelog string) string {
	return strings.Join([]string{
		"ticket: JIG-2",
		"state: green",
		"fix_budget: 1 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "alpha", "unit", "-"},
		}),
		"questions: none",
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "2", "1", "full", sha('0'), sha('c'), "4"},
			{"2", "clean", "s-reviewer", "claude", "claude-opus-5", "true", "0", "0", "0", "delta", sha('c'), sha('d'), "2"},
		}),
		axi.KV("publish", [][2]string{
			{"ticket", "JIG-2"}, {"pr", "https://example.test/acme/demo/pull/7"}, {"pr_state", "opened"}, {"head", sha('d')}, {"branch", "jig/JIG-2"},
			{"body_file", "tickets/JIG-2/pr/demo.md"}, {"body", body},
			{"changelog_file", "tickets/JIG-2/changelog/consolidated.md"}, {"changelog", changelog},
		}),
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

// rigNow is the rigs' clock: after every line the fixtures write (at), so a
// sub-step under way has run a known time on a golden.
var rigNow, _ = time.Parse(time.RFC3339, at(59, 30))

// rigHome is the rigs' home folder, so what a screen names as ~ does not
// depend on the machine the tests run on.
const rigHome = `C:\Users\Ada`

// newRig builds an 80 by 24 model on UTC, without color, in project demo, on a
// clock that stands at rigNow.
func newRig(t *testing.T, ticket string) *rig {
	t.Helper()
	r := newRunner()
	m := New(Config{RetryDelay: time.Millisecond, IdleTick: -1, Run: r.run, Ticket: ticket, Scope: []string{"--store", "/s"}, Project: "demo", Zone: time.UTC, Width: 80, Height: 24,
		Now: func() time.Time { return rigNow }, Home: rigHome})
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

// jig2Repos is JIG-2 published in two repos: its lines to the clean round, then
// a body and an approval for each repo, a pull request for each, and the end.
func jig2Repos() []events.Envelope {
	in := func(repo string, mod func(*journal.Line)) func(*journal.Line) {
		return func(l *journal.Line) {
			l.Repo, l.Branch = repo, "jig/JIG-2"
			mod(l)
		}
	}
	commit := func(sha string) func(*journal.Line) { return func(l *journal.Line) { l.Commit = sha } }
	approved := func(sha string) func(*journal.Line) {
		return func(l *journal.Line) { l.Commit, l.Provenance = sha, "person" }
	}
	opened := func(sha, url string) func(*journal.Line) {
		return func(l *journal.Line) { l.Commit, l.Outcome, l.URL = sha, "opened", url }
	}
	return append(jig2()[:8],
		jl(at(14, 0), "JIG-2", "", journal.EventPrBody, in("api", commit(sha('c')))),
		jl(at(14, 1), "JIG-2", "", journal.EventPrBody, in("web", commit(sha('d')))),
		jl(at(14, 10), "JIG-2", "", journal.EventPublishApproved, in("api", approved(sha('c')))),
		jl(at(14, 11), "JIG-2", "", journal.EventPublishApproved, in("web", approved(sha('d')))),
		jl(at(14, 20), "JIG-2", "", journal.EventPr, in("api", opened(sha('c'), "https://example.test/acme/api/pull/7"))),
		jl(at(14, 21), "JIG-2", "", journal.EventPr, in("web", opened(sha('d'), "https://example.test/acme/web/pull/3"))),
		jl(at(14, 22), "JIG-2", "", journal.EventPublishDone),
	)
}

// statusJIG2Repos is `jig status JIG-2` once it is published in two repos: the
// publish block has the branch and the changelog, and a table the pull request
// of each repo.
func statusJIG2Repos() string {
	return strings.Join([]string{
		"ticket: JIG-2",
		"state: green",
		"fix_budget: 1 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "api/alpha", "unit", "-"},
			{"b", "green", "1", "-", "-", "Show the config", "web/beta", "unit", "-"},
		}),
		"questions: none",
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "clean", "s-reviewer", "claude", "claude-opus-5", "true", "0", "0", "0", "full", "api:" + sha('0') + "+web:" + sha('0'), "api:" + sha('c') + "+web:" + sha('d'), "4"},
		}),
		axi.KV("publish", [][2]string{
			{"ticket", "JIG-2"}, {"branch", "jig/JIG-2"},
			{"changelog_file", "tickets/JIG-2/changelog/consolidated.md"}, {"changelog", "## Slices\n- a: ccccccc\n- b: ddddddd"},
		}),
		axi.Table("publish_prs", []string{"repo", "pr", "state", "head", "body_file"}, [][]string{
			{"api", "https://example.test/acme/api/pull/7", "opened", sha('c'), "tickets/JIG-2/pr/api.md"},
			{"web", "https://example.test/acme/web/pull/3", "opened", sha('d'), "tickets/JIG-2/pr/web.md"},
		}),
		axi.Help("JIG-2 is published: run `jig next` to see the tickets that are left"),
	}, "\n") + "\n"
}
