package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/mcp"
)

// noElicitation is a client that cannot put a question to the person.
type noElicitation struct{}

func (noElicitation) CanElicit() bool { return false }
func (noElicitation) Elicit(context.Context, string, map[string]any) (mcp.ElicitResult, error) {
	return mcp.ElicitResult{}, nil
}

// sampleArgs gives every param of a tool a value of its type.
func sampleArgs(tool mcp.Tool) mcp.Args {
	args := mcp.Args{}
	for _, p := range tool.Params {
		switch {
		case len(p.Enum) > 0:
			args[p.Name] = p.Enum[0]
		case p.Name == "ticket":
			args[p.Name] = "JIG-1"
		case p.Name == "question_id":
			args[p.Name] = "q-001"
		case p.Type == "boolean":
			args[p.Name] = true
		case p.Type == "integer":
			args[p.Name] = int64(1)
		case p.Type == "array":
			args[p.Name] = []string{"a.go"}
		default:
			args[p.Name] = "x"
		}
	}
	return args
}

// TestMCPToolsPassOnlyTheFlagsTheirCommandsHave: every tool runs a registered
// command, and every flag a tool can put on its command line is one that
// command declares in its help row, so a tool cannot drift from its command.
// The words after the bare -- are text, not flags.
func TestMCPToolsPassOnlyTheFlagsTheirCommandsHave(t *testing.T) {
	t.Parallel()
	var argvs [][]string
	tools := mcp.Tools(mcp.Options{Run: func(_ context.Context, argv []string, _ string) (string, int) {
		argvs = append(argvs, argv)
		return "asked:\n  ticket: JIG-1\n  slice: c\n  question: q-001\n", 0
	}})
	if len(tools) != 9 {
		t.Fatalf("%d tools, want 9", len(tools))
	}
	for _, tool := range tools {
		argvs = nil
		if _, err := tool.Handle(context.Background(), noElicitation{}, sampleArgs(tool)); err != nil {
			t.Errorf("tool %s: %v", tool.Name, err)
			continue
		}
		v, ok := verbNamed(tool.Verb)
		if !ok {
			t.Errorf("tool %s runs %q, which is no registered command", tool.Name, tool.Verb)
			continue
		}
		if len(argvs) == 0 {
			t.Errorf("tool %s ran no command", tool.Name)
			continue
		}
		declared := map[string]bool{}
		for _, f := range v.Spec.Flags {
			declared[f.Name] = true
		}
		for _, argv := range argvs {
			for _, w := range argv {
				if w == "--" {
					break
				}
				if strings.HasPrefix(w, "--") && !declared[strings.TrimPrefix(w, "--")] {
					t.Errorf("tool %s ran `%s`, but %s has no flag %s", tool.Name, strings.Join(argv, " "), tool.Verb, w)
				}
			}
		}
	}
}

// TestMCPPinFollowsTheFlagsACommandHas: --project goes to a command that
// takes it, --store to one that takes that, and a project given to a command
// that takes only --store becomes the store the machine mapping holds for it;
// observe takes neither and is not pinned.
func TestMCPPinFollowsTheFlagsACommandHas(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	e := testEnv(fx.Home)

	for _, tc := range []struct {
		name           string
		store, project string
		verb           string
		want           string
	}{
		{"nothing given", "", "", "status", ""},
		{"store to a command with both", "/s", "", "status", "--store /s"},
		{"project to a command with both", "", "p", "status", "--project p"},
		{"both given to a command with both", "/s", "p", "status", "--project p --store /s"},
		{"store to a command with only a store", "/s", "", "here", "--store /s"},
		{"project to a command with only a store", "", "fixture", "here", "--store " + fx.StoreDir},
		{"store beats the mapping for a command with only a store", "/s", "fixture", "here", "--store /s"},
		{"a command with neither", "/s", "fixture", "observe", ""},
		{"a name that is no command", "/s", "fixture", "nope", ""},
	} {
		pins, err := mcpPin(e, tc.store, tc.project)(tc.verb)
		if err != nil || strings.Join(pins, " ") != tc.want {
			t.Errorf("%s: pins = %q, %v, want %q", tc.name, pins, err, tc.want)
		}
	}
	if _, err := mcpPin(e, "", "no-such-project")("here"); err == nil || !strings.Contains(err.Error(), `no project "no-such-project" in the machine mapping`) {
		t.Errorf("a project that is not mapped: error = %v, want the mapping error", err)
	}
	if pins, err := mcpPin(e, "", "no-such-project")("status"); err != nil || len(pins) != 2 {
		t.Errorf("--project reaches a command that resolves it itself, unchecked: %q, %v", pins, err)
	}
}

