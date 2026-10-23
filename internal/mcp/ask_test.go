package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// askedOut is what `jig ask` prints for a question it recorded.
const askedOut = "asked:\n  ticket: JIG-1\n  slice: c\n  question: q-001\n  state: needs-input\n" +
	"help[1]:\n  Carry on with what the question does not block; the answer comes with `jig ask --wait --slice c`\n"

// scriptedClient is a Client that answers an elicitation as scripted.
type scriptedClient struct {
	can     bool
	res     ElicitResult
	err     error
	asked   int
	message string
	schema  map[string]any
}

func (c *scriptedClient) CanElicit() bool { return c.can }
func (c *scriptedClient) Elicit(_ context.Context, message string, schema map[string]any) (ElicitResult, error) {
	c.asked++
	c.message, c.schema = message, schema
	return c.res, c.err
}

// askRunner answers ask with askedOut and answer with answeredOut.
func askRunner(answerCode int) *fakeRunner {
	return &fakeRunner{answer: func(argv []string) (string, int) {
		switch argv[0] {
		case "ask":
			return askedOut, 0
		case "answer":
			if answerCode != 0 {
				return "error: question q-001 is answered, not open\ncode: QUESTION_NOT_OPEN\n", answerCode
			}
			return "answered:\n  ticket: JIG-1\n  question: q-001\n  slice: c\n  provenance: person\n  state: queued\n", 0
		}
		return "", 1
	}}
}

func askWith(t *testing.T, r *fakeRunner, c Client, args map[string]any) (Result, error) {
	t.Helper()
	tool := toolNamed(t, Tools(Options{Run: r.run}), "ask")
	raw, _ := jsonRaw(args)
	a, err := tool.decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Handle(context.Background(), c, a)
}

func TestAskPutsTheQuestionToThePersonAndRecordsTheirAnswerAsTheirs(t *testing.T) {
	t.Parallel()
	r := askRunner(0)
	c := &scriptedClient{can: true, res: ElicitResult{Action: "accept", Content: map[string]any{"answer": "  Casual,\nplease.  "}}}
	res, err := askWith(t, r, c, map[string]any{"ticket": "JIG-1", "slice": "c", "session": "s-1", "question": "Formal or casual?"})
	if err != nil || res.IsError {
		t.Fatalf("ask = %+v, %v", res, err)
	}
	if want := []string{"ask JIG-1 --slice c --session s-1 -- Formal or casual?", "answer JIG-1 q-001 --as person -- Casual,\nplease."}; strings.Join(r.lines(), "|") != strings.Join(want, "|") {
		t.Errorf("ran %q, want %q: the question recorded, then the answer as the person", r.lines(), want)
	}
	if c.asked != 1 || !strings.Contains(c.message, "JIG-1, slice c asks:") || !strings.HasSuffix(c.message, "Formal or casual?") {
		t.Errorf("elicited %d times with message %q, want once, naming the ticket and slice, ending in the question", c.asked, c.message)
	}
	props, _ := c.schema["properties"].(map[string]any)
	if c.schema["type"] != "object" || props["answer"] == nil || len(props) != 1 {
		t.Errorf("schema = %v, want an object with one field, answer", c.schema)
	}
	want := "answer:\n  ticket: JIG-1\n  slice: c\n  question: q-001\n  provenance: person\ntext:\n  Casual,\n  please.\n" +
		"help[1]:\n  The person answered in the client; carry on with slice c using this answer\n"
	if res.Text != want {
		t.Errorf("result:\n%s\nwant:\n%s", res.Text, want)
	}
}

func TestAskLeavesTheQuestionOpenWhenThePersonDoesNotAnswer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		res  ElicitResult
		why  string
	}{
		{"decline", ElicitResult{Action: "decline"}, "The person declined to answer now, so q-001 stays open"},
		{"cancel", ElicitResult{Action: "cancel"}, "The person dismissed the question, so q-001 stays open"},
		{"something else", ElicitResult{Action: "maybe"}, "The person dismissed the question, so q-001 stays open"},
		{"accept with nothing", ElicitResult{Action: "accept", Content: map[string]any{"answer": "  "}}, "The person sent an empty answer, so q-001 stays open"},
		{"accept with no field", ElicitResult{Action: "accept", Content: map[string]any{}}, "The person sent an empty answer, so q-001 stays open"},
		{"accept with a number", ElicitResult{Action: "accept", Content: map[string]any{"answer": 4}}, "The person sent an empty answer, so q-001 stays open"},
	} {
		r := askRunner(0)
		c := &scriptedClient{can: true, res: tc.res}
		res, err := askWith(t, r, c, map[string]any{"question": "Formal or casual?"})
		if err != nil || res.IsError {
			t.Errorf("%s: ask = %+v, %v, want the record and no error", tc.name, res, err)
			continue
		}
		if got := r.lines(); len(got) != 1 || !strings.HasPrefix(got[0], "ask ") {
			t.Errorf("%s: ran %q, want only the ask: nothing is answered", tc.name, got)
		}
		for _, want := range []string{"asked:\n  ticket: JIG-1\n  slice: c\n  question: q-001\n  state: needs-input\n", tc.why} {
			if !strings.Contains(res.Text, want) {
				t.Errorf("%s: result lacks %q:\n%s", tc.name, want, res.Text)
			}
		}
		if strings.Contains(res.Text, "--wait") {
			t.Errorf("%s: result sends the caller to jig ask --wait, which it has no way to run:\n%s", tc.name, res.Text)
		}
	}
}

