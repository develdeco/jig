# Chart: digester

jig stops dispatching the model and starts digesting what the model produces.
This is a chart in the intake shape (Destination · Notes · Decisions so far ·
Not yet specified · Out of scope), drafted in the jig repo because the design
is the repo's; its home is the store's `charts/digester/` once it has been
grilled, and `jig graduate digester` mints its tickets from `tickets.yaml`
beside it. The map decides, it never builds.

## Destination

A person builds a ticket by hand, in their own harness session (Claude Code
today; Codex, Gemini CLI and others by the same floor), with the harness's
own tools, subagents, background work and model choice. jig is attached to
that session, reached reactively through four doors: hooks that fire on what
the session does, skills that tell the session when to reach for jig, MCP
tools that let it ask and answer, and the CLI on PATH that every one of those
calls into. Through them jig

- **listens**: a commit lands, a test runs, a turn ends, a session starts;
- **advises**: the brief, the plan, what earlier slices built, the oracle's
  last wall time, the code graph, the ledger's recall, pulled when asked;
- **digests**: it runs the slice's oracle on the commit in the background and
  journals the result, so green is still what jig proved, never what the model
  claimed ([ADR 0020](../../adr/0020-jig-runs-the-slice-oracle-at-green.md));
- **corrects**: a stall, a brief edited after its slice verified, a commit no
  slice claims, tracked edits left uncommitted at a turn's end, said once,
  into the session, at the moment it matters; advisory in an interactive
  session, enforced in an unattended one.

The hand-built and jig paths stop being a choice. The gate, publish, intent
provenance, the fix budget, minted ids, the recorded branch and the store as
the only API are unchanged: they move from dispatch time to commit time.
`jig run` survives as "start a full harness session with jig attached in a
worktree", for unattended work, and the frontier becomes advice about what is
ready rather than a dispatcher.

## Notes

**The one distinction.** A constraint on *how the model works* is a box: the
stripped tool surface, one cold session per slice, the prompt as the whole
context, the rung jig picks, no background work, a timeout as the cost cap.
A constraint on *what counts as done* is a seam: jig ran the oracle on that
commit, the gate is a separate session reading git state, intent has a
provenance, asks are records, fix slices are forward-only under a budget, the
schema is the API. Boxes go. Seams stay and change only when they fire.
The verdict map at the end applies this to every ADR; four retire, six
invert, twenty keep.

**What already exists.** Most of the digester is built and waits behind the
wrong door:

- `project.Resolve` (`internal/project/project.go`) maps a bare working
  directory to a store three ways, the second being a clone of a multi-repo
  project through the per-machine mapping. That is the "is this a jig
  project" question, answered; nothing runs it at session start.
- A ticket's branch is a recorded fact ([ADR 0013](../../adr/0013-a-ticket-branch-is-recorded.md)),
  so a checkout's branch names its ticket without anyone saying so. Adoption,
  the exception today, is the ordinary shape tomorrow.
- The gate already reviews a branch built outside jig, infers intent from the
  operator's own transcripts ([ADR 0012](../../adr/0012-intent-provenance.md)),
  builds fixes on the author's branch and ships their commits unchanged. It
  listens to the hand-built session once, at the end, when the context is
  gone. ADR 0012's own known limit, the authoring session that is still being
  written to, is attached mode's default situation.
- The oracle runner, `verifyGreen`, the `oracle` and `verified` journal lines
  and the gate's pass reuse ([ADR 0021](../../adr/0021-the-gate-reuses-an-oracle-pass-on-the-same-tree.md))
  need no change to be fed from a commit hook instead of a result file. Run
  on a detached worktree at the sha, ADR 0021's "same tree" condition becomes
  structural.
- `jig _screen` is already a PreToolUse hook; the hook door is open.
- Intake is already a skill the session runs; it lacks the store-aware verbs.

