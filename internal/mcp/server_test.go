package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/observe"
)

// probeTool is a tool whose handler is the function given, with no params.
func probeTool(name string, h func(ctx context.Context, c Client, a Args) (Result, error)) Tool {
	return Tool{Name: name, Description: "probe", Handle: h}
}

func TestInitializeNegotiatesTheVersionAndDescribesTheServer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ asked, want string }{
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		{"2024-11-05", "2024-11-05"},
		{"2099-01-01", protocolVersions[0]},
		{"", protocolVersions[0]},
	} {
		w := serveServer(t, NewServer(Info{Name: "jig", Version: "9.9", Instructions: "use me"}, nil))
		m := w.call("initialize", map[string]any{"protocolVersion": tc.asked, "capabilities": map[string]any{}})
		var res struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				Tools map[string]any `json:"tools"`
			} `json:"capabilities"`
			ServerInfo   struct{ Name, Version string } `json:"serverInfo"`
			Instructions string                         `json:"instructions"`
		}
		if err := json.Unmarshal(m.Result, &res); err != nil {
			t.Fatalf("asked %q: %v", tc.asked, err)
		}
		if res.ProtocolVersion != tc.want || res.Capabilities.Tools == nil || res.ServerInfo.Name != "jig" || res.ServerInfo.Version != "9.9" || res.Instructions != "use me" {
			t.Errorf("asked %q: result = %s, want protocol %s, a tools capability, server jig 9.9 and the instructions", tc.asked, m.Result, tc.want)
		}
	}
}

func TestInitializeRefusesParamsThatAreNotAnObject(t *testing.T) {
	t.Parallel()
	w := serve(t, nil)
	if m := w.call("initialize", []int{1}); m.Error == nil || m.Error.Code != codeInvalidParams {
		t.Errorf("initialize with an array = %+v, want invalid params", m)
	}
}

func TestElicitationIsOfferedToAClientThatDeclaredForms(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		caps string
		want bool
	}{
		{"none", `{}`, false},
		{"empty", `{"elicitation":{}}`, true},
		{"form", `{"elicitation":{"form":{}}}`, true},
		{"form and url", `{"elicitation":{"form":{},"url":{}}}`, true},
		{"url only", `{"elicitation":{"url":{}}}`, false},
		{"null", `{"elicitation":null}`, false},
		{"another capability", `{"sampling":{}}`, false},
	} {
		var got bool
		tool := probeTool("probe", func(_ context.Context, c Client, _ Args) (Result, error) {
			got = c.CanElicit()
			return Result{Text: "ok"}, nil
		})
		w := serve(t, []Tool{tool})
		var caps map[string]any
		if err := json.Unmarshal([]byte(tc.caps), &caps); err != nil {
			t.Fatal(err)
		}
		w.initialize(caps)
		w.callTool("probe", nil)
		if got != tc.want {
			t.Errorf("%s: CanElicit = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestToolsListDescribesEveryTool(t *testing.T) {
	t.Parallel()
	w := serve(t, Tools(Options{Run: (&fakeRunner{}).run}))
	w.initialize(map[string]any{})
	m := w.call("tools/list", nil)
	var res struct {
		Tools []struct {
			Name        string
			Description string
			InputSchema struct {
				Type                 string
				Properties           map[string]map[string]any
				Required             []string
				AdditionalProperties *bool
			}
			Annotations map[string]any
		}
	}
	if err := json.Unmarshal(m.Result, &res); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		if tl.Description == "" || tl.InputSchema.Type != "object" || tl.InputSchema.AdditionalProperties == nil || *tl.InputSchema.AdditionalProperties {
			t.Errorf("tool %s: description %q, schema %+v, want a described object that takes no extra properties", tl.Name, tl.Description, tl.InputSchema)
		}
		for name, p := range tl.InputSchema.Properties {
			typ, _ := p["type"].(string)
			if desc, _ := p["description"].(string); desc == "" || (typ != "string" && typ != "boolean" && typ != "integer" && typ != "array") {
				t.Errorf("tool %s: property %s = %v, want a described property of a JSON Schema type", tl.Name, name, p)
			}
			if typ == "array" && p["items"] == nil {
				t.Errorf("tool %s: array property %s has no items", tl.Name, name)
			}
		}
		for _, r := range tl.InputSchema.Required {
			if _, ok := tl.InputSchema.Properties[r]; !ok {
				t.Errorf("tool %s requires %s, which is no property", tl.Name, r)
			}
		}
		if _, ok := tl.Annotations["readOnlyHint"].(bool); !ok {
			t.Errorf("tool %s has no readOnlyHint: %v", tl.Name, tl.Annotations)
		}
	}
	if want := "here next context claim ask answer note observe status"; strings.Join(names, " ") != want {
		t.Errorf("tools = %v, want %s", names, want)
	}
	for _, tl := range res.Tools {
		if tl.Name == "observe" {
			var kinds []string
			for _, k := range tl.InputSchema.Properties["event"]["enum"].([]any) {
				kinds = append(kinds, k.(string))
			}
			if strings.Join(kinds, " ") != strings.Join(observe.Kinds(), " ") || len(tl.InputSchema.Required) != 1 || tl.InputSchema.Required[0] != "event" {
				t.Errorf("observe: event enum %v, required %v, want the event kinds of observe and event required", kinds, tl.InputSchema.Required)
			}
		}
	}
}

func TestPingAndUnknownMethods(t *testing.T) {
	t.Parallel()
	w := serve(t, nil)
	if m := w.call("ping", nil); m.Error != nil || string(m.Result) != "{}" {
		t.Errorf("ping = %+v, want {}", m)
	}
	for _, method := range []string{"resources/list", "prompts/list", "logging/setLevel", "nope"} {
		if m := w.call(method, nil); m.Error == nil || m.Error.Code != codeMethodNotFound {
			t.Errorf("%s = %+v, want method not found", method, m)
		}
	}
	// A notification gets no reply: the next message is the ping's.
	w.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/roots/list_changed"})
	w.send(map[string]any{"jsonrpc": "2.0", "method": "no/such/notification"})
	if m := w.call("ping", nil); m.Error != nil {
		t.Errorf("ping after notifications = %+v", m)
	}
}

func TestMalformedInputGetsAnErrorAndTheServerCarriesOn(t *testing.T) {
	t.Parallel()
	w := serve(t, nil)
	w.sendRaw("not json")
	if m := w.recv(); m.Error == nil || m.Error.Code != codeParseError || string(m.ID) != "null" {
		t.Errorf("garbage = %+v, want a parse error with a null id", m)
	}
	w.sendRaw(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)
	if m := w.recv(); m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("a batch = %+v, want an invalid request", m)
	}
	w.sendRaw(`{"jsonrpc":"2.0","id":7}`)
	if m := w.recv(); m.Error == nil || m.Error.Code != codeInvalidRequest || string(m.ID) != "7" {
		t.Errorf("a message with no method = %+v, want an invalid request with the id", m)
	}
	w.sendRaw("")
	w.sendRaw("   ")
	if m := w.call("ping", nil); m.Error != nil {
		t.Errorf("ping after the bad lines = %+v", m)
	}
}