func TestAskFallsBackToTheRecordWhenTheClientCannotAsk(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		client *scriptedClient
		why    string
		asks   int
	}{
		{"no capability", &scriptedClient{can: false}, "The client cannot ask the person here, so q-001 stays open", 0},
		{"the client refuses", &scriptedClient{can: true, err: &RPCError{Code: codeMethodNotFound, Message: "method not found"}}, "The client could not ask the person: json-rpc error -32601: method not found, so q-001 stays open", 1},
		{"the client went away", &scriptedClient{can: true, err: errClosed}, "The client went away before the person answered, so q-001 stays open", 1},
	} {
		r := askRunner(0)
		res, err := askWith(t, r, tc.client, map[string]any{"question": "Formal or casual?"})
		if err != nil || res.IsError || !strings.Contains(res.Text, tc.why) {
			t.Errorf("%s: ask = %+v, %v, want the record saying %q", tc.name, res, err, tc.why)
		}
		if tc.client.asked != tc.asks {
			t.Errorf("%s: the client was asked %d times, want %d", tc.name, tc.client.asked, tc.asks)
		}
		if got := r.lines(); len(got) != 1 {
			t.Errorf("%s: ran %q, want only the ask", tc.name, got)
		}
	}
}

func TestAskStopsWhenItsRequestIsCancelledWhileThePersonDecides(t *testing.T) {
	t.Parallel()
	r := askRunner(0)
	ctx, cancel := context.WithCancel(context.Background())
	c := &cancellingClient{cancel: cancel}
	tool := toolNamed(t, Tools(Options{Run: r.run}), "ask")
	_, err := tool.Handle(ctx, c, Args{"question": "Formal or casual?"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ask = %v, want context canceled", err)
	}
	if got := r.lines(); len(got) != 1 {
		t.Errorf("ran %q, want the ask and no answer", got)
	}
}

type cancellingClient struct{ cancel func() }

func (*cancellingClient) CanElicit() bool { return true }
func (c *cancellingClient) Elicit(ctx context.Context, _ string, _ map[string]any) (ElicitResult, error) {
	c.cancel()
	<-ctx.Done()
	return ElicitResult{}, ctx.Err()
}

func TestAskReportsWhatItCannotRecord(t *testing.T) {
	t.Parallel()
	// The ask itself fails: the command output is the result, and nobody is asked.
	r := &fakeRunner{answer: func([]string) (string, int) { return "error: no slice\ncode: VALIDATION_ERROR\n", 2 }}
	c := &scriptedClient{can: true}
	res, err := askWith(t, r, c, map[string]any{"question": "x?"})
	if err != nil || !res.IsError || res.Text != "error: no slice\ncode: VALIDATION_ERROR\n" || c.asked != 0 {
		t.Errorf("failed ask = %+v, %v (asked %d times), want the error of the command, nobody asked", res, err, c.asked)
	}

	// The person answers but the answer is refused: a failed tool that says so.
	r = askRunner(1)
	c = &scriptedClient{can: true, res: ElicitResult{Action: "accept", Content: map[string]any{"answer": "Casual."}}}
	res, err = askWith(t, r, c, map[string]any{"question": "x?"})
	if err != nil || !res.IsError || !strings.Contains(res.Text, "could not be recorded") || !strings.Contains(res.Text, "QUESTION_NOT_OPEN") {
		t.Errorf("refused answer = %+v, %v, want a failed tool with the refusal", res, err)
	}

	// Output the code does not know is passed through, never guessed at.
	r = &fakeRunner{answer: func([]string) (string, int) { return "recorded something\n", 0 }}
	c = &scriptedClient{can: true}
	res, err = askWith(t, r, c, map[string]any{"question": "x?"})
	if err != nil || res.IsError || res.Text != "recorded something\n" || c.asked != 0 {
		t.Errorf("unknown output = %+v, %v (asked %d times), want it passed through", res, err, c.asked)
	}
}

func TestBlockFields(t *testing.T) {
	t.Parallel()
	out := "asked:\n  ticket: JIG-1\n  question: \"q-001\"\n  note: \"a \\\"b\\\"\\nc\"\n  noval\nhelp[1]:\n  question: other\n"
	got := blockFields(out, "asked")
	if got["ticket"] != "JIG-1" || got["question"] != "q-001" || got["note"] != "a \"b\"\nc" || len(got) != 3 {
		t.Errorf("fields = %v", got)
	}
	if f := blockFields(out, "missing"); f != nil {
		t.Errorf("a missing block = %v, want nil", f)
	}
	if f := blockFields("asked:\r\n  slice: c\r\n", "asked"); f["slice"] != "c" {
		t.Errorf("fields of CRLF output = %v, want slice c", f)
	}
}

func TestWithoutHelp(t *testing.T) {
	t.Parallel()
	if got := withoutHelp(askedOut); got != "asked:\n  ticket: JIG-1\n  slice: c\n  question: q-001\n  state: needs-input" {
		t.Errorf("withoutHelp = %q", got)
	}
	if got := withoutHelp("a:\n  b: c\n"); got != "a:\n  b: c" {
		t.Errorf("without a help block = %q", got)
	}
}

// elicitingWire is a server over pipes with a client that declared forms and
// the ask tool over a runner that records.
func elicitingWire(t *testing.T) (*wire, *fakeRunner) {
	t.Helper()
	r := askRunner(0)
	w := serve(t, Tools(Options{Run: r.run}))
	w.initialize(map[string]any{"elicitation": map[string]any{}})
	return w, r
}

func TestAskOverTheWireElicitsAndReturnsTheAnswer(t *testing.T) {
	t.Parallel()
	w, r := elicitingWire(t)
	id := w.request("tools/call", map[string]any{"name": "ask", "arguments": map[string]any{"question": "Formal or casual?"}})
	req := w.recv()
	if req.Method != "elicitation/create" || len(req.ID) == 0 {
		t.Fatalf("the server sent %+v, want an elicitation/create request", req)
	}
	// The question is on the record before the person is asked.
	if got := r.lines(); len(got) != 1 {
		t.Errorf("ran %q while the person decides, want the ask", got)
	}
	w.send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"action": "accept", "content": map[string]any{"answer": "Casual."}}})
	text, failed := toolText(t, w.response(id))
	if failed || !strings.Contains(text, "provenance: person") || !strings.Contains(text, "  Casual.\n") {
		t.Errorf("ask = %q (failed %v), want the answer of the person", text, failed)
	}
	if got := r.lines(); len(got) != 2 || got[1] != "answer JIG-1 q-001 --as person -- Casual." {
		t.Errorf("ran %q, want the answer as the person second", got)
	}
}

