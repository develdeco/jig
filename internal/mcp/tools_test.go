package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// runTool runs a tool of Tools(o) with a client that cannot ask the person.
func runTool(t *testing.T, o Options, name string, args map[string]any) (Result, error) {
	t.Helper()
	tools := Tools(o)
	tool := toolNamed(t, tools, name)
	raw, err := jsonRaw(args)
	if err != nil {
		t.Fatal(err)
	}
	a, err := tool.decode(raw)
	if err != nil {
		return Result{}, err
	}
	return tool.Handle(context.Background(), noClient{}, a)
}

type noClient struct{}

func (noClient) CanElicit() bool { return false }
func (noClient) Elicit(context.Context, string, map[string]any) (ElicitResult, error) {
	return ElicitResult{}, errors.New("no client")
}

func TestToolsTurnArgumentsIntoTheCommandLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"here", map[string]any{}, "here"},
		{"here", map[string]any{"ticket": "JIG-1", "slice": "b", "session": "s-1", "explain": true, "json": true}, "here JIG-1 --slice b --session s-1 --explain --json"},
		{"here", map[string]any{"explain": false}, "here"},
		{"next", map[string]any{"ticket": "JIG-1", "topology": true}, "next JIG-1 --topology"},
		{"context", map[string]any{"slice": "b", "json": true}, "context --slice b --json"},
		{"claim", map[string]any{"ticket": "JIG-2", "slice": "x", "session": "s-1", "unattended": true, "take": true, "release": true}, "claim JIG-2 --slice x --session s-1 --unattended --take --release"},
		{"ask", map[string]any{"ticket": "JIG-1", "slice": "c", "session": "s-1", "question": "Which tone?"}, "ask JIG-1 --slice c --session s-1 -- Which tone?"},
		{"ask", map[string]any{"question": "-x or -y?"}, "ask -- -x or -y?"},
		{"answer", map[string]any{"ticket": "JIG-1", "question_id": "q-002", "text": "Casual."}, "answer JIG-1 q-002 -- Casual."},
		{"answer", map[string]any{"question_id": "q-002", "text": "- Casual."}, "answer q-002 -- - Casual."},
		{"note", map[string]any{"slice": "b", "text": "Mind the second table."}, "note --slice b -- Mind the second table."},
		{"observe", map[string]any{"event": "tool", "session": "s-1", "agent": "a-1", "cwd": "/w", "tool": "Bash", "command": "go test", "exit": 1, "files": []string{"a.go", "b.go"}, "model": "m", "transcript": "/t.jsonl", "stop_active": true},
			"observe --event tool --session s-1 --agent a-1 --cwd /w --tool Bash --command go test --exit 1 --files a.go,b.go --model m --transcript /t.jsonl --stop-active"},
		{"observe", map[string]any{"event": "stop"}, "observe --event stop"},
		{"status", map[string]any{"ticket": "JIG-1"}, "status JIG-1"},
	} {
		r := &fakeRunner{}
		res, err := runTool(t, Options{Run: r.run}, tc.tool, tc.args)
		if err != nil || res.IsError {
			t.Errorf("%s %v: %+v, %v", tc.tool, tc.args, res, err)
			continue
		}
		if got := r.lines(); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s %v ran %q, want %q", tc.tool, tc.args, got, tc.want)
		}
		if r.stdins[0] != "" {
			t.Errorf("%s gave the command stdin %q, want none", tc.tool, r.stdins[0])
		}
	}
}

func TestTextAfterTheSeparatorIsOneArgumentHoweverItReads(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{}
	if _, err := runTool(t, Options{Run: r.run}, "note", map[string]any{"text": "two words\nand a line --slice x"}); err != nil {
		t.Fatal(err)
	}
	if got := r.calls[0]; len(got) != 3 || got[1] != "--" || got[2] != "two words\nand a line --slice x" {
		t.Errorf("argv = %q, want the text as the one argument after --", got)
	}
}

func TestPinsGoAmongTheFlagsBeforeTheText(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{}
	var asked []string
	o := Options{Run: r.run, Pin: func(verb string) ([]string, error) {
		asked = append(asked, verb)
		return []string{"--store", "/s"}, nil
	}}
	if _, err := runTool(t, o, "ask", map[string]any{"ticket": "JIG-1", "slice": "c", "question": "Which?"}); err != nil {
		t.Fatal(err)
	}
	if got := r.lines(); len(got) != 1 || got[0] != "ask JIG-1 --slice c --store /s -- Which?" {
		t.Errorf("ran %q, want the pins after the flags and before --", got)
	}
	if _, err := runTool(t, o, "status", map[string]any{"ticket": "JIG-1"}); err != nil {
		t.Fatal(err)
	}
	if got := r.lines()[1]; got != "status JIG-1 --store /s" {
		t.Errorf("status ran %q, want the pin after the ticket", got)
	}
	if strings.Join(asked, " ") != "ask status" {
		t.Errorf("pins were asked for %v, want the verb of each call", asked)
	}
}

