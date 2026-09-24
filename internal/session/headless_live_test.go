package session

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/outcome"
)

// TestHeadlessLiveCLI is the headless backend's contract test against the
// real Claude Code CLI: the local `claude` binary runs a scripted session
// through jig's generated settings, the CLI's own permission system, and
// the real `jig _screen` hook, with a mock Messages API on loopback
// standing in for the model - no credentials and no network. It is opt-in,
// since its result depends on whichever CLI version is installed:
//
//	JIG_LIVE_CLAUDE=1 go test ./internal/session -run Live
//
// Each scripted tool call probes one grant or one denial of the permission
// model (docs/adr/0008-headless-permission-model.md), and the session ends
// by writing result.json exactly as a real one must.
func TestHeadlessLiveCLI(t *testing.T) {
	if os.Getenv("JIG_LIVE_CLAUDE") == "" {
		t.Skip("set JIG_LIVE_CLAUDE=1 to run the headless contract test against the local claude CLI")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatalf("JIG_LIVE_CLAUDE is set but claude is not on PATH: %v", err)
	}
	jig := filepath.Join(buildBinary(t, filepath.Join("cmd", "jig"), "jig"), "jig")
	if runtime.GOOS == "windows" {
		jig += ".exe"
	}

	t.Run("build", func(t *testing.T) { liveBuildSession(t, jig) })
	t.Run("reviewer", func(t *testing.T) { liveReviewerSession(t, jig) })
}