func TestToolCallsAreCheckedBeforeTheyRun(t *testing.T) {
	t.Parallel()
	var ran int
	tool := Tool{
		Name: "probe", Description: "probe",
		Params: []Param{
			{Name: "name", Type: typeString, Required: true, Desc: "a name"},
			{Name: "mode", Type: typeString, Enum: []string{"fast", "slow"}, Desc: "a mode"},
			{Name: "count", Type: typeInteger, Desc: "a count"},
			{Name: "on", Type: typeBoolean, Desc: "a switch"},
			{Name: "list", Type: typeArray, Desc: "a list"},
		},
		Handle: func(context.Context, Client, Args) (Result, error) { ran++; return Result{Text: "ran"}, nil },
	}
	w := serve(t, []Tool{tool})
	for _, tc := range []struct {
		name string
		args any
		want string // text of the failed tool
	}{
		{"unknown argument", map[string]any{"name": "x", "colour": "red"}, `probe has no argument "colour"; its arguments are name, mode, count, on, list`},
		{"missing", map[string]any{}, `needs the argument "name"`},
		{"blank", map[string]any{"name": "  "}, `needs the argument "name"`},
		{"null", map[string]any{"name": nil}, `needs the argument "name"`},
		{"wrong type", map[string]any{"name": 4}, `argument "name" of probe: must be a string`},
		{"outside the enum", map[string]any{"name": "x", "mode": "medium"}, "must be one of fast, slow"},
		{"not an integer", map[string]any{"name": "x", "count": "3"}, "must be an integer"},
		{"not an integer either", map[string]any{"name": "x", "count": 1.5}, "must be an integer"},
		{"not a boolean", map[string]any{"name": "x", "on": "yes"}, "must be true or false"},
		{"not a list of strings", map[string]any{"name": "x", "list": []int{1}}, "must be an array of strings"},
		{"arguments not an object", []string{"x"}, "must be a JSON object"},
	} {
		m := w.call("tools/call", map[string]any{"name": "probe", "arguments": tc.args})
		got, isErr := toolText(t, m)
		if !isErr || !strings.Contains(got, tc.want) {
			t.Errorf("%s: text %q (failed %v), want a failed tool saying %q", tc.name, got, isErr, tc.want)
		}
	}
	if ran != 0 {
		t.Errorf("the handler ran %d times for calls that are all invalid", ran)
	}
	if got, isErr := w.callTool("probe", map[string]any{"name": "x", "mode": "fast", "count": 2, "on": true, "list": []string{"a"}}); isErr || got != "ran" {
		t.Errorf("a valid call = %q (failed %v), want ran", got, isErr)
	}
	// Protocol errors: no tool name, an unknown tool, params that are no object.
	for _, params := range []any{map[string]any{}, map[string]any{"name": "nope"}, []int{1}} {
		if m := w.call("tools/call", params); m.Error == nil || m.Error.Code != codeInvalidParams {
			t.Errorf("tools/call %v = %+v, want invalid params", params, m)
		}
	}
}

