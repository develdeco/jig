# Architecture

jig is one binary plus a set of session skills: the binary owns machinery
(dispatch, state, screening, git), skills own judgment (reading a brief,
writing code, reviewing a diff). The two only agree through the store on
disk — the schema below *is* the API between them, and between jig and any
future tooling that reads a ticket's history.

## Pipeline

```
brief          author brief.md + slices.yaml (human + intake skill)
  │            writes: <ticket>/brief.md, <ticket>/slices.yaml
  ▼
run(frontier)  dispatch queued, unblocked slices to a build session
  │            reads:  slices.yaml, slices/<id>.state, questions/*.md
  │            writes: slices/<id>.state, work/<id>.attempt-N.{slice,result}.json,
  │                    journal.ndjson, questions/q-NNN.md, start.<repo>.sha
  ▼
gate           review + re-verification round over the ticket's branch
  │            reads:  journal.ndjson, slices.yaml
  │            writes: gate/round-N/{findings.md,report.yaml,diff-changelog.md},
  │                    evidence/round-N/*, slices.yaml (fix slices, from_gate: N)
  ▼
publish        reconcile, revalidate, docs, squash, route → open the PR
               reads:  gate/round-N/*, journal.ndjson
               writes: changelog/{<ws>.md,consolidated.md}, pr/{evidence.md,<repo>.md},
                       ledger.md, platform/contract-index.md
```

`make` (the frontier loop) and `verifydeliver` (gate + publish) are separate
packages that share no in-memory state at all — package `make` never imports
`verifydeliver` state and vice versa. The store on disk is the entire
interface between them. That's deliberate: either package is splittable into
its own binary later with a `git mv`, no refactor required.

## The four homes

Every fact jig produces lives in exactly one of four places, chosen by what
invalidates it:

| Invalidated by | Home | What lives there |
|---|---|---|
| a jig release | binary + skills (process) | the machinery and judgment code itself |
| one repo's own change | that repo's `.claude/` + `jig.yaml` | conventions, oracle commands, env classes — knowledge specific to that repo |
| platform or ticket history | the truth repo | tickets at root, `platform/`, `ledger.md` |
| session end | nowhere, except receipts | `evidence/` — a session's reasoning dies with it; only its artifacts persist |

Knowledge flows down (repo → session), truth flows sideways (ticket →
ticket, repo → repo), learning flows up (session → ledger).

## Store schema

A store is a git repo. One ticket folder, in full:

```
project.yaml
platform/
  contract-index.md
ledger.md
<ticket>/
  brief.md
  slices.yaml
  start.<repo>.sha
  slices/
    <id>.state
  journal.ndjson
  questions/
    q-NNN.md
  work/
    <id>.attempt-N.slice.json
    <id>.attempt-N.result.json
  gate/
    round-N/
      findings.md
      diff-changelog.md
      report.yaml
  evidence/
    round-N/
  changelog/
    <workspace>.md
    consolidated.md
  pr/
    evidence.md
    <repo>.md
```

