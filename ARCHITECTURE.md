# Architecture

jig is one binary plus a set of session skills, attached to the harness session a person already runs (Claude Code today; Codex, Gemini CLI, Cursor, GitHub Copilot and OpenCode by the same floor): the binary owns machinery (state, verification, git, the hooks that observe), skills own judgment (reading a brief, writing code, reviewing a diff), and the harness owns the session, its tools and its model. jig starts no coding session and chooses no model ([ADR 0032](docs/adr/0032-jig-attaches-to-the-harness.md)). The two only agree through the store on disk - the schema below *is* the API between them, and between jig and any future tooling that reads a ticket's history.

## Pipeline

A ticket goes from a brief to a pull request in five stages. Each is a verb the session runs, or a record a hook writes on its behalf; none starts a session.

```
brief      author brief.md + slices.yaml (a person with the intake skill, after `jig recall`);
  │        `jig validate` checks them against the store and, when valid, approves the brief
  │        reads:  the store (other tickets, charts' fog, platform/, ledger.md)
  │        writes: <ticket>/brief.md, illustration.html (optional), slices.yaml, approval.yaml
  │                (pinned to the sha256 of brief.md, or of brief.md and illustration.html),
  │                journal.ndjson (approval)
  ▼
claim      `jig here`, `jig next`, `jig claim`, `jig context`: where the work is, what is ready,
  │        taking a slice, what a builder needs to start
  │        reads:  slices.yaml, slices/<id>.state, journal.ndjson, questions/*.md
  │        writes: slices/<id>.state, start.<repo>.sha, journal.ndjson (claim, release, question,
  │                answer, note), the claims log (a driver; an id at mint)
  ▼
build      the session builds in the person's own clone and commits with a `Jig-Slice:` trailer;
  │        git's post-commit hook and the harness's PostToolUse start `jig verify` in the
  │        background: the slice's oracle on a detached worktree of that commit (ADR 0020)
  │        reads:  the commit, the manifest at that commit
  │        writes: slices/<id>.state, journal.ndjson (verify-pending, oracle, recorded, verified,
  │                verify-error, correction, spend), <jig home>/ledger/ledger.ndjson (session,
  │                tool, hook, git-commit, note-delivered), <jig home>/logs/, and under
  │                <jig home>/evidence/ (not in the store) the scenarios' recordings
  ▼
gate       `jig gate` opens a round: oracles re-verified (a pass jig recorded on the same tree is
  │        reused, ADR 0021) and a request written; a fresh-context reviewer that jig did not
  │        start reads it (`jig review context`), reports (`jig finding`) and closes it
  │        (`jig review done`), and the round is recorded
  │        reads:  brief.md or intent.md (resolveIntent), slices.yaml, journal.ndjson,
  │                ticket.yaml (branch), gate/round-N/findings.yaml (cumulative fold)
  │        writes: work/gate.round-N.{review.json,open.yaml,findings.ndjson,result.json},
  │                gate/round-N/{findings.yaml,findings.md,report.yaml,diff-changelog.md},
  │                evidence/round-N/*, slices.yaml (fix slices, findings, from_gate: N),
  │                intent.md (--intent/--doc), ticket.yaml (branch) and start.<repo>.sha
  │                (--branch adoption only), and under <jig home>/evidence/ (not in the
  │                store) the round's request copy and recordings.json for the reviewer
  ▼
publish    `jig publish`: the PR body and review notes rendered for the reviewed head of each
           repo (--body), the head pushed unchanged and the PR opened or updated, one for each
           repo the ticket builds in (bare), a PR somebody else opened recorded (--pr <url>
           --repo r), or an approval of the head recorded (--approve);
           nothing is reconciled, squashed or committed on the branch
           reads:  gate/round-N/*, journal.ndjson, ticket.yaml
           writes: changelog/{<ws>.md,consolidated.md}, pr/{evidence.md,<repo>.md,
                   review-notes[-<repo>].md}, ledger.md, platform/contract-index.md,
                   journal.ndjson
                   (pr-body, publish-approved, publish-picks, pr, changelog, publish-done),
                   and under <jig home>/evidence/ the picked files and the picks request
```