**The harness floor.** The CLI on PATH, files on disk and git hooks work in
every harness, because every harness commits. The survey in the appendix
found the floor higher than assumed: Codex, Gemini CLI, Cursor and Copilot
CLI all have session-start, pre/post-tool and stop hooks as external commands
speaking JSON over stdin with exit 2 blocking, the same convention as Claude
Code, and OpenCode has the same points as an in-process plugin. So one `jig
observe` payload reader serves five harnesses through five thin installers,
plus one plugin shim. What is not floor: MCP elicitation (undocumented or
broken in Gemini CLI and OpenCode), a per-harness commit event (none has
one), and hooks firing before a folder is trusted. The git post-commit hook
covers the first two gaps, and every seam holds with the hooks absent; what
the hooks buy is timing (jig speaks into the session instead of into `jig
status`).

**What Claude Code gives, checked against its docs (2026-10-10).**

- SessionStart fires for interactive, `-p`, `--resume` and `--continue`
  sessions, says which (`source`), and injects context from plain stdout or
  `additionalContext`. `--bare` runs no hooks at all. In an interactive
  session hooks from every settings file, the user's included, wait until the
  folder is trusted.
- Hooks merge across levels; the user's `~/.claude/settings.json` applies to
  every project, which is where `jig attach` installs. Settings hooks also
  run inside subagents, carrying `agent_id`, so the sessions ledger keys on
  session and agent.
- Stop can refuse the turn's end with a reason the model acts on, guarded
  after eight refusals in a row; `stop_hook_active` marks the re-entry.
- PostToolUse on Bash carries the command, its stdout and stderr, but no
  documented exit code; `PostToolUseFailure` carries "Exit code N". Counting
  the session's own oracle run (below) depends on closing this gap.
- There is no git-commit event, and commits made outside Claude's tools are
  invisible to its hooks. The git post-commit hook is not optional; it is the
  listener, and the Claude Code hooks are the voice.
- `CLAUDE_PROJECT_DIR` names the project root; the `cwd` field follows `cd`
  and worktrees. Resolution uses the project directory.
- MCP elicitation is supported, with an `Elicitation` hook that can answer
  for the user; a `-p` run with prompts off cancels any elicitation.
- User-level skills auto-select from their description; nothing in a skill
  runs at session start, so the SessionStart hook is the one way in.

**Observation counts, claims do not.** A PostToolUse hook sees the command the
harness ran and its output. That is the harness's record, not the model's
claim. When the session ran exactly the slice's oracle command on a clean
tree at HEAD and it passed, jig may count that run and skip its own. ADR
0020's rule stands; its proof gains a second source. The counted run trusts
the owner's ignored files (ADR 0021) and records no scenarios (ADR 0029), so
its `oracle` line carries its source, and the gate and publish can tell the
two apart.

**One chain retires together.** ADR 0018 supplies the 30-minute number ADR
0020 bounds its run with, and ADR 0019 supplies the outcome after ADR 0020's
fix budget ("the failure climbs the staircase"). Retiring 0018 and 0019 leaves
0020 needing its own run bound and a budget-exhausted outcome; ADR 0015's
"park for a person" (`routed_why: budget`) is the ready shape.

**Pre-hoc permission becomes post-hoc observation.** The screen's PreToolUse
deny and its self-proof, and the gate lease's read-only guard, invert into
checks after the fact: compare HEAD and tracked files after a round, read the
harness's PostToolUse record. ADR 0009's eval already works this way.

