package e2e

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// rpcMsg is any JSON-RPC message jig mcp writes: a response (ID and Result or
// Error) or a request of the server to the client (ID and Method).
type rpcMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// mcpClient is a minimal MCP client over the stdio of a running `jig mcp`.
type mcpClient struct {
	t      *testing.T
	p      *jigProc
	nextID int
}

// startMCP starts `jig mcp args...` in cwd and initializes it as a client with
// the given capabilities.
func startMCP(t *testing.T, env jigEnv, cwd string, caps map[string]any, args ...string) *mcpClient {
	t.Helper()
	c := &mcpClient{t: t, p: startJig(t, env, cwd, append([]string{"mcp"}, args...)...)}
	res := c.call("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    caps,
		"clientInfo":      map[string]any{"name": "e2e", "version": "1"},
	})
	if res.Error != nil {
		t.Fatalf("initialize: %+v", res.Error)
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct{ Name string }
		Capabilities    struct{ Tools *json.RawMessage }
	}
	if err := json.Unmarshal(res.Result, &init); err != nil || init.ProtocolVersion != "2025-06-18" || init.ServerInfo.Name != "jig" || init.Capabilities.Tools == nil {
		t.Fatalf("initialize result = %s (%v), want protocol 2025-06-18, server jig, tools", res.Result, err)
	}
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return c
}

func (c *mcpClient) send(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	c.p.Write(append(b, '\n'))
}

// request sends a request and returns its id.
func (c *mcpClient) request(method string, params any) int {
	c.t.Helper()
	c.nextID++
	c.send(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	return c.nextID
}

// read returns the next message of the server.
func (c *mcpClient) read() rpcMsg {
	c.t.Helper()
	line, ok := c.p.Line(60 * time.Second)
	if !ok {
		c.t.Fatalf("jig mcp wrote nothing within 60s\nstderr:\n%s", c.p.Stop().Stderr)
	}
	var m rpcMsg
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		c.t.Fatalf("jig mcp wrote a line that is no JSON message: %q", line)
	}
	return m
}

// await returns the response to request id; any other message fails the test.
func (c *mcpClient) await(id int) rpcMsg {
	c.t.Helper()
	m := c.read()
	if m.Method != "" || string(m.ID) != strconv.Itoa(id) {
		c.t.Fatalf("waiting for the response to request %d, jig mcp sent %s %s (id %s)", id, m.Method, m.Params, m.ID)
	}
	return m
}

func (c *mcpClient) call(method string, params any) rpcMsg {
	c.t.Helper()
	return c.await(c.request(method, params))
}

// toolResult is what a tools/call result carries.
type toolResult struct {
	Text    string
	IsError bool
}

func decodeToolResult(t *testing.T, m rpcMsg) toolResult {
	t.Helper()
	if m.Error != nil {
		t.Fatalf("tools/call failed as a protocol error: %+v", m.Error)
	}
	var r struct {
		Content []struct{ Type, Text string }
		IsError bool
	}
	if err := json.Unmarshal(m.Result, &r); err != nil || len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatalf("tools/call result = %s (%v), want one text content", m.Result, err)
	}
	return toolResult{Text: r.Content[0].Text, IsError: r.IsError}
}

// tool calls a tool and returns its result.
func (c *mcpClient) tool(name string, args map[string]any) toolResult {
	c.t.Helper()
	return decodeToolResult(c.t, c.call("tools/call", map[string]any{"name": name, "arguments": args}))
}

// stop closes the server input and requires a clean exit.
func (c *mcpClient) stop() {
	c.t.Helper()
	if res := c.p.Stop(); res.Code != 0 {
		c.t.Errorf("jig mcp exit %d after its input closed, want 0\nstderr:\n%s", res.Code, res.Stderr)
	}
}

