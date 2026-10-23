// jig-hooks-plugin: written by jig hooks; jig hooks --harness opencode --uninstall removes it.
// OpenCode loads this file as a plugin. It hands the events jig needs to
// "jig observe --harness opencode" as JSON on stdin, never throws, and never
// holds OpenCode up for more than a few seconds.
import { spawnSync } from "node:child_process"

const COMMAND = "jig observe --harness opencode"
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
