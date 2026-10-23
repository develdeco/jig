package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// wait bounds every wait of these tests; a hang fails the test instead of
// the run.
const wait = 10 * time.Second

// rpc is a message the server wrote.
type rpc struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// wire is a server over a pair of pipes, and the client end of them.
type wire struct {
	t      *testing.T
	in     *io.PipeWriter
	msgs   chan rpc
	done   chan error
	cancel context.CancelFunc
	nextID int
}

// serve starts a server for tools and returns the client end. The server is
// stopped, and its output must be read to the end, when the test ends.
func serve(t *testing.T, tools []Tool) *wire {
	return serveServer(t, NewServer(Info{Name: "test", Version: "1.2.3"}, tools))
}

func serveServer(t *testing.T, srv *Server) *wire {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	w := &wire{t: t, in: inW, msgs: make(chan rpc, 64), done: make(chan error, 1), cancel: cancel}
	go func() {
		err := srv.Serve(ctx, inR, outW)
		outW.Close()
		w.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var m rpc
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("the server wrote a line that is no message: %q", sc.Text())
				continue
			}
			w.msgs <- m
		}
		close(w.msgs)
	}()
	t.Cleanup(func() {
		cancel()
		inW.Close()
	})
	return w
}

// send writes one message to the server.
func (w *wire) send(v any) {
	w.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		w.t.Fatal(err)
	}
	w.sendRaw(string(b))
}

func (w *wire) sendRaw(line string) {
	w.t.Helper()
	if _, err := io.WriteString(w.in, line+"\n"); err != nil {
		w.t.Fatalf("writing to the server: %v", err)
	}
}

// request sends a request and returns its id.
func (w *wire) request(method string, params any) int {
	w.t.Helper()
	w.nextID++
	w.send(map[string]any{"jsonrpc": "2.0", "id": w.nextID, "method": method, "params": params})
	return w.nextID
}

// recv returns the next message of the server.
func (w *wire) recv() rpc {
	w.t.Helper()
	select {
	case m, ok := <-w.msgs:
		if !ok {
			w.t.Fatal("the server ended its output while the test waited for a message")
		}
		return m
	case <-time.After(wait):
		w.t.Fatal("the server wrote nothing in time")
	}
	return rpc{}
}

// response returns the response to request id; any other message fails.
func (w *wire) response(id int) rpc {
	w.t.Helper()
	m := w.recv()
	if m.Method != "" || string(m.ID) != itoa(id) {
		w.t.Fatalf("waiting for the response to %d, the server sent %s %s (id %s)", id, m.Method, m.Params, m.ID)
	}
	return m
}

func (w *wire) call(method string, params any) rpc {
	w.t.Helper()
	return w.response(w.request(method, params))
}

// initialize runs the handshake with the client capabilities given.
func (w *wire) initialize(caps map[string]any) rpc {
	w.t.Helper()
	m := w.call("initialize", map[string]any{"protocolVersion": protocolVersions[0], "capabilities": caps, "clientInfo": map[string]any{"name": "t", "version": "0"}})
	w.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return m
}

// callTool calls a tool and returns its text and whether it failed.
func (w *wire) callTool(name string, args map[string]any) (string, bool) {
	w.t.Helper()
	return toolText(w.t, w.call("tools/call", map[string]any{"name": name, "arguments": args}))
}

func toolText(t *testing.T, m rpc) (string, bool) {
	t.Helper()
	if m.Error != nil {
		t.Fatalf("tools/call answered with the protocol error %+v", m.Error)
	}
	var r struct {
		Content []struct{ Type, Text string }
		IsError bool
	}
	if err := json.Unmarshal(m.Result, &r); err != nil || len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatalf("tools/call result = %s (%v), want one text content", m.Result, err)
	}
	return r.Content[0].Text, r.IsError
}

// finish closes the client input, waits for Serve to return, and returns what
// it returned and every message it wrote after the test last read.
func (w *wire) finish() ([]rpc, error) {
	w.t.Helper()
	w.in.Close()
	var err error
	select {
	case err = <-w.done:
	case <-time.After(wait):
		w.t.Fatal("Serve did not return after its input closed")
	}
	var rest []rpc
	for m := range w.msgs {
		rest = append(rest, m)
	}
	return rest, err
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// fakeRunner is a Runner that records the command lines it is given and
// answers from a function.
type fakeRunner struct {
	mu     sync.Mutex
	calls  [][]string
	stdins []string
	answer func(argv []string) (string, int)
}

func (f *fakeRunner) run(ctx context.Context, argv []string, stdin string) (string, int) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), argv...))
	f.stdins = append(f.stdins, stdin)
	f.mu.Unlock()
	if f.answer != nil {
		return f.answer(argv)
	}
	return "ran " + strings.Join(argv, " ") + "\n", 0
}

func (f *fakeRunner) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = strings.Join(c, " ")
	}
	return out
}

// toolNamed returns the tool of that name from a Tools list.
func toolNamed(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("no tool named %s", name)
	return Tool{}
}

func jsonRaw(v any) ([]byte, error) { return json.Marshal(v) }