// mcpLines parses what jig mcp wrote, one message per line, by response id.
func mcpLines(t *testing.T, out string) map[string]map[string]json.RawMessage {
	t.Helper()
	got := map[string]map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("jig mcp wrote a line that is no JSON message: %q", line)
		}
		got[string(m["id"])] = m
	}
	return got
}

func mcpToolText(t *testing.T, m map[string]json.RawMessage) (string, bool) {
	t.Helper()
	var r struct {
		Content []struct{ Text string }
		IsError bool
	}
	if err := json.Unmarshal(m["result"], &r); err != nil || len(r.Content) != 1 {
		t.Fatalf("message %v is no tool result: %v", m, err)
	}
	return r.Content[0].Text, r.IsError
}

// runMCP runs jig mcp over the requests, then lets its input end, and returns
// the responses by id.
func runMCP(t *testing.T, e env, requests ...string) map[string]map[string]json.RawMessage {
	t.Helper()
	all := append([]string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`}, requests...)
	var out bytes.Buffer
	code := run(e, []string{"mcp"}, &out, strings.NewReader(strings.Join(all, "\n")+"\n"))
	if code != 0 {
		t.Fatalf("jig mcp exit %d, want 0 once its input ended\n%s", code, out.String())
	}
	got := mcpLines(t, out.String())
	if len(got) != len(requests)+1 {
		t.Fatalf("%d messages, want one response for each of the %d requests:\n%s", len(got), len(requests)+1, out.String())
	}
	return got
}

// TestMCPRunsTheCommandsInProcess: the server reads the whole input, answers
// every request once it has ended, and exits 0. A tool result is exactly what
// the command prints, and a tool that writes (ask) writes the store.
func TestMCPRunsTheCommandsInProcess(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket

	wantStatus, _ := runMain(t, e, "", "status", T)
	got := runMCP(t, e, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{"ticket":"`+T+`"}}}`)
	if text, failed := mcpToolText(t, got["2"]); failed || text != wantStatus {
		t.Errorf("status tool = %q (failed %v), want what `jig status` prints:\n%s", text, failed, wantStatus)
	}

	got = runMCP(t, e,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask","arguments":{"ticket":"`+T+`","slice":"c","question":"Formal or casual?"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"next","arguments":{"ticket":"nope"}}}`)
	if text, failed := mcpToolText(t, got["2"]); failed || !strings.Contains(text, "question: q-001") || !strings.Contains(text, "q-001 stays open") {
		t.Errorf("ask tool = %q (failed %v), want the recorded question", text, failed)
	}
	if q, err := st.ReadQuestion(T, "q-001"); err != nil || q.Status != "open" || q.Body != "Formal or casual?" {
		t.Errorf("question = %+v, %v, want q-001 open", q, err)
	}
	if text, failed := mcpToolText(t, got["3"]); !failed || !strings.Contains(text, "code:") {
		t.Errorf("next for no such ticket = %q (failed %v), want a failed tool with the error of the command", text, failed)
	}
}

// TestMCPEndsAtTheFirstInterrupt: a server whose client never writes or hangs
// up stops when the run's context is cancelled, with the status of an interrupt.
func TestMCPEndsAtTheFirstInterrupt(t *testing.T) {
	t.Parallel()
	_, _, e := workFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	stdinR, stdinW := io.Pipe()
	defer stdinW.Close()
	done := make(chan int, 1)
	var out bytes.Buffer
	go func() { done <- run(e.withContext(ctx), []string{"mcp"}, &out, stdinR) }()
	cancel()
	select {
	case code := <-done:
		if code != exitInterrupted {
			t.Errorf("exit %d, want %d", code, exitInterrupted)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("jig mcp did not end after its context was cancelled")
	}
}

func TestMCPTakesNoArguments(t *testing.T) {
	t.Parallel()
	_, _, e := workFixture(t)
	out, code := runMain(t, e, "", "mcp", "extra")
	if code != 2 || !strings.Contains(out, "code: VALIDATION_ERROR") || !strings.Contains(out, "takes no arguments") {
		t.Errorf("jig mcp extra: exit %d, want 2 with a validation error\n%s", code, out)
	}
	out, code = runMain(t, e, "", "mcp", "-h")
	if code != 0 || !strings.Contains(out, "--store") || !strings.Contains(out, "--project") {
		t.Errorf("jig mcp -h: exit %d, want the flags\n%s", code, out)
	}
}