func TestAskOverTheWireFallsBackWhenTheClientAnswersWithAnError(t *testing.T) {
	t.Parallel()
	w, r := elicitingWire(t)
	id := w.request("tools/call", map[string]any{"name": "ask", "arguments": map[string]any{"question": "Formal or casual?"}})
	req := w.recv()
	w.send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "no elicitation here"}})
	text, failed := toolText(t, w.response(id))
	if failed || !strings.Contains(text, "no elicitation here") || !strings.Contains(text, "q-001 stays open") {
		t.Errorf("ask = %q (failed %v), want the record, saying why", text, failed)
	}
	if got := r.lines(); len(got) != 1 {
		t.Errorf("ran %q, want only the ask", got)
	}
}

func TestAskOverTheWireIsCancelledWithItsElicitation(t *testing.T) {
	t.Parallel()
	w, r := elicitingWire(t)
	id := w.request("tools/call", map[string]any{"name": "ask", "arguments": map[string]any{"question": "Formal or casual?"}})
	req := w.recv()
	w.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id}})
	// The server withdraws the elicitation it had out and answers nothing.
	note := w.recv()
	if note.Method != "notifications/cancelled" || !strings.Contains(string(note.Params), string(req.ID)) {
		t.Fatalf("the server sent %+v, want a cancelled notification for request %s", note, req.ID)
	}
	rest, err := w.finish()
	if err != nil || len(rest) != 0 {
		t.Errorf("after the cancel: Serve = %v, wrote %+v, want nothing more", err, rest)
	}
	if got := r.lines(); len(got) != 1 {
		t.Errorf("ran %q, want only the ask", got)
	}
}

func TestAskOverTheWireWhenTheClientHangsUpWhileAsking(t *testing.T) {
	t.Parallel()
	w, _ := elicitingWire(t)
	id := w.request("tools/call", map[string]any{"name": "ask", "arguments": map[string]any{"question": "Formal or casual?"}})
	w.recv()
	rest, err := w.finish() // the input ends: no answer can come
	if err != nil || len(rest) != 1 || string(rest[0].ID) != itoa(id) {
		t.Fatalf("Serve = %v, wrote %+v, want the one response to request %d", err, rest, id)
	}
	if text, failed := toolText(t, rest[0]); failed || !strings.Contains(text, "went away") || !strings.Contains(text, "q-001 stays open") {
		t.Errorf("ask = %q (failed %v), want the record, saying the client went away", text, failed)
	}
}