`work/` is store-side, not lease-side, on purpose: a build session's `git add
-A` runs inside its worktree lease, and must never sweep dispatch plumbing
into a slice's commit. `project.yaml`'s `schema_version` is the compatibility
contract — a store written by one jig version declares the layout a later
version must still read.

## Module responsibilities

The dir column is exact; a lint test parses this table and asserts every dir
exists.

| Package | Entry points | Input → Output |
|---|---|---|
| `axi/` | `Render`, `Table`, `KV`, `Help`, `RenderError`, `ExitCode` | labelled data → jig's plain-text output register and process exit codes |
| `board/` | `Board`, `Deferred` | a structural-round call → a not-implemented error (v0.1 stub; see [DECISIONS.md](./DECISIONS.md)) |
| `cmd/jig/` | `main`, `cmdScreen` | CLI args, or a PreToolUse hook payload on stdin → subcommand dispatch, or a screen allow/deny |
| `e2e/` | (tests only) | the fixture + fake backend → asserts the full brief→publish chain twice |
| `envrun/` | `Up`, `Shell` | a `manifest.EnvClass` + ticket/dir → a running `Handle`, or `Unavailable` |
| `fixture/` | `Generate` | test `Opts` → a temp fixture repo, its store, and a scripted attempt scenario |
| `gitx/` | `Run`, `RevParse`, `MergeBase`, `CommitsIn`, `IsLocalRemote`, `GuardedPush` | argv + a working dir → git plumbing output, or a refused push |
| `graphify/` | `Detect`, `Plane` | `project.Config` → a `Plane` (real or `Noop`) that finds code affected by a seed |
| `home/` | `Root`, `MachinePath`, `PoolDir` | `JIG_HOME` (or the real home dir) → per-machine paths |
| `journal/` | `Append`, `Read`, `RenderChangelog`, `RenderConsolidated`, `RenderDiffChangelog` | journal `Line` events → `journal.ndjson` and rendered changelogs |
| `make/` | `Run`, `Requeue`, `Schedule` | `Deps` + `RunOpts` → a `RunReport` (slices driven to green, paused, or stalled) |
| `manifest/` | `Resolve` | a repo dir → a `Manifest` of workspaces, oracle commands, env classes |
| `outcome/` | `ParseJSON`, `ParseText`, `Signature`, `StallCounter` | a session result (JSON or text) → a typed `Result`, and a stall signature |
| `pool/` | `Acquire` | repo/remote/target/branch/key → a `Lease` (a full clone, re-pointed to its start point) |
| `project/` | `Load`, `Resolve`, `InitStandalone`, `InitProject` | `project.yaml` + the machine mapping → a `Config` |
| `screen/` | `Command`, `SecretPath`, `ToolCall` | a shell command, path, or tool-call input → allow, or deny with a reason |
| `session/` | `New`, `Backend.Run` | a `Dispatch` (paths to `slice.json`/`result.json`) → `result.json` written to disk |
| `staircase/` | `Select`, `Disjoint`, `Default` | build `Signals` + `Config` → a model rung, disjoint from rungs already in use |
| `store/` | `Open`, `Lock`, `AtomicWrite`, `BriefSectionHashes`, `ReadSlices` | ticket-folder reads/writes → the truth-repo tree described above |
| `tracker/` | `New`, `Graduate` | `project.Config` → an `Adapter` (local, github, jira/linear stub, or command) |
| `verifydeliver/` | `Gate`, `Publish`, `RebaseOnto` | `Deps` + `GateOpts`/`PublishOpts` → a `GateReport`, or a `PublishReport` with an opened PR |

## Session backends

The contract between jig and any backend is pure disk: jig writes
`slice.json` (goal, oracle, workspace, prior attempt log, any answered
question), the backend runs a session in the lease worktree, and jig reads
back `result.json` (outcome, summary, commit, and — for `needs-input` — a
question). Nothing crosses in memory.

Three backends implement that same narrow interface:

- **fake** — replays a scripted scenario directory; no session, no network. The CI and fixture path.
- **headless** — runs a local `claude -p` subprocess; the command/secret screens attach as a PreToolUse hook (`jig _screen`).
- **herdr** — drives a remote agent through a WSL-hosted herdr terminal session; the same screens attach the same way.

Screens attach wherever the backend's tool-call surface allows a PreToolUse
hook; `fake` has no tool calls to screen.

## Safety

**Structural command screen.** A regex over the whole command line is not
enough to catch a disguised `git push`: `git -C <path> push` is a plain push
once `-C <path>` is consumed as a global option, and quoting or extra
whitespace defeats a pattern match without changing what git executes.
`screen.Command` instead parses each shell segment structurally — split,
tokenize, unquote, match the git binary, consume global options — and checks
the *resulting* subcommand and flags, so path or quoting tricks can't hide a
push from the screen the way they can from a regex.

**Secret-read screen.** `screen.SecretPath` denies any tool-call path shaped
like a live credential — `.env*`, `*_key*`, `id_rsa*`, `*.pem`,
`~/.aws/**`, `~/.config/gh/**` — checked against every path-like argument of
every tool call, not just git's.

**Guarded push.** `gitx.GuardedPush` refuses to push to a remote that is not
a local file path unless the caller has confirmed. `publish` is the only
command that ever pushes, and it only passes `confirmed` after its
interactive confirm (or `--yes`) has run.

## Testing

```sh
go test ./...
```

Every test gets its own `t.TempDir()`, `JIG_HOME` is always overridden via
`t.Setenv` so a test run never touches a real machine's jig home, git author
and committer identity and dates are pinned to a fixed fixture value so
commits hash the same on every run, and every remote used in tests is a
bare, file-path repo — no test ever talks to a real git host.