**Phases.** The stages are the ticket's four phases: brief (the `intake` skill), build (`build`: the claim and build stages), review (`review`: the gate) and publish (`publish`). The person asks for each by prompt, one at a time by default ("Build SHOP-1"), or names several for an automatic run ("Take SHOP-1 from brief to PR"). Each phase skill starts with `jig next <T>`, which says which phase the ticket is at, whether it can start and the prompt that asks for it; it stops with that prompt when the ticket is not at the skill's phase, and ends by relaying the prompt of the next one. A prompt that named later phases makes the skill go on instead of stopping, and a refusal ends the run. The brief's approval and the publish approval stay the person's unless the prompt delegates them ("go with your recommendations", "... to PR"). A delegated brief approval is recorded with agent provenance; a delegated publish takes the hand-off way (the session's own push, then `jig publish --pr`), which asks for no approval. Where a ticket stands is derived, never recorded: `internal/phase` reads it from the journal lines the stages already write ([Where the work is](#where-the-work-is), [ADR 0033](docs/adr/0033-phases-are-asked-for-by-prompt.md)).

**The path it replaced.** Before the digester, `jig run` (and `jig solve`, which chained run, gate and publish) was the second and third stage. The two paths side by side:

| Stage | Dispatched (retired) | Attached |
|---|---|---|
| build | `jig run` handed each queued, unblocked slice to a build session jig started in a lease, with a `slice.json`, a permission box and a model chosen by a staircase, and read back a `result.json` | the session is the person's own; it takes a slice with `jig claim`, pulls what it needs with `jig context`, builds in its own clone and commits with a `Jig-Slice:` trailer |
| green | the oracle ran when the builder reported green, in the lease, and a red run resumed the same session | the oracle runs in the background on each commit that names a slice, in a detached worktree, and a red run is a record the session reads at its next turn |
| gate | jig launched a reviewer session on the dearest rung, an intent summarizer first, and prompted for triage | `jig gate` opens a recorded round; any fresh-context agent reports it; independence is checked from who built |
| publish | reconciled, revalidated, wrote the memorize commit and squashed on the branch, launched a pick session, then pushed | pushes the reviewed head unchanged, or only renders the body, or records a PR; the demo is a picks file |

Every branch is now built outside jig, so there is no separate entry for one: `jig claim` records the branch the work is on when it names no ticket ([A ticket's branch](#a-tickets-branch)), and `jig gate <ticket> --branch <name>` still adopts a branch for a ticket that has no brief and no slices at all, which `publish` then pushes as it is. See [A ticket's branch](#a-tickets-branch) and [Publish](#publish).

`verify`, `observe` and `verifydeliver` (verification, the hooks, and the gate and publish) share no in-memory state at all: the verifier is a separate process started by git or by a hook, and the store on disk and the machine ledger are the entire interface between them and the session. That's [ADR 0004](docs/adr/0004-disk-only-pipeline-seam.md) made literal, and deliberate: any of them is splittable into its own binary later with a `git mv`, no refactor required.

## The doors

jig is reached reactively, through four doors, and each is a subcommand of the one binary ([ADR 0002](docs/adr/0002-one-binary-plus-skills.md)): a hook, a skill, an MCP tool and a command line all end in the same verbs, with the same flags, output and exit codes.

**Hooks.** `jig observe --harness <h>` (hidden) reads a harness's hook payload on stdin, hands it through that harness's adapter to `internal/observe` as a neutral event (`Harness, Kind, Session, Agent, Cwd, Tool, Command, Exit, Files, Model, Transcript, StopActive`, with kinds `session-start`, `prompt`, `tool`, `stop`, `session-end` and `post-commit`), and prints what the harness reads back: context for the model, a message for the person, a refusal to stop. It exits 0 and prints nothing whatever it is handed - a payload that is no payload, an unknown harness, a store that will not open - because Claude Code puts a SessionStart or UserPromptSubmit hook's stdout into the model's context and reads exit 2 as a refusal; what went wrong is one line in `<jig home>/logs/observe.log`. The adapters are the only code that knows a harness's shape: claude, codex, gemini, cursor, copilot, opencode, and `neutral` for a script or a crew with no hooks (`jig observe --event <kind> --session ... --command ...`). Only Claude Code's is proven against the real program (`e2e/hooks_live_test.go`, gated by `JIG_LIVE_CLAUDE`); the others are read from each vendor's documentation and pinned by golden files. Git's `post-commit` reaches the same core through the hidden `jig githook post-commit`.

| Event | What the core does | What the harness reads |
|---|---|---|
| session-start | registers the session in the ledger (harness, agent, cwd, clone, ticket, slice, model, HEAD), renders `jig here` for the directory, and sets `JIG_SESSION` to the harness's session id for the session's later shell commands. The harness sets it where its adapter can: Claude Code sources the file `CLAUDE_ENV_FILE` names before each Bash command, and jig writes only to the one Claude Code made for the session, in a directory named after its id. Elsewhere a line of the context asks the agent to pass the id | context |
| prompt | a `hook` ledger line; a `session-touch` when the ticket or slice changed, the first ticket of a session that started on none included, carrying the harness and model the session started with (so a ticket's stream, which leaves out lines about no ticket, still names the session); the notes the session has not heard | context |
| tool | when the tool ran a command and HEAD moved, starts the verification of the new commit; a `session-touch` as for a prompt; a `tool` ledger line; the notes not yet heard | context |
| stop | the findings below: a message to the person, or, for an unattended session, a refusal to stop | message or block |
| session-end | ends the session; a final call to every note source; a `note` journal line per question still open | none |
| post-commit | a `git-commit` ledger line, then the verification of HEAD | none |

A directory in no jig project is left alone: no ledger line, no output. Only `JIG_SESSION` is still set there, for a project the session works in later. The core is stateless - the machine ledger is its only memory of what a session was told and where HEAD was, read back from the end of the file (`ledger.ReadBackward`) - so a hook costs the distance to what it asks for. A commit is verified once whoever sees it first: before `verify.Spawn` the core looks for a `verify-spawned` ledger line of the same commit and slice, because git's hook starts the worker inside `git commit` and the PostToolUse hook runs right after, before the worker has written a journal line.

**Skills.** The project skills (`here`, `intake`, `build`, `review`, `publish`) tell a session when to reach for jig and what to run, and the four phase skills (`intake`, `build`, `review`, `publish`) begin with `jig next <T>`, stop and name the prompt that does the work when the ticket is not at their phase, and end by relaying the next prompt; the helpers (`jig-init`, `jig-attach`) help it start a project or attach a repository; `retro` and `platform-sync` are installed by name. They are embedded in the binary and `jig skill <name>` prints one as this binary carries it. In a project's untracked form a repo holds one stub named `jig` whose body sends the agent to `jig here` and `jig skill <name>`, so a repo never holds a stale copy of a skill.

**MCP.** `jig mcp` is a hand-rolled newline-delimited JSON-RPC 2.0 server on stdio. It serves nine tools (`here`, `next`, `context`, `claim`, `ask`, `answer`, `note`, `observe`, `status`), each the in-process command of the same name run through `run`, so a tool and its command cannot differ; a tool has no store or project argument, so `--store` and `--project` pin every call. `ask` records the question first and then puts it to the person through `elicitation/create` when the client declared form elicitation; an accepted answer is the only caller of `answer --as person`, and anything else leaves the question open.

**The CLI** is the floor under the other three. `jig here --json` and `jig observe` with neutral fields are the contract a crew with no hooks calls itself, and an explicit `--slice`, `JIG_SLICE`, or a passed-in or minted session id (`JIG_SESSION`) beats any inference from the directory and the branch.

**Installation** is `jig setup` (and `jig attach`, its form for the project the working directory is in), with the pieces as verbs of their own: `jig skills`, `jig hooks`, `jig githook`, `jig hint`, and `jig doctor`, which reads and never writes. The scope is always spelled out (`--global`, `--project <name>`, `--here`), never inferred, and what a piece writes depends on it:

| Piece | `--global` | `--here` / `--project` (untracked, the default) | with `--tracked` |
|---|---|---|---|
| skills | full text in `~/.claude/skills/` and `~/.agents/skills/` | one stub, `jig`, in `.claude/skills/` and `.agents/skills/`, each file in `.git/info/exclude` | the full text, not excluded |
| hooks | the harness's global settings file | the harness's project file (Claude Code's `.claude/settings.local.json`), excluded | `.claude/settings.json`, not excluded |
| githook | git's global `core.hooksPath` | each clone in scope: covered by the global wrappers, or a marked block in its post-commit | the same: git hooks are never tracked |
| hint | `~/.claude/CLAUDE.md`, and the Codex and Gemini memory files when their directory exists | `CLAUDE.local.md`, excluded | `AGENTS.md`, and `CLAUDE.md` when it exists and does not import `AGENTS.md` |

The global git hook is `core.hooksPath` pointing at `<jig home>/hooks`, which holds POSIX sh wrappers for every client hook except `reference-transaction` and `fsmonitor-watchman`. Each wrapper runs the repository's own hook, then the earlier global hook path (saved to `hooks/.previous`), then, for `post-commit` only, `jig githook post-commit`. Server-side hooks are not wrapped, and `jig doctor` warns about a clone that has one. A settings file, a memory file and a hook entry are the person's: jig owns the entries it wrote and recognizes them by detection (a hook entry by its command, a skill by the stamp on its last line, a hint by its marked block, the OpenCode plugin by its first line), and an uninstall takes out only those; the editors keep the person's keys, hooks and order, refuse a file they cannot read, and never write over it. The files that are the person's are written after one yes (`--yes`, or a prompt at a terminal; without either, `NEEDS_INPUT`). `jig setup --cloud` prints the environment script for a cloud session and writes nothing.

## Where the work is

`internal/locate` is the one place a directory becomes a project, a store, a repo, a branch, a ticket, a slice and a session, and says how it found each. `Locate(jigHome, cwd, opts)` returns the first of these that answers:

1. `--store`: the project of that store; the repo is the mapped clone that holds the directory.
2. The ignored list (`jig ignore`): a directory under an ignored one is none, whatever a marker, the map or a remote says.
3. A `jig.yaml` marker found walking up (`project.FindMarker`, which passes over any directory named `.claude`, so the per-repo manifest is never read as a marker). The store is the machine map's entry for the project, else a local path the marker names, else the sibling `<name>-tickets`. For an orchestrator, the repo is the one of `repos[].path` that holds the directory.
4. The machine map (`projects.yaml`) by path: the longest mapped clone or project root that holds the directory, a linked worktree reading as its clone.
5. The machine map by origin remote (`NormalizeRemote` ignores scheme, user, port, case, `.git` and trailing slashes).
6. A store directory: a `store.yaml` or `project.yaml` here, or the sibling store `jig init` makes.
7. None, with no error.

A clone found by marker or remote is added to the machine map on the spot (through `project.UpdateMachine`, a locked read-modify-write) unless it is under an ignored directory or `--no-map` is set; a live mapping is never replaced, so a second clone of a mapped repo is recognized and left unmapped. Then the branch (`git symbolic-ref`), the session (`--session`, else `JIG_SESSION`, else the id the harness sets in its agent's commands: Claude Code's `CLAUDE_CODE_SESSION_ID`, which any of its shell tools carries), the ticket (an argument, else the branch's: `Store.TicketForBranch` strips one `jig/` and tries the leading `KEY-N`, an id or an alias, then a leading issue number) and the slice (`--slice`, else `JIG_SLICE`, else the session's latest unreleased `claim` line) are filled in. Every verb that needs a store resolves it through `resolveStoreChecked`, which asks for the store alone: no git call at all inside a mapped clone. `jig here` is silent outside a project and never syncs, pushes or calls the mirror, which is what lets a SessionStart hook run it anywhere.

`internal/ready` is the one definition of readiness. `ComputeReady` and `Schedule` give the slices that are queued with every blocker green, batched per repo through a workspace-to-repo map (`Place`: in a project of one repo every workspace is that repo's; in a project of several a slice writes `<repo>/<workspace>`, or a bare workspace that exactly one repo's manifest declares; one that two repos declare is ambiguous and one that none declares is unplaced, and both belong to no repo until the slice names one); `Tickets` classes a ticket `closed` (it has a `closed:` entry in `ticket.yaml`, which wins over every other state, offers no slices and lists no blockers), `done` (a `publish-done` line), `blocked`, `new`, `ready`, `green` or `active`. A `merged` blocker holds its ticket back until its work has merged, not only until it published (`Ticket.Merge`, judged for each repo the blocker builds in by `JudgeRepo` over the facts `internal/mergefacts` reads: the recorded pull request when the host can say, else the blocker's branch tip being an ancestor of `origin/<target>` in the mapped clone; a squash or a rebase is never taken for a merge, and `Status.Why` says what each blocker is missing). A closed blocker is judged by its reason: `covered` and `superseded` release the tickets that wait for it, `not-planned` holds them. A `stacked` blocker waits for `publish-done` in `jig next`, while `jig claim` checks only that a stacked parent's gate is clean. `jig claim` refuses when unattended, and warns otherwise, on a brief nobody approved or one that changed after its approval (`CLAIM_NOT_APPROVED`), a published blocker whose work has not merged (`CLAIM_BLOCKED_MERGED`, naming the fact and what meets it), a blocker closed as not planned (`CLAIM_BLOCKER_NOT_PLANNED`) and a stacked parent whose gate is not clean (`CLAIM_STACKED_PARENT_NOT_CLEAN`); a closed ticket is refused outright (`TICKET_CLOSED`), as it is by `jig gate`, `jig publish`, `jig requeue` and `jig review context`. `jig next` with no ticket lists the tickets and then the ready ones in the owner's priority order (a `next` table: `rank:` in `ticket.yaml`, set by `jig rank`, lowest first, then id, the unranked last), flags a rank that two ready tickets hold, warns of each pair of ready tickets whose slices build in the same workspace of a repo (`workspace_overlap`), so a scheduler does not run them together, adds a `waiting` table naming the fact each blocked ticket's blockers lack, and warns when two tickets' local branches change one file. With a ticket it prints the ready slices by repo, the `start` of the ticket's branch in each, and the facts that hold a blocker back (`blocked_by[n]{ticket,kind,fact}`: `it has not published`, `its PR is open (<url>)`, `its PR was closed without merging (<url>)`, or `no merge on record`, with the command that records one; an answer the host gave earlier is dated). A ticket whose files cannot be read (a `slices.yaml` that does not parse) is listed by `jig next` and `jig recall` as `invalid`, with the problem (which names the file: its `slices.yaml` or a slice's state), its blockers and whether it was published still read, rather than failing the listing of the whole store, which `jig tui` reads.

`internal/phase` is the one derivation of where a ticket stands. `Derive` takes a ticket's journal lines and a facts struct (its slices and their states, the brief's approval, the outstanding asks, the gate rounds and the open one, the unpublished blockers, whether it is closed) and returns the four phases, brief, build, review and publish, each done, current or pending with its sub-steps (`slice:<id>`, `round:<n>`, `asks`, `body`, `approval`, `pr`, and so on); the current phase; whether it can start (`PHASE_BLOCKED`, `PHASE_BRIEF_NOT_APPROVED`, `PHASE_NOT_BUILT`, `PHASE_ROUND_OPEN`, `PHASE_ASKS_UNDECIDED`, `PHASE_NOT_REVIEWED`, `PHASE_DONE` and `PHASE_CLOSED` say why not); and the prompt that starts it ("Plan T", "Build T", "Review T", "Publish T"). A phase is done only when every phase before it is. Brief is done when approved, or when the work started without an approval ("never approved": an attended claim on an unapproved brief warns and goes through, and the hints do not send the person back for it); build when every slice is green and none is being verified; review when the last closed round is clean, no build line came after it and no round is open; publish at `publish-done`. `Ticket.For` answers for a phase the caller names, with a warning, never a refusal, when it is done. The package imports neither `internal/store` nor `internal/verifydeliver`, so `internal/tui` can import it: `cmd/jig` gathers the facts from the store (`phaseFacts`) and the TUI builds the same facts from the output of `jig status` and the journal lines it holds, and `TestTUIAndNextAgree` pins that both derive the same phase. `jig next <T>` prints the result (`phase`, `ready`, `prompt`, and `code` and `reason` when the phase cannot start; `--for <phase>` asks about a named phase), and `jig status`, `jig here`, the claim refusals and `GATE_NOT_GREEN` end their help with the same `ask: <prompt>`. There is no phase verb and no record of a phase starting or ending: the claim, gate-open, gate-reviewer, pr-body, publish-approved, pr, publish-done and approval lines already mark every boundary ([ADR 0033](docs/adr/0033-phases-are-asked-for-by-prompt.md)). `jig status` and `jig here` derive the phase without asking the repo host about a blocker's pull request, so `jig next <T>` is the exact answer.

`internal/stack` plans a stacked ticket (a `stacked` blocker). For each repo the ticket builds in (`ready.BuiltIn`) `stack.Read` says where its branch starts and where its pull request goes: in a repo the parent builds in too, while the parent has not merged there, both are the parent's branch as the clone holds it (local, or `origin/<branch>` when only the origin has it or the origin's is ahead); elsewhere, and once the parent merged there, they are `origin/<target>` and the target. It also says whether the parent's branch moved past the child's (a *restack*: the child merges the parent's head with `git merge <parent branch>`, which jig never performs) and whether the parent merged while the child lacks the target's head (a *retarget*). The plan is advice computed on read from `ticket.yaml`, the slices, the clones' refs and the parent's merge fact, and records nothing. `jig next <T>` prints it as a `stack[n]{repo,parent,start_from,pr_target}` table after `blocked_by`, with a note for a restack, a retarget or a parent's branch the clone lacks (the `ready` table's `start` is the plan's `start_from`); `jig here` prints it as a `stack` section; the publish reads it to choose a pull request's base; and the correction notes tell the child's session of a restack or a retarget.

## Verification

`jig verify` (`internal/verify`) runs a slice's oracle on a commit of the operator's clone and journals the outcome in the shapes the gate already reads. The foreground resolves the ticket, slice and sha, looks at the journal and a per-(commit, slice) lock, and either reports what exists or starts `jig verify --worker ...` detached (`internal/spawn`: a new session on Unix; on Windows a hidden console of its own, a new process group and job breakaway) with its output in `<jig home>/logs/verify/<store id>/<ticket>/<sha12>-<slice>.log`, noting the pid in the ledger (`verify-spawned`). `--wait` runs it inline. The worker:

1. writes `verify-pending`, with the commit's subject as its `summary` so a reader of the journal can name the change, and pushes, so the work is visible before it is done;
2. adds a detached worktree of the commit under `<jig home>/verify/<store id>/<ticket>/<sha12>-<slice>` (`git worktree add --detach`), checks the tree clean, reads the manifest from that checkout and holds the commit to the ticket's start sha (a descendant of it, and not it);
3. brings up the slice's env class if it names one, runs the oracle command (`m.OracleCmd`) with `JIG_RECORD_DIR` set, within the manifest's `timeout:` (30 minutes by default; a wedge guard that kills the process tree and counts as a failure, never a cost cap);
4. journals `oracle` (pass or fail, with command, env, seconds and source `jig`; on a failure the last 600 bytes as `summary` and a `signature`), `recorded` when the scenarios wrote anything (run `<slice>-v<attempt>`, ADR 0029); the slice's state moves (a pass is `green` from any state, a fail is `building` from any state but `needs-input`), and on a pass `verified` follows the state, so a reader woken by that line finds the slice green; and pushes, three tries, keeping the lines local when the push never goes through;
5. removes the worktree.

A run that cannot reach the oracle ends in `verify-error` with the reason. The attempt number is per slice and continues a slice that was built before. A pending record stands until `--rerun`, because the journal cannot tell a dead worker from a running one (the ledger's `verify-spawned` pids can). The gate's `reusablePasses` and `journal.BuiltCommits` read these lines unchanged, which is how a hand-built commit reaches the gate.

In a project of several repos a verification belongs to the repo the commit is in. `jig verify` takes the repo `locate` found around the working directory, else the one whose mapped clone is `--repo-dir`, else the project's only repo, and refuses to guess among several (`VALIDATION_ERROR`); a slice that builds in another repo is refused, with the clone to commit in, the observe hook starts no verification for it, and the oracle run is that of the workspace the slice names in the manifest of the commit's clone (`Manifest.WorkspaceIn`).

## Sessions, the ledger and events

The machine ledger, `<jig home>/ledger/ledger.ndjson`, is the second of the two logs; the ticket journal is the first. Its lines are `journal.Line` values: `session-start`, `session-touch`, `session-end`, `tool`, `hook`, `note-delivered`, `git-commit` and `verify-spawned` (a ledger written before ADR 0034 may also hold `graph` lines, which nothing reads). It is machine-local and is deliberately not a store subtree. `ledger.Append` takes the ledger lock and writes one `O_APPEND` line (a newline first when the file ends mid-line, so a torn write costs only itself), and refuses with `LEDGER_LOCKED` rather than write unlocked; `ledger.Read` skips malformed lines and `ReadFrom` follows by byte offset. A session is a fold of those lines (`ledger.Sessions`, one `Session` per id: harness, agents, cwd, clone, transcript, ticket, slice, model, effort, files, last tool, started, last seen, ended; `transcript` is the path of the harness's transcript of the session when a start or touch line named one, the latest if several, and the gate reads it to find the intent of the ticket's own sessions); it is live until a `session-end` line, with no staleness cutoff. `jig session start|touch|end|list` write and read them, and the `jig here` session section mints an id (`s-` and 12 hex digits) only in a project and only when none was given. Journal appends are one `O_APPEND` write in place under the checkpoint lock and the file lock, refused (`JOURNAL_LOCKED`) without them, and `journal.Read` leaves out any line that is not a JSON object, so a torn line hides nothing. Because a line's `ts` is RFC 3339 with nanoseconds trimmed of trailing zeros, two stamps of one file can differ in length, and a reader orders by parsing, never by comparing strings.

`jig events [T] [--follow] [--since t] [--all]` is the one reader of both logs: an ndjson stream of `{src, store, ...line}` ordered by each line's instant, with ties going to journal lines before ledger lines. A ledger line is in a ticket's stream when it names the ticket, or names none and its session was last bound to the ticket by an earlier ledger line. The stream promises order within a file only: a union-merged journal can put another machine's line anywhere in the file. A follower polls every 250 ms and holds no file open between polls; it tells new journal lines from old by hash (counting copies) and new ledger lines by offset. `jig tui [T]` is a reader of that stream plus `jig status` and `jig next --json`: seven screens (project, ticket, brief, build, review, publish, slice) whose keys act only by running verbs as the person at the keys, so every key leaves the lines its command leaves, and `internal/tui` reads no store. A verb the screens run writes its warnings into the output they show (the `env`'s `stderr`), never onto the terminal they own; anything else the process writes to stderr while the screens hold the terminal is kept and written out when they give it back. A ticket shows as four steps taken in order (brief, build, review, publish), which are `internal/phase`'s answer for the facts the screens hold (the output of `jig status` and the journal lines), so the screens and `jig next` never disagree about where a ticket stands; each step has its state in a few words and, once done, how long it took. Under the steps, for the step under the cursor (it follows the current step until the person moves it), a block lists that step's sub-steps, each with an icon for its state, a label and a few words: six on an 80 by 24 screen and more on a taller one, the ones that matter first (what needs a person, what is under way) and a count of the rest, taking its room from the blank lines before it takes any from the activity, which keeps three lines. Below the block is the step's activity: every journal line belongs to one step, and each line names the sub-step it advances in the block's words (`slice clamp ·`, `round 1 ·`, `body ·`). The "next" line is the first help line of `jig status`, which ends with the prompt that asks for the next phase, and takes up to two lines. The detail screens show the records, all from the tables and blocks `jig status` prints and from `jig brief show --text`, since the TUI reads no store: the brief's sections with the slices that cite them and the first lines of each, each slice's workspace and oracle, its commits with every run of its oracle and the end of a failed run's output, each gate round with its reviewer, independence and range, what the latest round found with the reviewer's reason, and the first lines of the pull request body and the changelog; a page longer than the window is moved by the cursor, and a page with no row to stand on by the arrow keys. A ticket screen that has had no event for three seconds reads the status again (at most once every three seconds, for the ticket on the screen only), because drafting a brief writes no journal line, and puts what that read shows of the brief, its sections and slices, in the brief's activity as lines the journal did not write. `jig tui <T>` on an id the store would mint and has not yet (a well-formed id under a declared key) opens a screen that waits for the ticket and fills in when the ticket has a line or a row in the ticket list. Lines are told in words: a session by the role it played (planner, builder, reviewer, numbered when several sessions played one; a session that played the planner or the builder and also reviewed is a reviewer subagent) with its harness and model from its `session-start` ledger line, a slice by its goal and a commit by the subject `jig verify` writes on its `verify-pending` line; a session's id shows in the build screen's sessions block and, shortened, on a round's reviewer line.

**`jig status`.** `jig status <T>` prints the ticket's `state:` as one word: `closed` (it wins over every other), `stalled`, `parked`, `env-blocked` or `green`, else `new` (no slices and no adopted branch), `queued` (slices, none ever claimed or verified; read from the journal, because a release puts a claimed slice back to queued) or `building` (a slice was claimed or has a verification). Then come the `closed` block (`reason`, `by`, `note`, `at`) of a closed ticket, the `slices` table, whose columns end in `workspace`, `oracle` (as `slices.yaml` names it) and `from_gate` (the round a fix slice came from), and `outstanding_asks[N]{id,risk,file:line,title,why,decision,decided_by}`; then the sections later commands register from files of their own (`registerStatusSection`, in order): `brief` (whether the brief as it is now is the approved one, who approved it and how, and each section with the slices that cite it), `driver`, `rounds[N]{round,verdict,reviewer,harness,model,independent,fix,ask,note,scope,base,head,files}`, one row for each recorded gate round, read by `verifydeliver.ReadRounds` (`verdict` is `clean`, `findings` or `not-independent`), `findings`, what the latest round raised, high risk first, `publish`, the pull request, its state and head, and the first lines of the body and the changelog, which for a ticket that publishes in several repos gives way to a `publish_prs[N]{repo,pr,state,head,body_file}` table, and `spend`. A cell not known is `-`. A reader parses the output by name, so a column or block added at the end changes nothing for it, and the TUI is such a reader.

## Notes and corrections

What a session needs to hear reaches it as a *note*. `observe.RegisterNoteSource` registers a source (`func(NoteCtx) ([]Note, error)`) from an `init()`; the core calls every source at each tool, prompt, stop and session-end event of a session on a ticket in a store that is here, and delivers each note once per session and `Ref` (a `note-delivered` ledger line is the memory), as context at the next tool call or prompt. `Note.Enforce` is the one switch: set, the note holds an *unattended* session back at Stop once (a session is unattended when its latest unreleased claim has `unattended: true`, or the `jig.yaml` marker says so; never an environment variable, since interactive cloud sessions exist); unset, it is only told. The sources are:

- `verification` (built in): a `verified` line since the session began is advisory; a failed oracle or a `verify-error` is enforced.
- `answer` (built in): an `answer` line for a question the session asked, with its provenance.
- `correction` (`internal/correct`, all enforced): a *stall*, two or more `oracle` failures of one slice with one signature since the slice's last `verified` or `requeue` line (an unattended stall also marks the slice `stalled`, so `jig requeue T --slice a` is a real remedy); *brief drift*, a verified slice citing a `from_brief` hash that is no section hash of the current `brief.md`; *scope drift*, a commit outside every workspace of the ticket's slices (a merge commit is judged by none); a *missing slice*, a commit on the ticket's branch whose `Jig-Slice` trailer is absent or names no slice of the ticket, made by a session that has claimed none, where the note names the slice (or the candidates, or none) whose workspace holds the files the commit changed and how to put it right (amend it with the trailer, or `jig verify T <sha> --slice s`), ends once the commit is amended away or verified, and verifies nothing itself; and, for a ticket stacked on another, a *restack* (the parent's branch moved past the child's: told once for each parent head, asking for `git merge <parent branch>`, which jig never performs) and a *retarget* (the parent merged: told once, to the session that found it). The first sighting of a Ref writes a `correction` journal line.
- `spend` (`internal/spend`, never enforced): the session's spend, summed from the transcript at Stop and SessionEnd (each API message once, subagents included, models with no price named and not guessed), as a cumulative `spend` journal line; held with the ticket's other sessions against `budget.yaml` (seeded from the marker's budget), it says so once at 80% of a limit and once past it. `jig status` shows a `spend` block; nothing is ever stopped for money.
- `next-phase` (`cmd/jig/observe_nextphase.go`): what comes next. The publish note, for any session and enforced when unattended: a clean round, a person's `publish-approved` naming the head it reviewed, and no `publish-done`. The review note, for an unattended session only: every slice green, no round open and no ask waiting for a decision, so the note says to open the gate (`jig gate T`) and have the change reviewed, once for each round to come (`review:<n>`), and who reviews: this session when it is fresh, else a subagent, with the reasons the session is not fresh.

At Stop an interactive session is shown `jig: <finding>` lines and never refused. An unattended one is refused once per fact (a `note-delivered` line of outcome `stop-block`, so a `stop_hook_active` stop with nothing new always ends) for: a verification still running, tracked files changed and not committed, and every enforced note not yet refused for. An open question is shown and holds nothing.

## The four homes

Every fact jig produces lives in exactly one of four places, chosen by what
invalidates it:

| Invalidated by | Home | What lives there |
|---|---|---|
| a jig release | binary + skills (process) | the machinery and judgment code itself |
| one repo's own change | that repo's `.claude/` (the manifest `.claude/jig.yaml`) and its root `jig.yaml` marker | conventions, oracle commands, env classes, invariant-sensitive paths, and which project the repo belongs to - knowledge specific to that repo |
| platform or ticket history | the truth repo | `tickets/<id>/`, `charts/`, `platform/`, `ledger.md` |
| session end | nowhere, except receipts | `evidence/` - a session's reasoning dies with it; only its artifacts persist |

Knowledge flows down (repo → session), truth flows sideways (ticket →
ticket, repo → repo), learning flows up (session → ledger).

What is true of this machine only lives under the jig home and in no store: the machine map `projects.yaml` (store, root and clones per project), `ignored.yaml`, the sessions ledger `ledger/ledger.ndjson`, the global git hooks `hooks/`, `logs/` (`observe.log` and the verification logs), the detached verification worktrees `verify/`, the pool of leases the gate and publish work in, `cache/pull-requests.json` (the states the repo host gave pull requests: a cache and never a record, since deleting it costs only the next reads), `prices.yaml`, `publish-terms.txt`, and the recordings below. The jig home is `JIG_HOME`, else the real home directory's.

One kind of file lives in none of the four, deliberately: a build's recordings
(screenshots, GIFs, videos, terminal recordings), under
`<jig home>/evidence/<store id>/<ticket>/`. It is a fifth home, justified by
size: the store is a long-lived repo every clone carries whole, so a 100 MiB
video does not belong in its history, and a lease is rewound between rounds.
What can lose a recording is the machine, so it lives with the machine, like
the pool. The recordings of a verification's oracle run
([ADR 0029](docs/adr/0029-demos-are-recordings-of-the-builds-end-to-end-scenarios.md))
live in it, one directory per oracle run, under
`<store id>/<ticket>/recordings/<commit>/<run>/`, and the journal's `recorded`
line is their manifest: the only thing about them that lives in the store.

The files a publish picks from the recordings are copied to
`<store id>/<ticket>/picks/<head>/` (as `rec-<n>.<ext>`). The picks request,
`<head>.picks.json`, sits beside that directory (the answer is the `--picks`
file the session wrote, wherever it put it), and the journal's `publish-picks`
line records the pick. `picks.json` names absolute paths of this machine, so it
stays out of the store. A reviewer round's list of the
recordings on the reviewed head's history, `recordings.json`, is written to
`<store id>/<ticket>/reviews/round-<n>/` for the same reason.

## Manifest and invariants

A repo's `.claude/jig.yaml` can declare an optional `invariants:` list of
repo-relative, `/`-separated paths that are sensitive to change: schema
migrations, numeric precision code, or any other area where a structural
change merits more care (they once floored a builder's model to the dearest rung). Each entry is either a directory
(ending in `/`), which covers its whole subtree, or a `path.Match` glob
pattern matched against each changed file. Every entry is validated and
matched in its `path.Clean`ed form, so a leading `./`, a doubled separator or
a redundant `..` segment still matches the paths git reports; `./` (the repo
root) covers every path in the repo.

Nothing in the binary reads the declared invariants today. They floored a
dispatch's model rung to the dearest model while jig chose models
([ADR 0019](docs/adr/0019-builders-open-on-sonnet-and-climb-on-failure.md)); with
the staircase gone from the binary
([ADR 0032](docs/adr/0032-jig-attaches-to-the-harness.md)) the list is still
validated and `Manifest.MatchesInvariant` still answers, so existing manifests
keep loading and a later check can use it.

The manifest may also carry `timeout:` (a duration such as `45m` or `1h30m`; a
bare number or a negative one is a manifest error): the bound on one
verification run (`Manifest.OracleLimit`, 30 minutes when absent). It kills the
oracle's process tree and counts as a failure; it is a wedge guard, never a cost
cap.

## Store schema

A store is a git repo, at `store.yaml`'s `schema_version: 3` (a v2 store's
`project.yaml`, at `schema_version: 2`, opens unchanged). A real
ticket lives under `tickets/<id>/`, `<id>` an id - 1 to 10 uppercase ASCII
letters and digits starting with a letter, a `-`, and a number from 1 with
no leading zero - under any key declared in the store config's `keys:` or
not: a key removed from it only stops new mints under it, so a
folder it already named keeps working. `Store.TicketDir(id)` is the one
function that names a ticket's folder, and `Store.TicketRelDir(id)` its
store-relative form (`tickets/<id>`) for a caller that shells out to git
against the store (a log/show path, a diff path, a rendered pull request
note's link); every caller goes through one or the other rather than
joining `tickets` and an id itself. `Store.TicketIDs` lists every id-shaped
folder under `tickets/`, sorted by key then by number; it is the one
function minting's per-key counter, A6's mirror and every other listing
that shows many tickets read tickets through. `charts/`, `platform/`, `trackers/`,
`ledger.md` and `store.yaml` (`project.yaml` in a v2 store) stay at the store root. One chart folder,
one tracker entry and one ticket folder, in full:

```
store.yaml          # project.yaml in a v2 store
claims.ndjson       # the claims log (single-main mode; writers mode keeps it on its own branch)
platform/
  contract-index.md
ledger.md
charts/
  <name>/
    map.md          # for people and sessions; jig never reads it
    tickets.yaml    # the handover jig reads and writes
trackers/
  <provider>/<scope>/
    <remote id>.yaml   # the record: ticket: <id> or chart: <name>, then the provider's own keys
    <remote id>        # a relative symlink to tickets/<id> or charts/<name>, made from the records
tickets/
  <id>/
    brief.md
    illustration.html # optional: the brief's illustration, part of what the approval covers (jig brief show)
    approval.yaml     # jig validate: who approved, when, and the sha256 of brief.md, or of brief.md and illustration.html (jig status: the brief block)
    budget.yaml       # jig budget: tokens and usd limits
    intent.md         # jig gate --intent/--doc; ignored when brief.md exists
    slices.yaml
    ticket.yaml       # optional: this ticket's own record - title, body, blockers, adopted branch, rank, closed, aliases
    start.<repo>.sha  # the sha the ticket's branch started from
    slices/
      <id>.state
    journal.ndjson
    questions/
      q-NNN.md
    work/
      gate.round-N.review.json      # the open round's request
      gate.round-N.open.yaml        # round, branch, intent hash, opened ts, and per repo its copy, head, base, scope, target, lease
      gate.round-N.findings.ndjson  # the reviewer's drafts (jig finding)
      gate.round-N.result.json      # what jig review done recorded
    gate/
      round-N/
        findings.yaml
        findings.md
        diff-changelog.md
        report.yaml
    evidence/
      round-N/
    changelog/
      <workspace>.md    # <repo>/<workspace>.md for a workspace named by its repo
      consolidated.md
    pr/
      evidence.md
      <repo>.md
      review-notes.md   # review-notes-<repo>.md when the ticket publishes in several repos
```

`work/` is store-side, not lease-side, on purpose: a round's plumbing must
never be swept into a commit of the product repo. The store config's
`schema_version` is the compatibility contract - a store written by one jig
version declares the layout a later version must still read.
`project.Config.CheckSchemaVersion`, called once from `cmd/jig`'s
`resolveStore` (the one place every command loads the store config, through
`project.LoadStoreConfig`, which reads `store.yaml` or `project.yaml`), accepts
schema 2 up to the current one (`MinSchemaVersion` 2, `CurrentSchemaVersion` 3),
refuses a `schema_version` newer than that outright, and one older than 2 for
every command but `jig store migrate` (which is built to open exactly such a
store and rewrite it onto schema 2) - `jig help` and `jig version` never
resolve a store at all, so neither check runs for them.
A build's recordings are not in this tree at all (see the fifth home
above): the journal's `recorded` line names them and holds their hashes.

`trackers/<provider>/<scope>/<remote id>` is the tracker tree: one entry per remote item. GitHub's scope is the lower-case `<owner>/<repo>` and its remote id the bare issue number (`trackers/github/develdeco/jig/65`). Beside each entry, `<remote id>.yaml` is the record: it names its ticket (`ticket:`, resolved through `Store.ResolveTicket`) or chart (`chart:`), and every other key is the provider's, kept as the provider wrote it. The entry itself is a relative symlink to the ticket's or chart's folder, committed as git stores symlinks. The record is the mapping and the link a view of it: `Store.RebuildTrackerLinks` makes every link from the records alone, with no provider and no network, and every sync runs it after it writes records. A clone whose git keeps no symlinks (`core.symlinks` off) gets no links and one warning per command; every record is still there. `Store.TrackerRelPath` is the one function that names an entry, next to `TicketRelDir`, and an entry's provider, scope and id are checked to be plain names before any write (`TRACKER_ENTRY_INVALID`), so an id read out of a file can never lead a write out of `trackers/`; writing a record over another owner's is refused (`TRACKER_ENTRY_TAKEN`). Ticket and chart folders hold no tracker data: a record still in `<ticket>/tracker/github.yaml` or `charts/<name>/github.yaml`, where the mirror first kept them, is read as a fallback (`TrackerIDs`) and moved into the tree by the next sync. `jig validate` reports what is wrong with the tree as advisories, never as problems with a brief: `TRACKER_RECORD_INVALID`, `TRACKER_RECORD_ORPHAN`, `TRACKER_RECORD_DUPLICATE`, `TRACKER_RECORD_MISPLACED`, `TRACKER_LINK_WRONG`, `TRACKER_RECORD_UNMOVED`, and `TRACKER_TREE_UNREADABLE` when the tree cannot be listed.

A brief's approval covers `illustration.html` when the ticket has one. `Store.BriefSHA256` is the sha256 of `brief.md` exactly as it is when there is no illustration, and otherwise the sha256 of the two lines `brief.md <sha256 of brief.md>` and `illustration.html <sha256 of illustration.html>`, so adding, editing or removing the illustration changes the hash, `jig status` reads `approval: changed`, and an unattended claim is refused until `jig validate` approves the brief again. Every approval recorded before illustrations existed keeps its hash and stays current. A slice's `from_brief` holds section hashes, so an illustration edit requeues no slice. `jig brief show [T]` is jig's one viewer of a brief ([ADR 0032](docs/adr/0032-jig-attaches-to-the-harness.md)): it serves the `## ` sections as plain HTML and the illustration as it is on `127.0.0.1` (answering only requests whose `Host` is a loopback name), prints their paths with `--path` or, where `CLAUDE_CODE_REMOTE` is set, instead of serving, and prints the sections as text with `--text` for the TUI.

`jig validate` also prints advisories, which never refuse a brief: the tracker tree's findings above, a ticket that has a brief and no `ticket.yaml` (`TICKET_NOT_MINTED`: mint it with `jig ticket new` and never make a ticket's folder by hand), and slices that fall into unconnected groups (`SLICES_UNCONNECTED`).

`ticket.yaml`'s optional keys include `rank:` and `closed:`. `rank` is an integer that puts a ready ticket in the owner's order: `jig rank <T> <n>` writes 1 and up (a hand edit may hold any integer, and `jig validate` has no rank rule) and `jig rank <T> --clear` removes the key. `closed` is `reason` (`covered`, `superseded` or `not-planned`), `by` (what stands for the ticket: a reference for covered, a ticket of the store for superseded, none for not-planned), an optional `note` and `at` (the UTC day); `jig close` writes it and `jig reopen` removes it, and each commits that one file through `Store.Claim` and pushes it. Both keys are optional, so neither bumps `schema_version`. `ticket.yaml` is decoded strictly, so a jig that predates `closed:` refuses a closed ticket's record (`TICKET_RECORD_INVALID`, help: upgrade jig), which keeps an old build from building a closed ticket by accident.

`jig store migrate --map <file> [--dry-run]` (`internal/migrate`, wired by
`cmd/jig/store.go`) is how a v1 store (tickets at the store root, one
`ticket_format`) becomes one at schema 2: the operator's rename map assigns
every existing ticket a new area key, `--dry-run` prints the resulting
rename and every file the migration would move, delete or rewrite without
changing anything, and the apply moves each ticket folder to
`tickets/<new id>/`, keeps the old id as an alias, rewrites `project.yaml`
and every chart, and commits once without pushing
([ADR 0031](docs/adr/0031-store-layout-v2-and-its-migration.md)).

`Store.aliasClaims`' memoized scan (below) is safe for a `Store` shared
across goroutines: a mutex guards both the cache and the scan that fills it. It is dropped after a ticket.yaml
write, as before, and now also after a checkpoint's pull
(`checkpoint.pull`), so an alias that arrives by a pull - the shape `jig
store migrate` leaves for every renamed ticket - resolves at once rather
than waiting for jig's own next write.

`jig store migrate` applies exactly the next step of `migrate.Steps` (`--to n` must
name it, "one step at a time", so each step is one reviewable store pull request): v1 to
v2 takes the rename map above, and v2 to v3 takes none - it swaps `project.yaml` for
`store.yaml` in one commit by the person, keeping every key the v2 file had (the
project's `name` becomes `projects: [name]`, its `repos` go to the marker), and writes
a `jig.yaml` marker into the mapped clone of the project's one repo, uncommitted. It
refuses a project of several repos or none, and a project with no clone mapped. A v2
store is a first-class citizen until its owner migrates it; the test fixture is pinned
to write v2 on purpose, as the standing proof that one still opens.

### Writers mode and the claims log

A store is in one of two modes, switched by a ref and not by a file in the tree: it is
in *writers mode* when `refs/heads/claims` or `refs/remotes/origin/claims` exists
(`Store.Mode`, a few file reads and no git process), else in *single-main mode*, which
is what every store was and is untouched. `jig store migrate --writers [--dry-run]`
converts a store: it refuses a dirty store or one not level with its origin, makes the
claims branch (a root commit holding one `id` line per existing id and alias), checks
out `writers/<name>`, commits the union `.gitattributes` and pushes both - never main.
It is idempotent, and a clone made afterwards is in writers mode from its first command.

In writers mode:

- The clone works on `writers/<name>`. The writer name is `git config jig.writer`, else
  the host name cleaned to lowercase letters, digits and single hyphens (at most 40
  characters); two clones must not share one (`WRITER_NAME_TAKEN`, refused before anything
  is merged or pushed when `origin/writers/<me>` is neither in HEAD nor ahead of it).
- A sync fetches every branch (`+refs/heads/*:refs/remotes/origin/*`) and merges every
  other `origin/writers/*` and the legacy `origin/main` read-only: a fast-forward when HEAD
  is part of the tip, else a merge commit by the person. `gitx.WritersAttributes` adds
  `*.ndjson merge=union` to the store's attributes (`StoreAttributes` is unchanged, and the
  in-process repo serves either text, since a merge driver converts no content). The union
  driver keeps both sides of a hunk and reorders lines, and when two writers have merged
  each other in turn the same line comes out twice, so after each real merge every
  `*.ndjson` it touched is cut back so no line is in it more often than in the side that has
  it most (`dedupeLines`); readers must never assume time order. A conflict in any other
  file aborts the merge as `STORE_CONFLICT`.
- A rewritten branch is refused and reported, never repaired: the machine-local ref
  `refs/jig/seen/<writer>` holds the tip last merged, and a tip that does not descend from
  it is skipped with the warning `WRITER_REWRITTEN` (`git update-ref -d` accepts a rewrite
  on purpose).
- A push moves `HEAD:refs/heads/writers/<me>` only. The in-process `gitx.Repo` cannot walk
  a merge commit, so in a writers store a fetch or push that meets one falls back to the
  git program (`ErrUseCLI`, [ADR 0011](docs/adr/0011-the-store-runs-git-in-process.md));
  commits stay in process.

The claims log holds the facts that only one writer may hold. It is one file,
`claims.ndjson`, with lines `{ts, kind, ticket, slice?, writer, session?, author}`, where
`kind` is `id` (an id or alias is reserved), `driver` (the writer drives the ticket) or
`release`. In writers mode it is alone on the orphan branch `claims`, which nobody checks
out: an append reads the tip, builds the blob, tree and commit with plumbing
(`gitx.RunInput`), moves `refs/heads/claims` with a compare-and-swap `update-ref`, and
pushes; a rejected push fetches, decides again on the log as the origin has it, and
retries (five more times), and a push that fails any other way restores the ref
(`CLAIMS_NOT_PUSHED`), because a claim is not a claim until the origin has it. In
single-main mode the same functions append to `claims.ndjson` at the store root on main
through `Store.Claim`. `Mint` reserves the next id on the log before it makes the folder
(`ID_NOT_CLAIMED` when the origin cannot take it), so a gap is an id reserved whose ticket
never landed. The driver of a ticket is its latest `driver` line that no `release` line of
the same writer has ended since: `jig claim` takes it, another writer's unreleased driver
is `DRIVER_HELD` (`--take` is the way past), `jig claim --release` lets go once no slice
claimed since is still held, and `jig status` names the driver. It lives in this log and
not in the machine ledger, because the ledger is machine-local and a driver is only useful
if another writer's machine can see it.

## Project configuration

A store's configuration file is `store.yaml` at schema 3 (`schema_version`, `keys`,
`trackers`, `platform`, `gate`, `context`, `projects`) or, in a v2 store,
`project.yaml` (the same, plus the project's `name` and `repos`); where this section
says `project.yaml`, a v3 store's `store.yaml` is meant. What describes the project -
its name, kind and repos - is the `jig.yaml` marker below. Beyond the required fields
(`platform` and `keys:`), the store config declares `trackers:`, a list
of mirrors of the store's tickets: absent or `[]` means none, and at most one `github` entry is
accepted (below, Mirror). A schema-2 `project.yaml` refuses
the two keys `jig store migrate` rewrites away: `ticket_format` (keys:'
predecessor) and `tracker:` (`trackers:`'s predecessor, including its one
old legal value, `tracker: local`) - both the reads that once made sense of
them are gone, so a schema-1 store still carrying either loads untouched by
`project.Load` (the schema check, not this one, is what refuses it outside
`jig store migrate`). `tracker:` and `trackers:` together, and `routes:`
(which only ever fed publish's now-gone route step), are refused at load
regardless of schema version, each with help naming the fix. `jig init` writes a v3
`store.yaml` and the test fixture (`internal/fixture`) a v2 `project.yaml`
(`schema_version: 2` on purpose), both with `trackers:
[]`, no `tracker:` or `ticket_format` key, and one key derived from the
project name - its letters and digits, uppercased, at most 10, so `JIG` for
jig, meaning "everything in `<name>`" - or the key `--key <KEY>` names
instead. It may also contain an optional `gate` block configuring the
gate's fix loop and risk floor:

```yaml
gate:
  fix_rounds: 3                # rounds per ticket that may queue fix slices
  fix_risks: [high, medium]    # risks whose fix findings become fix slices
  fix_slice_findings: 5        # most findings one fix slice carries
```

All three keys are optional and default to the values shown above. A `gate`
block with absent keys gains defaults for those keys. An absent gate block
entirely is equivalent to all defaults.

`keys:` declares the project's area keys, each a key to its one-line
meaning:

```yaml
keys:
  STORE: the store's layout, ids, records and git sync
  GRAPH: tickets, charts and the order between them
```

A key is 2 to 10 uppercase ASCII letters and digits, starting with a
letter (uppercase so a filesystem that ignores case, the Windows and
macOS default, never confuses `STORE-1` with `store-1`); a key breaking
that rule, or a meaning that is empty, is refused when the config loads.
An id is `<key>-<n>`, n counted from 1 per key, worked out from the store
(`Store.TicketIDs`) rather than stored anywhere - one past the highest
number among both the ids and the aliases that carry the key, so a number
once minted (even under a since-renamed ticket) is never minted again. A key
removed from `project.yaml` only stops new mints under it - its tickets keep
working, and an id never changes.

`ticket.yaml` may carry `aliases:`, a list of a ticket's earlier ids, kept
unchanged by every rewrite of the record (no command renames a ticket; only
`jig store migrate` writes real ones).
`Store.ResolveTicket(idOrAlias)` is the one resolver: it turns idOrAlias
into the ticket's current id, unchanged when nothing claims it (so a
caller's own "not found" check still fires), and refused
(`TICKET_ALIAS_COLLISION`, naming every claimant) when more than one ticket
claims it - two tickets list it under `aliases:`, or it is also another
ticket's own id. Every command that takes a ticket id resolves its argument
here before anything else, printing `<typed> is now <current>` when they
differ (`jig events` resolves silently, so its stream stays clean); `jig graduate`'s and `jig validate`'s `blocked_by` refs, and the
GitHub mirror's blocked-by link and "Waits for" line, resolve each ref the
same way. `Store.CheckAliases` is the same collision check run over the
whole store, which `jig validate` reports alongside a ticket's own
`blocked_by` problems, since a collision can involve two tickets neither of
which is the one being validated.

`jig ticket new --key <KEY>` mints under that key (omittable when the
project declares exactly one); `jig graduate` reads a `key:` on each
`tickets.yaml` entry it creates the same way. Either resolves through
`project.Config.ResolveKey`, which refuses a key the project does not
declare - listing the declared keys and their meanings - before anything
is minted; `jig graduate` checks every entry's key this way before it
creates any of the chart's tickets.

**`fix_rounds`** is the number of gate rounds per ticket that may queue fix
slices without human intervention. Once this budget is exhausted, remaining fix
findings are parked for a human to keep or dismiss, marked with `routed_why:
budget` in `findings.yaml`. A value of 0 means no round queues fix slices
unattended - every fix is parked. Negative values are refused at load time.

**`fix_risks`** lists the risks (high, medium, or low) whose fix findings
become fix slices. A fix finding whose risk is not in this list is routed as a
note instead: its status becomes `noted`, `routed_as: note` is recorded, and it
never reaches the triage batch. This holds whatever its build target: a
below-floor fix in a file outside every declared workspace is a note, not an
ask. An ask finding keeps its routing at any risk.

**`fix_slice_findings`** is the maximum number of findings one fix slice can
carry. A round's kept fixes, grouped by (workspace, oracle), are packed into
slices greedily by file, taken in path order: findings in the same file
always share a slice, and a file with more findings than the bound gets one
slice of its own, over the bound, rather than being split itself. A group
that fits in one slice keeps its existing id,
`fix-<round>-<workspace>-<oracle>`; a split group's slices are numbered
`-1`, `-2`, ... from 1. Must be at least 1. Invalid values are refused at
load time.

`gate.review_effort`, `builder_effort` and `staircase:` are still accepted and
validated (an effort is `low`, `medium`, `high`, `xhigh`, `max` or empty), so an
existing configuration keeps loading, but nothing in the binary reads them: jig starts
no session, the harness chooses its own model and effort, and a gate reviewer's are
what it reports of itself
([ADR 0032](docs/adr/0032-jig-attaches-to-the-harness.md)).

Invalid configuration values are refused when the store config loads:
a negative `fix_rounds`, a `fix_risks` entry that is not `high`, `medium` or
`low`, a `fix_slice_findings` below 1, or an effort that is not one of `low`,
`medium`, `high`, `xhigh`, `max` or empty.

**The marker.** A project is defined by a `jig.yaml` at the root of its repo (`jig init`
writes it; an orchestrator's sits above its nested repos; a nested repo may carry a
`kind: member` one). Every key is optional on read and an unknown one is ignored, so an
older jig reads a newer marker: `jig` (the format version, a string), `project` (the
name), `store` (the store's remote, written by `jig share`; a local store is found by the
sibling `<name>-tickets` rule and the machine map, so the file never carries a local
path), `kind` (`project`, `orchestrator` or `member`), `repos` (each `name`, `path`,
`remote` and `target`, kept by `jig repos add|remove|sync`; a remote only where neither
`.gitmodules` nor the nested `.git` supplies one), `expects` (`harness` and `hooks`: what
`jig setup --here` installs by default), `branches: {from: <tracker>}` (which tracker's
ids lead branch names), `budget: {tokens, usd}` (the default spend limit, recorded on a
ticket at its first recorded spend), `distribution` (how the repos are shared among
people, echoed by `jig next`) and `unattended` (the default for a claim). It is not the
per-repo manifest, `<repo>/.claude/jig.yaml`, which marker code never reads:
`FindMarker` looks only at a directory's own top level and skips a directory named
`.claude`. `project.LoadConfig` completes a v3 `Config` from the marker found at the
project's roots (`WithMarkerRepos` fills the name and the repos, a repo's remote being
the marker's, else its clone's origin, else `.gitmodules`' URL, else its directory); a v3
store whose marker cannot be found has no `Repos`, and callers guard `len(cfg.Repos)`.
`lint.TestStoreConfigIsReadThroughTheLoader` fails when the store config's file names
appear as string literals outside the few packages that own them.

**`jig init`** runs only at a project's root (`project.CheckInitDir`). With neither
`--standalone` nor `--orchestrator` it reads the case off the folder (`InferKind`: direct
children with a `.git`, or `.gitmodules` entries, make an orchestrator) and says which it
picked before it writes. It creates `<name>-tickets` beside the folder (git on `main`,
`store.yaml`, `platform/`, `ledger.md`, `.gitignore`, `.gitattributes`, one commit by the
person), writes `jig.yaml` and maps the project, and commits nothing in the folder. An
orchestrator's nested repos are named by path, or by URL, fetched as a submodule with
`ignore = all` or, with `--clone`, as a gitignored clone whose `remote:` goes in
`jig.yaml` (a checkout does not have a gitignored clone, so the marker is the only place
its URL can come from). A project is local until `jig share` publishes it: it sets or
creates the remotes (`gh repo create --private`), writes `store:` into `jig.yaml`, commits
that one file as the person, and pushes the store, the nested repos it gave a remote and
the project's own repo last.

## Mirror

`internal/mirror` is jig's own GitHub mirror, replacing a bridge program
that used to run outside jig and could not run while jig used the store.
`Store.AfterCheckpoint` (`internal/store/store.go`) is the one hook every
checkpoint calls, through `Store.RunCheckpointHook`, after its own commit
(and push, on a store with an origin): `Store.Push` calls it itself (a
slice changing state, a requeue, a verification, a gate round, a
publish), and `jig ticket new`, `jig graduate`, `jig rank`, `jig close` and `jig reopen` call it
explicitly once their own `Store.Claim` lands, since `Claim` stays hook-free - the mirror's own
claims (`claimIssue`, landing a new issue's record) reach the store through
`Claim` too, and hooking `Claim` itself would re-enter the mirror. Either
way it runs `mirror.Sync` in the same process, best-effort - a failure
prints one warning naming the cause and never fails the command, and the
next checkpoint retries. The
`SyncReport` that sync returns is rendered through the resolving command's
own writer (`cmd/jig/mirror_report.go`'s `renderSyncReport`, which
`resolveStore` hands `stdout`), so a drift line, a skipped item or an
unlinked blocker a checkpoint found prints in the output of the command it
rode in on; `Store.Warn` is wired the same way, so a failed sync's warning
is a labelled block in that output rather than bare stderr. `jig trackers
sync [--dry-run] [--store] [--project]` (`cmd/jig/trackers.go`) runs the
same `Sync` on demand and renders the same report through the same
function - the second of the two places brief.md names. `--dry-run` reads
the store and GitHub and writes to neither (a store whose items all still
need creating reaches no client at all, having nothing to read). The mirror
depends on the store, never the reverse: `internal/mirror` holds what any
tracker provider would share, and `internal/mirror/github` is the one
GitHub client today, a thin GraphQL wrapper
(`internal/mirror/github/client.go`) over the mutations and queries the
bridge proved, taking its endpoint and token source as arguments (no `gh`
on PATH is `GH_NOT_INSTALLED`) so every test points it at a fake GraphQL
server and none reaches github.com. That client paces itself as GitHub asks
of a client that creates content: one mutation per second
(`MutationInterval`, a package default production never overrides), and any
request that fails with a server error, a 429 or 403, or a GraphQL
`RATE_LIMITED` error is retried with doubling backoff, four attempts in
all; a client error is reported at once, since retrying it would only
repeat it.

`project.yaml`'s `trackers:` list takes at most one `github` entry (`repo:`
the issue home, `project:` the GitHub Project's URL; both required,
`VALIDATION_ERROR` otherwise), validated by `internal/project`. A config
with none is `mirror.Sync`'s own no-op, reported rather than refused, since
every checkpoint of every store calls it whether or not a tracker is
configured.

A sync's steps, every time, in order: read the store; create every ticket
(id order) and chart (name order) with no record yet, its issue opened with
only its title and footer as a body (so every ticket has a number before a
body that cites it is rendered), the creation itself claimed like an id
(`Store.Claim`: committed alone, pushed at once; a rejected push's pull
bringing in another clone's record for the same ticket or chart closes this
clone's own issue as not planned, with a `Duplicate of #<number>` comment,
and adopts the other instead of retrying); update every title, body and
open/closed state to the store's own rendering (a Done ticket's or chart's
issue closes as completed, a ticket closed as superseded or not planned closes as not planned, every other reopens); bring each issue's links to match the store -
each chart's tickets sub-issues of the chart's issue, each `blocked_by`
edge a native blocked-by link, each link compared against what GitHub
currently shows rather than against the record alone, so a link removed on
GitHub is restored and one added there is removed (both as drift), and a
blocker with no issue yet is only reported, never retried as a failure;
resolve the GitHub Project (creating its Status and Store ID fields, or
completing the Status field's options, when missing, and linking the issue
repo when it is not linked) and place every issue and every open or merged
pull request on it, with its Status and Store ID - skipping the placement
entirely when the item already carries both, so an up-to-date store sends
no board mutation; write every record. A record naming an issue repo other
than the `trackers:` entry's own `repo:` refuses the sync as
`MIRROR_REPO_CHANGED` (moving issues to a new repo is T-30) rather than
opening a second issue elsewhere. The renderer (`internal/mirror/render.go`) and the
Status/pull-request rules (`internal/mirror/status.go`) match the bridge's
own output byte for byte, so the cutover changes nothing already on the
board; see DECISIONS.md for the exact golden format, qualifying and
truncation rules.

Each record - a record of the tracker tree, `trackers/github/<owner>/<repo>/<number>.yaml`,
whose `ticket:` or `chart:` key the store owns and whose other keys are the mirror's, in the
bridge's own shape - keeps `repo`,
`issue`, `node_id`, `item` (the board item id), `prs:` (each with `repo`,
`number`, `node_id`, `item`) and `synced:`: `title`, `body_sha256`,
`state`, `status`, `store_id`, `links.parent` and `links.blocked_by` - what
the last sync itself wrote to every field jig owns (an issue's title, body,
open/closed state, parent and blocked-by links, and a board item's Status
and Store ID; everything else - labels, assignees, other fields, comments,
views - is the repo owner's). When jig is about to write the store's value
and GitHub's current value differs from `synced:`, that is drift: someone
edited a jig-owned field on GitHub, jig prints `drift, overwritten: ...`
naming the ticket or chart, the issue number and the field, and writes the
store's value anyway - through the writer of the command whose checkpoint
found it. A change made in the store prints no drift line,
since GitHub still matched what was last written. A record with no
`synced:` yet (adopted from the bridge, which never kept one) is given no
benefit of the doubt either way: its first sync populates `synced:` from
whatever GitHub already shows, silently when that already matches the
store's own rendering - the cutover's own critical path - and with no
false drift report when it does not. A recorded issue gone from GitHub
(deleted, or its repo gone) is opened again rather than failing the sync, and its record moves to the new number.

A sync first moves any record it finds in a ticket's or chart's own folder into the tree (the issue is updated in place, never created again, and the repo check has passed for every one of them first, so a refused sync moves nothing), claims a new issue's record and link in one commit, and makes the links again once it has written the records, with no provider configured too. A record that cannot be read refuses the sync (`MIRROR_RECORD_INVALID`, naming the file) rather than let its ticket look like one with no issue. A ticket closed as covered is Done and its issue closes as completed; one closed as superseded or not planned has the Status `Not planned` (the sixth option, grey, "Closed without being built", which the first sync that finds a project without it adds, keeping every other option) and its issue closes as not planned. The body says how it was closed (`**Closed:** covered by develdeco/jig#82`, then the note), a superseded ticket naming its replacement's issue, and a ticket reopened has its issue reopened. A chart is Done when it has tickets and each is Done or Not planned. The closure is the store's word, so none of it is drift.

## Module responsibilities

The dir column is exact; a lint test parses this table and asserts every dir
exists.

| Package | Entry points | Input → Output |
|---|---|---|
| `cmd/jig/` | `main`, `Main`, `run`, `registerVerb` | CLI args and an `env` (the environment, the working directory and the seams a test replaces; the process's for the binary) → subcommand dispatch through the verb registry (each verb registers its help row and entry point from an `init()` of its own file, and the `hereSection` and `statusSection` registries take the blocks later commands add): setting up (`init`, `repos`, `share`, `setup`, `attach`, `skills`, `hooks`, `githook`, `hint`, `doctor`, `skill`), finding the work (`here`, `next`, `map`, `ignore`, `recall`), tickets and slices (`ticket`, `rank`, `graduate`, `validate`, `claim`, `context`, `ask`, `answer`, `note`, `verify`, `requeue`, `close`, `reopen`, `budget`), the gate and publish (`gate`, `review context`, `finding`, `review done`, `triage`, `publish`), reading and watching (`status`, `events`, `tui`, `brief show`, `session start|touch|end|list`, `mcp`, and the hidden `observe` and `githook post-commit`) and the store (`store migrate`, `trackers sync`, `version`); the binary starts no coding session and links none of the session code (`lint/deps_test.go`) |
| `e2e/` | (tests only) | the fixture + the fake harness (`fakeHarness`: scenario commits, hook payloads, waits for detached workers) → `TestDigesterFlow`, the whole attached flow from `jig setup` to a published pull request, and a test per verb; with `JIG_LIVE_CLAUDE`, `TestHooksLive` drives the real `claude` CLI against a stand-in Messages API; `JIG_E2E_BINARY` → the same against an installed jig; helpers that run jig with a payload on stdin or start it and read its output as it runs |
| `internal/axi/` | `Render`, `Table`, `KV`, `Help`, `RenderError`, `ExitCode` | labelled data → jig's plain-text output register and process exit codes |
| `internal/briefview/` | `Handler`, `Source`, `Page`, `Title`, `Summary`, `IllustrationRoute` | a ticket's `brief.md` and optional `illustration.html` → jig's one viewer of a brief: a loopback-only handler that serves the `## ` sections as plain HTML (no script, nothing fetched; the files read again on each request) and the illustration as it is, the page renderer, and the title and section summary `jig brief show --text` prints; backs `jig brief show` |
| `internal/claudetest/` | `API`, `Session`, `Serve` | scripted sessions (tool calls in order) → a stand-in Messages API on loopback that the real `claude` CLI runs against, for the live CLI tests; `API.Observe` hands a test the text of every message of each request |
| `internal/correct/` | `Check`, `Input`, `Note`, `Stall`, `Stalls`, `Stalled`, `Drift`, `BriefDrift`, `Verified`, `ScopeDrift`, `Scope`, `WorkspacePaths`, `SliceWorkspace`, `SliceWorkspaces`, `Outside`, `SliceGuess`, `Unsliced`, `Owners`, `RestackRef`, `RetargetRef` | a ticket journal, its slices and brief, and the commit a tool call made → the corrections a session needs: a slice whose oracle failed the same way twice (marked stalled when unattended), a brief that changed under verified slices, a commit outside every slice workspace (a merge commit is none), a commit that names no slice with the slices its files point to (each file belongs to the slices of the deepest workspace that holds it), a parent the ticket is stacked on whose branch moved past it (`restack`, once per parent head) or that merged (`retarget`, told once); each recorded as a `correction` journal line when first found and returned as a note for the observe hook to deliver |
| `internal/envrun/` | `Up`, `Shell`, `ShellOutput`, `ShellOutputEnv`, `ChildEnv`, `RecordDirEnv`, `KillTree` | a `manifest.EnvClass` + ticket/dir → a running `Handle`, or `Unavailable`; a command with a time limit → its output (an oracle run), `ShellOutputEnv` with extra environment variables for that command alone, no command it runs inherits `JIG_RECORD_DIR` (`ChildEnv`); a process tree ended whole |
| `internal/events/` | `Envelope`, `SrcJournal`, `SrcLedger`, `Journal`, `Scope`, `Read`, `Reader`, `NewReader`, `Follow`, `FollowOptions`, `PollInterval` | ticket journals and the machine ledger (the ledger lines about the scope's tickets, or all of them) → one stream of `Envelope`s (a `journal.Line` with the file kind and, on journal lines, the project) ordered by each line's instant; the same followed by polling, opening and closing every file each time, and telling new lines from old by hash (journals) and offset (ledger) |
| `internal/fixture/` | `Build`, `Generate`, `RepoRoot`, `RemoveBrief`, `Branch` | a dir + `Opts` → a fixture repo, its store, and a scripted attempt scenario (plus the machine mapping under `Opts.Home`, by default the jig home `home.Root` resolves); `Generate` builds into a `t.TempDir()`; `RepoRoot`: a caller's source file → the module root; with `Opts.SecondRepo`, a second repo (one workspace, `gamma`, listed second in the project) and a ticket slice `e` on it; with `Opts.Orchestrator`, an orchestrator folder (a `jig.yaml` marker of kind orchestrator) holding two nested repos, `api` and `web`, each with a manifest and a bare remote, over a v3 store with no remote and a ticket with a slice in each repo; `Branch` and `Fixture.Branch` are the fixture ticket's default branch, and `RemoveBrief` makes a ticket brief-less keeping that branch; the fake `gh` answers `pr view <url> --json state` from its pull requests (`GhPull.Repo` names the repository in the url) |
| `internal/gittest/` | `Run`, `AtExit` | `*testing.M` → a hermetic git config for the whole test binary, then its exit code |
| `internal/gitx/` | `Run`, `RunEnv`, `RunRaw`, `MaintenanceAuto`, `RevParse`, `MergeBase`, `CommitsIn`, `IsAncestor`, `Missing`, `DiffNameOnly`, `FileExistsAtRev`, `PathExistsAtRev`, `IsLocalRemote`, `GuardedPush`, `CommonDir`, `SameDir`, `TopLevel`, `CommitTime`, `OpenRepo` (`Repo`: `State`, `CommitAll`, `Push`, `Fetch`), `RunInput`, `RunInputEnv`, `RefExists`, `WritersAttributes`, `WorktreeAddDetached`, `WorktreeRemove`, `Trailer`, `Identity`, `ResolveIdentity`, `Repo.CommitAllAs` | argv + a working dir → git plumbing output, or a refused push; a store's directory → the same store operations in process (go-git), or `ErrUseCLI` for the caller's git-program path; a command with standard input (`RunInput`), and whether a ref exists, read from the ref files with no process (`RefExists`); `WritersAttributes` is `StoreAttributes` plus the union merge driver for `*.ndjson`, and the in-process repo serves either; the identity a commit in a directory is authored with, from `git var GIT_AUTHOR_IDENT` (`IDENTITY_REQUIRED` when none), and `CommitAllAs`, `CommitAll` with the identity looked up only once there is something to commit |
| `internal/home/` | `Root`, `RootFrom`, `UserDirFrom`, `MachinePath`, `PoolDir`, `IntentExcerptDir`, `IntentScratchDir`, `RecordDir`, `PicksDir`, `ReviewDir`, `PullRequestStatesPath`, `LedgerPath`, `HooksDir`, `LogsDir`, `VerifyWorktreeDir`, `VerifyLogPath`, `IgnoredPath` | `JIG_HOME` (or the real home dir), read from the process (`Root`) or from a `getenv` it is given (`RootFrom`) → the jig home root, which `cmd/jig` resolves once and passes down; a root → per-machine paths, including the directories intent excerpts and summarizer scratch directories go under, where the recordings of one builder's oracle run (`RecordDir`: store id, ticket, commit, run) live, where the recordings a publish picked are staged (`PicksDir`: store id, ticket, head), and where a reviewer round's list of the recordings is written (`ReviewDir`: store id, ticket, round); the file this machine keeps the host's answers about pull requests in (`PullRequestStatesPath`); the machine ledger file, the global git hook directory, the logs directory, and the detached worktree and log of one verification (`VerifyWorktreeDir`, `VerifyLogPath`: store id, ticket, commit, slice); the list of directories `jig ignore` told jig not to track |
| `internal/intent/` | `NewClaudeReader`, `Reader.Load`, `ReaderFor`, `Among`, `Best`, `RenderExcerpt` | the one session a ledger record names (`Reader.Load`; `ReaderFor` maps a ledger harness name to its reader, Claude Code only) → its `Session`, and the sessions among them ordered by overlap with a round's files with no bar (`Among`); a repo's git common dir + a time window → matching local agent `Session`s (`Discover`, the fallback search); a scope diff's files → the `Match` whose excerpt a gate reviewer reads (`Best`, which scores against a bar) |
| `internal/journal/` | `Append`, `Read`, `BuiltCommits`, `GreenClaims`, `FailedAttempts`, `LastOracleSeconds`, `VerifiedSlices`, `RenderChangelog`, `RenderConsolidated`, `RenderDiffChangelog`, `Spend`, the `Event*` constants, `VerificationState`, `Pending`, `NextAttempt`, `Path` | journal `Line` events (a `recorded` line carries the `Recording`s of a builder's green oracle run; a `publish-picks` line carries the `Pick` made of them) → `journal.ndjson` and rendered changelogs; a ticket's journal → the commits jig built and verified; a `Line` also carries the digester fields (session, agent, harness, source, signature, summary, provenance, spend, the `repo` a `pr-body`, `publish-approved` or `pr` line is about in a project of several repos, and so on), the event names are constants, the default `ts` is RFC 3339 with nanoseconds, and `Append` holds the store's checkpoint lock; `Append` is one `O_APPEND` write in place under the checkpoint and file locks and is refused (`JOURNAL_LOCKED`) without them, and `Read` leaves out any line that is not a JSON object, so a torn line hides nothing |
| `internal/ledger/` | `Append`, `Read`, `ReadFrom`, `Sessions`, `Recent`, `StartSession`, `TouchSession`, `EndSession`, `MintID`, `Stamp`, `ReadBackward` | the jig home root and a `journal.Line` → one `O_APPEND` line in `ledger/ledger.ndjson` under the ledger lock (`LEDGER_LOCKED` on contention); the file → its lines, skipping malformed ones, or the lines after a byte offset and the next offset; the session lines (start, touch, end, tool) → one `Session` per id (cwd, clone, transcript, ticket, slice, agents, files, last tool, started, last seen, ended), and writers for the session lines; the ledger newest line first, in chunks from the end, up to a byte bound (`ReadBackward`) |
| `internal/locate/` | `Where`, `Opts`, `Locate`, `NormalizeRemote` | a jig home root and a directory → where it is (project, store, repo, branch, ticket, slice, session, and how each was found): `--store`, then a `jig.yaml` marker found walking up, the machine map by path (a linked worktree reads as its clone) and by origin remote, then a store directory, else none; a clone found by marker or remote is added to the machine map unless it is under a `jig ignore`d directory; stores of both layouts, and the repo's name is the marker's |
| `internal/manifest/` | `Resolve`, `MatchesInvariant`, `Manifest.OracleLimit`, `Manifest.WorkspaceIn` | a repo dir → a `Manifest` of workspaces, oracle commands, env classes, and invariant-sensitive paths; a file path → whether it matches a declared invariant; a `timeout:` key bounds a verification run (`OracleLimit`, 30 minutes by default); a repo's name and the workspace a slice writes → the workspace of that repo's manifest, reading `<repo>/<id>` and a bare id (`WorkspaceIn`) |
| `internal/mcp/` | `Server`, `NewServer`, `Info`, `Client`, `ElicitResult`, `Tool`, `Param`, `Args`, `Result`, `Tools`, `Options`, `Runner`, `Instructions` | newline-delimited JSON-RPC 2.0 on stdio (`initialize`, `ping`, `tools/list`, `tools/call`, cancellation) → jig's commands served to an agent harness as nine tools, each a command run in process through a `Runner` with the arguments a model may give it, its stdout the result; `ask` also puts its question to the person with `elicitation/create` when the client declared forms, records the accepted answer as the person's, and falls back to the recorded question |
| `internal/media/` | `Verify`, `Kind`, `PlainName`, `PlainParents` (every directory between an evidence directory and a media directory is a plain one), `LstatPinned`, `HashRegularFile`, `EntryLabel` | a directory jig made + a session's listing of files in it → the `File`s that passed what `gh ... --attach` accepts (a plain name, an allowed type, a regular non-empty file within its size limit, hashed from the very file checked), or a refusal naming the first that did not; a standard-library leaf, so `verify` and `verifydeliver` can both use it |
| `internal/mergefacts/` | `Reader` (`Of`), `PRCache` (`Read`), `PRs`, `PRRead` | a ticket → whether its work merged, per repo it builds in: its journal (`publish-done`, the `pr` lines), the state the repo host gives the latest pull request recorded for the repo, and the ticket's branch in the repo's mapped clone against `origin/<target>`, judged by `ready.JudgeRepo`; a pull request url → its state from `gh` through `repohost.PRState`, kept in `<jig home>/cache/pull-requests.json` (a merged answer for good, an open or closed one for an age the caller sets, the last answer when the host cannot be asked) and asked once per run - wired into `jig claim` and `jig next` |
| `internal/migrate/` | `LoadMap`, `BuildPlan`, `Apply`, `CheckSchemaVersion`, `CheckClean`, `CheckLevelWithOrigin`, `CheckNoLeases`, `BranchWarnings`, `RewriteProjectYAML`, `OldTicketIDs`, `Steps`, `NextStep`, `RewriteProjectYAMLToV3` | a rename map + a v1 store → a `Plan` (every ticket's old id, new id and title, and every file the migration would move, delete or rewrite), refusing a ticket the map leaves out, one the store lacks, an undeclared key, or a map that declares the key the store's old ids already carry; the refusals `jig store migrate`'s apply alone runs (schema 1, clean, level with origin, no lease held) and the warning it only prints (a `jig/<old id>` branch still on a repo's origin); a `Plan` → every ticket folder moved, its record rewritten (title, body and `aliases:` resolved on the v1 store, `blocked_by` translated), its local tracker files retired, every chart and `project.yaml` rewritten, committed once ([ADR 0031](docs/adr/0031-store-layout-v2-and-its-migration.md)); a store at schema_version v → the one step to v+1 (`Steps`: v1 to v2 with a rename map, v2 to v3, where `project.yaml` becomes `store.yaml` and the project's `jig.yaml` lands in its mapped clone); an apply with no identity in the store is refused (`IDENTITY_REQUIRED`) before it moves anything |
| `internal/mirror/` | `Sync`, `ResolveTicketTitleBody`, `RenderTicketBody`, `RenderChartBody`, `TicketStatus`, `ChartStatus`, `IsOpen`, `FindPullRequests`, `ScanTitleAndBody`, `LoadPublishTerms` | `Deps` (a store, `project.Config`, the jig home, a `github.Client`) + `SyncOpts` → a `SyncReport` (issues created, updated, recreated, linked, placed on the board, skipped on a publish-safety hit, and every drift line), the records written in the tracker tree (moved there from the old per-ticket places) in the bridge's own shape, and a GitHub Project kept current (a ticket closed without a pull request has its issue closed too, as completed when covered and as not planned otherwise, with the Status `Not planned` unless it is Done) - wired into every `Store.AfterCheckpoint` and `jig trackers sync`; a mirrored issue's footer names the ticket's branch when another tracker leads branch names |
| `internal/mirror/github/` | `New`, `Client` (`CreateIssue`, `FetchIssue`, `UpdateIssue`, `CloseIssueCompleted`/`CloseIssueNotPlanned`, `ReopenIssue`, `AddComment`, `AddSubIssue`/`RemoveSubIssue`, `AddBlockedBy`/`RemoveBlockedBy`, `PullRequestsByHead`, `LookupProject`, `EnsureProject`, `PlaceItem`, `ItemFieldValues`, `RepositoryIsPublic`) | an endpoint + token (a fake GraphQL server and a fixed token in every test, `api.github.com/graphql` and `gh auth token` in production) → the GraphQL mutations and queries `internal/mirror` drives a sync through, one mutation per second and retried with backoff (4 attempts) on a server error or a rate limit; `EnsureProject` completes the Status field's six options, `Not planned` among them, keeping every existing one |
| `internal/observe/` | `Event`, `Result`, `Adapter`, `AdapterFor`, `Harnesses`, `Deps`, `Handle`, `Run`, `RunEvent`, `Note`, `NoteCtx`, `NoteSource`, `RegisterNoteSource` | a harness hook payload → a neutral event (per-harness adapters: claude, codex, gemini, cursor, copilot, opencode, neutral) → session records in the machine ledger, the verification of a commit that moved HEAD (once, whoever saw it first), the notes a session has not heard, a stop message or an unattended refusal to stop, a note of a question left open; and back into the shape the harness reads; nothing outside a jig project is recorded |
| `internal/outcome/` | `ParseJSON`, `ParseText`, `Signature`, `StallCounter` | a session result (JSON or text) → a typed `Result`, and a stall signature |
| `internal/phase/` | `Derive`, `OpenRoundIn`, `EverClaimed`, `Prompt`, `Parse`, `Ticket.For` | a ticket's facts (brief and approval, slices and their states, outstanding asks, closed and open gate rounds, unpublished blockers, whether it is closed) and its journal lines → the four phases (brief, build, review, publish) each done, current or pending with its sub-steps, the current phase, whether it can start, why not (`PHASE_BRIEF_NOT_APPROVED`, `PHASE_BLOCKED`, `PHASE_NOT_BUILT`, `PHASE_ROUND_OPEN`, `PHASE_ASKS_UNDECIDED`, `PHASE_NOT_REVIEWED`, `PHASE_DONE`, `PHASE_CLOSED`) and the prompt that starts it ("Plan T", "Build T", "Review T", "Publish T"); `For` answers the same for a phase a caller names, warning (never refusing) when it is done; a closed ticket is done whatever its journal says, with no prompt; a publish whose lines name several repos has a pull request sub-step for each; `Facts.SessionName` and `Facts.ReviewerName` word a session and the reviewer of a round (the TUI says "reviewer subagent" for a session that also built); `EverClaimed(lines)` says whether any slice was ever claimed or verified, which `jig status` reads to tell `queued` from `building`; pure, so the TUI imports it and `jig next`, `jig status`, `jig here` and the claim and gate refusals all read it |
| `internal/pool/` | `Acquire`, `Dir`, `Usable`, `CheckTicket`, `Compare`, `DivergedError`, `RequireBuilt`, `HoldsUnpushedBuilt`, `MustExistOnOrigin`, `RecutUnlessBuilt` | the jig home root + repo/remote/target/branch + a ticket and its role (build, gate, publish) → a `Lease` (a full clone, re-pointed to its start point, and synced with its branch when origin has it; anything git shows is not a repository of its own is moved aside and cloned afresh; a merge, rebase, am, cherry-pick or revert a crashed session left unfinished is ended with `--quit` and an unmerged index reset to HEAD, never moving a branch, and named in `Lease.Recovered`) |
| `internal/project/` | `LoadStoreConfig`, `LoadConfig`, `WithMarkerRepos`, `Config.CheckSchemaVersion`, `Init`, `InferKind`, `CheckInitDir`, `Sync`, `ResolveRepoArgs`, `FetchRepo`, `PlanShare`, `RunShare`, `FindMarker`, `FindProjectMarker`, `LoadMarker`, `WriteMarker`, `SetMarkerRepos`, `SetMarkerStore`, `InitProject`, `UpdateMachine`, `LoadIgnored`, `Ignore`, `Unignore`, `IsIgnored` | a store (`store.yaml` of a v3 store, `project.yaml` of a v2 one) plus the project's `jig.yaml` marker and the machine mapping under the jig home root → a `Config` (the store's settings, the project's name and repos); a `Config` + whether an older schema is allowed → nil, or a refusal naming `jig store migrate` (older) or an upgrade (newer); a folder → a project (a sibling v3 store committed as the person, the marker, the machine-map entry) as a single repo or an orchestrator over the repos nested in it, fetched as submodules or gitignored clones; an orchestrator's `repos:` list kept in step with its folder; a project → its remotes set (or created through the host's CLI), the store's remote recorded in `jig.yaml`, and the store and repo pushed; a directory → the nearest `jig.yaml` marker walking up from it (the per-repo `.claude/jig.yaml` manifest is never one); the machine map is written atomically under a lock, and `UpdateMachine` is a locked read-modify-write; the list of directories `jig ignore` told jig not to track; the marker's `branches: from:` says which tracker leads branch names (`Config.BranchesFrom`) |
| `internal/ready/` | `ComputeReady`, `Schedule`, `Batches`, `Place`, `SplitWorkspace`, `ProjectRepos`, `BuiltIn`, `Waves`, `Tickets`, `Merge`, `JudgeRepo`, `ByPriority`, `SharedRanks`, `Sites`, `Overlaps`, `JoinBlockers`, `Requeue`, `RequeueSlice` | a ticket's slices and their states → the slices ready to build (queued, every blocker green), batched per repo, and their depth; the repos of a project with the workspaces and oracles their clones' manifests declare → for each slice's workspace the repo that owns it and its id there (`<repo>/<workspace>`, or a bare id one repo declares), and the bare ones nobody or several repos declare (`Place`, `SplitWorkspace`), and the repos a ticket's slices build in (`BuiltIn`); tickets, their blockers and their journals → closed, done, blocked, ready, active, green or new; a merged blocker holds its ticket back until its work has merged (`Ticket.Merge`: judged per repo from the recorded pull request, then the branch being in the target, then nothing, by `JudgeRepo`), `Status.Why` says what each blocker misses, and `StateClosed` releases the tickets that wait for it when it is covered or superseded; ready tickets and their ranks → the order to start them (rank ascending, then id, the unranked last) and the ranks two of them hold; ready tickets and the (repo, workspace) sites their slices build in → each pair that builds in one site; a ticket and `--from-brief-diff` → every slice whose brief section changed back to queued with its question closed and a `requeue` line journaled; a ticket and a stalled or env-blocked slice → that slice queued, attempts kept (`jig requeue`) |
| `internal/recall/` | `Build`, `Read`, `Report`, `Duplicates`, `CoveredBy`, `Similarity`, `Threshold`, `ParseLedger`, `FogItems` | the open tickets (with the state `internal/ready` gave them) and the Material read from the store (charts maps, platform documents, ledger.md) and a query → a report: the open tickets and their blocked_by edges, the tickets whose title or body comes near the query (token Jaccard of at least 0.5), each chart fog section, the platform documents, and the ledger answers; the same similarity tells `jig validate` that a slice goal reads like shipped work. Backs `jig recall` |
| `internal/repohost/` | `New`, `Host.CreatePR`, `Host.CreatePRWithMedia`, `Host.FindOpenPR`, `Host.UpdatePR`, `Host.UpdatePRWithMedia`, `Host.CommentPR`, `Host.ReadPRBody`, `PRState`, `ParsePRURL`, `Host.PRHead`, `CreateRepo` | a repo's own `remote:` → a `Host` (a GitHub host, for a remote on github.com over ssh or https; refused up front, `GH_NOT_INSTALLED`, when `gh` is not on PATH) or `nil` (any other remote: a local path, or another host); a branch, base, title and a body file → an opened or updated pull request, with or without the picked recordings attached, found by its qualified head into a base, or read back; a PR url → the state `gh` gives it, open, closed or merged, and whether it could tell (`PRState`); a pull request URL → its host, repository and number (`ParsePRURL`), and the branch, commit and state it is from (`Host.PRHead`); a private repository created through `gh` and made the origin of a local repo (`CreateRepo`) |
| `internal/revieweval/` | `LoadCorpus`, `RunCorpus`, `MatchRound`, `ScoreRound`, `RenderReport` | a labeled corpus (`testdata/revieweval`) + a session backend → a `CaseScore` per case, matched structurally against seeded gold through the real reviewer contract; its `launch.go` maps a `verifydeliver.Launch` to a screened `session.Dispatch`, so a reviewer the evaluation launches is the only session jig still starts |
| `internal/screen/` | `Command`, `SecretPath`, `ToolCall`, `Granted`, `Grants`, `Hook` | a shell command, path, or tool-call input → allow, or deny with a reason; a tool name → whether a passing screen grants it; a PreToolUse hook payload on stdin → the allow or deny decision a screened headless session's hook prints (the program `testdata/fixture/screenhook`; the jig binary links none of it) |
| `internal/session/` | `New`, `Backend.Run` | a `Dispatch` (paths to `slice.json`/`result.json`, and for a gate round the one file of recordings the reviewer is told to read) → `result.json` written to disk; two backends, `fake` (a scenario player) and `headless` (a screened `claude -p`, its hook program named by `Options.ScreenBinary`); linked by the reviewer evaluation only, never by the jig binary (`lint/deps_test.go`) |
| `internal/setup/` | `Harness` (`Harnesses`, `HarnessNamed`: `GlobalFile`, `ProjectFile`, `Merge`, `Unmerge`, `Installed`, `Plan`), `MergeClaude`, `UnmergeClaude`, `PlanClaude`, `JigCommand`, `OwnedCommand`, `ObserveCommand`, `OpenCodeShim`, `GitConfig` (`InstallGlobal`, `UninstallGlobal`, `InspectGlobal`, `InstallClone`, `UninstallClone`, `InspectClone`), `WrapperScript`, `ExcludeFile`, `UnexcludeFile`, `InstallSkills`, `RemoveSkills`, `InspectSkills`, `StubBody`, `MergeHint`, `UnmergeHint`, `PlanHint`, `Finding` | a scope and a harness → hook entries merged into the harness's file in its own order (Claude Code, Codex, Gemini CLI, Cursor, GitHub Copilot, an OpenCode plugin), global git hook wrappers that run each repository's own hooks and the earlier `core.hooksPath`, skills (in full, or one stub in a project), and the hint in the agents' memory files, installed or removed idempotently, owning only what jig wrote, recognized by its command, stamp, markers or first line and not recorded; and the state, cost and fixing command of each piece (`jig setup`, `attach`, `skills`, `hooks`, `githook`, `hint`, `doctor`) |
| `internal/slicectx/` | `Build`, `Input`, `Context`, `Context.Blocks` | a store, a ticket, a slice and the clone it builds in → what a builder needs to start: the brief sections the slice is cut from and the brief's Seams, the commits (found by their `Jig-Slice` trailer) and files of the slices already verified, the oracle command and how long jig's last run took, the failed attempts with why, and the slice's questions and notes; rendered as text blocks or one JSON object by `jig context`, which runs no context tool ([ADR 0034](docs/adr/0034-jig-advises-context-and-runs-no-context-tool.md)) |
| `internal/spawn/` | `Spec`, `Detached` | a program, its arguments, directory, environment and log path → a detached process (new session on Unix; on Windows a hidden console of its own, a new process group and job breakaway) whose pid is returned and which outlives the caller |
| `internal/spend/` | `Totals`, `FromTranscript`, `Prices`, `LoadPrices`, `Line`, `Latest`, `FromLines`, `ReadBudget`, `WriteBudget`, `ProjectDefault`, `Check`, `Notes` | a Claude transcript (and its subagents') → the tokens and list-price dollars one session spent, each API message once, the models with no price named and not guessed; those totals → a cumulative `spend` journal line per session, summed per ticket; a ticket's `budget.yaml` (seeded from the jig.yaml budget of the project) held against them → near, over, or none, told as an advisory `observe` note and shown in status, never enforced |
| `internal/stack/` | `Read`, `Judge`, `Plan` (`Stacked`, `In`), `Repo` (`Shared`), `Merges` | a ticket stacked on another (a `stacked` blocker) → for each repo it builds in, where its branch starts and where its pull request goes: the parent's branch (as the clone holds it, local or `origin/`) in a repo they share while the parent has not merged there, the target elsewhere and once it merged; whether the parent's branch moved past the child's (a restack) and whether the parent merged while the child lacks the target's head (a retarget). Advice only: read by `jig next`, `jig here`, the publish (`verifydeliver.Deps.Stack`) and the correction notes (`correct.Input.Stack`); it rebases and merges nothing |
| `internal/staircase/` | `Select`, `Dearest`, `Default` | build `Signals` (the slice's failed attempts, invariant match) + `Config` → a model rung: invariant floored to the dearest rung, one rung up per failed attempt, otherwise the first rung; `Dearest` is the reviewer evaluation's default model; the jig binary does not link it |
| `internal/store/` | `Open`, `TicketDir`, `TicketRelDir`, `Lock`, `AtomicWrite`, `BriefSectionHashes`, `ReadSlices`, `ReadChart`, `WriteChart`, `ReadTicket`, `Ticket.Adopted`, `ReadTicketDeps`, `CreateTicketRecord`, `WriteTicketBranch`, `CheckAdoptableBranch`, `TicketBranch`, `ResolveTicketBranch`, `TicketFilePath`, `StartSHAPath`, `WriteStartSHA`, `Store.ID`, `TicketIDs`, `Mint`, `Claim`, `ResolveTicket`, `CheckAliases`, `LockCheckpoint`, `TryLock`, `ReadApproval`, `WriteApproval`, `BriefSHA256`, `TicketForBranch`, `BranchLeadID`, `Mode`, `Writer`, `PlanWriters`, `MigrateToWriters`, `ClaimLine`, `Claims`, `AppendClaim`, `Driver`, `ClaimDriver`, `ReleaseDriver`, `Question.Provenance`, `AnswerAs`, `AddQuestion`, `ReadQuestion`, `BriefSections`, `BriefTitle`, `Slug`, `Ticket.Slug`, `TrackerIDs`, `BranchLeadIssue`, `Store.BranchesFrom`, `Store.BranchesLedByGitHub`, `Closed`, `Store.CloseTicket`, `Store.ReopenTicket`, `Store.SetTicketRank`, `Store.TicketFileRelPath`, `TrackerEntry`, `Store.TrackerRelPath`, `Store.TrackerRecordRelPath`, `Store.TrackerRecords`, `Store.TrackerRecordsFor`, `Store.WriteTrackerRecord`, `Store.LinkTrackerEntry`, `Store.RebuildTrackerLinks`, `Store.CheckTrackerTree`, `Store.LegacyTrackerRecords`, `Store.MoveLegacyTrackerRecord`, `Store.SymlinksOn`, `Store.ReloadTrackerTree`, `IllustrationFile` | ticket-folder and chart-folder reads/writes → the truth-repo tree described above; an id → its folder under `tickets/`, absolute (`TicketDir`) or store-relative (`TicketRelDir`); a store clone → the stable id its machine-local files are keyed by; `tickets/` → every id-shaped folder under any key, sorted by key then number (`TicketIDs`); a key + a ticket's own record → the next id under that key, its folder created and its `ticket.yaml` written whole (refused, before anything is written, when jig cannot use the id it computed); a mint (or other write) + a commit message → that id landed on the store's origin, retried after a push the origin rejects, undone and refused (`ID_NOT_CLAIMED`) after too many or any other failed push, committed with no push on a store with no origin; `Sync`, `Push` and `Claim` (and `journal.Append`) hold a cross-process checkpoint lock that is re-entrant within a process; a ticket's approval record (`approval.yaml`, pinned to the sha256 of its brief, or, when it has an `illustration.html`, to the digests of both files hashed together); a branch name → the ticket it leads with (`jig/` stripped, the leading id resolved through aliases); writers mode (a `claims` branch exists): the clone works on `writers/<name>`, merges every other writer branch and the legacy main with the union driver for NDJSON files, pushes its own branch only, and refuses a rewritten branch and a taken name (`WRITER_REWRITTEN`, `WRITER_NAME_TAKEN`); the claims log (`claims.ndjson`, on the `claims` branch in writers mode and on main otherwise) holds the one-at-a-time facts, a minted id and a ticket's driver (`DRIVER_HELD`), and an append is not a claim until the origin has it; `Mint` reserves the id on the log first in a writers store; a question file records who its answer speaks for, and every read of one is retried briefly on Windows, where an open that meets the atomic rename fails with a sharing violation; `Open` takes a directory with `store.yaml` or `project.yaml`; a ticket's default branch is `<id>-<slug>` (the slug fixed at mint, never recorded as `branch:` - the record's, else the title's, else the brief's first heading with a leading `<id>:` taken off, and the bare id when none gives one; a mirrored ticket's GitHub issue number leads when the project's branches are led by GitHub), and `TicketForBranch` also binds a branch led by a ticket's issue number (`how` `remote`); every commit is authored by the identity git resolves in the store clone (`Store.identity`, resolved on first use and kept), and `Claim` refuses with `IDENTITY_REQUIRED` before it writes; a remote item (`TrackerEntry`: provider, scope, id) and the ticket or chart it is of (`TrackerOwner`) → its record and its link in `trackers/`, with `TrackerIDs` reading a ticket's issue from the tree and falling back to the record in its own folder; a ticket's `closed:` entry (`Closed`: reason covered, superseded or not-planned, by, note, at) is written through `Claim` by `jig close` and `jig reopen`, and `Closed.Releases` says whether the tickets that wait for a closed ticket go on (covered, superseded) or stay held (not-planned); `rank:` is set or cleared by `SetTicketRank` |
| `internal/termrec/` | `Cast`, `Event`, `ReadAsciicast`, `Cast.WriteAsciicast`, `Cast.Validate`, `Cast.SVG`, `SVGOptions`, `Cast.FinalText`, `NewRecorder`, `Recorder.Stream`, `Recorder.Cast` | a program's writes to its pipes, each stream teed into a `Recorder.Stream` → a terminal recording (its size and each write with its time; asciicast v2); a recording → an animated SVG of the screen as it changed, within `MaxSVGBytes`, or the last frame as text ([ADR 0029](docs/adr/0029-demos-are-recordings-of-the-builds-end-to-end-scenarios.md)) |
| `internal/tui/` | `Model`, `New`, `Config`, `Source`, `Runner`, `Run`, `Look`, `Standing`, `StandingOf`, the `State*` names | the event stream (`events.Envelope`s from a `Source`) and the output of the verbs it runs (`jig status`, `jig next --json`) → seven screens (project, ticket, brief, build, review, publish, slice); a ticket's four steps in order, which are `internal/phase`'s answer for the facts the screens hold (the status and the journal), so the screens and `jig next` never disagree about where a ticket stands; under them, for the step under the cursor, a sub-steps block (six sub-steps on an 80 by 24 screen and more on a taller one, each with a state, a label and a few words, the ones that matter first and a count of the rest, taking its room from the blank lines before it takes any from the activity, which keeps three lines), then its activity, each line naming the sub-step it advances in the block's words, and the "next" line, which takes up to two lines; the detail screens show the records (the brief's sections with their first lines, read from `jig brief show --text` and kept until a line about the brief arrives; each slice's workspace and oracle and its commits with every run of its oracle; each gate round with its reviewer, independence and range; what the latest round found; the first lines of the pull request body and changelog), all from the tables and blocks `jig status` prints, a page longer than the window being moved by the cursor, and a page with no row to stand on by the arrow keys; a ticket screen with no event for three seconds reads the status again (one read at most every three seconds, for the ticket on the screen) and puts what that shows of the brief in the brief's activity; the header says a step and its state as a phrase; `jig tui <T>` on a ticket the store would mint and has not yet (a well-formed id under a declared key) opens a screen that waits for it and fills in when the ticket has a line or a row in the ticket list; drawn with an icon and a color per state, and keys that act through verbs (answer as a person, keep or dismiss an ask, gate, publish: approve, then yes); on a terminal it takes the screen, on a pipe it prints each screen whole; it reads no store (a test pins that) |
| `internal/verify/` | `Deps`, `Request`, `Result`, `Run`, `Spawned`, `Spawn`, `WorkerArgs`, `LogPath` | a ticket, a slice and a commit of the operator's clone → the slice's oracle run on a detached worktree of that commit (under a per-(commit, slice) lock), recorded as the frontier recorded a builder's green run: `verify-pending`, `oracle` (with `summary` and `signature` on a failure), `recorded`, `verified`, `verify-error`, and the slice's state; `Spawn` starts the same as a detached `jig verify --worker` and notes its pid in the ledger |
| `internal/verifydeliver/` | `GatePrepare`, `GateFinish`, `CurrentRound`, `ReadStatus`, `Publish`, `PublishBody`, `PublishRecordPR`, `PublishApprove`, `ParsePicksResult`, `RenderPicksPrompt`, `Freshness`, `ReadRounds`, `TriageDecisions`, `Gate` (the driver for a launched reviewer), `Launcher`, `Launch`, `NewLaunchedGateSource` | `Deps` + `GateOpts`/`PublishOpts` → an open gate round (`Prepared`: the request a reviewer that jig did not start reads, written under the store's `work/`) and, when the reviewer reports, a `GateReport` with a verdict and whether the reviewer was independent of the builders (provenance from the journal and the ledger); a `PublishReport` (the reviewed head pushed unchanged and its PR opened or updated, the body carrying a `## Demo` section and its media when the shipped head has one, the flows picked from the build's recordings by a `--picks` file), a `BodyReport`, a `RecordReport` (a PR somebody else opened, checked against the branch and the head) or an `ApproveReport`; `jig publish` opens a pull request for each repo the ticket's slices build in, from the one branch, each body naming the others (`multirepo.go`: `publishRepos`, the links, `pendingRepos`); a stacked ticket's pull request goes into the parent's branch while the parent has not merged in the repo (`Deps.Stack`, `publishTarget.base`), its body has a `## Stacked on <parent>` section, and a bare publish refuses `PUBLISH_PARENT_NOT_PUSHED` while that branch is not on the origin; a gate round covers the repos the ticket's slices build in and names their files `<repo>/<path>` (`roundrepos.go`); a `TriageDecision` per finding (the latest `jig triage` line, pending when it comes after the last gate-round or gate-clean line) that `recordedTriage` applies when a round closes and `jig status` and the review request show beforehand; the gate rounds a ticket has recorded read back for display (`ReadRounds`: each round's verdict, reviewer, independence, reviewed range, paths read and the findings it raised, joined from report.yaml, findings.yaml, the round's request while it is in `work/`, and the journal's gate lines); whether a session is fresh for a ticket, from the journal's lines and the ledger's against the ticket (`Freshness`: fresh, not fresh with why, or unknown); the round's intent excerpt, from the transcripts of the sessions the ledger names for the ticket (the driver, then the builders), else from the search of local transcripts (`intent_infer.go`); no reconcile, squash or memorize commit is ever made on the branch; the package links no session code (`Launcher` is implemented by `internal/revieweval`) |

## Session backends

jig starts no coding session ([ADR 0032](docs/adr/0032-jig-attaches-to-the-harness.md)). `internal/session` remains for one consumer, the reviewer evaluation (`internal/revieweval`, [ADR 0009](docs/adr/0009-review-eval-structural-gold.md)), and the jig binary links none of it: `lint/deps_test.go TestCLIDoesNotLinkSession` pins that `go list -deps ./cmd/jig`, on linux, darwin and windows, names none of `internal/{session,staircase,screen,frontier}`.

The contract between that code and a backend is still pure disk: a `Dispatch` names the paths of `slice.json` and `result.json` (and, for a gate round, the one file of recordings the reviewer is told to read, `ExtraReadFile`), the backend runs a session in a worktree, and the caller reads back `result.json` (outcome, summary, commit, and - for `needs-input` - a question). Nothing crosses in memory. Two backends implement that same narrow interface:

- **fake** - replays a scripted scenario directory; no session, no network. The CI and fixture path.
- **headless** - runs a local `claude -p` subprocess in `dontAsk` permission mode. The command and secret screens attach as a PreToolUse hook whose program is named by `Options.ScreenBinary` (required for a screened dispatch; the program is `testdata/fixture/screenhook`, invoked as `<program> _screen`, and it is not part of the jig binary); its allow is the only grant of the session's shell and read tools, though the operator's own user settings (which still load) can grant more on top. Edits are granted only inside the worktree and on the dispatch's own `result.json`. Its shell waits up to 30 minutes on a command and runs nothing in the background, since nothing collects a background command after the session's turn ends. The shell and reads it grants are the operator's own and are not confined, so this backend is not a security boundary.

The herdr backend, which drove a remote agent through herdr (inside WSL on Windows), is gone with `jig run`, and so is `WSLPath`; the scrub of a session's repeated paths still knows the WSL mount spelling (`verifydeliver/hostpaths.go`), since a session that runs in WSL on a Windows checkout sees it.

`verifydeliver` keeps the session-free half of a launched reviewer: `Launcher` (`Launch(Launch) error`, `Readable(file) bool`), `Launch` (ticket, round, worktree, request, result and recordings paths, model, effort, prompt), `NewLaunchedGateSource` and `RoundInput.Readable`. `internal/revieweval/launch.go` is the session link: it maps a `Launch` to a screened `session.Dispatch` (slice `gate`, attempt = round, `ExtraReadFile` = the recordings list) and answers `Readable` with `screen.ToolCall`. A backend that can continue a session it ran implements `session.Resumer` (`RunResumable`, `claude -p --resume <id>`), which only headless does; nothing in the binary calls it.

## Gate reviewer contract

A gate round is a record, and jig launches no model to make it. `jig gate` runs `GatePrepare` (`internal/verifydeliver/recorded.go`): it syncs the store, checks that every slice is green (`--early` skips that), points a gate lease, in each repo the ticket's slices build in (the project's one repo, or the repos `ready.Place` finds the slices in), at the copy of the branch to review ([A ticket's branch](#a-tickets-branch)), resolves the round's intent, journals `gate-open` (with the round's number), runs each repo's manifest's oracles for the slices that build there (a pass jig recorded on the same tree is reused, [ADR 0021](docs/adr/0021-the-gate-reuses-an-oracle-pass-on-the-same-tree.md)), resolves the scope base and diff, and writes the round's request. The round then stays *open* in the store's `work/` - the request `gate.round-N.review.json`, `gate.round-N.open.yaml` (round, branch, intent hash, opened ts, and for each repo its copy, head, base, scope, target and lease, the reviewer's own session, model and effort, and `--yes`) and the reviewer's drafts `gate.round-N.findings.ndjson` - and nothing is written under `gate/round-N/` until the round is closed. Reopening a round whose head has not moved keeps the drafts; a moved head starts again.

The request keeps the disk-only contract of the build sessions it once sat beside. `review.json` names the ticket, round, scope, base and head sha (in a project of several repos, a `repos` list instead of the four top-level fields scope, base, head and worktree: an entry for each repo the slices build in, with its worktree, branch, scope, base, head, oracles and the files it must cover), the round's resolved intent (source and path; `resolveIntent`, precedence brief.md then intent.md then none), slices/journal paths, manifest oracles, `oracles_passed` (every oracle run the gate made on this head before the review, all passed, or reused with `reused_from` from a pass of the same command, under the same env classes, that jig recorded on a commit with the same tree), the cumulative `open`/`dismissed` findings folded from every earlier round (an `open` ask also carries the decision a person or an agent has recorded for it with `jig triage`, and who made it, until the next round closes), and `must_review`: every file the scope diff touched plus every still-open finding's file. Nothing committed to the store names the jig home, so the store's copy has the worktree, excerpt and recordings paths cleared, and the whole request, with those paths, is a second copy under the jig home (`home.ReviewDir`, beside `recordings.json`) that `jig review context` prints as its `request:` row and `--json` returns. In a project of several repos every file the round names is `<repo>/<path>` - `must_review`, `reviewed_paths`, a finding's `file`, `findings.yaml` - and a finding's workspace is `<repo>/<id>`, so its fix slice builds in the repo of the finding with that repo's oracle. A project of one repo's request is unchanged.

The reviewer is any agent with a fresh context that jig did not start, a subagent or another session:

- `jig review context <T>` opens the round (or re-prints the open one, with the gate's defaults) and prints what a reviewer reads: `must_review`, `open`, `dismissed`, `oracles_passed`. `--session`, `$JIG_SESSION`, `--model` and `--effort` are recorded in the open record as what the reviewer says of itself, and the first time it names a session the journal gets a `gate-reviewer` line (the round, session, agent, model and effort), so a watcher sees the round being reviewed before it closes; a round reopened at the same head keeps its reviewer and journals the line again after its `gate-open`.
- `jig finding` appends one draft to `gate.round-N.findings.ndjson`, held to the rules of a result finding (line, action, risk, the oracle named when the manifest has more than one), or confirms an earlier finding with `--still-present <id> --line n`: unchanged earlier findings are confirmed by id and current line, which jig expands into the earlier finding before folding the round ([ADR 0022](docs/adr/0022-the-reviewer-confirms-an-unchanged-finding-by-id.md)).
- `jig review done` closes the round from the drafts plus `--reviewed <paths>` and `--summary`, or from a whole `--result` file. This is `GateFinish`: it re-checks that the branch's head is still the head reviewed (`REVIEW_STALE_HEAD` otherwise: run `jig gate` again; this post-hoc guard replaces the lease guard of a launched reviewer), parses and validates the report, folds the round and writes `gate/round-N/`. With no round open it is `REVIEW_NOT_OPEN`.

**Who reviews.** `jig gate` (after it opens the round) and `jig next <T>` (when review can start) tell the calling session (`envSession`) whether it may review the ticket itself. A session is *fresh* for a ticket when its id carries no journal line of the ticket but the gate's, triage and publish lines and the bookkeeping jig writes for a session, and no ledger line of it changed a file, made a commit, ran `git commit` in a shell or claimed; it is *not fresh* otherwise (the reasons are named, up to three), and *unknown* when the ledger has never heard of it and the journal has no line of it. A fresh session reviews in its own context. Any other is advised to spawn a subagent: in Claude Code the Agent tool (Task in older versions) with `subagent_type: general-purpose` and `model: opus` unless the person named another, run in the foreground (`run_in_background: false`) with no worktree of its own, and handed a prompt that says it did not build the ticket and is to run the review skill and reply with the verdict line. The main session never runs `jig review context` itself: jig sees a session and its subagents as one session (the commands a subagent runs carry no agent id), so if both read the round it cannot tell which closed it, and the round is not independent. The reviewer only reads the person's clone; a mutation check goes in the round's own worktree and is put back. With no session (the TUI's `g`) `jig gate` gives the generic advice. This is advice only: `jig review done` still judges independence, below.

The review is a read: test evidence is the oracles' job, and they ran on this head before the review, so the reviewer runs no tests, with an empty `oracles_passed` too ([ADR 0017](docs/adr/0017-the-reviewer-reads-the-gate-tests.md)). jig cannot confine an agent it did not start, so it does not try; it checks the report strictly and rejects the round (`REVIEW_INVALID`, the round stays open) on an unknown `action` or `risk`, a finding whose file is neither present at head nor deleted in the scope diff, an oracle that isn't a manifest oracle, a `prior` naming no known finding, a `still_present` id naming no known finding, repeated, or also a finding's `prior`, a `still_present` finding whose file is gone without a deletion, or `reviewed_paths` missing a `must_review` path.

**Independence.** The builders of a ticket are the (session, agent) pairs on its journal `claim`, `verify-pending` and `oracle` lines and on ledger `git-commit` and tool lines naming files. The reviewer is, in order: `--session`, `$JIG_SESSION`, the session in the open record, or the latest ledger `tool` line whose command runs `jig review context <T>` after the round opened. A reviewer that is one of the builders gives verdict `not-independent` and `REVIEW_NOT_INDEPENDENT`, the round recorded and the verb exiting 1; `report.yaml` carries `independent` (`true`, `false` or `unknown`), `reviewer` (session, agent, how), `effort` and `oracle_names`. An unknown reviewer is `independent: unknown`, counted with a note, and publish accepts it; publish refuses `not-independent` (`PUBLISH_NOT_INDEPENDENT`) and an unclean round (`PUBLISH_NOT_CLEAN`). Builders are read by ticket (and, from the ledger, by the ticket's branch, which has one name in every repo), so a round over several repos is judged against the builders of all of them.

**Recordings as evidence.** When the build recorded its end-to-end scenarios ([ADR 0029](docs/adr/0029-demos-are-recordings-of-the-builds-end-to-end-scenarios.md)) at the reviewed head or an ancestor of it, the round also hands the reviewer a `recordings.json` (`internal/verifydeliver/recordings.go`). The list is publish's selection - the latest recording of each flow, scenario and step, each checked again against its file under the jig home - latest build first, at most 50, each entry `{scenario, flow, step, caption, commit, kind, file}` with the absolute path of the file; `omitted` counts the ones left out by the cut or because the screen would refuse the reviewer's read of the file's name (`.env*`, `*_key*`). It is written under the jig home (`<jig home>/evidence/<store id>/<ticket>/reviews/round-<n>/`), never in `work/`: it names files of this machine, and the store is shared. What the recordings show is evidence like any other: a finding still needs a cause in the diff and cites a recording by its scenario, step and commit, and since a reviewer's words reach the store, the lease, the jig home and the store are left out by name from every finding's free text and the summary. A round with no recordings writes no file.

**Intent.** The round's intent has a provenance ([ADR 0012](docs/adr/0012-intent-provenance.md)): `brief` and `explicit` (`jig gate --intent`/`--doc`, recorded in `intent.md`, refused when the ticket has a brief) are the person's own words and binding; `inferred` is a hint; `none` means nothing states it. With no brief and no explicit intent, `PrepareReviewRequest` looks for the author's own session. It first asks the machine ledger which sessions worked the ticket (the ticket's driver, then its builders: the sessions on its claim, verify and oracle lines and on the ledger's commit and file-changing tool lines, never a reviewer's) and reads only their recorded transcripts, `intent.Among` picking the one that mentions the most of the round's changed files, with no bar. When the ledger yields nothing readable (hooks never ran, a session recorded no transcript path, the file is gone, or the harness has no reader), it searches: `internal/intent`'s Claude Code reader discovers the operator's local transcripts (under `RoundInput.UserHome`) whose working directory belongs to the same repository as the operator's mapped clone (`RoundInput.OperatorClone`; identity by git common dir, `internal/gitx.CommonDir` compared with `gitx.SameDir`) within a window anchored to the ticket's own merge-base and head commit times, `intent.Best` scores each candidate by file overlap with the scope diff, and the best match's excerpt (the developer's and the assistant's own text only - tool calls, tool results and thinking dropped, and so is a user record the transcript marks as the harness's own or as someone else's) is capped and written under the jig home (`home.IntentExcerptDir`), never the store, since a transcript can hold secrets. The request names it as `intent_excerpt` and the reviewer reads it; no model summarizes it, so the summarizer dispatch, its scratch directory and its restore of the lease went with the dispatch path. With no mapped clone, no home, no transcripts or no match, the request carries an `intent_note` saying why.

**Findings bookkeeping** (`findings.go`) folds the round onto the cumulative state: a finding's identity across rounds lives only in its own `prior` field, an open finding clears when its file was reviewed and nothing routed reported it again, or when its file no longer exists at head at all (checked directly against the lease, whatever this round's own scope diff says), and a finding recurring for the second time - once a fix slice built for it has actually gone green - is routed to a human regardless of the reviewer's own label. A would-be open fix that clears every other check (recurrence bound, a missing build target, the risk floor) is parked for a human the same way once the ticket's fix budget (`gate.fix_rounds`, compared against `UsedFixBudget`'s count of earlier rounds whose own `report.yaml` lists a fix slice it appended) is reached. Each of these three forced-ask cases records which one it was in `findings.yaml`'s additive `routed_why` field (`recurrence`, `build-target`, or `budget`), empty for an `ask` the reviewer reported as such itself.

Routing (`route.go`) then applies triage to this round's `fix` batch and `ask` findings. Triage has no prompts any more: `jig triage <T> <finding> keep|dismiss [--reason]` records a decision as a `triage` journal line (provenance `person` at a terminal, else `agent`), and the next round applies what was decided (`recordedTriage`). Until then `jig status`'s `outstanding_asks` rows show the decision (`decision`, `decided_by`; `keep (no build target)` for a keep the round will leave asked) and the next round's request carries it on the ask's `open` row; the ask itself stays open, so nothing is applied before the round closes ([ADR 0007](docs/adr/0007-gate-reviewer-owns-bookkeeping-not-judgment.md)). A decision is *pending* from its `triage` line until the next round closes, and *spent* after. An ask nobody decided stays undecided - a budget-parked one always - unless the round was opened with `--yes`, which falls back to `DefaultTriage`: every fix kept, every `ask` with a full build target (a derived workspace and a resolvable oracle) kept, one missing either part left undecided. Every kept finding becomes a fix slice - one per workspace and oracle for the kept fixes, one each for a kept `ask` - and the fix slices start `queued`, so `jig next` and `jig claim` pick them up. Routing and triage both finish, and every fix slice from them is appended, before the round is pushed. A command that fails partway is the exception, and a deliberate one: `Gate` and `Publish` both commit and push best-effort on any error after their own first store write - `Gate`'s `gate-open` line, `Publish`'s first rendered file - under a subject naming the ticket and the failure, rather than leaving it for the next command's own `Sync` to sweep up anonymously.

## A ticket's branch

A ticket's working branch is the `branch:` recorded in its `ticket.yaml` when there is one, else a name derived from its id ([ADR 0013](docs/adr/0013-a-ticket-branch-is-recorded.md), amended by ADR 0032). `store.ResolveTicketBranch` is the one resolver, and every command that names the branch resolves it there, once:

1. a recorded `branch:` wins, validated (the target branch and names git rejects are `TICKET_BRANCH_INVALID`);
2. otherwise `<id>-<slug>`. The slug is `ticket.yaml`'s `slug:`: the lowercase ASCII words of the title joined by `-`, cut at a word boundary to at most 40 characters, fixed when `jig ticket new` or `jig graduate` mints the ticket so that editing the title never renames the branch (an older ticket derives it on read, and recording a branch writes it). The id is the GitHub issue number from the ticket's record in the tracker tree (`store.TrackerIDs`; a ticket with none there falls back to the record in its own folder) once the ticket is mirrored and the project's branches are led by GitHub (the marker's `branches: from:` is empty or `github`), else the store id;
3. a ticket whose record and title give no slug takes the slug of the first heading (`# `) of its brief, with a leading `<id>:` taken off (`store.BriefTitle`), and with none of them the branch is the bare id (the GitHub issue number once mirrored, else the store id). A ticket minted by `jig ticket new` records its slug; one made by hand has none, so its slug follows its brief's heading and editing the heading renames its branch. `jig/<id>` is no longer produced anywhere, and a branch led by it still binds to its ticket.

The derived name is never written to `branch:`, so a ticket is *adopted* exactly when a branch is recorded: the branch was chosen by someone else, and the ticket needs no brief and no slices to be gated. `ticket.yaml` is decoded strictly, so an older jig refuses a ticket that has `slug:` (`TICKET_RECORD_INVALID`, help: upgrade jig).

**Binding.** `Store.TicketForBranch` runs the other way and binds a branch to its ticket with no claim and no record. It strips one `jig/` and tries the leads that cannot both begin one name: a leading `KEY-N` (a ticket id, else an alias, so `jig/<old id>` branches keep binding after a migration), then a leading bare number, which is a mirrored ticket's GitHub issue number (`102-anything` binds the ticket mirrored to issue 102; `102` is not `1020`, and `0102-x` binds nothing). Two tickets carrying one number is `TICKET_REMOTE_ID_COLLISION` (the ticket's own id in the name still binds). The issue index is a fresh scan of the tracker tree's records on each numeric lookup and is deliberately not cached, since a record may have landed in the tree (a pull, a sync) since the last read and a branch the issue number just made must bind at once. The name changes when the mirror gives a ticket its number, so a branch begun on the store-id name still binds, but the mirror then looks for pull requests from the issue-number name; recording the branch is the cure.

**A claim records a branch.** `jig claim` on a branch that names no ticket (not this one, not another, and not the target) records it with `WriteTicketBranch`, after the start sha and before the push, never replacing a recorded branch. Recording makes the ticket adopted: gate, publish and `jig next` name that branch from then on.

**Adoption by the gate.** `jig gate <ticket> --branch <name>` adopts a branch built outside jig (`resolveGateBranch`). A later `--branch` naming another branch is `BRANCH_MISMATCH`; naming the recorded one changes nothing. Adoption is refused for a name `ResolveTicketBranch` would refuse (`store.CheckAdoptableBranch`: the target, a name git rejects or expands), for a branch that neither the clone nor origin has (`BRANCH_NOT_FOUND`), and for a ticket whose own branch jig already verified commits on (`jigBuilt`, `TICKET_ALREADY_BUILT`: a `verified` journal line, a green `result` line naming a commit, or a slice in state `green`), judged from the store, never from the machine. The round writes the record and the start sha (the branch's tip) only after every precondition has passed, right before its `gate-open` journal line, so a refusal leaves nothing behind. Which commits jig verified is the `verified` lines alone (`journal.BuiltCommits`).

**The start sha** (`start.<repo>.sha`) is what a verified commit must descend from (`verifyGreen`: a descendant, and not the start sha itself) and where a gate round's scope falls back when the branch has no merge base with the target. `jig claim` writes it when absent: the merge base of HEAD and `origin/<target>` in the clone. Adoption records the branch's tip in each repo the round covers. `jig verify` with no start sha falls back to that merge base.

**What a round reviews.** The work happens in the operator's own clone, so `pointAtBranchCopy` points the gate lease (and, the same way, the publish lease) at the clone's committed branch (whatever is dirty in its working tree is not part of the branch and is never read), compared with origin's by `pool.Compare`: origin has no copy or the clone's is ahead, the clone's; origin's is ahead of the clone's or the two are one, origin's; each has commits the other lacks, `BRANCH_DIVERGED`, since jig merges nothing that is not its own and the person decides how their branch and origin's are joined. Where the clone has no copy (no clone is mapped, the work is on another machine, or a build lease of an earlier jig holds it) the rule of that earlier jig applies, over the build lease and origin (`chooseBuiltCopy`, with `BUILD_LEASE_MISSING`); with no copy anywhere the round is `BRANCH_NOT_FOUND`, and a branch is never cut from the target for a round. Publish ships the copy the round reviewed, by the same function.

**Sync rule.** `pool.Acquire` fetches with `--prune` and compares a local branch with `origin/<branch>` when origin has one, for every role: no commits of its own, it fast-forwards; ahead of origin, it is kept; commits on both sides, `BRANCH_DIVERGED`. The gate and publish leases are disposable, so `Gate` and `Publish` drop their own local copy of the branch before acquiring (`restoreLeaseBeforeAcquire`, `dropLeaseBranch`). A lease also recovers from an operation a crashed session left unfinished: a merge, rebase, am, cherry-pick or revert is ended with `--quit` and an unmerged index reset to HEAD, never moving a branch, and named in `Lease.Recovered`.

## Publish

`jig publish` ships the ticket's reviewed branch and has four forms, mutually exclusive; each is a step of the same hand-off between jig and an agent that does the git and `gh` work, and none reconciles, squashes or commits on the branch, so the person's branch never diverges from origin's because of a publish.

- **`--body [--picks FILE] [--repo r]`** (`PublishBody`) renders `pr/<repo>.md` and `pr/review-notes.md` (in a ticket that publishes in several repos, `pr/review-notes-<repo>.md`) for the reviewed head of each repo the ticket builds in, or of the one `--repo` names, and journals a `pr-body` line for each; it changes no lease and no branch, and pushes nothing.
- **`--pr <url>`** (`PublishRecordPR`) records a pull request somebody else opened. When the repo has a host it checks through `gh` (`Host.PRHead`, `repohost.ParsePRURL`) that the pull request is from the ticket's branch (`PR_HEAD_MISMATCH`) and at the reviewed head (`PUBLISH_UNREVIEWED_HEAD`); with no host it records the url unverified and warns. It takes `--repo` when the ticket builds in several repos (`VALIDATION_ERROR` without it), journals a `pr` line naming the repo, and journals `publish-done` when it is the last repo without a pull request (`pendingRepos`).
- **`--approve`** (`PublishApprove`) journals `publish-approved` with the head and its provenance, `person` at a terminal, else `agent`. A ticket that builds in several repos gets one line for each repo, with the repo.
- **Bare `jig publish [--yes] [--repo r] [--picks FILE]`** (`Publish`) pushes the reviewed head unchanged and opens or updates the pull request, in this order. `--repo r` narrows any form to one repo of the ticket; by default every repo it builds in is covered, and a publish narrowed to one repo of two stops short and names the pending repo. Steps 1 to 6 only read and refuse, and come before the first store write, so a refusal there, in any repo, leaves the store and the other repos untouched; from the rendering (step 7) on, a failure commits and pushes the store best-effort under a subject naming it.

1. **Preconditions** (`readyToPublish`). The ticket's record is read once and names the branch (`store.ResolveTicketBranch`); every slice is green; the repos are the ones the gate reviewed (`gateRepos`: the project's one repo, or the repos the slices build in) or the one `--repo` names (`publishRepos`), and the branch, which has one name in every repo, is resolved against each target; the latest gate round is clean (`PUBLISH_NOT_CLEAN`) and not `independent: false` (`PUBLISH_NOT_INDEPENDENT`); and a binding intent, brief or explicit, hashes to the sha256 that round's `report.yaml` recorded (`PUBLISH_UNREVIEWED_INTENT`), so an edit after the round cannot be published as the reviewed intent, silently. An inferred or absent intent has nothing pinned to check.
2. **The publish lease** is restored pristine, drops its own copy of the branch, is acquired, and pointed at the copy the gate reviewed by the function the gate uses, `pointAtBranchCopy` (`BRANCH_DIVERGED`, `BRANCH_NOT_FOUND`).
3. **Ship what was reviewed.** The head at that point must be the last clean round's `reviewed_sha` for the repo, when a reviewer round recorded heads (`PUBLISH_UNREVIEWED_HEAD`, `checkReviewedHead`); a round that recorded heads for other repos only is refused too, and a scripted round records none at all and is let through.
4. **A fast-forward or nothing.** A branch already on origin must be a descendant of origin's copy (`PUBLISH_NOT_FAST_FORWARD`, `requireFastForward`); publish never forces. The copy was compared with origin's when the lease was pointed at it, so what this catches is a push since.
5. **The pull request host** is built here, not after the push, from the repo's own `remote:` (`repohost.New`): a GitHub host for a remote on github.com over ssh or https, with any user, refused up front (`GH_NOT_INSTALLED`) when `gh` is not on PATH; `nil` for any other remote - a local path, as this store's own is, or another host. With a host, the branch's open pull request into the target is looked up (`FindOpenPR`, exact: the pull requests endpoint with the qualified head, the base and the open state, which GitHub applies, so no page of other forks' pull requests can hide it); a lookup that fails refuses the publish. A closed or merged pull request from the branch, or an open one into another base, is not that one: a new one is opened. With no host, nothing is looked up and publish pushes the branch and opens nothing.
6. **A change to publish.** `PUBLISH_NO_DIVERGENCE` and the non-empty range check refuse an empty change.
7. **The pull request's own docs.** `pr/<repo>.md` is rendered, with `pr/review-notes.md` beside it (below), naming the other repos' pull requests under `## Part of <ticket>` when the ticket publishes in several, and a `## Stacked on <parent>` section when the pull request goes into a parent's branch.
8. **Confirm.** `--yes`, a person's recorded `publish-approved` for this head (for the head of each repo, when the ticket publishes in several; one question covers them all), or an accepted question (which says whether it would push and open a pull request, push and update the open one, or - with no host - only push the branch); otherwise `PUBLISH_DECLINED`. A pick's staged media are checked once more before the push (`PUBLISH_PICKS_CHANGED`: origin untouched, and the next publish reuses the pick and stages afresh).
9. **Push, pull request.** In order, for each repo: `gitx.GuardedPush` of the branch, then, with a host, the pull request into its base (the target, or the branch of the parent a stacked ticket builds on while the parent has not merged in the repo; `PUBLISH_PARENT_NOT_PUSHED` refuses, before anything is pushed, when that branch is not on the origin): the open one is updated (`UpdatePR`, `gh pr edit --body-file`) with `pr/<repo>.md` as its body, otherwise one is opened (`CreatePR`) with it. A pull request with a `## Demo` section's media (`DemoRenderResult.MediaFiles`) goes through `CreatePRWithMedia`/`UpdatePRWithMedia` instead: both add `--attach <file>` once per staged file to the same `gh pr create`/`gh pr edit` call, run with the staging directory the pick copied them to (`DemoRenderResult.MediaDir`) as `gh`'s own working directory, after checking that the installed `gh` supports `--attach` on the very subcommand about to run (cached per subcommand, since the two do not necessarily agree); without support the pull request is opened or updated with no media, and the `attached` return tells publish to say why. Once the call returns, publish reads the pull request's body back (`ReadPRBody`, `gh pr view <url> --json body`; a failed read-back is a warning) and checks it for any of the section's own `./<name>` references still sitting in it unrewritten (`checkUnrewrittenReferences`). `gh` rewrites a recognized image reference in place but not a video's bare path, so a reference still unrewritten is patched, not merely reported: `rewriteUnrewrittenReferences` finds the upload URL `gh` appended for that file in the very body just read, moves it to where the reference stands, and publish edits the pull request again with the fix; only a file `gh` appended no URL for at all is left as it was and named on stderr. Once the pull request exists, its first comment is `pr/review-notes.md`, posted through `CommentPR` (`gh pr comment`); a post that fails is a warning, never a publish failure. The `pr` journal line records which (`updated`, `opened`, or `none:no-host`) and the pushed head, and in a project of several repos the repo. A repo opened before the last has its body rendered again with the other pull requests' urls and its pull request updated.
10. **Store docs.** The changelogs, the ledger entry (titled by the first slice that did not come from a gate round, else the recorded title, so an adopted ticket keeps the name it was minted with), the contract index and the evidence are written after the branch is out, so a publish that is declined or fails before the push leaves none of them behind; `publish-done` follows once every repo the ticket builds in has a `pr` line since the last `publish-done` (`pendingRepos`), and the store is pushed.

The body, `pr/<repo>.md`: `## What changed` and `## Verification` always, `## Intent` first when the round's intent is a binding source with text of its own, and `## Demo` between `## What changed` and `## Verification` when a pick stands - never more than these four `## ` sections, in this order, and never another, bar the two that follow `## Intent` when they apply: `## Stacked on <parent>`, while the pull request goes into the parent's branch (no section once the parent merged, or in the child's own repo), and, for a ticket that publishes in several repos, `## Part of <ticket>`, one line `- also <repo>: <url>` for each other repo, `pending` until its pull request is recorded. In a project of several repos a body tells the slices of its repo, the findings whose file begins with its repo and the review notes' coverage as repo-relative paths.

- `## Intent` comes from the last clean round's intent provenance and is left out entirely for `inferred` or `none` - an inferred intent comes from the author's own private agent session and never reaches a pull request ([ADR 0012](docs/adr/0012-intent-provenance.md), [.github/SECURITY.md](.github/SECURITY.md)) - and for a binding source whose file yields no text of its own, rather than publishing a heading stating no intent: a brief with no first `## ` section with text under it (`firstBriefSection`, and never the whole brief in its place), an `intent.md` that is front matter and nothing else. A brief that yielded nothing is the one omission publish warns about (before the confirmation prompt, naming the brief). The intent text itself is rendered intact, with its own headings demoted below the section level (`demoteHeadings`), so a whole design doc recorded by `jig gate --doc` cannot add an extra `## ` section of its own or outrank the body's with a `# ` title; a fenced code block the text leaves open is closed before it is embedded (`closeOpenFence`), and a `## ` line inside a fence does not end the brief's first section either.
- `## What changed` is, for an adopted ticket, the author's own pre-adoption commits first (subject and short sha, from the merge base with the target up to the start sha recorded at adoption); then one bullet per green slice in slice order (the first line of its goal and its short commit sha, omitted when a slice recorded none), fix slices grouped after the others as fixes from review. One line per bullet keeps a fix slice's goal from dumping the findings' own detail into the body, which is what the comment is for.
- `## Demo` comes from the recordings the build made, when it made any ([ADR 0029](docs/adr/0029-demos-are-recordings-of-the-builds-end-to-end-scenarios.md)). The candidates are the journal's `recorded` lines whose commit the reviewed head holds (`gitx.Missing`); of the lines that recorded a flow, scenario and step, the latest one's, every file of it with that key, each checked again against its file under the jig home (`pickCandidates`, `recordingProblem`; a failure is named in the output and not offered). With none, the body has no `## Demo` section. Otherwise jig writes the picks request, `picks.json` (beside the picks directory, naming absolute paths of this machine, so it stays out of the store), and an agent answers with a picks file, `{flows: [{title, items: [{id, caption}]}], summary}`, passed as `--picks FILE`. With recordings to pick and no file, `--body` and bare publish print `picks: pending` and stop at the request. The answer is parsed strictly (`ParsePicksResult`) and validated in jig's words (`resolvePicks`: known ids, none twice, step order within a candidate flow, bounded non-empty words, at most `media.MaxFiles`). The picked files are copied into `PicksDir` (pinned once made, and cleared of whatever an earlier pick of the head left), each hashed as it is copied (`copyRecording`), as `rec-<n>.<ext>` in flow order, and go to the host's attach call; the section (`renderPicksSection`) is the summary, then a `### <title>` per flow with one bullet per recording in the one reference form `gh` actually rewrites for its kind: an image as markdown image syntax, `![caption](./<name>)`, a video as the plain bullet `./<name>: caption`, which the read-back above patches itself where the host can. Host paths are left out of the summary, titles and captions whole. A refused pick is journaled (`publish-picks`, `refused: <reason>`), said on stderr and in `PublishReport.Picks`, and the body has no `## Demo` section. A publish of the same head over the same candidates and intent (their fingerprint) takes the earlier pick from the journal, re-validated.
- `## Verification` names the oracles green at the last clean round (`oracle_names` from `report.yaml`, else `SortedOracleNames` of the manifest Publish resolved) and the reviewed head, the revalidation tier (always `none`), and one line counting the review's findings by how they ended (fixed, dismissed, noted, asked), pointing at the pull request's first comment for the detail - never a store path.
- `pr/review-notes.md`: the last round's own summary (`findings.yaml`'s `summary`, not its verdict), every finding across every round ordered by risk with how it ended (fixed by slice X and cleared at round N, kept with the human's decision, dismissed by a human, noted, or asked and still open), and coverage - the files the change touched (Publish's own diff of the ship range) against the files the reviewer read (every round's own reviewed paths). Both render from the one pass `collectFindingsWithOutcomes` makes over every round's `findings.yaml`, plus what the store already has (slices, the journal, the gate rounds), with no model call (`writePRBody`, `writeReviewNotes`, `internal/verifydeliver/render.go`).

The report names the head pushed (unchanged) and the pull request's url, or why there is none, for each repo.

## Safety

**What jig starts.** The jig binary starts no coding session and links no code that does ([ADR 0032](docs/adr/0032-jig-attaches-to-the-harness.md)). It runs `git` (through `gitx` only), `gh` for the pull request host, the oracle commands of the repo's own manifest (in a detached worktree, with a time bound), and, detached, itself (`jig verify --worker`). What it reads of a harness session is what the harness hands a hook, the transcript's usage and, for a gate round with no stated intent, an excerpt of the transcript of a session that worked the ticket; and what it writes in a person's files is bounded by the installers below. `jig brief show` is the one verb that listens: on `127.0.0.1` only, answering requests whose `Host` is a loopback name (so a page in the person's browser cannot read a brief by pointing a name at the address), with no script and nothing fetched in its own page, and serving a ticket's `illustration.html`, project-authored HTML, as it is.

**Installers and hooks.** `jig setup` and the piece verbs edit files that are the person's: a harness settings file, a memory file, git's global `core.hooksPath`. They write only after a yes (`--yes`, or a prompt at a terminal; with neither, `NEEDS_INPUT` and nothing written), preserve every key, hook and order that is not jig's, refuse a file they cannot read (invalid JSON, comments, a wrong type) and never write over it, write atomically through a symlink, and own only what they can recognize again: a hook entry by `setup.OwnedCommand` (a bare `jig`, or a path whose last element is `jig` or `jig.exe`, then `observe` or `githook`; the binary must be named `jig` to install one, `BINARY_NAME` otherwise), a skill by its version stamp, a hint by its marked block. A global git hook wrapper runs the repository's own hook and the earlier `core.hooksPath` first, and its exit code is theirs. `jig observe` exits 0 and prints nothing whatever it is handed, records nothing for a directory in no jig project, and logs what went wrong to `<jig home>/logs/observe.log`. The machine ledger it writes holds, for sessions in jig projects only, the commands a session ran (the first 400 bytes, whitespace flattened) and the files it touched; it stays on the machine and is not in the store. Claude Code ignores project hooks in a folder it does not trust and with `--bare`; the install output says so.

**Structural command screen.** A regex over the whole command line is not enough to catch a plainly-spelled but oddly-quoted `git push`: `git -C <path> push` is a plain push once `-C <path>` is consumed as a global option, and quoting or extra whitespace defeats a pattern match without changing what git executes. `screen.Command` instead parses each shell segment structurally - split, tokenize, unquote, match the git binary, consume global options - and checks the *resulting* subcommand and flags, so a quoting trick can't hide a push from the screen the way it can from a regex. This is a per-token accident guard, not confinement: an indirection the shell itself resolves - a git alias (`-c alias.x=push`), a shell variable, or command substitution - still runs `push` once the shell expands it, after the screen already judged the unexpanded tokens. The screen guards the headless sessions of the reviewer evaluation only. See [ADR 0008](docs/adr/0008-headless-permission-model.md).

**Secret-read screen.** `screen.SecretPath` denies any tool-call path shaped like a live credential - `.env*`, `*_key*`, `id_rsa*`, `*.pem`, `.netrc`, `_netrc`, `.npmrc`, plus a fixed list of exact credential files that sit beside ordinary config in the same directory (`.git-credentials`, `.claude/.credentials.json`, and the like) - or that has a credential directory anywhere among its path segments, not only at the root: `.aws`, `.ssh`, `.gnupg`, `.config/gh`, `.docker`, `.kube` - checked against the arguments each tool names files with, which the screen takes from the tool itself rather than from one shared key list, and against what those arguments resolve to on disk, so a symlink pointing at a credential directory is denied by where it lands. A network share or a Windows device path is judged by its spelling alone and never resolved, since resolving one can dial a remote host. A credential directory counts as much as a file inside it, since a tool given a search root reads everything under it. This is an accident guard, not confinement: a shell glob, a variable, a junction, or a hard link the resolution step doesn't see can still reach a credential the literal check would have caught, and a content search over an ordinary directory that happens to hold a credential file still returns it. See [ADR 0008](docs/adr/0008-headless-permission-model.md) for why a denylist of path spellings cannot close that gap.

**Headless permission model (the reviewer evaluation).** This model is not a security boundary: a `headless` session's shell runs with the operator's own user rights and is not confined to its worktree, so it is only as safe as running that shell yourself would be. A `claude -p` session can't be asked anything, so the headless backend grants every tool it needs up front and runs in `dontAsk` mode, which denies everything else. In a screened dispatch, the shell and file-read tools are granted only by the screen hook's allow (`screen.Granted`). Claude Code treats a hook it cannot launch as no decision, and its own read-only classifier still lets part of the shell through, so the backend proves the screen before every screened dispatch: it runs the hook program once, directly, with a call the screen must deny, and a hook that is missing, fails, answers nothing, or allows it stops the dispatch with `SCREEN_UNAVAILABLE`. The edit tools are granted by path-scoped permission rules for the worktree and the dispatch's `result.json`. Web access, subagents, skills and MCP servers are left out of the session entirely. Rules and hook travel as one inline `--settings` object, and `--setting-sources user` keeps the worktree's own `.claude/settings.json` out: that file is content under review. The worktree's `CLAUDE.md` is passed by jig itself (`--append-system-prompt-file`) from the committed HEAD tree, not the working tree, capped at 64 KiB (`LEASE_MEMORY_TOO_LARGE`). A session is bounded by `JIG_HEADLESS_TIMEOUT` (90 minutes by default). See [ADR 0008](docs/adr/0008-headless-permission-model.md); `JIG_LIVE_CLAUDE=1 go test ./internal/session -run Live` checks the model against the installed CLI through a local mock of the Messages API; CI no longer runs it, and its `claude-cli` job runs `TestHooksLive` against the latest CLI release instead.

**Guarded push.** `gitx.GuardedPush` refuses to push to a remote that is not a local file path unless the caller has confirmed. `publish` is the only command that pushes the ticket branch, and it only passes `confirmed` after its confirmation (`--yes`, a person's recorded approval of the head, or an interactive yes) has run. `jig share` also pushes, the store, the repos it gave a remote and the project's own repo, and only after its plan was confirmed. Every command separately pushes the store's own bookkeeping commits to the store's remote (`store.Push`) as it works; that push is unguarded by design - it moves jig's own journal and ticket-folder state, not product code. In writers mode it pushes `writers/<name>` and the claims branch only, never main.

**Transcripts stay local.** A gate round with no stated intent reads the Claude Code transcripts of the sessions the ledger names for the ticket, else the operator's transcripts for the repo (Intent, above), and `spend` reads a session's transcript for usage. The excerpt of the matched session lives under the jig home, never the store; the store's copy of the request has its path cleared, and what the store's push carries is the request's metadata (the session id, the diff files). No model summarizes the excerpt. There is no setting that turns the excerpt off; stating the intent stops it ([SECURITY.md](.github/SECURITY.md) has the full list).

**Single git owner.** Only `gitx` runs git: it spawns the git program for users' repositories and runs git in process, through go-git, for the store ([ADR 0011](docs/adr/0011-the-store-runs-git-in-process.md)). `lint.TestNoGitSpawnOutsideGitx` parses every other package and fails on an `os/exec` call or `exec.Cmd` literal whose program resolves to `git`, and `lint.TestNoGoGitOutsideGitx` on any import of go-git. Every gitx call runs with `-c maintenance.auto=false`, so none leaves git's detached background maintenance running; the flag is argv-only, so a user's own git still maintains their repos. Every call also drops an inherited `GIT_DIR` and the other variables that would point git at a repository other than the one its working directory names, and `cmd/jig` clears them from its own process at startup (`gitx.ClearRepoEnv`), so hooks, workers and oracles never inherit them.

**Synchronous maintenance.** jig's long-lived repos still get upkeep: `store.Push` after a successful push and `pool.Acquire` after a reuse fetch call `gitx.MaintenanceAuto`, a foreground `git maintenance run --auto` with `gc.autoDetach=false`. It is best-effort; a failure never fails the push or the acquire.

**Many processes, one store clone.** Hooks, detached verification workers and the person's own commands all run jig against the same store clone at once. `Store.LockCheckpoint` serializes their git operations across processes (`<git common dir>/jig-checkpoint`, 2 minutes, re-entrant within a process; on timeout it proceeds and warns), `Sync`, `Push`, `Claim` and `journal.Append` hold it, and `Push` lets go of it before the checkpoint hook runs, so a slow mirror does not keep other processes out. Do not start or resolve a rebase or merge in the store clone by hand while jig is running: `abortFailedPull` (`internal/store/store.go`) aborts any rebase it finds there after its own pull fails, with no record of whether it was the one that started it.

**Store history names the person.** Every commit jig makes in a store is authored and committed by the identity git resolves in the store clone (`Store.identity`, from `git var GIT_AUTHOR_IDENT`); a clone with none is refused with `IDENTITY_REQUIRED` and the two commands that set one, and jig never commits under a name of its own. `Claim`, `init` and `migrate` resolve it before their first write; a command with nothing to commit needs none. `lint.TestNoArtificialStoreIdentity` fails on a literal name or email and on `-c user.name=jig`.

## Testing

```sh
go test -timeout 30m ./...
```

Every test gets its own `t.TempDir()` and its own jig home (passed as an argument to the packages that use one, as an `env` of its own to `cmd/jig`'s `run`, or as `JIG_HOME` in the jig binary's own subprocess environment in `e2e`), so a test run never touches a real machine's jig home, and every remote used in tests is a bare, file-path repo - no test ever talks to a real git host. `cmd/jig` reads the environment, the working directory, the terminal check, the clock, the spawner, the executable and `PATH` lookups through that `env`, so a test replaces each with `withSpawn`, `withClock`, `withExecutable`, `withLookPath` and the like (a test env's spawner refuses by default, so a test cannot start a process by accident), and `e2e` hands each binary run an environment of its own, so both run their tests in parallel.

The end-to-end tests drive a scripted agent, `fakeHarness`: it commits a scenario patch with the `Jig-Slice:` trailer in the clone, fires the harness's hook payloads at `jig observe` (`Hook`, `Bash`), runs jig with a session, and waits for the detached verification workers it started (`WaitVerifiers`, which kills what is left and fails the test). `TestDigesterFlow` is the whole flow in one test - setup, validate, claim, commits verified by git's hook, a question asked and answered, the gate by a second session, a publish - and is what a pull request shows as its demo (run with `-v` for the transcript); `TestTUIAndNextAgree` walks a ticket from a drafted brief to a published pull request and compares the screens' standing with `jig next --json` at each state, and the orchestrator fixture (`Opts.Orchestrator`) and the fake `gh` (`GhPull`) carry the multi-repo and merge tests. `TestHooksLive` (gated by `JIG_LIVE_CLAUDE`) runs the real `claude -p` against a stand-in Messages API (`internal/claudetest`) and is the only proof that the hook JSON works. A test that installs a global `core.hooksPath` uses `withGitGlobal`, so it reaches only its own config file; a test that starts `jig verify` waits for or kills its workers.

Packages whose tests make commits call `gittest.PinIdentity()` in `TestMain`, which pins all six `GIT_AUTHOR_*`/`GIT_COMMITTER_*` variables so commits hash the same on every run. The environment beats config, so in those test binaries the store's commits carry the pinned identity too. A test that asserts the person authored a store commit sets `user.name` and `user.email` in the store clone and runs the binary `withoutIdentity`; one that asserts the refusal also sets `user.useConfigOnly`, so git cannot guess an identity from the host.

Every package whose tests run git has a `TestMain` built on `gittest.Run`, which points `GIT_CONFIG_GLOBAL` at a generated config (`maintenance.auto = false`, `receive.autogc = false`, `gc.autoDetach = false`) and sets `GIT_CONFIG_NOSYSTEM=1`. That reaches every git process the binary spawns, including `git-receive-pack` behind a local push, so no detached maintenance outlives a test. With no system or user config, a test that needs a git setting (for example `core.autocrlf`) sets it itself.