**The gate stays a session jig launches.** That is the one deliberate
asymmetry: lines that shape the reviewer (ADR 0017's "run no tests", ADR
0023's rung) are seams of the gate, not boxes, even where a builder-side twin
of the same sentence (ADR 0018) is a box.

**Events are records; a daemon is a reader, never the truth.** jig already
has an event log: the ticket's `journal.ndjson`, append-only, one line per
thing that happened (ADR 0001 resumes by recomputing from it; ADR 0030 hangs
the mirror off its checkpoints). The digester adds the second log, the
machine ledger under the jig home, in the same shape. Those two files are the
bus. Three kinds of process touch it, and none is required for the others to
be correct:

- **writers** are short-lived: a hook invocation appends a line and exits; a
  command appends at its checkpoint;
- **workers** outlive a hook: the background verification is a detached
  process that takes a per-clone lock, runs the oracle, appends `oracle` and
  `verified`, and exits. A resident worker (`jig watch`) is the same code run
  long, draining a queue, so two commits in quick succession serialize on one
  env class instead of racing. It is an optimization a machine opts into, and
  a hook falls back to spawning when none is running;
- **readers** tail the two logs: `jig status` once, `jig events --follow` as
  a stream, the TUI as a screen, and any other interface the same way.

A daemon-first design (the shape no-mistakes takes) is one worker plus one
reader resident at all times. jig can run that way, and nothing in the seams
depends on it: pull the daemon and the hooks still append, the verifier still
runs detached, the TUI still tails. That is ADR 0001's "resume is recomputing
the frontier from what's on disk", kept.

**The TUI is the minimum interface, and it is a client.** `jig tui` reads
`jig events --follow` and the same verbs every other door uses: tickets in
flight and their slices, verifications running against the oracle's last wall
time, questions waiting for a person (answered there with person provenance,
ADR 0012), parked findings to keep or dismiss (today an interactive confirm
inside `jig gate`), the publish confirm, spend against budget. Building it on
the stream is what keeps the stream honest for the next interface (an
editor pane, a web page, a chat bot): if the TUI needs a fact the stream does
not carry, the stream gains it, not the TUI. This is where the digester and
the interaction plane meet; the TUI is that chart's first front end as much
as this one's.

**Why a fifth home.** A session and its pending verifications outlive the
session's turn and die with the machine, so they live under the jig home
beside the pool and the evidence tree, not in the store and not in the repo.
Only what reaches a checkpoint is journaled; the non-checkpoint lines the
dispatcher wrote (`dispatch`, `graph`) move there. The same ledger is where a
second session learns a first one drives the ticket, and where ADR 0012's
inference becomes a lookup instead of a transcript search.

## Decisions so far

Each is marked by provenance as intake requires.

1. **jig attaches to the harness; it does not spawn the model for interactive
   work.** `jig run` is kept for unattended runs and inverts to launching a
   full harness session with jig attached. _user-confirmed_
2. **Boxes go, seams stay, and the seams fire at commit time.** Green means
   jig ran the oracle on that commit; the gate stays a separate session; intent
   keeps its provenance; asks stay records; the store stays the only API.
   _user-confirmed_
3. **jig is reached through hooks, skills, MCP and the CLI, never by being in
   the loop.** Every verb is callable from the middle of someone else's session
   against a working tree jig does not own, and is idempotent, since hooks
   fire twice. The new surfaces are subcommands of the one binary (ADR 0002).
   _user-confirmed_
4. **Planning is a partnership with the store.** Intake runs inside the hand-
   built session and jig supplies recall, open tickets and their edges, charts
   and platform docs; validate checks the plan against the store (duplicates,
   covered work, dangling edges). _user-confirmed_
5. **jig keeps its own ledger of sessions and events heading to a checkpoint**,
   under the jig home, keyed by the harness's session and agent id; the
   store's journal gets checkpoint outcomes only. _user-confirmed_
6. **Project recognition is the machine mapping plus a global SessionStart
   hook; the ticket is the branch.** One hook installed beside the skills runs
   a read-only verb from the project directory; unmapped means silent exit.
   Every surveyed harness has such a hook; a repo-side `AGENTS.md` line (read
   by all five) is the fallback for an untrusted folder or a harness run with
   hooks off, a hint and never a grant. _defaulted: proposed here, grilled
   against the multi-repo question, not yet confirmed_
7. **Verification runs in the background on a detached worktree at the commit,
   started by a git post-commit hook.** Claude Code's PostToolUse is the voice
   that reports it, not the listener; the session is never blocked on a test
   run. _defaulted_
8. **Correction is advisory in interactive sessions and enforced in unattended
   ones.** The seam is the same; only the hook's refusal differs. _defaulted_
9. **Cost governance moves from walls to records.** Budgets are records like
   the fix budget; spend is reported, not capped by a timeout. _defaulted_
10. **The first ticket proves the thesis on a real ticket**: attach, verify in
    the background, journal, and let the existing gate and publish finish.
    _defaulted_
11. **Events are append-only records in two logs, the store journal and the
    machine ledger; every interface, jig's own TUI first, is a reader of one
    stream over both; a resident daemon is an optional worker and reader,
    never what makes the records true.** The TUI is the minimum interface
    jig ships; others plug into the same stream. _user-confirmed (TUI as
    minimum, pluggable interfaces); defaulted (records as the bus, daemon
    optional)_

## Not yet specified (fog)

- **Where the gate reads a branch that exists only in the operator's clone.**
  Today the gate reviews origin's copy or a build lease's. An attached session
  commits in the clone and may not push. Push-first (the gate reviews origin),
  clone-as-source (the pool reads the mapped clone), or both by rule. ADR
  0013's lease-copy cases collapse either way to "the clone or origin".
- **Slice attribution.** A `Jig-Slice:` commit trailer, a `jig claim` verb, or
  inference from the files a commit touches against each slice's workspace.
  Likely trailer first, claim as the override, inference as the note. ADR
  0025's attempt ranges become the diff-trees of the commits naming the slice.
- **Counting the session's own oracle run.** PostToolUse's Bash record has no
  documented exit code. Either `PostToolUseFailure` closes it, or the counted
  run is dropped and jig always runs its own.
- **The run bound and the budget-exhausted outcome for ADR 0020**, once 0018's
  number and 0019's climb are gone. Proposed: the manifest's own timeout, and
  a park with `routed_why: budget`.
- **Waking an idle session.** No harness lets an outside process type into an
  idle interactive session, so an answer, a red verification or a note waits
  for the next hook point. Claude Code's SessionStart can return `watchPaths`
  and a `FileChanged` hook then fires on those files; the docs say it returns
  only `watchPaths`, not context. Verify whether it can carry a nudge; if it
  can, the idle case folds into the Stop-hook case.
- **Spend measurement.** Claude Code transcripts carry token usage; other
  harnesses may not. What a `spend` journal line can promise portably.
- **Who sets advisory versus enforced.** `project.yaml`, the ticket record, or
  the way the session was started (`jig run` versus by hand).
- **Testing the digester.** The fake backend replays scripted sessions. The
  digester needs a fake harness that emits hook payloads and commits, and e2e
  tests that drive a clone, not a lease.
- **herdr.** A remote agent with no local hooks and no local commits attaches
  through the store and origin alone. Whether that is attach at all or a
  separate backend that stays as it is.
- **Detached worker or resident worker by default.** A detached `jig verify`
  per commit is simplest and needs a per-clone lock; a resident `jig watch`
  serializes and debounces but must be started by something. Whether `jig
  attach` installs a user service, or the TUI doubles as the worker while it
  is open, or neither.
- **Windows.** Git hooks run under the shell git finds; a background
  verification must detach from the session's process tree the way `kill.go`
  already handles the reverse.
- **Verb names.** Working names are used in `tickets.yaml`; the names are the
  skills' vocabulary and deserve one decision.
- **The interaction plane.** How a person talks to a running jig shares the
  primitive (records plus harness front ends) and the store-safety
  prerequisite with this chart; the two must be grilled together before either
  graduates.

## Out of scope

- The fleet (`fleet-liaison`, parallel `jig solve`) and `retro` as v0.2 verbs.
- The GitHub mirror and trackers (ADR 0030 is unchanged).
- Review quality (ADR 0009, 0022) and the reviewer's own prompt.
- Recordings and the PR demo (ADR 0029) beyond moving the recording to the
  background verification.
- Interfaces beyond the TUI (editor panes, web, chat). They plug into the
  same stream; which to build is the interaction-plane chart's call.

## ADR verdict map

Keep: the rule holds as written. Invert: the rule holds, its mechanism flips
from dispatch time to commit time or from jig-launches to jig-listens.
Retire: the rule was a box.

| ADR | Verdict | Under attached mode |
|---|---|---|
| 0001 no orchestration framework | keep | Disk state and resume-by-recompute stay; the while-loop survives only inside `jig run`, and the frontier becomes a read verb ("what is unverified, what is next"). |
| 0002 one binary plus skills | keep | Skills become the entry point; "dispatch" drops from the binary's list except for `jig run`; the git hook, the SessionStart hook and the MCP server are subcommands of the one `jig`. |
| 0003 store schema is the API | keep | Hooks, skills and MCP are more readers of the same subtrees under the same `schema_version`; the sessions ledger is deliberately not a store subtree. |
| 0004 disk-only pipeline seam | keep | Becomes literally true: the verifier is a separate process started by git, sharing nothing with the session but the store and the sha. |
| 0005 tracker is a projection | keep | Unchanged. |
| 0006 Go packages are internal | keep | Strengthened: every new door goes through commands, flags, output and exit codes; "unmapped means silent exit" is an exit-code contract. |
| 0007 gate owns bookkeeping, not judgment | keep | Unchanged for the gate; the deferred "files the reviewer opened" evidence becomes available wherever the harness emits PostToolUse records. |
| 0008 headless permission model | retire | The harness owns permissions. What survives is for the gate session jig still launches: capability versus instructions (no settings from the code under review, `CLAUDE.md` from HEAD's tree); the read-only guard inverts to a post-round check. The kill mechanics move to the background oracle runner as a wedge guard, never a cost cap. |
| 0009 review eval, structural gold | keep | The gate's default model is named outright rather than derived from the builder staircase. |
| 0010 dependencies live on the ticket | keep | The edges feed recall and validate; "a stacked child starts only on a clean parent gate" is enforced at claim or attach time, advisory when interactive. |
| 0011 the store runs git in process | keep | Motive strengthens: a post-commit hook must return fast, so cheap store writes matter more. |
| 0012 intent has a provenance | keep; inference inverts | The precedence and provenance stand. Inference stops searching transcripts by file overlap: the ledger knows which session holds the ticket, and the session can state intent at claim. |
| 0013 a ticket's branch is recorded | keep | Adoption becomes the ordinary shape; the build-lease cases of `chooseBuiltCopy` collapse to the clone or origin; "the commits jig built" reads "the commits jig verified". |
| 0014 demo session at the gate | retire | Already superseded by 0029. |
| 0015 fix budget and risk floor | keep | "A person at a terminal" is the attached session's owner; the fix-slice size cap loses its fresh-context rationale and stays only as a readability bound. |
| 0016 tests at agreed seams | keep | The `Seams` section is pulled by the context verb instead of dumped into `slice.json`. |
| 0017 the reviewer reads, the oracles test | keep | `reused_from` is fed by commit-time evidence. |
| 0018 builders test narrowly, no background | retire | The harness keeps its shell and background behavior. The background verification answers the waiting problem; "uncommitted tracked edits at Stop" answers the lost-run problem; narrow tests become skill advice. |
| 0019 builders open on Sonnet and climb | retire the staircase; keep the failure classification | jig chooses no model per attempt. `journal.FailedAttempts` becomes correction's input ("same failure twice"). `jig run` may pick its opening model. |
| 0020 jig runs the oracle at green | invert | The trigger flips from `result.json` to an observed commit; the red run becomes a record the session reads at its next turn; the fix budget parks for a person instead of climbing; the run bound is re-derived. |
| 0021 the gate reuses an oracle pass | keep | The same-tree condition becomes structural on a detached worktree; a counted in-session run carries its source on the line. |
| 0022 the reviewer confirms by id | keep | Unchanged. |
| 0023 dearest rung and efforts | keep for the reviewer; retire builder effort | The harness's model and effort are recorded in the ledger, not chosen; `jig run` alone still sets them. |
| 0024 builders are told the oracle's time | invert | The background run stamps `seconds`; the context verb returns it by `LastOracleSeconds`'s rule. |
| 0025 a builder reads earlier slices | invert | The context verb returns the same fields; a slice's files are the commits that name it, replacing attempt ranges. |
| 0026 code graph starting points | invert | The graph runs on demand in the mapped clone; the `graph` line moves to the ledger. |
| 0027 jig's testing method | keep | "At a builder's green" becomes "at each commit naming a slice"; the intake skill gains weight. |
| 0028 jig mints every id | keep | Unchanged; `ticket new` and `graduate` run from inside the session. |
| 0029 demos are recordings | invert | Recording moves to the background verification, run named per verification; a counted in-session run records nothing. |
| 0030 mirror from inside every command | keep | Checkpoints come from verification writes and verbs; read-only verbs pay no sync. |

## Appendix: the other harnesses' floor

Surveyed 2026-10-10 from the official docs or their sources (the Codex,
Gemini CLI, OpenCode and Copilot docs repositories at HEAD; Cursor's site
through search). "Unconfirmed" means no official text was found.

| Harness | Lifecycle hooks | Repo instruction file | MCP and elicitation | Skills (user-level) | Session id and transcript |
|---|---|---|---|---|---|
| Codex CLI | `hooks.json` or `[hooks]` in `config.toml`, per layer (`~/.codex`, trusted `.codex/`, managed); SessionStart, SessionEnd, UserPromptSubmit, PreToolUse, PostToolUse, PermissionRequest, Stop, Interrupt, SubagentStart/Stop, Pre/PostCompact | `AGENTS.md` from the git root down to cwd, plus `~/.codex/AGENTS.md` | MCP in `config.toml`; elicitation exists, auto-rejected unless `mcp_elicitations` is on | `SKILL.md` skills (`~/.codex/skills` unconfirmed); custom prompts deprecated | `codex exec --json` emits `thread_id`; rollouts under `$CODEX_HOME/sessions/` as JSONL |
| Gemini CLI | `hooks` in `settings.json` (project, user, system); SessionStart, SessionEnd, BeforeAgent, AfterAgent, BeforeTool, AfterTool, Before/AfterModel, PreCompress, Notification; `GEMINI_SESSION_ID`, `GEMINI_PROJECT_DIR` | `GEMINI.md` by default; `AGENTS.md` only when `context.fileName` names it; stops at `.git` | MCP in `settings.json`; elicitation undocumented, open bug reports "Method not found" | `~/.gemini/skills/` or `~/.agents/skills/`; TOML commands | Sessions under `~/.gemini/tmp/<hash>/chats/`; hooks get `session_id` and `transcript_path` |
| OpenCode | JS/TS plugins only (`~/.config/opencode/plugins/`): `tool.execute.before/after`, `session.created`, `session.idle` | `AGENTS.md` upward from cwd, `~/.config/opencode/AGENTS.md`, `CLAUDE.md` as fallback | MCP in `opencode.json`; elicitation undocumented, open issues say requests hang | `~/.config/opencode/skills/`, `.agents/skills`, `.claude/skills` | `opencode run --format json`, `opencode export <id>`; storage under `~/.local/share/opencode/` |
| Cursor CLI | `~/.cursor/hooks.json` or `.cursor/hooks.json`; sessionStart, sessionEnd, stop, pre/postToolUse, before/afterShellExecution, afterFileEdit (CLI fires fewer than the IDE, per forum) | `AGENTS.md` and `CLAUDE.md` at the root, `.cursor/rules`; no global file | MCP in `mcp.json`; elicitation supported since 1.5 | `~/.cursor/skills/`, `~/.agents/skills/` | `agent -p --output-format json` returns `session_id`; transcript path unconfirmed |
| Copilot CLI | `.github/hooks/*.json`, `~/.copilot/hooks/*.json`, also reads `.claude/settings.json`; sessionStart, sessionEnd, userPromptSubmitted, pre/postToolUse, postToolUseFailure, agentStop (can block), subagentStart/Stop, preCompact | `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`, `~/.copilot/instructions/` | MCP in `~/.copilot/mcp-config.json` or `.mcp.json`; elicitation as a `notification` hook type | `~/.copilot/skills/`, `~/.agents/skills/`, `.claude/skills` | `~/.copilot/session-state/<id>/events.jsonl`; hooks carry `sessionId` |

What this buys the chart:

- **One payload reader, five installers.** Four harnesses speak the Claude
  Code convention; OpenCode needs a plugin shim that calls the same verb.
- **`.agents/skills` is the one drop location** honored by Gemini CLI,
  OpenCode, Cursor and Copilot CLI (Codex unconfirmed), beside
  `~/.claude/skills` for Claude Code.
- **Elicitation is not floor.** The question record stays the primary ask;
  elicitation is enrichment where the harness supports it (ticket 14).
- **A session id is floor; a transcript path is not.** Every harness exposes
  the id, so the sessions ledger keys on it; spend from transcripts is only
  possible where one is readable (Claude Code, Codex, Gemini CLI, OpenCode,
  Copilot CLI), which bounds ticket 12.
