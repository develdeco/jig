package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// The JSON-RPC 2.0 error codes the server uses.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// RPCError is a JSON-RPC error object. A call the server makes to the client
// that the client answers with one comes back as an *RPCError.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

// errClosed is what a call to the client returns once the client has hung up.
var errClosed = errors.New("the client closed the connection")

// message is any JSON-RPC message that arrives: a request (method and id), a
// notification (method, no id) or a response (id and result or error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type messageKind int

const (
	kindInvalid messageKind = iota
	kindRequest
	kindNotification
	kindResponse
)

// kind classifies m. An id is a string or a number, never null: MCP forbids a
// null id, so a request that has one is invalid.
func (m message) kind() messageKind {
	if m.JSONRPC != "2.0" {
		return kindInvalid
	}
	hasID := len(m.ID) > 0
	if hasID && !validID(m.ID) {
		return kindInvalid
	}
	switch {
	case m.Method != "" && hasID:
		return kindRequest
	case m.Method != "":
		return kindNotification
	case hasID && (m.Result != nil || m.Error != nil):
		return kindResponse
	}
	return kindInvalid
}

// validID reports whether raw is a JSON string or number.
func validID(raw json.RawMessage) bool {
	switch c := raw[0]; {
	case c == '"':
		return true
	case c == '-' || (c >= '0' && c <= '9'):
		return true
	}
	return false
}

// decodeMessage reads one line as a message. An error is a parse error when
// the line is no JSON at all and an invalid request when it is JSON that is
// no single message (a batch, a scalar); the code goes with it.
func decodeMessage(line []byte) (message, *RPCError) {
	line = bytes.TrimSpace(line)
	if !json.Valid(line) {
		return message{}, &RPCError{Code: codeParseError, Message: "the message is not valid JSON"}
	}
	if line[0] == '[' {
		return message{}, &RPCError{Code: codeInvalidRequest, Message: "batches are not supported: send one message per line"}
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return message{}, &RPCError{Code: codeInvalidRequest, Message: "the message is not a JSON-RPC object"}
	}
	return m, nil
}

// response is what the server writes back for a request.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// outRequest and outNotification are what the server sends the client.
type outRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type outNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// conn is the server's end of the stdio pipe: one line per message out, and
// the book of the calls it made to the client that wait for an answer. Writes
// are whole lines under a lock, so messages from several goroutines never
// interleave.
type conn struct {
	wmu sync.Mutex
	w   io.Writer

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan message
	closed  bool
	done    chan struct{}
}

func newConn(w io.Writer) *conn {
	return &conn{w: w, pending: map[string]chan message{}, done: make(chan struct{})}
}

// send writes v as one line. The encoder ends it with the newline and leaves
// <, > and & as they are, which keeps the text of a tool readable on the wire.
func (c *conn) send(v any) error {
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.w.Write(line.Bytes())
	return err
}

// reply answers request id with a result.
func (c *conn) reply(id json.RawMessage, result any) error {
	if result == nil {
		result = struct{}{}
	}
	return c.send(response{JSONRPC: "2.0", ID: id, Result: result})
}

// replyError answers request id with an error. A request whose id could not
// be read is answered with a null id, as JSON-RPC says.
func (c *conn) replyError(id json.RawMessage, e *RPCError) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return c.send(response{JSONRPC: "2.0", ID: id, Error: e})
}

// notify sends a notification to the client.
func (c *conn) notify(method string, params any) error {
	return c.send(outNotification{JSONRPC: "2.0", Method: method, Params: params})
}

// call sends a request to the client and waits for its answer. It returns
// the result, an *RPCError when the client answered with one, errClosed when
// the client hung up first, or ctx's error, in which case it tells the client
// to stop working on the request.
func (c *conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errClosed
	}
	c.nextID++
	id := c.nextID
	key := strconv.FormatInt(id, 10)
	ch := make(chan message, 1)
	c.pending[key] = ch
	c.mu.Unlock()

	forget := func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}
	if err := c.send(outRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		forget()
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		forget()
		_ = c.notify("notifications/cancelled", map[string]any{"requestId": id, "reason": "the request was cancelled"})
		return nil, ctx.Err()
	case <-c.done:
		forget()
		return nil, errClosed
	}
}

// deliver hands a response from the client to the call waiting for it. One
// nobody waits for (the call was cancelled) is dropped.
func (c *conn) deliver(m message) {
	key := string(m.ID)
	c.mu.Lock()
	ch, ok := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if ok {
		ch <- m
	}
}

// close fails every call still waiting and every later one: the client has
// hung up, so no answer will come.
func (c *conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
}