// liveBuildSession runs a build-shaped dispatch: the screen denies a push,
// reads outside the lease are granted, edits are granted in the lease and
// on the result file only, and a commit lands with nothing but the
// session's own change in it.
func liveBuildSession(t *testing.T, jig string) {
	root := t.TempDir()
	worktree := filepath.Join(root, "lease")
	work := filepath.Join(root, "store", "T-1", "work")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{worktree, work, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	liveRepo(t, worktree)

	d := Dispatch{
		Ticket:     "T-1",
		Slice:      "a",
		Attempt:    1,
		Worktree:   worktree,
		SliceJSON:  filepath.Join(work, "a.attempt-1.slice.json"),
		ResultJSON: filepath.Join(work, "a.attempt-1.result.json"),
		Model:      "claude-haiku-4-5",
		Prompt:     "You are a jig build session for slice a of ticket T-1.",
		Screen:     true,
	}
	if err := os.WriteFile(d.SliceJSON, []byte(`{"id":"a","goal":"say hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(work, "a.attempt-1.other.json")
	outsideFile := filepath.Join(outside, "x.txt")

	api := &mockMessagesAPI{steps: []mockStep{
		{name: "screen denies a push", call: bash("git push origin HEAD"), wantErr: "Blocked `git push`"},
		{name: "read slice.json outside the lease", call: fixed("Read", map[string]any{"file_path": d.SliceJSON}), wantOut: `"goal":"say hello"`},
		{name: "write outside the lease", call: write(outsideFile, "x"), wantDenied: true},
		{name: "write in the lease", call: write(filepath.Join(worktree, "hello.txt"), "hello\n")},
		{name: "write a sibling of result.json", call: write(sibling, "x"), wantDenied: true},
		{name: "commit", call: bash("git add -A && git -c user.name=jig-test -c user.email=test@example.invalid commit -q -m hello && git rev-parse HEAD")},
		{name: "write result.json", call: func(prior []mockToolResult) mockToolCall {
			sha := regexp.MustCompile(`[0-9a-f]{40}`).FindString(prior[5].Content)
			return mockToolCall{"Write", map[string]any{"file_path": d.ResultJSON, "content": `{"outcome":"green","summary":"live contract","commit":"` + sha + `"}`}}
		}},
	}}
	runLive(t, jig, api, d)

	for _, path := range []string{outsideFile, sibling} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists after a denied write (stat: %v)", path, err)
		}
	}
	data, err := os.ReadFile(d.ResultJSON)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	res := outcome.ParseJSON("slice", data)
	head, err := gitx.RevParse(worktree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != outcome.Green || res.Commit != head {
		t.Errorf("result = %+v, want green at the lease HEAD %s", res, head)
	}
	files, err := gitx.Run(worktree, "show", "--name-only", "--format=", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(files) != "hello.txt" {
		t.Errorf("the session's commit holds %q, want only hello.txt: no dispatch plumbing belongs in the lease", files)
	}
	if status, err := gitx.Run(worktree, "status", "--porcelain"); err != nil || status != "" {
		t.Errorf("lease not clean after the session: %q (%v)", status, err)
	}
}

// liveReviewerSession runs a gate-reviewer-shaped dispatch (Slice "gate",
// review.json as its input): it reads review.json from the store, reads the
// diff through git, and writes its result.json.
func liveReviewerSession(t *testing.T, jig string) {
	root := t.TempDir()
	worktree := filepath.Join(root, "gate-lease")
	work := filepath.Join(root, "store", "T-1", "work")
	for _, dir := range []string{worktree, work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := liveRepo(t, worktree)
	if err := os.WriteFile(filepath.Join(worktree, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(worktree, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(worktree, liveIdentity, "commit", "-q", "-m", "change"); err != nil {
		t.Fatal(err)
	}
	head, err := gitx.RevParse(worktree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	d := Dispatch{
		Ticket:     "T-1",
		Slice:      "gate",
		Attempt:    1,
		Worktree:   worktree,
		SliceJSON:  filepath.Join(work, "gate.round-1.review.json"),
		ResultJSON: filepath.Join(work, "gate.round-1.result.json"),
		Model:      "claude-haiku-4-5",
		Prompt:     "You are a jig gate reviewer for round 1 of ticket T-1.",
		Screen:     true,
	}
	if err := os.WriteFile(d.SliceJSON, []byte(`{"ticket":"T-1","round":1,"scope":"full"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	review := `{"verdict":"clean","findings":[],"closures":[],"summary":"live contract"}`

	api := &mockMessagesAPI{steps: []mockStep{
		{name: "read review.json", call: fixed("Read", map[string]any{"file_path": d.SliceJSON}), wantOut: `"scope":"full"`},
		{name: "read the diff", call: bash("git diff --stat " + base + ".." + head), wantOut: "a.go"},
		{name: "write result.json", call: write(d.ResultJSON, review)},
	}}
	runLive(t, jig, api, d)

	if got, err := os.ReadFile(d.ResultJSON); err != nil || string(got) != review {
		t.Errorf("result.json = %q (%v), want %q", got, err, review)
	}
	if after, err := gitx.RevParse(worktree, "HEAD"); err != nil || after != head {
		t.Errorf("gate lease HEAD moved to %s (%v), want %s", after, err, head)
	}
}

// liveIdentity pins the identity of the commits the test itself makes.
var liveIdentity = []string{
	"GIT_AUTHOR_NAME=jig-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
	"GIT_COMMITTER_NAME=jig-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
}

// liveRepo initializes dir as a git repo with one empty commit and returns
// that commit's sha.
func liveRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := gitx.Run(dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(dir, liveIdentity, "commit", "-q", "--allow-empty", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	sha, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// runLive points the CLI at api (with a throwaway config dir and a dummy
// key, so no real account or setting is involved), runs d through the
// headless backend with jig as the screen hook, and checks every scripted
// step ran with the expected outcome.
func runLive(t *testing.T, jig string, api *mockMessagesAPI, d Dispatch) {
	t.Helper()
	srv := httptest.NewServer(api)
	defer srv.Close()
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "jig-contract-test-not-a-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	t.Setenv("DISABLE_AUTOUPDATER", "1")

	backend, err := New("headless", Options{ScreenBinary: jig})
	if err != nil {
		t.Fatalf("New(headless): %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- backend.Run(d) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Minute):
		t.Fatal("claude did not finish within 5 minutes")
	}

	results := api.lastResults()
	if len(results) != len(api.steps) {
		t.Fatalf("the session ran %d of %d scripted tool calls: %+v", len(results), len(api.steps), results)
	}
	for i, step := range api.steps {
		r := results[i]
		switch {
		case step.wantDenied:
			// The permission system's own refusal text is the CLI's, and
			// it reads differently per mode and version, so the assertion
			// is the structured flag plus the file staying absent, which
			// the caller checks.
			if !r.IsError {
				t.Errorf("step %d (%s): got no error, want the call refused", i, step.name)
			}
		case step.wantErr != "":
			if !r.IsError || !strings.Contains(r.Content, step.wantErr) {
				t.Errorf("step %d (%s): got error=%v %q, want an error containing %q", i, step.name, r.IsError, r.Content, step.wantErr)
			}
		case r.IsError:
			t.Errorf("step %d (%s): unexpected error %q", i, step.name, r.Content)
		case !strings.Contains(r.Content, step.wantOut):
			t.Errorf("step %d (%s): output %q does not contain %q", i, step.name, r.Content, step.wantOut)
		}
	}
}

// mockToolCall is one tool_use block the mock model emits.
type mockToolCall struct {
	Name  string
	Input map[string]any
}

// mockToolResult is one tool_result block the CLI sent back.
type mockToolResult struct {
	Content string
	IsError bool
}

// mockStep is one scripted tool call: call builds it from the results of
// the steps before it, and the result must contain wantOut, or be an error
// containing wantErr.
type mockStep struct {
	name string
	call func(prior []mockToolResult) mockToolCall
	// wantOut and wantErr match text jig itself owns: the session's own
	// output, and the screen's deny reason. wantDenied is for a refusal the
	// CLI words, where only the structured flag is jig's to rely on.
	wantOut    string
	wantErr    string
	wantDenied bool
}

func fixed(name string, input map[string]any) func([]mockToolResult) mockToolCall {
	return func([]mockToolResult) mockToolCall { return mockToolCall{name, input} }
}

func bash(command string) func([]mockToolResult) mockToolCall {
	return fixed("Bash", map[string]any{"command": command, "description": "jig contract step"})
}

func write(path, content string) func([]mockToolResult) mockToolCall {
	return fixed("Write", map[string]any{"file_path": path, "content": content})
}

// mockMessagesAPI is a minimal Anthropic Messages API. A request that
// offers tools gets the next scripted tool call, chosen by how many tool
// results its conversation already holds; any other request, or one past
// the script, gets a final text message. Responses stream as server-sent
// events when the request asks for a stream, as the CLI does.
type mockMessagesAPI struct {
	steps []mockStep

	mu      sync.Mutex
	results []mockToolResult // the longest conversation's tool results
	seq     atomic.Int64
}

func (m *mockMessagesAPI) lastResults() []mockToolResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.results
}

func (m *mockMessagesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	var results []mockToolResult
	for _, msg := range req.Messages {
		results = append(results, toolResultsOf(msg.Content)...)
	}

	block := map[string]any{"type": "text", "text": "done"}
	stop := "end_turn"
	if len(req.Tools) > 0 {
		m.mu.Lock()
		if len(results) >= len(m.results) {
			m.results = results
		}
		m.mu.Unlock()
		if n := len(results); n < len(m.steps) {
			call := m.steps[n].call(results)
			block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_jig_%02d", n), "name": call.Name, "input": call.Input}
			stop = "tool_use"
		}
	}
	id := fmt.Sprintf("msg_jig_%d", m.seq.Add(1))
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

// toolResultsOf extracts the tool_result blocks of one message's content,
// which is either a plain string (no blocks) or an array of blocks whose
// own content is a string or an array of text blocks.
func toolResultsOf(content json.RawMessage) []mockToolResult {
	var blocks []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"is_error"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []mockToolResult
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
		out = append(out, mockToolResult{Content: text, IsError: b.IsError})
	}
	return out
}
