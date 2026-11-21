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
