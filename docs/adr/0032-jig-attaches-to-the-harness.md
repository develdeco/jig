# jig attaches to the harness: boxes go, seams stay

**Amended by [ADR 0034](0034-jig-advises-context-and-runs-no-context-tool.md):** `jig context` returns no code a graph links to the goal. Running a context tool for the session is a box, so jig runs none and says the code is the session's to find with its own tools; the verdict map's row for 0026 reads retire.

jig began as a dispatch loop. `jig run` and `jig solve` started a coding session for each ready slice - a `claude -p` subprocess or a herdr agent in a pooled worktree - handed it a `slice.json`, and read back a `result.json`. Around that session jig built a box: a headless permission model with a screen hook (ADR 0008), a model staircase and efforts (ADRs 0019, 0023), a prompt that taught the session to test narrowly (ADR 0018), a wait loop, and a resume turn when the oracle came back red (ADR 0020). It launched three more sessions of its own, an intent summarizer, a reviewer and a pick session for the demo. Everything jig knew about the work it knew because it had started the session that did it.

The person building a ticket does not work that way. They sit in their own harness session (Claude Code today; Codex, Gemini CLI and others by the same floor) with that harness's tools, subagents, background work and model choice, and jig competed with all of it. The dispatch path and the hand-built path were a choice the person had to make, and the second one got none of jig's guarantees. The digester chart (decisions 1 to 34, confirmed 2026-10-23) settles it the other way: jig is attached to the session the person already runs, and the guarantees move to where that session leaves traces.

## Decision