func TestAToolCallDoesNotBlockTheServer(t *testing.T) {
	t.Parallel()
	started, release := make(chan struct{}), make(chan struct{})
	slow := probeTool("slow", func(context.Context, Client, Args) (Result, error) {
		close(started)
		<-release
		return Result{Text: "done"}, nil
	})
	w := serve(t, []Tool{slow})
	id := w.request("tools/call", map[string]any{"name": "slow"})
	<-started
	if m := w.call("ping", nil); m.Error != nil {
		t.Fatalf("ping while a tool runs = %+v", m)
	}
	close(release)
	if got, _ := toolText(t, w.response(id)); got != "done" {
		t.Errorf("slow = %q, want done", got)
	}
}

func TestACancelledRequestGetsNoReply(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	blocked := probeTool("blocked", func(ctx context.Context, _ Client, _ Args) (Result, error) {
		close(started)
		<-ctx.Done()
		return Result{Text: "too late"}, nil
	})
	w := serve(t, []Tool{blocked})
	id := w.request("tools/call", map[string]any{"name": "blocked"})
	<-started
	w.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id, "reason": "changed my mind"}})
	// Serve returns once its handlers have, and the server has said all it will.
	rest, err := w.finish()
	if err != nil {
		t.Errorf("Serve = %v, want nil", err)
	}
	for _, m := range rest {
		t.Errorf("the server wrote %+v after the request was cancelled, want nothing", m)
	}
}

func TestEndOfInputLetsRunningRequestsFinishAndAnswer(t *testing.T) {
	t.Parallel()
	started, release := make(chan struct{}), make(chan struct{})
	slow := probeTool("slow", func(context.Context, Client, Args) (Result, error) {
		close(started)
		<-release
		return Result{Text: "done"}, nil
	})
	w := serve(t, []Tool{slow})
	id := w.request("tools/call", map[string]any{"name": "slow"})
	<-started
	w.in.Close() // a client that sent its requests and hung up, as a pipe does
	close(release)
	rest, err := w.finish()
	if err != nil || len(rest) != 1 || string(rest[0].ID) != itoa(id) {
		t.Fatalf("after the input ended: Serve = %v, wrote %+v, want one response to request %d", err, rest, id)
	}
	if got, _ := toolText(t, rest[0]); got != "done" {
		t.Errorf("response = %q, want done", got)
	}
}

func TestContextCancelStopsServeAndCancelsTheRequests(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	var sawCancel bool
	var mu sync.Mutex
	blocked := probeTool("blocked", func(ctx context.Context, _ Client, _ Args) (Result, error) {
		close(started)
		<-ctx.Done()
		mu.Lock()
		sawCancel = true
		mu.Unlock()
		return Result{}, ctx.Err()
	})
	w := serve(t, []Tool{blocked})
	w.request("tools/call", map[string]any{"name": "blocked"})
	<-started
	w.cancel()
	_, err := w.finish()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Serve = %v, want context canceled", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawCancel {
		t.Errorf("the running request was not cancelled")
	}
}

func TestAMessageOverTheLimitEndsTheSession(t *testing.T) {
	t.Parallel()
	w := serveServer(t, &Server{info: Info{Name: "x"}, maxMessage: 200})
	if m := w.call("ping", nil); m.Error != nil {
		t.Fatal(m.Error)
	}
	// The server stops reading at the line, so a pipe write of it would wait for ever.
	go io.WriteString(w.in, `{"jsonrpc":"2.0","id":9,"method":"ping","params":{"pad":"`+strings.Repeat("x", 500)+`"}}`+"\n")
	var err error
	select {
	case err = <-w.done: // the server ends the session by itself
	case <-time.After(wait):
		t.Fatal("Serve did not end at a message over the limit")
	}
	w.in.Close()
	if err == nil || !strings.Contains(err.Error(), "reading the client") {
		t.Errorf("Serve = %v, want the error of reading a message that is too long", err)
	}
	for m := range w.msgs {
		t.Errorf("the server answered a message it refused to read: %+v", m)
	}
}
func TestAHandlerThatPanicsCostsOnlyItsOwnRequest(t *testing.T) {
	t.Parallel()
	boom := probeTool("boom", func(context.Context, Client, Args) (Result, error) { panic("kaboom") })
	w := serve(t, []Tool{boom})
	if m := w.call("tools/call", map[string]any{"name": "boom"}); m.Error == nil || m.Error.Code != codeInternalError || !strings.Contains(m.Error.Message, "kaboom") {
		t.Errorf("a panicking tool = %+v, want an internal error naming the panic", m)
	}
	if m := w.call("ping", nil); m.Error != nil {
		t.Errorf("ping after the panic = %+v", m)
	}
}

func TestAToolThatFailsIsAFailedToolWithItsMessage(t *testing.T) {
	t.Parallel()
	bad := probeTool("bad", func(context.Context, Client, Args) (Result, error) { return Result{}, errors.New("no such thing") })
	w := serve(t, []Tool{bad})
	if got, failed := w.callTool("bad", nil); !failed || got != "no such thing" {
		t.Errorf("bad = %q (failed %v), want the error as the text of a failed tool", got, failed)
	}
}
