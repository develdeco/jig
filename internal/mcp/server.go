package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// protocolVersions are the revisions of the Model Context Protocol the server
// speaks, newest first. A client that asks for one of them gets it; any other
// request is answered with the newest, and the client decides whether it can
// go on from there.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// maxMessage bounds one line of input: a message larger than this ends the
// session rather than growing without limit.
const maxMessage = 16 << 20

// Info describes the server to the client.
type Info struct {
	Name    string
	Version string
	// Instructions is how a model should use the tools, sent once at
	// initialize.
	Instructions string
}

// Server serves a set of tools to one MCP client over a pair of streams.
type Server struct {
	maxMessage int // 0: the default, maxMessage
	info       Info
	tools      []Tool
}

// NewServer returns a server that offers tools.
func NewServer(info Info, tools []Tool) *Server {
	return &Server{info: info, tools: tools}
}

// Client is what a tool handler may ask of the client that called it.
type Client interface {
	// CanElicit reports whether the client declared that it can put a
	// question to the person with a form.
	CanElicit() bool
	// Elicit shows the person message and a form for the fields schema
	// describes, and waits for what they do. It fails when the client cannot
	// or will not take the request, and with ctx's error when the request is
	// cancelled or the server is shutting down.
	Elicit(ctx context.Context, message string, schema map[string]any) (ElicitResult, error)
}

// ElicitResult is the person's answer to an elicitation: accept (Content
// holds the form), decline, or cancel.
type ElicitResult struct {
	Action  string
	Content map[string]any
}

// Serve reads newline-delimited JSON-RPC messages from in and writes the
// replies to out until in ends, ctx is cancelled, or the stream breaks. A
// request runs on a goroutine of its own, so a tool that asks the person a
// question does not stop the server reading the answer. When in ends, the
// requests already running finish and are answered before Serve returns; the
// calls they have out to the client fail, since no answer can come. When ctx
// ends, the running requests are cancelled. Serve returns nil when in ends
// and ctx's error when ctx does.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ss := &session{srv: s, c: newConn(out), inflight: map[string]context.CancelFunc{}}

	lines := make(chan readResult)
	stopReader := make(chan struct{})
	defer close(stopReader)
	go readLines(in, lines, stopReader, s.messageLimit())

	var wg sync.WaitGroup
	defer func() {
		ss.c.close()
		wg.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-lines:
			if r.err != nil {
				return r.err
			}
			if r.eof {
				return nil
			}
			m, rerr := decodeMessage(r.line)
			if rerr != nil {
				_ = ss.c.replyError(nil, rerr)
				continue
			}
			switch m.kind() {
			case kindResponse:
				ss.c.deliver(m)
			case kindNotification:
				ss.notification(m)
			case kindRequest:
				if m.Method != "tools/call" { // quick, and in order: initialize must be seen by the rest
					ss.handle(ctx, m)
					continue
				}
				rctx := ss.begin(ctx, m)
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer ss.end(m)
					ss.handle(rctx, m)
				}()
			default:
				_ = ss.c.replyError(m.ID, &RPCError{Code: codeInvalidRequest, Message: "not a JSON-RPC 2.0 request"})
			}
		}
	}
}

// readResult is a line of input, or the end of it.
type readResult struct {
	line []byte
	eof  bool
	err  error
}

// readLines sends each non-blank line of in, then the end of it. It gives up
// when stop is closed, so a Serve that returned early does not leave it
// blocked on a send.
func readLines(in io.Reader, out chan<- readResult, stop <-chan struct{}, limit int) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, min(64<<10, limit)), limit)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		select {
		case out <- readResult{line: bytes.Clone(line)}:
		case <-stop:
			return
		}
	}
	r := readResult{eof: true}
	if err := sc.Err(); err != nil {
		r = readResult{err: fmt.Errorf("mcp: reading the client: %w", err)}
	}
	select {
	case out <- r:
	case <-stop:
	}
}

// session is one client connection: what it declared at initialize and the
// requests of it that are running.
type session struct {
	srv *Server
	c   *conn

	mu       sync.Mutex
	elicits  bool
	inflight map[string]context.CancelFunc // by request id
}

// begin registers request m as running and returns the context it runs in,
// which a cancelled notification for it ends.
func (s *session) begin(parent context.Context, m message) context.Context {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.inflight[string(m.ID)] = cancel
	s.mu.Unlock()
	return ctx
}

