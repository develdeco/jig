# A session without hooks has its reviewer start as a separate session

Decision 39 of the digester chart made the reviewer of a ticket an agent that did not build it, and `jig gate` and `jig next` tell the calling session who that is: the session itself when it is fresh for the ticket, else a subagent it spawns ([ADR 0037](0037-a-chart-is-run-from-the-main-session.md) keeps that rule). A round's close judges the reviewer by session and agent (`verifydeliver.JudgeIndependence`): the session comes from `--session` or the environment (`$JIG_SESSION`, else `CLAUDE_CODE_SESSION_ID`), and the agent from the machine ledger's `tool` lines of that session that ran `jig review context` (`LedgerReviewerAgent`). Only the PostToolUse hook writes a `tool` line, with the agent id of the call.

A session with no jig hooks (GATE-3 was built from a Claude desktop session with none, 2026-11-03) followed the advice and lost the round: its subagent ran with the parent's session id, no ledger line named its agent, so jig saw the builder close the round and recorded it `REVIEW_NOT_INDEPENDENT`, which publish refuses. The same day a scratch reproduction gave the same verdict, and measured what a reviewer can run under on Claude Code 2.1.232 and 2.1.309: a subagent's environment is the parent's, with no variable naming the agent; a nested `claude -p` gives its tools a session id of its own even when it inherits `CLAUDE_CODE_SESSION_ID`, and `CLAUDECODE=1` does not stop it, but it does inherit `JIG_SESSION`, which jig reads first.

## Decision

**jig reads the hooks from the ledger.** `verifydeliver.Fresh` gains `AgentIDs`: the ledger has a `tool` line of the session. It is the line the round's close reads the reviewer's agent from, so it says exactly whether a subagent of the session can be told from the session. jig's own lines for a session (`session-touch`, `verify-spawned`) and git's `git-commit` line carry no agent and do not count.

**A session that built the ticket and has no agent ids is told to start a separate session.** `jig gate` and `jig next` say why (what the session did, and that jig has no hook record of its tool calls) and give the command, for the Bash tool in the background (a review outlasts a shell call's time limit):

```
env -u JIG_SESSION -u CLAUDE_CODE_SESSION_ID claude -p '<the reviewer prompt>' --model opus --permission-mode bypassPermissions < /dev/null
```

The variables are the ones jig names a session by (`locate.SessionEnv` and `locate.HarnessSessionEnv`), so the new session reviews under its own id; the prompt is the subagent's, quoted for sh; the input is `/dev/null`, since `claude -p` otherwise waits for input on the shell tool's stdin (2.1.232 waited 3 s and warned in GATE-4's own first round); the session waits for it to exit and relays the verdict line it prints. Neither the session nor a subagent of it may close the round. A fresh session still reviews itself, a session with agent ids still spawns a subagent, and so does a session jig has no record of: none of the builders is that session, so neither is its subagent.

**The refusal points at the advice.** The help of `REVIEW_NOT_INDEPENDENT` says to run `jig gate` again from the session that built the change and have the reviewer it advises review the next round.

## Rejected alternatives

- **An agent id on the review verbs** (`jig review context --agent-id`). Without hooks nothing from the harness names the agent, so the id would be the reviewer's own word, which a builder could give as well; independence would rest on a claim, not a record.
- **Reading the harness's settings files for jig's hook entries.** They say what is installed, not what reached jig, and a project, user, local or managed settings file can each hold or override them. The ledger is what the judgment reads.
- **Unsetting every `CLAUDE*` variable** (what the GATE-3 session did by hand). The measurement shows the child makes its own session id, so only `JIG_SESSION` needs to go; unsetting the rest would drop a person's `CLAUDE_CONFIG_DIR` or provider settings and start the reviewer signed in elsewhere or not at all.
- **A narrower grant than `bypassPermissions`** (`--allowedTools`). The reviewer runs jig, reads the code, edits the round's worktree to probe and runs the project's narrowest test command, which no fixed list names; a list wide enough for that is the whole shell. A headless session cannot ask, and the person approves the command where the session asks before running one.
- **jig starting the reviewer.** jig starts no coding session ([ADR 0032](0032-jig-attaches-to-the-harness.md)); it advises, and the session runs the command.

## Consequences

- Amends decision 39 and [ADR 0037](0037-a-chart-is-run-from-the-main-session.md)'s reading of it: the reviewer of a session's ticket is a subagent only where the session's hooks give its tool calls agent ids. In a chart run from a session with no hooks every build subagent shares the session's id, so each ticket's reviewer is a separate session, as `jig gate` advises.
- The review and chart skills defer to the advice; `CONTEXT.md`'s *Fresh session* and `ARCHITECTURE.md`'s "Who reviews" name the separate session, and so does the next-phase note an unattended session gets at Stop, for a session whose record has no agent ids (a harness that reports its stops but not its tool calls, say).
- The advice is written for Claude Code; another harness starts a fresh session of its own with the same prompt. A harness that sets no session id for its tools gives a round with no known reviewer, `independent: unknown`, which publish accepts and counts.
- `e2e`'s `TestHooklessSessionReviewsInASeparateSession` runs the advised command in sh with `testdata/fixture/claudestub` as `claude`, standing in for Claude Code's measured behavior (it keeps the environment it starts with and sets its own session id for its tools); a change to that behavior in Claude Code is not caught by it.
