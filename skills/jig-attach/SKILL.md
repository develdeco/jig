---
name: jig-attach
description: Attaches the repo you are in to its jig project and sets this machine up for it - skills, hooks and git hooks. Use after cloning a project's repo, or when jig is installed but a session shows no ticket and `jig doctor` lists missing pieces.
---

# jig-attach

`jig attach` sets up the project you are in for your own agents. It needs a project: a `jig.yaml` marker in the working directory or above it.

## Steps

1. `jig here` - it prints the project when you are in one. Silent means no project here.
2. Silent, but the repo has a `jig.yaml`? Run from the folder that holds it. No `jig.yaml` at all: the repo is not in a project; `jig-init` starts one, or run `jig map <repo>=<path>` inside the project it belongs to.
3. `jig attach`. With no terminal it stops with NEEDS_INPUT and says what it would write; show that to the person, then run `jig attach --yes`.
4. `jig doctor` - each piece, its state, its cost, and the command that fixes it.
5. Start a new session. A harness reads its hooks and skills when it starts.

## What it writes

The default is the untracked form, kept out of `git status` through `.git/info/exclude`:

| Piece | File |
|---|---|
| skills | one stub in `.claude/skills/jig/` and `.agents/skills/jig/` that sends the agent to `jig skill <name>` |
| hooks | `.claude/settings.local.json` |
| hint | `CLAUDE.local.md` |
| git hook | a block in the clone's post-commit, unless the global hooks cover it |

## Choices

| Want | Run |
|---|---|
| the team to commit the files | `jig attach --tracked` (full skills, `.claude/settings.json`, `AGENTS.md`); only when asked |
| other harnesses | `--harness codex,gemini,cursor,copilot,opencode`, or `all` |
| a project of several repos | `--members` sets up each member repo too |
| only some pieces | `--skills --hooks --githook --hint` |
| take it away | `jig attach --uninstall --yes` |
| every project on this machine | `jig setup --global --artifacts --yes` |

## Limits

- Claude Code ignores project hooks in a folder it does not trust and with `--bare`; `jig here` and the skills still work.
- The hook entries call `jig`: it has to be on PATH under that name.
- A repo that sets its own `core.hooksPath` is not reached by the global git hooks; attach puts a block in its post-commit.

## Never

- Do not edit `settings.json`, `CLAUDE.md` or a hook by hand to wire jig; setup owns its entries and `--uninstall` removes only them.
- Do not use `--tracked` or `--global` unless the person asked: they change shared files and the home directory.
