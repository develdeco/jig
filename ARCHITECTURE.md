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
  │            reads:  brief.md or intent.md (resolveIntent), slices.yaml, journal.ndjson,
  │                    gate/round-N/findings.yaml (cumulative fold)
  │            writes: work/gate.round-N.{review,result}.json,
  │                    gate/round-N/{findings.yaml,findings.md,report.yaml,diff-changelog.md},
  │                    evidence/round-N/*, slices.yaml (fix slices, findings, from_gate: N),
  │                    intent.md (--intent/--doc only)
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

A store is a git repo. One chart folder and one ticket folder, in full:

```
project.yaml
platform/
  contract-index.md
ledger.md
charts/
  <name>/
    map.md          # for people and sessions; jig never reads it
    tickets.yaml    # the handover jig reads and writes
<ticket>/
  brief.md
  intent.md         # jig gate --intent/--doc, or inferred; ignored when brief.md exists
  slices.yaml
  ticket.yaml       # optional: this ticket's own record - title, branch, blockers
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
    intent.json        # intent inference, when attempted
    intent.result.json # kept only when jig accepted it
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

## Module responsibilities

The dir column is exact; a lint test parses this table and asserts every dir
exists.

| Package | Entry points | Input → Output |
|---|---|---|
| `cmd/jig/` | `main`, `cmdScreen` | CLI args, or a PreToolUse hook payload on stdin → subcommand dispatch, or a screen allow/deny |
| `demo/fixture/` | `main` | `-out <dir>` + `-scenario <name>` → a fixture built via `internal/fixture`, and shell `export` lines (JIG_HOME, store dir, scenario dir, ticket id) for a VHS tape to eval |
| `e2e/` | (tests only) | the fixture + fake backend → asserts the full brief→publish chain twice; with `JIG_LIVE_CLAUDE`, README's Quickstart through the real `claude` CLI; `JIG_E2E_BINARY` → the same against an installed jig |
| `internal/axi/` | `Render`, `Table`, `KV`, `Help`, `RenderError`, `ExitCode` | labelled data → jig's plain-text output register and process exit codes |
| `internal/claudetest/` | `API`, `Session`, `Serve` | scripted sessions (tool calls in order) → a stand-in Messages API on loopback that the real `claude` CLI runs against, for the live CLI tests |
| `internal/envrun/` | `Up`, `Shell` | a `manifest.EnvClass` + ticket/dir → a running `Handle`, or `Unavailable` |
| `internal/fixture/` | `Build`, `Generate`, `RepoRoot` | a dir + `Opts` → a fixture repo, its store, and a scripted attempt scenario (plus the machine mapping under `Opts.Home`, by default the jig home `home.Root` resolves); `Generate` builds into a `t.TempDir()`; `RepoRoot`: a caller's source file → the module root |
| `internal/frontier/` | `Run`, `Requeue`, `RequeueSlice`, `Schedule` | `Deps` + `RunOpts` → a `RunReport` (slices driven to green, parked, env-blocked, or stalled) |
| `internal/gittest/` | `Run`, `AtExit` | `*testing.M` → a hermetic git config for the whole test binary, then its exit code |
| `internal/gitx/` | `Run`, `RunEnv`, `RunRaw`, `MaintenanceAuto`, `RevParse`, `MergeBase`, `CommitsIn`, `IsAncestor`, `DiffNameOnly`, `FileExistsAtRev`, `IsLocalRemote`, `GuardedPush`, `CommonDir`, `SameDir`, `TopLevel`, `CommitTime`, `OpenRepo` (`Repo`: `State`, `CommitAll`, `Push`, `Fetch`) | argv + a working dir → git plumbing output, or a refused push; a store's directory → the same store operations in process (go-git), or `ErrUseCLI` for the caller's git-program path |
| `internal/graphify/` | `Detect`, `Plane` | `project.Config` → a `Plane` (real or `Noop`) that finds code affected by a seed |
| `internal/home/` | `Root`, `MachinePath`, `PoolDir`, `IntentExcerptDir`, `IntentScratchDir` | `JIG_HOME` (or the real home dir) → the jig home root, which `cmd/jig` resolves once and passes down; a root → per-machine paths, and the directories intent excerpts and summarizer scratch directories go under |
| `internal/intent/` | `NewClaudeReader`, `Best`, `RenderExcerpt` | a repo's git common dir + a time window → matching local agent `Session`s; a scope diff's files → the `Match` a model then summarizes |
| `internal/journal/` | `Append`, `Read`, `RenderChangelog`, `RenderConsolidated`, `RenderDiffChangelog` | journal `Line` events → `journal.ndjson` and rendered changelogs |
| `internal/manifest/` | `Resolve` | a repo dir → a `Manifest` of workspaces, oracle commands, env classes |
| `internal/outcome/` | `ParseJSON`, `ParseText`, `Signature`, `StallCounter` | a session result (JSON or text) → a typed `Result`, and a stall signature |
| `internal/pool/` | `Acquire`, `Dir`, `Usable`, `CheckTicket` | the jig home root + repo/remote/target/branch + a ticket and its role (build, gate, publish) → a `Lease` (a full clone, re-pointed to its start point; anything git shows is not a repository of its own is moved aside and cloned afresh) |
| `internal/project/` | `Load`, `Resolve`, `InitStandalone`, `InitProject` | `project.yaml` + the machine mapping under the jig home root → a `Config` |
| `internal/revieweval/` | `LoadCorpus`, `RunCorpus`, `MatchRound`, `ScoreRound`, `RenderReport` | a labeled corpus (`testdata/revieweval`) + a session backend → a `CaseScore` per case, matched structurally against seeded gold through the real reviewer contract |
| `internal/screen/` | `Command`, `SecretPath`, `ToolCall`, `Granted`, `Grants` | a shell command, path, or tool-call input → allow, or deny with a reason; a tool name → whether a passing screen grants it |
| `internal/session/` | `New`, `Backend.Run` | a `Dispatch` (paths to `slice.json`/`result.json`) → `result.json` written to disk |
| `internal/staircase/` | `Select`, `Disjoint`, `Default` | build `Signals` + `Config` → a model rung, disjoint from rungs already in use |
| `internal/store/` | `Open`, `Lock`, `AtomicWrite`, `BriefSectionHashes`, `ReadSlices`, `ReadChart`, `WriteChart`, `ReadTicket`, `ReadTicketDeps`, `CreateTicketRecord`, `WriteTicketBranch`, `TicketBranch`, `ResolveTicketBranch`, `TicketFilePath` | ticket-folder and chart-folder reads/writes → the truth-repo tree described above |
| `internal/tracker/` | `New`, `Graduate`, `CheckMinted` | `project.Config` → an `Adapter` (local, github, jira/linear stub, or command); a `Graduation` (a chart's ordered ticket drafts) → the minted ids, each with its store folder created and its `ticket.yaml` (title and blockers) written; a freshly minted id → refused when jig cannot use it, before anything is written under it |
| `internal/verifydeliver/` | `Gate`, `Publish`, `RebaseOnto` | `Deps` + `GateOpts`/`PublishOpts` → a `GateReport`, or a `PublishReport` with an opened PR |

## Session backends

The contract between jig and any backend is pure disk: jig writes
`slice.json` (goal, oracle, workspace, prior attempt log, any answered
question), the backend runs a session in the lease worktree, and jig reads
back `result.json` (outcome, summary, commit, and - for `needs-input` - a
question). Nothing crosses in memory.

Three backends implement that same narrow interface:

- **fake** - replays a scripted scenario directory; no session, no network. The CI and fixture path.
- **headless** - runs a local `claude -p` subprocess in `dontAsk` permission
  mode. The command/secret screens attach as a PreToolUse hook
  (`jig _screen`); its allow is jig's own only grant for the session's shell
  and read tools, though the operator's own user settings (which still
  load) can grant more on top. Edits are granted only inside the lease and
  on the dispatch's own `result.json` (see Safety). The shell and reads it
  grants are the operator's own and are not confined to the lease, so this
  backend is not a security boundary.
- **herdr** - drives a remote agent through herdr, exec'd natively off Windows and, on Windows, inside a WSL login shell (`JIG_WSL_DISTRO` picks the distro; unset uses WSL's default); it has no PreToolUse hook to attach a screen to, so herdr sessions are not screened.

Screens attach only where the backend's tool-call surface allows a
PreToolUse hook, which today is `headless` alone; `fake` has no tool calls
to screen, and `herdr`'s tool calls run inside the remote agent it drives,
outside jig's own process.

## Gate reviewer contract

A gate round's reviewer dispatch (`internal/verifydeliver/review.go`) is the
same disk-only contract as a build session, narrowed to read-only: jig
writes `work/gate.round-N.review.json` (the ticket, round, scope, base and
head sha, the round's resolved intent - source and path, `Gate`'s own
`resolveIntent`, precedence brief.md then intent.md then none - plus
slices/journal paths, manifest oracles, and the cumulative `open`/
`dismissed` findings folded from every earlier round), the backend
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

**Intent inference.** When `resolveIntent` comes back `"none"` and the
round is about to dispatch a reviewer (nothing outstanding and the scope
diff empty skips it the same as any other round - a missing mapped clone
alone does not, since the reviewer still runs; only inference itself is
skipped), the reviewer source (`review.go`'s `Round` calls
`intent_infer.go`'s `inferIntent`; `Gate` does not, since only the
reviewer source holds the backend and the scope diff) tries to infer one
before that dispatch. `internal/intent`'s Claude Code reader discovers the
operator's local sessions (transcripts under `RoundInput.UserHome`) whose
working directory belongs to the same repository as the operator's mapped
clone (`RoundInput.OperatorClone`; identity by git common dir,
`internal/gitx.CommonDir` compared with `gitx.SameDir` - nothing matches
with no mapped clone) within a window anchored to the ticket's own
merge-base and head commit times, `internal/intent.Best` scores each
candidate by file overlap with the scope diff, and the best match's excerpt
(the developer's and the assistant's own text only - tool calls, tool
results and thinking dropped, and so is a user record the transcript marks
as the harness's own or as someone else's, while a record with no origin
stays for the summarizing model to judge - capped, written under the jig
home, `RoundInput.Home` - never the store, since a transcript can hold
secrets) is handed to a summarizer dispatch through the same
backend and disk contract: jig writes `work/intent.json`, the session writes
`work/intent.result.json` (`{"summary": "..."}`, strictly parsed - empty,
malformed or over 4 KiB fails the attempt), and jig records
`<ticket>/intent.md` (source `"inferred"`, the agent, session id and score)
for this round's own `review.json` and every later round to reuse. A result
jig did not accept is removed rather than left under `work/` for the
round's store push. Both roots arrive as `Deps.Home` and `Deps.UserHome`,
which `cmd/jig` resolves once; one that could not be resolved is named as
the reason rather than searched for elsewhere. The summarizer does not run in
the lease: its session's working directory is a fresh, empty scratch
directory under the jig home (`home.IntentScratchDir`, one per dispatch,
removed after it whatever the dispatch returned), so under the headless
backend its edit grant is that directory and its own result file, and the
code under review is neither where it works nor anywhere it may edit. That
is not confinement - the headless backend is not a security boundary
([ADR 0008](docs/adr/0008-headless-permission-model.md)), so a session's shell
can still reach the lease - and jig does not chase what a session leaves
there. The dispatch also disables session persistence
(`session.Dispatch.NoSessionPersistence`, which the headless backend turns
into `--no-session-persistence`), so under the headless backend the excerpt
is not saved a second time in the operator's Claude Code data. After every summarizer dispatch,
whatever it returned, jig checks the lease's HEAD and tracked tree are
unchanged, as the reviewer's own guard does; a change (or a check that
could not be made) puts the lease back with `resetLeasePristine` (`git reset
--hard`, then `git clean -fd`), the recovery the reviewer's own round already
performs around its dispatch, and fails the inference open; a lease the check
confirms unchanged is left exactly as it is. That shared restore's behavior
with a Windows junction planted in the lease is a known gap of it, tracked
separately from inference. The round's `Review` carries the intent the
reviewer was given and the exact bytes of its file, and `Gate` reports and
hashes those. Inference fails open at every step - no mapped clone, no home
to look in, no transcripts, no match, a dispatch failure, a bad summary, or
a summarizer that moved HEAD or changed a tracked file - leaving the round's
intent at `"none"`, with the reason shown in the gate report's own `intent`
row; a gate round fails only
if that restore itself fails, since it cannot safely continue on a lease that
might still be dirty (an inference whose scratch directory cannot be made is
not attempted, and fails open with that reason).

Findings bookkeeping (`findings.go`) then folds the round onto the
cumulative state: a finding's identity across rounds lives only in its own
`prior` field, an open finding clears when its file was reviewed and
nothing routed reported it again, or when its file no longer exists at
head at all (checked directly against the lease, whatever this round's
own scope diff says), and a finding recurring for the second time - once
a fix slice built for it has actually gone green - is routed to a human
regardless of the reviewer's own label. Routing (`route.go`) first runs
triage over this round's `fix` batch and `ask` findings - a human at a
terminal decides the batch (accept all, or list ids to dismiss) and each
`ask` (keep, with an optional decision, or dismiss); `--yes` or a
non-terminal stdin runs `DefaultTriage` instead (every fix kept, every
`ask` with a full build target - a derived workspace and a resolvable
oracle - kept, one left undecided when either part is missing) - then
turns every kept finding into a fix slice: one per workspace and oracle
for the kept fixes, one each for a kept `ask`. Routing and triage both
finish, and every fix slice from them is appended, before `Gate` pushes
the store at the end of a successful round. A command that fails partway
is the exception, and a deliberate one: `Gate` and `Publish` both commit
and push best-effort on any error after their own first journal line -
`Gate`'s `gate-open` line, `Publish`'s own `reconcile` line - under a
subject naming the ticket and the failure, rather than leaving it for
the next command's own `Sync` to sweep up anonymously.

## Safety

**Structural command screen.** A regex over the whole command line is not
enough to catch a plainly-spelled but oddly-quoted `git push`: `git -C
<path> push` is a plain push once `-C <path>` is consumed as a global
option, and quoting or extra whitespace defeats a pattern match without
changing what git executes. `screen.Command` instead parses each shell
segment structurally - split, tokenize, unquote, match the git binary,
consume global options - and checks the *resulting* subcommand and flags,
so a quoting trick can't hide a push from the screen the way it can from a
regex. This is a per-token accident guard, not confinement: an indirection
the shell itself resolves - a git alias (`-c alias.x=push`), a shell
variable, or command substitution - still runs `push` once the shell
expands it, after the screen already judged the unexpanded tokens. See
[ADR 0008](docs/adr/0008-headless-permission-model.md).

**Secret-read screen.** `screen.SecretPath` denies any tool-call path shaped
like a live credential - `.env*`, `*_key*`, `id_rsa*`, `*.pem`, `.netrc`,
`_netrc`, `.npmrc`, plus a fixed list of exact credential files that sit
beside ordinary config in the same directory (`.git-credentials`,
`.claude/.credentials.json`, and the like) - or that has a credential
directory anywhere among its path segments, not only at the root:
`.aws`, `.ssh`, `.gnupg`, `.config/gh`, `.docker`, `.kube` - checked
against the arguments each tool names files with, which the
screen takes from the tool itself rather than from one shared key list, and
against what those arguments resolve to on disk, so a symlink in the lease
pointing at a credential directory is denied by where it lands. A network
share or a Windows device path is judged by its spelling alone and never
resolved, since resolving one can dial a remote host. A credential
directory counts as much as a file inside it, since a tool given a search
root reads everything under it. This is an accident guard, not
confinement: a shell glob, a variable, a junction, or a hard link the
resolution step doesn't see can still reach a credential the literal check
would have caught, and a content search over an ordinary directory that
happens to hold a credential file still returns it. See
[ADR 0008](docs/adr/0008-headless-permission-model.md) for why a denylist
of path spellings cannot close that gap.

**Headless permission model.** This model is not a security boundary: a
`headless` session's shell runs with the operator's own user rights and is
not confined to the lease, so it is only as safe as running that shell
yourself would be. What it does guarantee is disclosed below, and is
narrower than "safe to run against anything." A `claude -p` session can't
be asked anything, so the headless backend grants every tool it needs up
front and runs in `dontAsk` mode, which denies everything else. In a screened
dispatch (every dispatch jig makes), the shell and file-read tools are
granted only by the screen hook's allow (`screen.Granted`), so jig itself
grants them nothing without a passing screen. That is not the whole story:
Claude Code treats a hook it cannot launch as no decision, and its own
read-only classifier still lets part of the shell through, so a dead screen
would leave a session reading the machine with nothing saying so. jig
therefore proves its own binary's screen before every screened dispatch: it
runs `jig _screen` once, directly rather than through the hook wiring
Claude Code itself launches, with a call the screen must deny, and a hook
that is missing, fails, answers nothing, or allows it stops the dispatch
with `SCREEN_UNAVAILABLE` instead of starting the session. The edit tools are
granted by path-scoped permission rules for the lease worktree and the
dispatch's `result.json`, nothing else in the store. Web access, subagents,
skills and MCP servers are left out of the session entirely. Rules and hook
travel as one inline `--settings` object, and `--setting-sources user`
keeps the lease's own `.claude/settings.json` out: that file is content
under review, and loading it would run a hook the ticket branch chose and
could widen what the session may edit. That source also carries the lease's
`CLAUDE.md`, which the repo is meant to have, so jig passes that file itself
(`--append-system-prompt-file`): a settings file grants capability, while
`CLAUDE.md` only tells a session how the repo works. That file is read from
the lease's committed HEAD tree, not its working tree, so an uncommitted
symlink or hard link a screened session left behind cannot carry an outside
file into the next dispatch's system prompt this way - only committed
content ever reaches it - and it is capped at 64 KiB, refusing the dispatch
(`LEASE_MEMORY_TOO_LARGE`) rather than forwarding an oversized blob. A
session is bounded by `JIG_HEADLESS_TIMEOUT` (90 minutes by default): the
bound ends jig's wait and kills the session's process tree, so an
unattended run fails instead of hanging. A child the CLI leaves behind
after exiting normally is not jig's to kill either way; jig simply stops
waiting on it once `WaitDelay` elapses. A session that wrote its result
before the bound is honored, since the disk contract is what decides. It
is not a sandbox: a granted shell is not confined to the lease, and neither
are `Read`, `Glob` and `Grep`, whose only limit is the credential denylist.
See [ADR 0008](docs/adr/0008-headless-permission-model.md); `JIG_LIVE_CLAUDE=1
go test ./internal/session -run Live` checks the model against the
installed CLI through a local mock of the Messages API, and CI's
`claude-cli` job runs it against the latest CLI release.

**Guarded push.** `gitx.GuardedPush` refuses to push to a remote that is not
a local file path unless the caller has confirmed. `publish` is the only
command that pushes the ticket branch, and it only passes `confirmed` after
its interactive confirm (or `--yes`) has run. Every command separately
pushes the store's own bookkeeping commits to the store's remote
(`store.Push`) as it works; that push is unguarded by design - it moves
jig's own journal and ticket-folder state, not product code.

**Transcripts stay local.** A reviewer round with no stated intent reads the
operator's Claude Code transcripts for the repo (Intent inference, above). The
excerpt of the matched session lives under the jig home, never the store;
what the store's push carries is the summary jig accepted, the request
naming the session, and nothing a rejected summarizer result left behind
([SECURITY.md](.github/SECURITY.md) has the full list). There is no setting
that turns inference off; stating the intent stops it.

**Single git owner.** Only `gitx` runs git: it spawns the git program for
users' repositories and runs git in process, through go-git, for the store
([ADR 0011](docs/adr/0011-the-store-runs-git-in-process.md)).
`lint.TestNoGitSpawnOutsideGitx` parses every other package and fails on an
`os/exec` call or `exec.Cmd` literal whose program resolves to `git`, and
`lint.TestNoGoGitOutsideGitx` on any import of go-git. Every gitx call runs with
`-c maintenance.auto=false`, so none leaves git's detached background
maintenance running; the flag is argv-only, so a user's own git still
maintains their repos. Every call also drops an inherited `GIT_DIR` and the
other variables that would point git at a repository other than the one its
working directory names, and `cmd/jig` clears them from its own process at
startup (`gitx.ClearRepoEnv`), so sessions and oracles never inherit them.

**Synchronous maintenance.** jig's long-lived repos still get upkeep:
`store.Push` after a successful push and `pool.Acquire` after a reuse fetch
call `gitx.MaintenanceAuto`, a foreground `git maintenance run --auto` with
`gc.autoDetach=false`. It is best-effort; a failure never fails the push or
the acquire.

**One session per store clone.** While a jig session is running, do not run
a second one against the same store clone, and do not start or resolve a
rebase or merge there by hand: `abortFailedPull` (`internal/store/store.go`)
aborts any rebase it finds there after its own pull fails, with no record of
whether it was the one that started it, so a hand-started or hand-resolved
rebase or merge in that window is lost exactly like a second jig session
racing the first would be.

## Testing

```sh
go test ./...
```

Every test gets its own `t.TempDir()` and its own jig home (passed as an
argument to the packages that use one, or as `JIG_HOME` via `t.Setenv` to
`cmd/jig` and the binary), so a test run never touches a real machine's jig
home, and every
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
