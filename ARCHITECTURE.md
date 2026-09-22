# Architecture

jig is one binary plus a set of session skills: the binary owns machinery
(dispatch, state, screening, git), skills own judgment (reading a brief,
writing code, reviewing a diff). The two only agree through the store on
disk - the schema below *is* the API between them, and between jig and any
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
gate           re-verification round: oracles, then a reviewer session's find/route/triage
  │            reads:  brief.md, slices.yaml, journal.ndjson, gate/round-N/findings.yaml (cumulative fold)
  │            writes: work/gate.round-N.{review,result}.json,
  │                    gate/round-N/{findings.yaml,findings.md,report.yaml,diff-changelog.md},
  │                    evidence/round-N/*, slices.yaml (fix slices, findings, from_gate: N)
  ▼
publish        reconcile, revalidate, docs, squash, route → open the PR
               reads:  gate/round-N/*, journal.ndjson
               writes: changelog/{<ws>.md,consolidated.md}, pr/{evidence.md,<repo>.md},
                       ledger.md, platform/contract-index.md
```

`frontier` (the frontier loop) and `verifydeliver` (gate + publish) are
separate packages that share no in-memory state at all - package `frontier`
never imports `verifydeliver` state and vice versa. The store on disk is the
entire interface between them. That's deliberate: either package is
splittable into its own binary later with a `git mv`, no refactor required.

## The four homes

Every fact jig produces lives in exactly one of four places, chosen by what
invalidates it:

| Invalidated by | Home | What lives there |
|---|---|---|
| a jig release | binary + skills (process) | the machinery and judgment code itself |
| one repo's own change | that repo's `.claude/` + `jig.yaml` | conventions, oracle commands, env classes - knowledge specific to that repo |
| platform or ticket history | the truth repo | tickets at root, `platform/`, `ledger.md` |
| session end | nowhere, except receipts | `evidence/` - a session's reasoning dies with it; only its artifacts persist |

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
    gate.round-N.review.json
    gate.round-N.result.json
  gate/
    round-N/
      findings.yaml
      findings.md
      diff-changelog.md
      report.yaml
      spec-input.md   # --branch --doc only
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
contract - a store written by one jig version declares the layout a later
version must still read.

## Module responsibilities

The dir column is exact; a lint test parses this table and asserts every dir
exists.

| Package | Entry points | Input → Output |
|---|---|---|
| `cmd/jig/` | `main`, `cmdScreen` | CLI args, or a PreToolUse hook payload on stdin → subcommand dispatch, or a screen allow/deny |
| `e2e/` | (tests only) | the fixture + fake backend → asserts the full brief→publish chain twice |
| `internal/axi/` | `Render`, `Table`, `KV`, `Help`, `RenderError`, `ExitCode` | labelled data → jig's plain-text output register and process exit codes |
| `internal/envrun/` | `Up`, `Shell` | a `manifest.EnvClass` + ticket/dir → a running `Handle`, or `Unavailable` |
| `internal/fixture/` | `Generate` | test `Opts` → a temp fixture repo, its store, and a scripted attempt scenario |
| `internal/frontier/` | `Run`, `Requeue`, `Schedule` | `Deps` + `RunOpts` → a `RunReport` (slices driven to green, paused, or stalled) |
| `internal/gittest/` | `Run`, `AtExit` | `*testing.M` → a hermetic git config for the whole test binary, then its exit code |
| `internal/gitx/` | `Run`, `RunEnv`, `RunRaw`, `MaintenanceAuto`, `RevParse`, `MergeBase`, `CommitsIn`, `IsAncestor`, `DiffNameOnly`, `FileExistsAtRev`, `IsLocalRemote`, `GuardedPush` | argv + a working dir → git plumbing output, or a refused push |
| `internal/graphify/` | `Detect`, `Plane` | `project.Config` → a `Plane` (real or `Noop`) that finds code affected by a seed |
| `internal/home/` | `Root`, `MachinePath`, `PoolDir` | `JIG_HOME` (or the real home dir) → per-machine paths |
| `internal/journal/` | `Append`, `Read`, `RenderChangelog`, `RenderConsolidated`, `RenderDiffChangelog` | journal `Line` events → `journal.ndjson` and rendered changelogs |
| `internal/manifest/` | `Resolve` | a repo dir → a `Manifest` of workspaces, oracle commands, env classes |
| `internal/outcome/` | `ParseJSON`, `ParseText`, `Signature`, `StallCounter` | a session result (JSON or text) → a typed `Result`, and a stall signature |
| `internal/pool/` | `Acquire` | repo/remote/target/branch/key → a `Lease` (a full clone, re-pointed to its start point) |
| `internal/project/` | `Load`, `Resolve`, `InitStandalone`, `InitProject` | `project.yaml` + the machine mapping → a `Config` |
| `internal/screen/` | `Command`, `SecretPath`, `ToolCall` | a shell command, path, or tool-call input → allow, or deny with a reason |
| `internal/session/` | `New`, `Backend.Run` | a `Dispatch` (paths to `slice.json`/`result.json`) → `result.json` written to disk |
| `internal/staircase/` | `Select`, `Disjoint`, `Default` | build `Signals` + `Config` → a model rung, disjoint from rungs already in use |
| `internal/store/` | `Open`, `Lock`, `AtomicWrite`, `BriefSectionHashes`, `ReadSlices` | ticket-folder reads/writes → the truth-repo tree described above |
| `internal/tracker/` | `New`, `Graduate` | `project.Config` → an `Adapter` (local, github, jira/linear stub, or command) |
| `internal/verifydeliver/` | `Gate`, `Publish`, `RebaseOnto` | `Deps` + `GateOpts`/`PublishOpts` → a `GateReport`, or a `PublishReport` with an opened PR |

## Session backends

The contract between jig and any backend is pure disk: jig writes
`slice.json` (goal, oracle, workspace, prior attempt log, any answered
question), the backend runs a session in the lease worktree, and jig reads
back `result.json` (outcome, summary, commit, and - for `needs-input` - a
question). Nothing crosses in memory.

Three backends implement that same narrow interface:

- **fake** - replays a scripted scenario directory; no session, no network. The CI and fixture path.
- **headless** - runs a local `claude -p` subprocess; the command/secret screens attach as a PreToolUse hook (`jig _screen`).
- **herdr** - drives a remote agent through herdr, exec'd natively off Windows and, on Windows, inside a WSL login shell (`JIG_WSL_DISTRO` picks the distro; unset uses WSL's default); it has no PreToolUse hook to attach a screen to, so herdr sessions are not screened.

Screens attach only where the backend's tool-call surface allows a
PreToolUse hook, which today is `headless` alone; `fake` has no tool calls
to screen, and `herdr`'s tool calls run inside the remote agent it drives,
outside jig's own process.

## Gate reviewer contract

A gate round's reviewer dispatch (`internal/verifydeliver/review.go`) is the
same disk-only contract as a build session, narrowed to read-only: jig
writes `work/gate.round-N.review.json` (the ticket, round, scope, base and
head sha, brief/slices/journal paths, manifest oracles, and the cumulative
`open`/`dismissed` findings folded from every earlier round), the backend
runs a session against `must_review` - every file the scope diff touched
plus every still-open finding's file - and jig reads back
`work/gate.round-N.result.json` (findings, `reviewed_paths`, a summary). The
reviewer edits, commits, and pushes nothing; jig checks this itself (HEAD
and the tracked tree unchanged after dispatch) rather than trusting the
session, and rejects the round (`REVIEW_INVALID`) if either moved, or if
`result.json` fails strict structural validation - an unknown `action` or
`risk`, a finding whose file is neither present at head nor deleted in the
scope diff, an oracle that isn't a manifest oracle, a `prior` naming no
known finding, or `reviewed_paths` missing a `must_review` path all fail the
round loudly rather than falling back to a partial result.

Findings bookkeeping (`findings.go`) then folds the round onto the
cumulative state: a finding's identity across rounds lives only in its own
`prior` field, an open finding clears when its file was reviewed and
nothing routed reported it again, and a finding recurring for the second
time is routed to a human regardless of the reviewer's own label. Routing
(`route.go`) first runs triage over this round's `fix` batch and `ask`
findings - a human at a terminal decides the batch (accept all, or list
ids to dismiss) and each `ask` (keep, with an optional decision, or
dismiss); `--yes` or a non-terminal stdin runs `DefaultTriage` instead
(every fix kept, every `ask` with a derived workspace kept, one left
undecided when its file lies in no declared workspace) - then turns every
kept finding into a fix slice: one per workspace and oracle for the kept
fixes, one each for a kept `ask`. Routing and triage both finish, and
every fix slice from them is appended, before `Gate` pushes the store - so
a round is never partially applied.

## Safety

**Structural command screen.** A regex over the whole command line is not
enough to catch a disguised `git push`: `git -C <path> push` is a plain push
once `-C <path>` is consumed as a global option, and quoting or extra
whitespace defeats a pattern match without changing what git executes.
`screen.Command` instead parses each shell segment structurally - split,
tokenize, unquote, match the git binary, consume global options - and checks
the *resulting* subcommand and flags, so path or quoting tricks can't hide a
push from the screen the way they can from a regex.

**Secret-read screen.** `screen.SecretPath` denies any tool-call path shaped
like a live credential - `.env*`, `*_key*`, `id_rsa*`, `*.pem`,
`~/.aws/**`, `~/.config/gh/**` - checked against every path-like argument of
every tool call, not just git's.

**Guarded push.** `gitx.GuardedPush` refuses to push to a remote that is not
a local file path unless the caller has confirmed. `publish` is the only
command that pushes the ticket branch, and it only passes `confirmed` after
its interactive confirm (or `--yes`) has run. Every command separately
pushes the store's own bookkeeping commits to the store's remote
(`store.Push`) as it works; that push is unguarded by design - it moves
jig's own journal and ticket-folder state, not product code.

**Single git owner.** Only `gitx` spawns `git`; `lint.TestNoGitSpawnOutsideGitx`
parses every other package and fails on an `os/exec` call or `exec.Cmd`
literal whose program resolves to `git`. Every gitx call runs with
`-c maintenance.auto=false`, so none leaves git's detached background
maintenance running; the flag is argv-only, so a user's own git still
maintains their repos.

**Synchronous maintenance.** jig's long-lived repos still get upkeep:
`store.Push` after a successful push and `pool.Acquire` after a reuse fetch
call `gitx.MaintenanceAuto`, a foreground `git maintenance run --auto` with
`gc.autoDetach=false`. It is best-effort; a failure never fails the push or
the acquire.

## Testing

```sh
go test ./...
```

Every test gets its own `t.TempDir()`, `JIG_HOME` is always overridden via
`t.Setenv` so a test run never touches a real machine's jig home, and every
remote used in tests is a bare, file-path repo - no test ever talks to a
real git host.

Product commits on the ticket branch (reconcile, memorize, squash) use the
operator's git identity, resolved with `gitx.IdentityEnv` from their mapped
clone, since the pool lease has none of their repo-local config. Packages
whose tests reach those commits call `gittest.PinIdentity()` in `TestMain`,
which pins all six `GIT_AUTHOR_*`/`GIT_COMMITTER_*` variables so commits hash
the same on every run. The environment beats config, so in those test
binaries the store's bookkeeping commits carry the pinned identity too,
instead of jig's own `jig <jig@invalid>`.

Every package whose tests run git has a `TestMain` built on `gittest.Run`,
which points `GIT_CONFIG_GLOBAL` at a generated config
(`maintenance.auto = false`, `receive.autogc = false`, `gc.autoDetach = false`)
and sets `GIT_CONFIG_NOSYSTEM=1`. That reaches every git process the binary
spawns, including `git-receive-pack` behind a local push, so no detached
maintenance outlives a test. With no system or user config, a test that
needs a git setting (for example `core.autocrlf`) sets it itself.
