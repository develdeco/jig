package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
)

// jigHookBin is the once-built path to a real cmd/jig binary, shared by
// every test in this file. TestScreenHookBinary* drive it as a subprocess
// fed a JSON payload on stdin exactly as Claude Code's PreToolUse hook
// does, so the unreadable-input deny fix is proven against the actual
// `jig _screen` production binary, not only the in-process runScreen call
// TestScreenDenyAllow and TestScreenDeniesUnreadableInput already cover.
var (
	jigHookBinOnce sync.Once
	jigHookBin     string
	jigHookBinErr  error
)

func builtJigHookBinary(t *testing.T) string {
	t.Helper()
	jigHookBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "jig-screen-hook-bin")
		if err != nil {
			jigHookBinErr = fmt.Errorf("create build dir: %w", err)
			return
		}
		gittest.AtExit(func() { os.RemoveAll(dir) })
		name := "jig"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		out := filepath.Join(dir, name)
		root := fixture.RepoRoot(t)
		goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
		if runtime.GOOS == "windows" {
			goBin += ".exe"
		}
		cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, filepath.Join(root, "cmd", "jig"))
		if output, err := cmd.CombinedOutput(); err != nil {
			jigHookBinErr = fmt.Errorf("build cmd/jig: %v\n%s", err, output)
			return
		}
		jigHookBin = out
	})
	if jigHookBinErr != nil {
		t.Fatalf("%v", jigHookBinErr)
	}
	return jigHookBin
}

// runJigScreenHook execs the real jig binary as `jig _screen`, writes
// input to its stdin, and returns its stdout - the same invocation shape
// Claude Code uses for the PreToolUse hook (exec form, one JSON call in,
// one JSON decision or nothing out).
func runJigScreenHook(t *testing.T, bin, input string) string {
	t.Helper()
	cmd := exec.Command(bin, "_screen")
	cmd.Stdin = strings.NewReader(input)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running the real jig _screen binary on %s: %v (stderr: %s)", input, err, stderr.String())
	}
	return out.String()
}

// TestScreenHookBinaryDeniesUnreadableInput drives the real, compiled
// `jig _screen` binary - not runScreen in-process - with payload shapes an
// adversarial review found allowed: a known tool whose required argument
// is missing, sent under the wrong key, or sent with a type SecretPath
// cannot read must come back denied.
func TestScreenHookBinaryDeniesUnreadableInput(t *testing.T) {
	bin := builtJigHookBinary(t)
	cases := []struct {
		name     string
		input    string
		wantDeny bool
	}{
		{"denied git push, control", `{"tool_name":"Bash","tool_input":{"command":"git push"}}`, true},
		{"command under the wrong key", `{"tool_name":"Bash","tool_input":{"cmd":"git push"}}`, true},
		{"empty tool_input", `{"tool_name":"Bash","tool_input":{}}`, true},
		{"no tool_input at all", `{"tool_name":"Bash"}`, true},
		{"command is a list", `{"tool_name":"Bash","tool_input":{"command":["git","push"]}}`, true},
		{"granted commit, control", `{"tool_name":"Bash","tool_input":{"command":"git commit -m x"}}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := runJigScreenHook(t, bin, c.input)
			denied := strings.Contains(out, `"permissionDecision":"deny"`)
			if denied != c.wantDeny {
				t.Fatalf("jig _screen on %s\nstdout = %q\nwant deny=%v", c.input, out, c.wantDeny)
			}
			if c.wantDeny && !strings.Contains(out, "cannot be judged") && !strings.Contains(out, "history must not be pushed") {
				t.Errorf("jig _screen on %s\nstdout = %q\nwant a reason", c.input, out)
			}
		})
	}
}

// TestScreenHookBinaryGrantsEveryTool drives the real `jig _screen` binary
// for every tool a passing screen grants (screen.Granted), with a
// well-formed call to each, confirming the allow decision the binary
// itself prints - not a stand-in.
func TestScreenHookBinaryGrantsEveryTool(t *testing.T) {
	bin := builtJigHookBinary(t)
	cases := []struct {
		tool  string
		input string
	}{
		{"Bash", `{"command":"git status"}`},
		{"Read", `{"file_path":"/store/T-1/work/a.attempt-1.slice.json"}`},
		{"Glob", `{"pattern":"**/*.go"}`},
		{"Grep", `{"pattern":"func main"}`},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			out := runJigScreenHook(t, bin, `{"tool_name":"`+c.tool+`","tool_input":`+c.input+`}`)
			want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"screened by jig"}}` + "\n"
			if out != want {
				t.Fatalf("jig _screen %s %s\nstdout = %q\nwant   = %q", c.tool, c.input, out, want)
			}
		})
	}
}