**jig attaches to the harness; it does not spawn the model.** It starts no coding session, attended or not, and chooses no model, effort or tool grant. An unattended run is a harness session that someone else starts (a person, a crontab, a crew's scheduler) whose claim carries `--unattended`.

**Boxes go, seams stay.** The two words sort everything the dispatch path did:

- A *box* is jig containing, driving or speaking for a session: the permission model and its screen, the staircase and efforts, the dispatch prompt and `slice.json` and `result.json`, the lease a builder works in, the wait loop, the resume turn, the herdr backend, and the sessions jig launched to summarize, review and pick. A harness has its own permissions, shell, background behavior and model choice, and a box around it can only get in its way.
- A *seam* is a point where the work is checked or recorded in a way that a person, another tool or a later step can rely on: green is what jig proved by running the slice's oracle, intent keeps its provenance, a review is independent of the builders, a gate round is a record, asks are records, ids are minted by jig, a ticket's branch is recorded, the store is the only API. A seam does not depend on who ran the session.

The seams stay and keep their rules. What changes is when they fire: not at dispatch, when jig starts a session, but at commit, when jig sees one.

jig is reached through four doors and is never in the loop:

- **Hooks** fire on what the session does. One verb, `jig observe`, takes a harness's hook payload through a per-harness adapter into a neutral event, and a global git `post-commit` hook calls the same core. It exits 0 and prints nothing whatever it is handed, because a hook that complains does harm.
- **Skills** tell the session when to reach for jig: `here`, `intake`, `build`, `review` and `publish` for a project, `jig-init` and `jig-attach` as global helpers, all embedded in the binary.
- **MCP** (`jig mcp`) serves the commands as tools to a harness, and puts a question to the person through the client's own form when it has one.
- **The CLI** is the floor under all three. Every verb is callable from the middle of someone else's session against a working tree jig does not own, and is idempotent, because hooks fire twice. Every adapter is a thin caller.

Everything a door does is a subcommand of the one binary (ADR 0002), a command, flags, output and an exit code (ADR 0006). A hook entry, a skill, a git hook, a hint and an OpenCode plugin are owned by detection, not by a record: jig recognizes what it wrote by its command, stamp, marker or first line, and an uninstall takes out only that. jig records nothing about a directory that is in no jig project.

## What the seams do now

**Where the work is.** `locate` is the one place a directory becomes a project and a store: the `--store` flag, a `jig.yaml` marker found walking up, the machine map by path (a linked worktree reads as its clone) and by origin remote, a store directory, else none. A clone found by marker or remote is mapped on the spot and a live mapping is never replaced; `jig ignore` is the person's veto; `jig here` is silent outside a project, so a hook can run it anywhere. A project is a `jig.yaml` at its repo's root (`jig init`) and a sibling store whose own config is `store.yaml` at schema 3 (`keys`, `trackers`, `platform`, `gate`, `context`, `projects`); a v2 store with its `project.yaml` opens unchanged and moves up with `jig store migrate`, one step at a time, so ADR 0031's "enforced on every command" for schema 2 reads "schema 2 up to the current one is accepted". A project is local until `jig share` publishes it. The ticket is the branch: a name `<id>-<slug>` binds a branch to its ticket with no claim (the leading id, an alias or a mirrored issue number), the default name is derived and never recorded, and a branch a session chose for itself is recorded by its claim (ADR 0013).

**Green is what jig ran, at the commit.** ADR 0020's rule stands and its trigger flips. A `Jig-Slice` trailer, or a claim as the override, names the slice a commit belongs to; the post-commit hook and the harness's PostToolUse both start `jig verify`, which is idempotent per (commit, slice). It runs the slice's oracle on a fresh detached worktree of the operator's clone, in a detached process, and journals what the frontier once journaled (`verify-pending` first, then `oracle`, `recorded`, `verified`), so the gate's reuse of an oracle pass (ADR 0021) and `BuiltCommits` read it unchanged. The session is never blocked on a test run. A red run is a record the session reads at its next turn, not a resume. The run bound is the manifest's `timeout:`, a wedge guard and never a cost cap; recordings of the scenarios the run executes are named per verification (ADR 0029). jig counts only its own run: counting the session's own is not built.

**The session is told, once, what it needs.** What `slice.json` carried is pulled: `jig context` returns the brief sections a slice is cut from, the commits and files of the slices already verified (found by their trailer), the oracle and how long jig's last run took, the failed attempts, the code a graph links to the goal and the slice's questions and notes (ADRs 0024, 0025 and 0026 now describe this verb). `jig claim` checks the ticket is ready, refusing an unattended claim and warning an attended one. A question parks its slice and an answer sends it back to work, by `ask --wait`, the Stop hook or the next prompt, with its provenance (person or agent) recorded. Three corrections come from facts the journal already holds, with no second record: a stall (the same failure signature twice), a brief that changed under verified work, and a commit outside every slice workspace. They are advisory in an interactive session and enforced in an unattended one, where a Stop is refused once per fact (`stop_hook_active` always ends it): a verification running, tracked files uncommitted, a failed oracle or a correction, and a ticket a person approved that is not yet published. Planning is a partnership with the store: `jig recall` shows what exists before a ticket is written, and `jig validate` saying `valid: yes` is the approval, pinned to the brief's hash.

**The gate is a record, and independence is by provenance.** ADR 0017's rules and ADR 0007's division stand, and jig launches no reviewer. `jig gate` opens a round and starts nothing; a fresh-context agent that jig did not start reads it with `jig review context`, reports with `jig finding`, and closes it with `jig review done`, and the round is recorded when that call returns. The open round lives in the store's `work/`, and the reviewed copy is the operator clone's committed branch against origin's. A round the reviewer built part of is `not-independent`, judged from the sessions on the claim, verification and ledger lines, and publish refuses it. The intent summarizer is gone: with no brief and no explicit intent, the request names an excerpt of the author's matching local session (found through the machine ledger's record of the sessions that worked the ticket, else matched by file overlap, by `internal/intent`) and the reviewer reads it, while ADR 0012's provenance and precedence stand. Triage lost its prompts: `jig triage` records keep or dismiss, by a person at a terminal or by an agent.

**Publish is the harness's push with jig's body and record.** `jig publish` has four forms: bare, which pushes the reviewed head unchanged and opens or updates the PR after a confirmation; `--body`, which renders the PR body and review notes and changes nothing else; `--pr <url>`, which records a PR somebody else opened and checks it against the branch and the reviewed head; and `--approve`, which records an approval of the head. It never reconciles, squashes or commits on the branch, so the person's branch never diverges from what they pushed, and jig adds no check to a push the harness makes itself. The demo is a picks file written from the build's recordings, not a session of jig's own.

**The store names the person and survives several writers.** Every commit jig makes in a store is authored by the identity git resolves in the store clone, and a clone with none is refused (`IDENTITY_REQUIRED`); jig has no identity of its own. A store is in single-main mode, as it always was, until `jig store migrate --writers` moves it to writers mode: each clone pushes only `writers/<name>` and merges the others read-only with a union driver for NDJSON files, and the facts that only one writer may hold, a minted id (ADR 0028) and the driver of a ticket, go through an append-only claims log on a branch of its own that every writer pushes by compare-and-swap. No writer pushes main or rewrites another branch, and a rewritten branch is refused and reported, never repaired. The driver lives in that log and not in the machine ledger, which is machine-local. The in-process git of ADR 0011 still commits, and fetches and pushes of a history with merges go to the git program.

**Events are records in two logs.** The ticket journal holds what happened to a ticket and goes to the store; the machine ledger holds sessions, tool calls and commits seen on this machine, keyed by the harness's session and agent id, and is not a store subtree (ADR 0003 holds: hooks, skills and MCP are more readers of the same subtrees under one `schema_version`). `jig events` is the one reader of both, and `jig tui` a reader of that stream that acts only by running verbs; neither makes a record true. Cost is two records and a note, never a wall: a `spend` line per session summed from the transcript, a per-ticket `budget.yaml`, an advisory near or over note, and nothing is ever stopped for money.

## What is deliberately not here

- `jig run`, `jig solve` and `jig _screen`, the frontier package, the herdr backend, the `router` and `fleet-liaison` skills, the staircase and efforts for builders, and the three sessions jig launched. The reviewer evaluation (ADR 0009) still launches reviewers on `internal/session`, and the binary links none of that code (`lint TestCLIDoesNotLinkSession`).
- What survives of ADR 0008 is advice for whoever launches a review session, which is not jig: capability against instructions (no settings from the code under review, `CLAUDE.md` from the committed tree), and a post-round check where the read-only guard used to be.

## Not built

These were named in the chart and no ticket of the build carries them:

- `jig watch`, a fog item.
- Counting the session's own run of the oracle; the `source` field on the `oracle` line stays for it.
- Cloud sessions and Remote Control (chart decision 19: local first; `jig setup --cloud` only prints an environment script), multi-repo v2 stores in `jig store migrate`, and a live check of any harness but Claude Code (the other adapters and hook files are read from each vendor's documentation and pinned by golden files).

## Appendix: ADR verdict map

*Keep*: the rule holds as written. *Invert*: the rule holds, its mechanism flips from dispatch time to commit time or from jig-launches to jig-listens. *Retire*: the rule was a box.

| ADR | Verdict | Under attached mode |
|---|---|---|
| 0001 no orchestration framework | keep | Disk state and resume-by-recompute stay; the while-loop goes with `jig run` (decision 26), and the frontier becomes a read verb ("what is unverified, what is next"). |
| 0002 one binary plus skills | keep | Skills become the entry point; "dispatch" and `_screen` drop from the binary's list; the git hook, the SessionStart hook, the MCP server, `init`, `share`, `setup` with `attach` as its alias, the piece verbs, `repos` and `doctor` are subcommands of the one `jig`, and the helper skills ship in it beside the project skills. |
| 0003 store schema is the API | keep | Hooks, skills and MCP are more readers of the same subtrees under the same `schema_version`; the sessions ledger is deliberately not a store subtree. |
| 0004 disk-only pipeline seam | keep | Becomes literally true: the verifier is a separate process started by git, sharing nothing with the session but the store and the sha. |
| 0005 tracker is a projection | keep | Unchanged. |
| 0006 Go packages are internal | keep | Strengthened: every new door goes through commands, flags, output and exit codes; "unmapped means silent exit" is an exit-code contract. |
| 0007 gate owns bookkeeping, not judgment | keep | Unchanged for the gate; the deferred "files the reviewer opened" evidence becomes available wherever the harness emits PostToolUse records. |
| 0008 headless permission model | retire | The harness owns permissions, and `_screen` retires with the box (decision 22). What survives is advice to whoever launches the review session (decision 17: not jig): capability versus instructions (no settings from the code under review, `CLAUDE.md` from HEAD's tree); the read-only guard inverts to a post-round check. The kill mechanics move to the background oracle runner as a wedge guard, never a cost cap. |
| 0009 review eval, structural gold | keep | The gate's default model is named outright rather than derived from the builder staircase. |
| 0010 dependencies live on the ticket | keep | The edges feed recall and validate; "a stacked child starts only on a clean parent gate" is enforced at claim or attach time, advisory when interactive. |
| 0011 the store runs git in process | keep | Motive strengthens: a post-commit hook must return fast, so cheap store writes matter more. |
| 0012 intent has a provenance | keep; inference inverts | The precedence and provenance stand. Inference stops searching transcripts by file overlap: the ledger knows which session holds the ticket, and the session can state intent at claim. The ledger half is built for tickets whose sessions ran hooks, with the search kept as the fallback. |
| 0013 a ticket's branch is recorded | keep | Adoption becomes the ordinary shape; the build-lease cases of `chooseBuiltCopy` collapse to the clone or origin; "the commits jig built" reads "the commits jig verified". The default name is `<id>-<slug>`, the id from the tracker that names branches, and `jig/<id>` goes (decision 31). |
| 0014 demo session at the gate | retire | Already superseded by 0029. |
| 0015 fix budget and risk floor | keep | "A person at a terminal" is the attached session's owner; the fix-slice size cap loses its fresh-context rationale and stays only as a readability bound. |
| 0016 tests at agreed seams | keep | The `Seams` section is pulled by the context verb instead of dumped into `slice.json`. |
| 0017 the reviewer reads, the oracles test | keep | `reused_from` is fed by commit-time evidence. |
| 0018 builders test narrowly, no background | retire | The harness keeps its shell and background behavior. The background verification answers the waiting problem; "uncommitted tracked edits at Stop" answers the lost-run problem; narrow tests become skill advice. |
| 0019 builders open on Sonnet and climb | retire the staircase; keep the failure classification | jig chooses no model, ever (decisions 26, 27). `journal.FailedAttempts` becomes correction's input ("same failure twice"). |
| 0020 jig runs the oracle at green | invert | The trigger flips from `result.json` to an observed commit; the red run becomes a record the session reads at its next turn; the fix budget parks for a person instead of climbing; the run bound is re-derived. |
| 0021 the gate reuses an oracle pass | keep | The same-tree condition becomes structural on a detached worktree; a counted in-session run carries its source on the line. |
| 0022 the reviewer confirms by id | keep | Unchanged. |
| 0023 dearest rung and efforts | keep for the reviewer; retire builder effort | The harness's model and effort are recorded in the ledger, not chosen (decision 27); the reviewer's rung is a seam of the review record that whoever launches the reviewer honors. |
| 0024 builders are told the oracle's time | invert | The background run stamps `seconds`; the context verb returns it by `LastOracleSeconds`'s rule. |
| 0025 a builder reads earlier slices | invert | The context verb returns the same fields; a slice's files are the commits that name it, replacing attempt ranges. |
| 0026 code graph starting points | invert | The graph runs on demand in the mapped clone; the `graph` line moves to the ledger. |
| 0027 jig's testing method | keep | "At a builder's green" becomes "at each commit naming a slice"; the intake skill gains weight. |
| 0028 jig mints every id | keep | Unchanged; `ticket new` and `graduate` run from inside the session; the mint lands on the claims branch (decision 12) and is authored by the person (decision 32). |
| 0029 demos are recordings | invert | Recording moves to the background verification, run named per verification; a counted in-session run records nothing. |
| 0030 mirror from inside every command | keep | Checkpoints come from verification writes and verbs; read-only verbs pay no sync. |

Where the build landed differently from this map:

- **0010**: `jig claim` checks that a stacked parent's gate is clean (`CLAIM_STACKED_PARENT_NOT_CLEAN`, a refusal unattended and a warning attended), while `jig next` waits for a stacked blocker's `publish-done`. A merged blocker waits for its work to merge, not only for `publish-done`, and a stacked child is told where its branch starts and when the parent moved or merged.
- **0012**: the summarizer launch is gone. The excerpt is found first through the machine ledger, which records the transcript of each session that worked the ticket, and only when that yields nothing readable is it matched to the change by file overlap (`internal/intent`); the reviewer reads it either way. A session stating its intent at claim is not built.
- **0019**: correction counts equal failure signatures on `oracle` lines, not `journal.FailedAttempts`, which no verb calls any more. The `staircase:` and `builder_effort:` keys still load, so existing configuration keeps working, but nothing in the binary reads them.
- **0015, 0016, 0017, 0021, 0022, 0027, 0030**: kept, as the map says.
- **0011, 0013 and 0031** carry an *Amended* line for what the build changed in them; **0008, 0014, 0018 and 0019** are superseded; **0012, 0020, 0023, 0024, 0025, 0026 and 0029** carry a one-line status that names the inversion.
