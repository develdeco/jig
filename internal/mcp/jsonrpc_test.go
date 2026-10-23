package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDecodeMessage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		kind messageKind
		code int // the error code, 0 for none
	}{
		{"request", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, kindRequest, 0},
		{"request with a string id", `{"jsonrpc":"2.0","id":"a-1","method":"ping"}`, kindRequest, 0},
		{"notification", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, kindNotification, 0},
		{"result", `{"jsonrpc":"2.0","id":3,"result":{}}`, kindResponse, 0},
		{"error", `{"jsonrpc":"2.0","id":3,"error":{"code":-1,"message":"no"}}`, kindResponse, 0},
		{"null result", `{"jsonrpc":"2.0","id":3,"result":null}`, kindResponse, 0},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"ping"}`, kindInvalid, 0},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"ping"}`, kindInvalid, 0},
		{"another version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, kindInvalid, 0},
		{"no version", `{"id":1,"method":"ping"}`, kindInvalid, 0},
		{"nothing to say", `{"jsonrpc":"2.0","id":1}`, kindInvalid, 0},
		{"not json", `hello`, kindInvalid, codeParseError},
		{"truncated", `{"jsonrpc":"2.0","id":1,`, kindInvalid, codeParseError},
		{"a batch", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, kindInvalid, codeInvalidRequest},
		{"a scalar", `42`, kindInvalid, codeInvalidRequest},
		{"a string", `"ping"`, kindInvalid, codeInvalidRequest},
	} {
		m, rerr := decodeMessage([]byte(tc.line))
		switch {
		case tc.code != 0 && (rerr == nil || rerr.Code != tc.code):
			t.Errorf("%s: error = %+v, want code %d", tc.name, rerr, tc.code)
		case tc.code == 0 && rerr != nil:
			t.Errorf("%s: error = %+v, want none", tc.name, rerr)
		case rerr == nil && m.kind() != tc.kind:
			t.Errorf("%s: kind = %d, want %d", tc.name, m.kind(), tc.kind)
		}
	}
}

func TestReplyKeepsTheIDVerbatimAndAlwaysHasAResult(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	c := newConn(&out)
	if err := c.reply(json.RawMessage(`"abc"`), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.replyError(nil, &RPCError{Code: codeParseError, Message: "bad"}); err != nil {
		t.Fatal(err)
	}
	want := `{"jsonrpc":"2.0","id":"abc","result":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"bad"}}` + "\n"
	if out.String() != want {
		t.Errorf("wrote:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestSendLeavesAngleBracketsAndAmpersandsAlone(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	c := newConn(&out)
	if err := c.reply(json.RawMessage("1"), map[string]any{"text": "a<b && c>d\u2028"}); err != nil {
		t.Fatal(err)
	}
	line := out.String()
	if !strings.Contains(line, `"a<b && c>d\u2028"`) || strings.Count(line, "\n") != 1 {
		t.Errorf("wrote %q, want < > & as they are, U+2028 escaped, and one line", line)
	}
}

// lockedBuffer is a buffer several goroutines may write to, as stdout is.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func TestConnWritesWholeLinesFromManyGoroutines(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	c := newConn(&out)
	big := strings.Repeat("x", 100000)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := c.notify("notifications/test", map[string]any{"text": big}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(out.b.String(), "\n"), "\n")
	if len(lines) != 400 {
		t.Fatalf("%d lines, want 400", len(lines))
	}
	for i, l := range lines {
		var m message
		if err := json.Unmarshal([]byte(l), &m); err != nil || m.Method != "notifications/test" {
			t.Fatalf("line %d is not a whole message: %v", i, err)
		}
	}
}

// sentRequest reads the request the conn wrote to buf and returns its id.
func waitRequest(t *testing.T, out *lockedBuffer) message {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		out.mu.Lock()
		line, _, ok := strings.Cut(out.b.String(), "\n")
		out.mu.Unlock()
		if ok {
			var m message
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the conn wrote no request")
	return message{}
}

func TestCallWaitsForItsResponse(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	c := newConn(&out)
	type res struct {
		raw json.RawMessage
		err error
	}
	got := make(chan res, 1)
	go func() {
		raw, err := c.call(context.Background(), "elicitation/create", map[string]any{"message": "hi"})
		got <- res{raw, err}
	}()
	req := waitRequest(t, &out)
	if req.Method != "elicitation/create" || len(req.ID) == 0 {
		t.Fatalf("request = %+v", req)
	}
	// A response nobody waits for is dropped; the right one resolves the call.
	c.deliver(message{ID: json.RawMessage("999"), Result: json.RawMessage(`{"x":1}`)})
	c.deliver(message{ID: req.ID, Result: json.RawMessage(`{"action":"accept"}`)})
	r := <-got
	if r.err != nil || string(r.raw) != `{"action":"accept"}` {
		t.Errorf("call = %s, %v", r.raw, r.err)
	}
}

func TestCallReturnsAnErrorResponseAsAnError(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	c := newConn(&out)
	got := make(chan error, 1)
	go func() {
		_, err := c.call(context.Background(), "elicitation/create", nil)
		got <- err
	}()
	req := waitRequest(t, &out)
	c.deliver(message{ID: req.ID, Error: &RPCError{Code: codeMethodNotFound, Message: "no"}})
	var rerr *RPCError
	if err := <-got; !errors.As(err, &rerr) || rerr.Code != codeMethodNotFound {
		t.Errorf("call error = %v, want the client's error", err)
	}
}

func TestCallTellsTheClientWhenItIsCancelled(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	c := newConn(&out)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, "elicitation/create", nil)
		got <- err
	}()
	req := waitRequest(t, &out)
	cancel()
	if err := <-got; !errors.Is(err, context.Canceled) {
		t.Fatalf("call error = %v, want context canceled", err)
	}
	lines := strings.Split(strings.TrimSpace(out.b.String()), "\n")
	var note message
	if len(lines) != 2 || json.Unmarshal([]byte(lines[1]), &note) != nil || note.Method != "notifications/cancelled" {
		t.Fatalf("wrote %q, want the request and then a cancelled notification", lines)
	}
	var p struct{ RequestID json.RawMessage }
	if json.Unmarshal(note.Params, &p); string(p.RequestID) != string(req.ID) {
		t.Errorf("cancelled names request %s, want %s", p.RequestID, req.ID)
	}
	c.deliver(message{ID: req.ID, Result: json.RawMessage(`{}`)}) // late: dropped, no panic
}

func TestCallFailsOnceTheClientHasHungUp(t *testing.T) {
	t.Parallel()
	var out lockedBuffer
	c := newConn(&out)
	got := make(chan error, 1)
	go func() {
		_, err := c.call(context.Background(), "elicitation/create", nil)
		got <- err
	}()
	waitRequest(t, &out)
	c.close()
	c.close() // twice is fine
	if err := <-got; !errors.Is(err, errClosed) {
		t.Errorf("waiting call error = %v, want errClosed", err)
	}
	if _, err := c.call(context.Background(), "elicitation/create", nil); !errors.Is(err, errClosed) {
		t.Errorf("later call error = %v, want errClosed", err)
	}
}
