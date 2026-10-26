package journal

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/store"
)

// TestDigesterFieldsRoundTripUnderTheirWireKeys: every field the digester
// adds is written under its key and read back whole, a zero value writes no
// key at all, and an exit status of 0 is told apart from none.
func TestDigesterFieldsRoundTripUnderTheirWireKeys(t *testing.T) {
	zero := 0
	in := Line{
		TS: "2026-10-23T08:00:00.123456789Z", Ticket: "JIG-1", Slice: "a", Event: EventOracle, Outcome: "fail",
		Session: "sess-1", Agent: "agent-2", Harness: "claude", Source: "session",
		Signature: "go test: FAIL TestX", Summary: "--- FAIL: TestX", Provenance: "agent", Unattended: true,
		Branch: "jig/JIG-1", URL: "https://example.invalid/pr/1", Ref: "r1-f2", Tool: "Bash", Cwd: "/work/repo",
		Exit: &zero, Files: []string{"a.go", "b.go"}, PID: 4242,
		Spend: &Spend{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, USD: 1.25},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session", "agent", "harness", "source", "signature", "summary", "provenance", "unattended", "branch", "url", "ref", "tool", "cwd", "exit", "files", "pid", "spend"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("line has no %q key: %s", key, data)
		}
	}
	if exit, ok := wire["exit"]; !ok || exit != float64(0) {
		t.Errorf("exit = %v, want a present 0", exit)
	}
	var out Line
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip:\n got %+v\nwant %+v", out, in)
	}

	bare, _ := json.Marshal(Line{TS: "t", Ticket: "JIG-1", Event: EventVerified})
	if string(bare) != `{"ts":"t","ticket":"JIG-1","event":"verified"}` {
		t.Errorf("a line with none of the new fields writes %s, want no extra keys", bare)
	}
	var old Line
	if err := json.Unmarshal([]byte(`{"ts":"t","ticket":"JIG-1","event":"result","outcome":"green"}`), &old); err != nil || old.Exit != nil || old.Spend != nil || old.Files != nil {
		t.Errorf("an older line reads as %+v, %v; want the new fields empty", old, err)
	}
}

// TestAppendDefaultsToNanosecondTime: a line with no ts is stamped with now
// to the nanosecond, a ts the caller set is kept, and both parse as RFC3339
// (what every reader of the journal parses).
func TestAppendDefaultsToNanosecondTime(t *testing.T) {
	st := newTestStore(t)
	before := time.Now().Add(-time.Second)
	fractional := false
	for i := 0; i < 5 && !fractional; i++ {
		if err := Append(st, "JIG-1", Line{Event: EventDispatch}); err != nil {
			t.Fatal(err)
		}
		lines, err := Read(st, "JIG-1")
		if err != nil {
			t.Fatal(err)
		}
		ts := lines[len(lines)-1].TS
		parsed, err := time.Parse(time.RFC3339, ts)
		if err != nil || parsed.Before(before) || parsed.After(time.Now().Add(time.Second)) {
			t.Fatalf("ts %q = %v, %v; want RFC3339 and now", ts, parsed, err)
		}
		fractional = strings.Contains(ts, ".")
	}
	if !fractional {
		t.Error("five stamped lines and none has fractional seconds")
	}
	if err := Append(st, "JIG-1", Line{Event: EventDispatch, TS: "2026-01-02T03:04:05Z"}); err != nil {
		t.Fatal(err)
	}
	lines, _ := Read(st, "JIG-1")
	if got := lines[len(lines)-1].TS; got != "2026-01-02T03:04:05Z" {
		t.Errorf("ts = %q, want the caller's kept", got)
	}
}

// TestAppendWaitsForTheCheckpointLock: a clone's journal append takes the
// checkpoint lock, so it never lands in the middle of another process's Sync
// or Push. The lock is held here through a second handle on its file, which
// excludes in the same way another process would.
func TestAppendWaitsForTheCheckpointLock(t *testing.T) {
	st := newTestStore(t)
	// A clone as far as the lock goes: its git directory is there to put the
	// lock file in.
	if err := os.MkdirAll(filepath.Join(st.Root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	release, held, err := store.TryLock(filepath.Join(st.Root, ".git", "jig-checkpoint"), 0)
	if err != nil || !held {
		t.Fatalf("TryLock = held %v, err %v", held, err)
	}

	done := make(chan error, 1)
	go func() { done <- Append(st, "JIG-1", Line{Event: EventNote}) }()
	select {
	case err := <-done:
		t.Fatalf("Append returned (%v) while the checkpoint lock was held", err)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Append never went on after the checkpoint lock was released")
	}
	if lines, err := Read(st, "JIG-1"); err != nil || len(lines) != 1 {
		t.Fatalf("Read = %d lines, %v; want the one append", len(lines), err)
	}
}

// TestEventConstants: events.go names every event once, in the form
// Event<CamelCase> for the kebab-case value, and the names the existing call
// sites write as literals are all there.
func TestEventConstants(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "events.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	valueRE := regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)
	byValue := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok {
				t.Fatalf("%s is not a string literal", vs.Names[0].Name)
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil || !valueRE.MatchString(value) {
				t.Errorf("%s = %s, want a kebab-case name", vs.Names[0].Name, lit.Value)
			}
			if prev, dup := byValue[value]; dup {
				t.Errorf("%q is named twice: %s and %s", value, prev, vs.Names[0].Name)
			}
			byValue[value] = vs.Names[0].Name
			var camel strings.Builder
			for _, part := range strings.Split(value, "-") {
				camel.WriteString(strings.ToUpper(part[:1]) + part[1:])
			}
			if want := "Event" + camel.String(); vs.Names[0].Name != want {
				t.Errorf("%q is named %s, want %s", value, vs.Names[0].Name, want)
			}
		}
	}
	for _, value := range []string{
		"adopt", "answer", "changelog", "dispatch", "env-down", "env-unavailable", "env-up", "fix-slice",
		"gate-clean", "gate-open", "gate-round", "lease-recovered", "memorize", "oracle", "pr",
		"publish-done", "publish-picks", "question", "reconcile", "recorded", "requeue", "result",
		"revalidate", "route", "squash", "stall", "verified",
		"verify-pending", "verify-error", "claim", "release", "driver", "note", "correction", "spend",
		"approval", "triage", "pr-body", "publish-approved",
		"session-start", "session-touch", "session-end", "tool", "git-commit", "verify-spawned",
		"note-delivered", "hook",
	} {
		if _, ok := byValue[value]; !ok {
			t.Errorf("no constant for the event %q", value)
		}
	}
	if EventVerifyPending != "verify-pending" || EventVerified != "verified" {
		t.Error("EventVerifyPending / EventVerified do not carry their names")
	}
}
