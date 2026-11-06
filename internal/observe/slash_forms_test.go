package observe

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/phase"
)

// slashPayloads builds, for each adapter, the payload of a prompt and of a
// file edit as the harness sends them. json.Marshal escapes a backslash as
// two, as a harness does: a payload with a lone backslash is not JSON.
var slashPayloads = map[string]struct {
	prompt, edit func(cwd, file string) map[string]any
}{
	"claude": {
		func(cwd, _ string) map[string]any {
			return map[string]any{"session_id": "s1", "hook_event_name": "UserPromptSubmit", "prompt": "add a flag", "cwd": cwd}
		},
		func(cwd, file string) map[string]any {
			return map[string]any{"session_id": "s1", "hook_event_name": "PostToolUse", "tool_name": "Edit", "cwd": cwd, "tool_input": map[string]any{"file_path": file}}
		},
	},
	"codex": {
		func(cwd, _ string) map[string]any {
			return map[string]any{"session_id": "s1", "hook_event_name": "UserPromptSubmit", "prompt": "add a flag", "cwd": cwd}
		},
		func(cwd, file string) map[string]any {
			return map[string]any{"session_id": "s1", "hook_event_name": "PostToolUse", "tool_name": "apply_patch", "cwd": cwd, "tool_input": map[string]any{"file_path": file}}
		},
	},
	"gemini": {
		func(cwd, _ string) map[string]any {
			return map[string]any{"session_id": "s1", "hook_event_name": "BeforeAgent", "prompt": "add a flag", "cwd": cwd}
		},
		func(cwd, file string) map[string]any {
			return map[string]any{"session_id": "s1", "hook_event_name": "AfterTool", "tool_name": "write_file", "cwd": cwd, "tool_input": map[string]any{"file_path": file}}
		},
	},
	"cursor": {
		func(cwd, _ string) map[string]any {
			return map[string]any{"conversation_id": "s1", "hook_event_name": "beforeSubmitPrompt", "prompt": "add a flag", "workspace_roots": []string{cwd}}
		},
		func(cwd, file string) map[string]any {
			return map[string]any{"conversation_id": "s1", "hook_event_name": "afterFileEdit", "file_path": file, "workspace_roots": []string{cwd}}
		},
	},
	"copilot": {
		func(cwd, _ string) map[string]any {
			return map[string]any{"sessionId": "s1", "hook_event_name": "userPromptSubmitted", "prompt": "add a flag", "cwd": cwd}
		},
		func(cwd, file string) map[string]any {
			return map[string]any{"sessionId": "s1", "hook_event_name": "postToolUse", "toolName": "edit", "cwd": cwd, "toolArgs": map[string]any{"file_path": file}}
		},
	},
	"neutral": {
		func(cwd, _ string) map[string]any {
			return map[string]any{"session": "s1", "event": "prompt", "prompt": "add a flag", "cwd": cwd}
		},
		func(cwd, file string) map[string]any {
			return map[string]any{"session": "s1", "event": "tool", "tool": "edit", "cwd": cwd, "files": []string{file}}
		},
	},
	"opencode": {
		func(cwd, _ string) map[string]any {
			return map[string]any{"session": "s1", "event": "prompt", "prompt": "add a flag", "cwd": cwd}
		},
		func(cwd, file string) map[string]any {
			return map[string]any{"session": "s1", "event": "tool", "tool": "edit", "cwd": cwd, "files": []string{file}}
		},
	},
}

// slashForms is a native path written with backslashes and with forward
// slashes. Where a backslash is a filename character, only the slash form is
// a path.
func slashForms(p string) []string {
	forms := []string{filepath.ToSlash(p)}
	if bs := strings.ReplaceAll(p, "/", `\`); runtime.GOOS == "windows" {
		forms = append([]string{bs}, forms...)
	}
	return forms
}

func TestEveryAdapterHearsTheSameNotesForEitherSlash(t *testing.T) {
	t.Parallel()
	for _, name := range Harnesses() {
		pl, ok := slashPayloads[name]
		if !ok {
			t.Errorf("adapter %s has no payload in slashPayloads", name)
			continue
		}
		a, _ := AdapterFor(name)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			forms := len(slashForms("x")) // the count depends only on the OS
			for c := 0; c < forms; c++ {
				r := newRig(t)
				cwd := slashForms(r.fx.RepoDir)[c]
				data, err := json.Marshal(pl.prompt(cwd, ""))
				if err != nil {
					t.Fatal(err)
				}
				if got := heard(t, r.d, a, data); !strings.Contains(got, "works on no ticket") {
					t.Errorf("prompt with cwd %s: context = %s, want the fresh-work note", cwd, got)
				}
				for f := 0; f < forms; f++ {
					e := newPhaseRig(t)
					e.onTicketBranch()
					e.phases[e.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
					cwd := slashForms(e.fx.RepoDir)[c]
					file := slashForms(filepath.Join(e.fx.RepoDir, "pkg", "main.go"))[f]
					data, err := json.Marshal(pl.edit(cwd, file))
					if err != nil {
						t.Fatal(err)
					}
					if got := heard(t, e.d, a, data); !strings.Contains(got, "jig claim") {
						t.Errorf("edit of %s with cwd %s: context = %s, want the claim note", file, cwd, got)
					}
				}
			}
		})
	}
}

// heard is the context the core gives the session for a payload: what the
// adapter makes of it, not how the harness is told (some harnesses are told
// nothing).
func heard(t *testing.T, d Deps, a Adapter, data []byte) string {
	t.Helper()
	ev, err := a.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	res, err := Handle(d, ev)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return res.Context
}
