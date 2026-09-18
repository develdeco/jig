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
gate           review + re-verification round over the ticket's branch
  │            reads:  journal.ndjson, slices.yaml, start.<repo>.sha,
  │                    gate/round-N/{findings.yaml,report.yaml} (prior rounds)
  │            writes: work/gate.round-N.{review,result}.json (reviewer dispatch),
  │                    gate/round-N/{findings.yaml,findings.md,report.yaml,diff-changelog.md},
  │                    evidence/round-N/* (scripted rounds only), slices.yaml (fix slices, from_gate: N)
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

Two fields are additive since v0.1, marshaling with no key at all when unset
so existing on-disk bytes are unchanged: a slice's `rung` (`""` or
`"cheapest"`, a staircase pin - a mechanical fix-slice bundle always pins
cheapest) and a slice state's `signature` (the stall signature that tripped a
`stalled` state, cleared once the slice reaches green).

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
| `internal/frontier/` | `Run`, `Requeue`, `Schedule` | `Deps` + `RunOpts` → a `RunReport` (slices driven to green, parked, env-blocked, or stalled) |
| `internal/gittest/` | `Run`, `AtExit` | `*testing.M` → a hermetic git config for the whole test binary, then its exit code |
| `internal/gitx/` | `Run`, `RunEnv`, `RunRaw`, `MaintenanceAuto`, `RevParse`, `MergeBase`, `IsAncestor`, `CommitsIn`, `IsLocalRemote`, `GuardedPush` | argv + a working dir → git plumbing output, or a refused push |
| `internal/graphify/` | `Detect`, `Plane` | `project.Config` → a `Plane` (real or `Noop`) that finds code affected by a seed |
| `internal/home/` | `Root`, `MachinePath`, `PoolDir` | `JIG_HOME` (or the real home dir) → per-machine paths |
| `internal/journal/` | `Append`, `Read`, `RenderChangelog`, `RenderConsolidated`, `RenderDiffChangelog` | journal `Line` events → `journal.ndjson` and rendered changelogs |
| `internal/manifest/` | `Resolve` | a repo dir → a `Manifest` of workspaces, oracle commands, env classes |
| `internal/outcome/` | `ParseJSON`, `ParseText`, `Signature`, `StallCounter` | a session result (JSON or text) → a typed `Result`, and a stall signature |
| `internal/pool/` | `Acquire` | repo/remote/target/branch/key → a `Lease` (a full clone, re-pointed to its start point) |
| `internal/project/` | `Load`, `Resolve`, `InitStandalone`, `InitProject` | `project.yaml` + the machine mapping → a `Config` |
| `internal/revieweval/` | `LoadCorpus`, `RunCorpus`, `RenderReport` | a labeled case corpus + a session backend → per-case `CaseScore` (found/missed/false-positive), scored against `gold.yaml` |
| `internal/screen/` | `Command`, `SecretPath`, `ToolCall` | a shell command, path, or tool-call input → allow, or deny with a reason |
| `internal/session/` | `New`, `Backend.Run` | a `Dispatch` (paths to `slice.json`/`result.json`) → `result.json` written to disk |
| `internal/staircase/` | `Select`, `SelectPinned`, `Disjoint`, `Default` | build `Signals` + `Config` (+ an optional pin) → a model rung, disjoint from rungs already in use |
| `internal/store/` | `Open`, `Lock`, `AtomicWrite`, `BriefSectionHashes`, `ReadSlices`, `AppendSlices` | ticket-folder reads/writes → the truth-repo tree described above |
| `internal/tracker/` | `New`, `Graduate` | `project.Config` → an `Adapter` (local, github, jira/linear stub, or command) |
| `internal/verifydeliver/` | `Gate`, `NewReviewerGateSource`, `Publish`, `RebaseOnto` | `Deps` + `GateOpts`/`PublishOpts` → a `GateReport` (findings, fix slices, and `reviewed_sha` on a real reviewer round), or a `PublishReport` with an opened PR |

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

The gate reviewer's dispatch reuses this same disk contract, `Slice: "gate"`
in the `Dispatch`: jig writes `review.json` (paths, not contents, like the
build side's `slice.json`) and the reviewer session writes `result.json`.
`fake` plays a gate dispatch back by copying
`<scenario>/gate/round-<n>/review-result.json` verbatim into `result.json` -
distinct from a scripted build attempt's `patch.diff`/`result.json` pair,
and the worktree is never touched; missing scenario coverage for a round is
an error, never a silently clean round.

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

The gate reviewer's live eval corpus (`internal/revieweval`) is env-gated so
it never runs unattended in CI:
`JIG_REVIEWEVAL_BACKEND=headless go test ./internal/revieweval -run Eval`
drives a real backend against the corpus under `testdata/revieweval/`,
`JIG_REVIEWEVAL_MODEL` picks the model (default `claude-sonnet-5`), and
`JIG_REVIEWEVAL_REPORT` writes the plain-text report to a file. CI runs only
the structural path - a test-local scripted stub backend, keyed by case name
(not `session`'s own `fake` backend, which is keyed by round), replaying
scripted results, both a perfect one and a seeded-regression one - which
proves the scorer itself can fail, not just pass. Matching is one-to-one
(each result finding satisfies at most one gold entry), and a case that
fails outright counts every one of its gold findings as missed.

`lint/workflow_test.go` parses `.github/workflows/{ci,release,smoke}.yml` and
asserts the invariants that have already bitten or must hold - ci's OS matrix
and its gofmt/vet/test steps, release firing only on version tags and always
gated behind ci's `workflow_call`, smoke's required `tag` input - by shape
rather than exact text, so a routine workflow edit does not churn the test.
