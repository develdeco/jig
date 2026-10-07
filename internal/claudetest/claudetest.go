// Package claudetest runs the real Claude Code CLI against a scripted
// stand-in for the Anthropic Messages API, so a test drives a real session
// - the CLI, its permission system, and jig's screen hook - with no
// credentials, no model and no network. Only the model's replies are
// scripted: each one is the next tool call of a Session.
package claudetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ToolCall is one tool_use block the scripted model emits.
type ToolCall struct {
	Name  string
	Input map[string]any
}

// ToolResult is one tool_result block the CLI sent back.
type ToolResult struct {
	Content string
	IsError bool
}

// Step is one scripted tool call: Call builds it from the results of the
// steps before it, and its result must contain WantOut, or be an error
// containing WantErr, or be refused (WantDenied).
type Step struct {
	Name string
	Call func(prior []ToolResult) ToolCall
	// WantOut and WantErr match text jig itself owns: the session's own
	// output, and the screen's deny reason. WantDenied is for a refusal the
	// CLI words, where only the structured flag is jig's to rely on.
	WantOut    string
	WantErr    string
	WantDenied bool
}

// Tool returns a Step.Call that always makes the same tool call.
func Tool(name string, input map[string]any) func([]ToolResult) ToolCall {
	return func([]ToolResult) ToolCall { return ToolCall{name, input} }
}

// Bash returns a Step.Call that runs command in the session's shell.
func Bash(command string) func([]ToolResult) ToolCall {
	return Tool("Bash", map[string]any{"command": command, "description": "jig contract step"})
}

// Write returns a Step.Call that writes content to path.
func Write(path, content string) func([]ToolResult) ToolCall {
	return Tool("Write", map[string]any{"file_path": path, "content": content})
}

// Session is one scripted session: the tool calls its model makes, in
// order.
type Session struct {
	Steps []Step

	mu      sync.Mutex
	results []ToolResult      // the longest conversation's tool results
	tools   []json.RawMessage // the tool schemas the CLI offered
}

// ToolSchema returns the input schema the CLI offered for the tool named
// name in the session's requests, decoded, or nil when it offered none.
func (s *Session) ToolSchema(name string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, raw := range s.tools {
		var tool struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"input_schema"`
		}
		if json.Unmarshal(raw, &tool) == nil && tool.Name == name {
			return tool.InputSchema
		}
	}
	return nil
}

// Results returns the tool results the CLI sent back in the session's
// longest conversation: one per step that ran.
func (s *Session) Results() []ToolResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.results
}

func (s *Session) record(results []ToolResult, tools []json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(results) >= len(s.results) {
		s.results = results
	}
	if len(tools) > 0 {
		s.tools = tools
	}
}

// Check fails t unless every step ran with the outcome it wants.
func (s *Session) Check(t testing.TB) {
	t.Helper()
	results := s.Results()
	for i, step := range s.Steps {
		if i >= len(results) {
			t.Errorf("step %d (%s) never ran: the session made %d of %d scripted tool calls: %+v", i, step.Name, len(results), len(s.Steps), results)
			return
		}
		r := results[i]
		switch {
		case step.WantDenied:
			// The permission system's own refusal text is the CLI's, and
			// it reads differently per mode and version, so the assertion
			// is the structured flag, plus whatever the caller checks on
			// disk.
			if !r.IsError {
				t.Errorf("step %d (%s): got no error, want the call refused", i, step.Name)
			}
		case step.WantErr != "":
			if !r.IsError || !strings.Contains(r.Content, step.WantErr) {
				t.Errorf("step %d (%s): got error=%v %q, want an error containing %q", i, step.Name, r.IsError, r.Content, step.WantErr)
			}
		case r.IsError:
			t.Errorf("step %d (%s): unexpected error %q", i, step.Name, r.Content)
		case !strings.Contains(r.Content, step.WantOut):
			t.Errorf("step %d (%s): output %q does not contain %q", i, step.Name, r.Content, step.WantOut)
		}
	}
}

// API is a minimal Anthropic Messages API that serves scripted sessions.
// Route names the session a conversation belongs to, from its prompt (the
// text of its first message); it is asked once per prompt, and nil means
// the conversation is not a scripted one. A request that offers tools gets
// its session's next step, chosen by how many tool results the
// conversation already holds; any other request - no tools, no session, or
// past the end of the script - gets a final text message. A response
// streams as server-sent events when the request asks for a stream, as the
// CLI's do. Route runs on the server's goroutine, so it reports a problem
// with t.Errorf, never t.Fatal.
type API struct {
	Route func(prompt string) *Session

	mu       sync.Mutex
	sessions map[string]*Session
	seq      atomic.Int64
}

// session returns the session for prompt, asking Route the first time.
func (a *API) session(prompt string) *Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[prompt]; ok {
		return s
	}
	if a.sessions == nil {
		a.sessions = map[string]*Session{}
	}
	s := a.Route(prompt)
	a.sessions[prompt] = s
	return s
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/v1/messages") {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Stream   bool              `json:"stream"`
		Tools    []json.RawMessage `json:"tools"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var results []ToolResult
	for _, msg := range req.Messages {
		results = append(results, toolResultsOf(msg.Content)...)
	}

	block := map[string]any{"type": "text", "text": "done"}
	stop := "end_turn"
	if len(req.Tools) > 0 && len(req.Messages) > 0 {
		if s := a.session(textOf(req.Messages[0].Content)); s != nil {
			s.record(results, req.Tools)
			if n := len(results); n < len(s.Steps) {
				call := s.Steps[n].Call(results)
				block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_jig_%02d", n), "name": call.Name, "input": call.Input}
				stop = "tool_use"
			}
		}
	}
	id := fmt.Sprintf("msg_jig_%d", a.seq.Add(1))
	usage := map[string]int{"input_tokens": 1, "output_tokens": 1}

	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": "mock",
			"content": []any{block}, "stop_reason": stop, "usage": usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	event := func(name string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	}
	event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": "mock",
		"content": []any{}, "stop_reason": nil, "usage": usage,
	}})
	if block["type"] == "tool_use" {
		input, _ := json.Marshal(block["input"])
		event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
			"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{},
		}})
		event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
			"type": "input_json_delta", "partial_json": string(input),
		}})
	} else {
		event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": block["text"]}})
	}
	event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 1}})
	event("message_stop", map[string]any{"type": "message_stop"})
}

// textOf returns a message content's text: the content itself when it is a
// plain string, else its text blocks joined by newlines.
func textOf(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(content, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolResultsOf extracts the tool_result blocks of one message's content,
// which is either a plain string (no blocks) or an array of blocks whose
// own content is a string or an array of text blocks.
func toolResultsOf(content json.RawMessage) []ToolResult {
	var blocks []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"is_error"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []ToolResult
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		var text string
		if json.Unmarshal(b.Content, &text) != nil {
			var parts []struct {
				Text string `json:"text"`
			}
			json.Unmarshal(b.Content, &parts)
			for _, p := range parts {
				text += p.Text
			}
		}
		out = append(out, ToolResult{Content: text, IsError: b.IsError})
	}
	return out
}

// Serve starts api on loopback for the rest of t and points the claude CLI
// at it through the environment, with t.Setenv so every process t starts
// inherits it: the API's URL, a dummy key, a throwaway config dir, and no
// auto-updater or nonessential traffic, so no real account or setting is
// involved.
func Serve(t testing.TB, api *API) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "jig-contract-test-not-a-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	t.Setenv("DISABLE_AUTOUPDATER", "1")
}
