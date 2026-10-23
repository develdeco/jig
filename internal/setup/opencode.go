package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// OpenCode has no hook file: it loads every JavaScript file in its plugins
// directory (~/.config/opencode/plugins/, <repo>/.opencode/plugins/) as a
// plugin whose exported function returns handlers. jig writes one file there,
// jig.js, that hands the few events it needs to jig as JSON on stdin:
//
//	jig observe --harness opencode
//
// The file is jig's alone. It is recognized by its first line, and an install
// refuses to replace a jig.js that does not carry it.
//
// What the shim sends (the contract for the opencode adapter of jig observe):
//
//	{"hook":"event","cwd":"<dir>","event":{"type":"session.created"|"session.idle"|"session.deleted","properties":{...}}}
//	{"hook":"chat.message","cwd":"<dir>","input":{"sessionID":...},"output":{"parts":[...]}}
//	{"hook":"tool.execute.after","cwd":"<dir>","input":{"tool":"bash","sessionID":...,"callID":...},"output":{...},"command":"<the shell command>"}
//
// Only the bash tool is reported, and of OpenCode's many events only the three
// that open, idle and close a session: a process per streamed chunk would cost
// more than the whole session. The payload schema is the one OpenCode
// documents for plugins; only Claude Code's is proven end to end.

// OpenCodeEvents are the plugin handlers the shim installs, for display.
func OpenCodeEvents() []string {
	return []string{"event", "chat.message", "tool.execute.before", "tool.execute.after"}
}

const openCodeMarker = "// jig-hooks-plugin:"

const openCodeShim = `// jig-hooks-plugin: written by jig hooks; jig hooks --harness opencode --uninstall removes it.
// OpenCode loads this file as a plugin. It hands the events jig needs to
// "jig observe --harness opencode" as JSON on stdin, never throws, and never
// holds OpenCode up for more than a few seconds.
import { spawnSync } from "node:child_process"

const COMMAND = %s
const SESSION_EVENTS = new Set(["session.created", "session.idle", "session.deleted"])

function send(payload) {
  try {
    spawnSync(COMMAND, {
      input: JSON.stringify(payload),
      shell: true,
      timeout: 15000,
      windowsHide: true,
      stdio: ["pipe", "ignore", "ignore"],
    })
  } catch {
    // a failing jig must not get in the way of the session
  }
}

export const JigPlugin = async ({ directory, worktree }) => {
  const cwd = directory || worktree
  const commands = new Map()
  return {
    event: async ({ event }) => {
      if (SESSION_EVENTS.has(event?.type)) send({ hook: "event", cwd, event })
    },
    "chat.message": async (input, output) => {
      send({ hook: "chat.message", cwd, input, output: { parts: output?.parts } })
    },
    "tool.execute.before": async (input, output) => {
      if (input?.tool === "bash" && output?.args?.command) commands.set(input.callID, output.args.command)
    },
    "tool.execute.after": async (input, output) => {
      if (input?.tool !== "bash") return
      const command = commands.get(input.callID) ?? ""
      commands.delete(input.callID)
      send({ hook: "tool.execute.after", cwd, input, output, command })
    },
  }
}
`

// OpenCodeShim is the plugin file that runs jig (the program part JigCommand
// gives) for OpenCode's events.
func OpenCodeShim(jig string) []byte {
	var quoted bytes.Buffer
	enc := json.NewEncoder(&quoted)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(ObserveCommand(jig, "opencode")) // a string always encodes
	return []byte(strings.Replace(openCodeShim, "%s", strings.TrimRight(quoted.String(), "\n"), 1))
}

func openCodeOwned(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimPrefix(data, utf8BOM), []byte(openCodeMarker))
}

func mergeOpenCode(data []byte, jig string) ([]byte, bool, error) {
	want := OpenCodeShim(jig)
	switch {
	case len(bytes.TrimSpace(data)) == 0:
		return want, true, nil
	case !openCodeOwned(data):
		return nil, false, errors.New("this file is not one jig wrote (its first line is not jig's); move it aside or install somewhere else")
	case bytes.Equal(data, want):
		return data, false, nil
	}
	return want, true, nil
}

// unmergeOpenCode removes the shim; a file that is not jig's is left alone.
// Nothing to write with a change means the file goes.
func unmergeOpenCode(data []byte) ([]byte, bool, error) {
	if !openCodeOwned(data) {
		return data, false, nil
	}
	return nil, true, nil
}

func installedOpenCode(data []byte) ([]string, error) {
	if !openCodeOwned(data) {
		return nil, nil
	}
	return OpenCodeEvents(), nil
}