// elicitationRequest reads the server request to the client and checks it is
// an elicitation/create of a form with an answer field; it returns the
// message shown to the person and the id to answer.
func (c *mcpClient) elicitationRequest() (message string, id json.RawMessage) {
	c.t.Helper()
	m := c.read()
	if m.Method != "elicitation/create" || len(m.ID) == 0 {
		c.t.Fatalf("jig mcp sent %s %s (id %s), want an elicitation/create request", m.Method, m.Params, m.ID)
	}
	var p struct {
		Message         string `json:"message"`
		RequestedSchema struct {
			Type       string
			Properties map[string]struct{ Type string }
			Required   []string
		} `json:"requestedSchema"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		c.t.Fatalf("elicitation params = %s: %v", m.Params, err)
	}
	if p.RequestedSchema.Type != "object" || p.RequestedSchema.Properties["answer"].Type != "string" || len(p.RequestedSchema.Required) != 1 {
		c.t.Errorf("requestedSchema = %+v, want an object with one required string field answer", p.RequestedSchema)
	}
	return p.Message, m.ID
}

// respond answers a request of the server.
func (c *mcpClient) respond(id json.RawMessage, result any) {
	c.t.Helper()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// TestMCPElicitationAnswersAsk: a client that declares elicitation is asked
// for the answer through the form. The person accepts, and the question is
// answered: the question file, the slice (back to queued, nobody holds it) and
// the journal answer line speak for the person, and the tool call returns the
// answer to the agent that asked.
func TestMCPElicitationAnswersAsk(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	c := startMCP(t, env, fx.RepoDir, map[string]any{"elicitation": map[string]any{}})

	id := c.request("tools/call", map[string]any{"name": "ask", "arguments": map[string]any{
		"ticket": fx.Ticket, "slice": "c", "session": "s-agent", "question": "Formal or casual?",
	}})
	message, askID := c.elicitationRequest()
	for _, want := range []string{fx.Ticket, "slice c", "Formal or casual?"} {
		if !strings.Contains(message, want) {
			t.Errorf("elicitation message %q lacks %q", message, want)
		}
	}
	// While the person has not answered the question is recorded and the
	// slice is parked on it.
	if q, err := st.ReadQuestion(fx.Ticket, "q-001"); err != nil || q.Status != "open" || q.Body != "Formal or casual?" {
		t.Errorf("while the person decides the question = %+v, %v, want q-001 open", q, err)
	}
	if ss, _ := st.ReadSliceState(fx.Ticket, "c"); ss.State != "needs-input" || ss.Question != "q-001" {
		t.Errorf("while the person decides slice c is %+v, want needs-input on q-001", ss)
	}

	c.respond(askID, map[string]any{"action": "accept", "content": map[string]any{"answer": "Casual, please."}})
	res := decodeToolResult(t, c.await(id))
	if res.IsError {
		t.Fatalf("ask failed: %s", res.Text)
	}
	for _, want := range []string{"answer:\n", "  question: q-001\n", "  slice: c\n", "  provenance: person\n", "text:\n  Casual, please.\n"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("ask result lacks %q:\n%s", want, res.Text)
		}
	}

	q, err := st.ReadQuestion(fx.Ticket, "q-001")
	if err != nil || q.Status != "answered" || q.Answer != "Casual, please." || q.Provenance != "person" {
		t.Errorf("question = %+v, %v, want answered by the person with the text", q, err)
	}
	if ss, _ := st.ReadSliceState(fx.Ticket, "c"); ss.State != "queued" || ss.Question != "" {
		t.Errorf("after the answer slice c is %+v, want queued with no question", ss)
	}
	var asked, answered []journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		switch l.Event {
		case journal.EventQuestion:
			asked = append(asked, l)
		case journal.EventAnswer:
			answered = append(answered, l)
		}
	}
	if len(asked) != 1 || asked[0].Session != "s-agent" || asked[0].Ref != "q-001" || asked[0].Provenance != "agent" || asked[0].Summary != "Formal or casual?" {
		t.Errorf("question lines = %+v, want one from s-agent, agent provenance", asked)
	}
	if len(answered) != 1 || answered[0].Ref != "q-001" || answered[0].Slice != "c" || answered[0].Provenance != "person" || answered[0].Summary != "Casual, please." {
		t.Errorf("answer lines = %+v, want one for q-001 with Provenance person", answered)
	}

	// The server carries on after the elicitation.
	if got := c.tool("status", map[string]any{"ticket": fx.Ticket}); got.IsError || !strings.Contains(got.Text, "q-001") {
		t.Errorf("status after the answer = %+v, want the ticket with q-001", got)
	}
	c.stop()
}

// TestMCPDeclinedElicitationLeavesTheQuestionOpen: a person who declines (or
// dismisses) the form has not answered. The question stays recorded and open,
// the slice stays parked on it, no answer line is written, and the tool says
// so rather than failing.
func TestMCPDeclinedElicitationLeavesTheQuestionOpen(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	c := startMCP(t, env, fx.RepoDir, map[string]any{"elicitation": map[string]any{}})

	for i, action := range []string{"decline", "cancel"} {
		qid := "q-00" + strconv.Itoa(i+1)
		id := c.request("tools/call", map[string]any{"name": "ask", "arguments": map[string]any{
			"ticket": fx.Ticket, "slice": "c", "question": "Question number " + strconv.Itoa(i+1) + "?",
		}})
		_, askID := c.elicitationRequest()
		c.respond(askID, map[string]any{"action": action})
		res := decodeToolResult(t, c.await(id))
		if res.IsError || !strings.Contains(res.Text, "  question: "+qid+"\n") || !strings.Contains(res.Text, qid+" stays open") {
			t.Errorf("ask after %s = %+v, want the record of %s staying open and no error", action, res, qid)
		}
		if q, err := st.ReadQuestion(fx.Ticket, qid); err != nil || q.Status != "open" || q.Answer != "" {
			t.Errorf("after %s question %s = %+v, %v, want it open and unanswered", action, qid, q, err)
		}
	}
	if ss, _ := st.ReadSliceState(fx.Ticket, "c"); ss.State != "needs-input" {
		t.Errorf("slice c = %+v, want it still parked (needs-input)", ss)
	}
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventAnswer {
			t.Errorf("a declined elicitation wrote an answer line: %+v", l)
		}
	}
	c.stop()
}

// TestMCPAskWithoutElicitationFallsBackToTheRecord: a client that does not
// declare elicitation is never sent a request. The question is recorded, as
// `jig ask` does, and the answer, given later by another tool call, reaches
// the agent through context.
func TestMCPAskWithoutElicitationFallsBackToTheRecord(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	c := startMCP(t, env, fx.RepoDir, map[string]any{})

	// c.tool fails the test if the server sends anything but the response.
	res := c.tool("ask", map[string]any{"ticket": fx.Ticket, "slice": "c", "question": "- Formal or casual?"})
	for _, want := range []string{"asked:\n", "  question: q-001\n", "  state: needs-input\n", "q-001 stays open"} {
		if res.IsError || !strings.Contains(res.Text, want) {
			t.Errorf("ask result lacks %q (error %v):\n%s", want, res.IsError, res.Text)
		}
	}
	if strings.Contains(res.Text, "--wait") {
		t.Errorf("ask result points at jig ask --wait, which the tool does not have:\n%s", res.Text)
	}
	if q, err := st.ReadQuestion(fx.Ticket, "q-001"); err != nil || q.Status != "open" || q.Body != "- Formal or casual?" {
		t.Errorf("question = %+v, %v, want q-001 open with the text that begins with a dash", q, err)
	}

	// The answer comes from another tool call, as an agent's.
	ans := c.tool("answer", map[string]any{"ticket": fx.Ticket, "question_id": "q-001", "text": "Casual, please."})
	if ans.IsError || !strings.Contains(ans.Text, "  provenance: agent\n") || !strings.Contains(ans.Text, "  state: queued\n") {
		t.Errorf("answer = %+v, want answered as an agent and the slice queued", ans)
	}
	ctx := c.tool("context", map[string]any{"ticket": fx.Ticket, "slice": "c"})
	if ctx.IsError || !strings.Contains(ctx.Text, "questions[1]{id,status,question,answer,provenance}:") || !strings.Contains(ctx.Text, "Casual, please.") {
		t.Errorf("context = %+v, want the answered question", ctx)
	}

	// A refusal comes back as a failed tool, with the command's error.
	again := c.tool("answer", map[string]any{"ticket": fx.Ticket, "question_id": "q-001", "text": "Formal."})
	if !again.IsError || !strings.Contains(again.Text, "code: QUESTION_NOT_OPEN") {
		t.Errorf("answering twice = %+v, want a failed tool with QUESTION_NOT_OPEN", again)
	}
	c.stop()
}

// TestMCPToolsReturnWhatTheCommandsPrint: here, next and status return exactly
// what the commands print for the same directory; claim, note and observe do
// what the commands do to the store and the ledger; a bad call is a failed
// tool, not a dead server.
func TestMCPToolsReturnWhatTheCommandsPrint(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	c := startMCP(t, env, fx.RepoDir, map[string]any{})

	for _, tc := range []struct {
		tool string
		args map[string]any
		cli  []string
	}{
		{"here", map[string]any{"ticket": fx.Ticket, "session": "s-fixed"}, []string{"here", fx.Ticket, "--session", "s-fixed"}},
		{"here", map[string]any{"json": true, "session": "s-fixed"}, []string{"here", "--json", "--session", "s-fixed"}},
		{"next", map[string]any{}, []string{"next"}},
		{"next", map[string]any{"ticket": fx.Ticket, "topology": true}, []string{"next", fx.Ticket, "--topology"}},
		{"status", map[string]any{"ticket": fx.Ticket}, []string{"status", fx.Ticket}},
	} {
		want := runJig(t, env, fx.RepoDir, tc.cli...)
		got := c.tool(tc.tool, tc.args)
		if got.IsError != (want.Code != 0) || got.Text != want.Stdout {
			t.Errorf("tool %s %v: isError %v, text:\n%s\nwant what `jig %s` prints (exit %d):\n%s", tc.tool, tc.args, got.IsError, got.Text, strings.Join(tc.cli, " "), want.Code, want.Stdout)
		}
	}
	if got := c.tool("context", map[string]any{"ticket": fx.Ticket, "slice": "b"}); got.IsError || !strings.Contains(got.Text, "context:\n") || !strings.Contains(got.Text, "  slice: b\n") {
		t.Errorf("context b = %+v, want the context of slice b", got)
	}

	// claim: a slice is taken with the session named, and let go again.
	got := c.tool("claim", map[string]any{"ticket": fx.Ticket, "slice": "b", "session": "s-1"})
	if got.IsError || !strings.Contains(got.Text, "  result: claimed\n") || !strings.Contains(got.Text, "  session: s-1\n") {
		t.Errorf("claim = %+v, want b claimed by s-1", got)
	}
	if ss, _ := st.ReadSliceState(fx.Ticket, "b"); ss.State != "building" {
		t.Errorf("after the claim slice b is %s, want building", ss.State)
	}
	got = c.tool("claim", map[string]any{"ticket": fx.Ticket, "slice": "b", "session": "s-2"})
	if !got.IsError || !strings.Contains(got.Text, "code: SLICE_HELD") {
		t.Errorf("a second session's claim = %+v, want a failed tool with SLICE_HELD", got)
	}
	got = c.tool("claim", map[string]any{"ticket": fx.Ticket, "slice": "b", "session": "s-1", "release": true})
	if got.IsError || !strings.Contains(got.Text, "  result: released\n") {
		t.Errorf("claim release = %+v, want released", got)
	}

	// note: the text may begin with a dash, and lands in the journal of the slice.
	got = c.tool("note", map[string]any{"ticket": fx.Ticket, "slice": "b", "session": "s-1", "text": "--careful with the second table"})
	if got.IsError || !strings.Contains(got.Text, "noted:\n") {
		t.Errorf("note = %+v, want noted", got)
	}
	var note journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventNote {
			note = l
		}
	}
	if note.Slice != "b" || note.Session != "s-1" || note.Provenance != "agent" || note.Summary != "--careful with the second table" {
		t.Errorf("note line = %+v, want the note on slice b from s-1 as an agent", note)
	}

	// observe: an event with no harness payload reaches the machine ledger.
	got = c.tool("observe", map[string]any{"event": "session-start", "session": "s-mcp", "cwd": fx.RepoDir})
	if got.IsError {
		t.Errorf("observe = %+v, want no error", got)
	}
	var started bool
	for _, l := range readLedger(t, env) {
		if l.Event == journal.EventSessionStart && l.Session == "s-mcp" {
			started = true
		}
	}
	if !started {
		t.Errorf("observe session-start left no session-start line for s-mcp in the ledger")
	}

	// A bad call is a failed tool; an unknown tool is a protocol error; the
	// server answers on.
	if bad := c.tool("note", map[string]any{"ticket": fx.Ticket}); !bad.IsError || !strings.Contains(bad.Text, `"text"`) {
		t.Errorf("note without text = %+v, want a failed tool naming text", bad)
	}
	if m := c.call("tools/call", map[string]any{"name": "nope"}); m.Error == nil || m.Error.Code != -32602 {
		t.Errorf("an unknown tool = %+v, want the invalid params error", m)
	}
	if m := c.call("ping", nil); m.Error != nil {
		t.Errorf("ping = %+v", m.Error)
	}
	c.stop()
}

// TestMCPHoldsEveryToolToTheStoreItWasGiven: started in a directory that is in
// no project, the server works through --store, and through --project for the
// store that name maps to, including for the commands that take only one of
// the two flags.
func TestMCPHoldsEveryToolToTheStoreItWasGiven(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	elsewhere := t.TempDir()

	// Nothing pins the directory: the tools find no store.
	c := startMCP(t, env, elsewhere, map[string]any{})
	if got := c.tool("status", map[string]any{"ticket": fx.Ticket}); !got.IsError || !strings.Contains(got.Text, "no jig store found") {
		t.Errorf("status outside a project = %+v, want no store found", got)
	}
	c.stop()

	for name, args := range map[string][]string{
		"--store":   {"--store", fx.StoreDir},
		"--project": {"--project", "fixture"},
	} {
		c := startMCP(t, env, elsewhere, map[string]any{}, args...)
		for _, tool := range []string{"status", "next", "here"} { // here takes --store only
			got := c.tool(tool, map[string]any{"ticket": fx.Ticket})
			if got.IsError || !strings.Contains(got.Text, fx.Ticket) {
				t.Errorf("mcp %s: tool %s = %+v, want it to work on the store of the fixture", name, tool, got)
			}
		}
		c.stop()
	}

	c = startMCP(t, env, elsewhere, map[string]any{}, "--project", "no-such-project")
	if got := c.tool("here", map[string]any{}); !got.IsError || !strings.Contains(got.Text, `no project "no-such-project" in the machine mapping`) {
		t.Errorf("here with a project that is not mapped = %+v, want the mapping error", got)
	}
	c.stop()
}