func (s *session) end(m message) {
	s.mu.Lock()
	cancel := s.inflight[string(m.ID)]
	delete(s.inflight, string(m.ID))
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// notification acts on the notifications that matter: the client cancelling a
// request. The rest (initialized, progress, a changed roots list) need
// nothing.
func (s *session) notification(m message) {
	if m.Method != "notifications/cancelled" {
		return
	}
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(m.Params, &p) != nil || len(p.RequestID) == 0 {
		return
	}
	s.mu.Lock()
	cancel := s.inflight[string(p.RequestID)]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// handle runs request m and writes its reply. A request that was cancelled
// gets none, as the protocol says; a handler that panics costs only its own
// request.
func (s *session) handle(ctx context.Context, m message) {
	result, rerr := s.dispatch(ctx, m)
	if ctx.Err() != nil {
		return
	}
	if rerr != nil {
		_ = s.c.replyError(m.ID, rerr)
		return
	}
	_ = s.c.reply(m.ID, result)
}

func (s *session) dispatch(ctx context.Context, m message) (result any, rerr *RPCError) {
	defer func() {
		if r := recover(); r != nil {
			result, rerr = nil, &RPCError{Code: codeInternalError, Message: fmt.Sprintf("internal error: %v", r)}
		}
	}()
	switch m.Method {
	case "initialize":
		return s.initialize(m.Params)
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return s.listTools(), nil
	case "tools/call":
		return s.callTool(ctx, m.Params)
	}
	return nil, &RPCError{Code: codeMethodNotFound, Message: fmt.Sprintf("method not found: %s", m.Method)}
}

func (s *session) initialize(params json.RawMessage) (any, *RPCError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Elicitation map[string]json.RawMessage `json:"elicitation"`
		} `json:"capabilities"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &RPCError{Code: codeInvalidParams, Message: "initialize: params are not an object with a protocolVersion and capabilities"}
		}
	}
	version := protocolVersions[0]
	if slices.Contains(protocolVersions, p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	// An empty elicitation capability means forms (the revision that added
	// elicitation had no other kind); a client that lists kinds must list
	// form for the server to ask it one.
	elicitation := p.Capabilities.Elicitation
	_, form := elicitation["form"]
	s.mu.Lock()
	s.elicits = elicitation != nil && (len(elicitation) == 0 || form)
	s.mu.Unlock()

	res := map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": s.srv.info.Name, "version": s.srv.info.Version},
	}
	if s.srv.info.Instructions != "" {
		res["instructions"] = s.srv.info.Instructions
	}
	return res, nil
}

func (s *session) listTools() any {
	tools := make([]map[string]any, len(s.srv.tools))
	for i, t := range s.srv.tools {
		tools[i] = t.describe()
	}
	return map[string]any{"tools": tools}
}

func (s *session) callTool(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, &RPCError{Code: codeInvalidParams, Message: "tools/call: params need a tool name"}
	}
	i := slices.IndexFunc(s.srv.tools, func(t Tool) bool { return t.Name == p.Name })
	if i < 0 {
		return nil, &RPCError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", p.Name)}
	}
	t := s.srv.tools[i]
	args, err := t.decode(p.Arguments)
	if err != nil {
		return textResult(err.Error(), true), nil
	}
	res, err := t.Handle(ctx, s, args)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &RPCError{Code: codeInternalError, Message: ctx.Err().Error()}
		}
		return textResult(err.Error(), true), nil
	}
	return textResult(res.Text, res.IsError), nil
}

// Result is what a tool hands back: text for the model, and whether the tool
// failed (the model sees a failed tool text, so it can correct itself).
type Result struct {
	Text    string
	IsError bool
}

func textResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// CanElicit implements Client.
func (s *session) CanElicit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.elicits
}

// Elicit implements Client.
func (s *session) Elicit(ctx context.Context, message string, schema map[string]any) (ElicitResult, error) {
	raw, err := s.c.call(ctx, "elicitation/create", map[string]any{"message": message, "requestedSchema": schema})
	if err != nil {
		return ElicitResult{}, err
	}
	var r struct {
		Action  string         `json:"action"`
		Content map[string]any `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return ElicitResult{}, errors.New("the answer of the client to the elicitation is not an object with an action")
	}
	return ElicitResult{Action: r.Action, Content: r.Content}, nil
}

// messageLimit is the longest line of input Serve accepts.
func (s *Server) messageLimit() int {
	if s.maxMessage > 0 {
		return s.maxMessage
	}
	return maxMessage
}