func TestAPinThatFailsStopsTheCall(t *testing.T) {
	t.Parallel()
	r := &fakeRunner{}
	o := Options{Run: r.run, Pin: func(string) ([]string, error) { return nil, errors.New("no project") }}
	if _, err := runTool(t, o, "next", map[string]any{}); err == nil || err.Error() != "no project" {
		t.Errorf("error = %v, want the pin error", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("the command ran: %v", r.lines())
	}
}

func TestAPositionalThatIsAFlagIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tool string
		args map[string]any
		name string
	}{
		{"status", map[string]any{"ticket": "--store=/elsewhere"}, "ticket"},
		{"answer", map[string]any{"ticket": "JIG-1", "question_id": "-h", "text": "x"}, "question_id"},
	} {
		r := &fakeRunner{}
		_, err := runTool(t, Options{Run: r.run}, tc.tool, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.name) || !strings.Contains(err.Error(), "dash") {
			t.Errorf("%s %v: error = %v, want a refusal naming %s", tc.tool, tc.args, err, tc.name)
		}
		if len(r.calls) != 0 {
			t.Errorf("%s %v: the command ran: %v", tc.tool, tc.args, r.lines())
		}
	}
}

func TestWhatTheCommandPrintsIsTheResult(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		out    string
		code   int
		want   string
		failed bool
	}{
		{"output", "here:\n  project: p\n", 0, "here:\n  project: p\n", false},
		{"nothing", "", 0, "(no output)", false},
		{"blank", "\n  \n", 0, "(no output)", false},
		{"a refusal", "error: no\ncode: CLAIM_NOT_APPROVED\n", 1, "error: no\ncode: CLAIM_NOT_APPROVED\n", true},
		{"a usage error", "error: bad\ncode: VALIDATION_ERROR\n", 2, "error: bad\ncode: VALIDATION_ERROR\n", true},
	} {
		r := &fakeRunner{answer: func([]string) (string, int) { return tc.out, tc.code }}
		res, err := runTool(t, Options{Run: r.run}, "claim", map[string]any{})
		if err != nil || res.Text != tc.want || res.IsError != tc.failed {
			t.Errorf("%s: result %+v, %v, want %q (failed %v)", tc.name, res, err, tc.want, tc.failed)
		}
	}
}

func TestCommandsRunOneAtATime(t *testing.T) {
	t.Parallel()
	var running, most atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	r := &fakeRunner{answer: func([]string) (string, int) {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		running.Add(-1)
		return "ok\n", 0
	}}
	tools := Tools(Options{Run: r.run})
	tool := toolNamed(t, tools, "status")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tool.Handle(context.Background(), noClient{}, Args{"ticket": "JIG-1"}); err != nil {
				t.Error(err)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-entered
		release <- struct{}{}
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("%d commands ran at once, want 1", most.Load())
	}
}

func TestACallCancelledWhileItWaitsItsTurnDoesNotRun(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	r := &fakeRunner{answer: func(argv []string) (string, int) {
		if argv[0] == "next" {
			close(entered)
			<-release
		}
		return "ok\n", 0
	}}
	tools := Tools(Options{Run: r.run})
	done := make(chan struct{})
	go func() {
		defer close(done)
		toolNamed(t, tools, "next").Handle(context.Background(), noClient{}, Args{})
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() {
		_, err := toolNamed(t, tools, "status").Handle(ctx, noClient{}, Args{"ticket": "JIG-1"})
		waiting <- err
	}()
	cancel()
	close(release)
	<-done
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Errorf("the cancelled call returned %v, want context canceled", err)
	}
	if got := r.lines(); len(got) != 1 || got[0] != "next" {
		t.Errorf("ran %q, want only next", got)
	}
}

func TestEveryParamHasATypeAndADescription(t *testing.T) {
	t.Parallel()
	for _, tool := range Tools(Options{Run: (&fakeRunner{}).run}) {
		if tool.Verb == "" || tool.Handle == nil {
			t.Errorf("tool %s has verb %q and handler %v", tool.Name, tool.Verb, tool.Handle != nil)
		}
		seen := map[string]bool{}
		for _, p := range tool.Params {
			if p.Desc == "" || seen[p.Name] {
				t.Errorf("tool %s: param %s has description %q or repeats", tool.Name, p.Name, p.Desc)
			}
			seen[p.Name] = true
			switch p.Type {
			case typeString, typeBoolean, typeInteger, typeArray:
			default:
				t.Errorf("tool %s: param %s has type %q", tool.Name, p.Name, p.Type)
			}
			if p.Kind != Flag && p.Type != typeString {
				t.Errorf("tool %s: positional or text param %s is a %s, want a string", tool.Name, p.Name, p.Type)
			}
		}
	}
}
