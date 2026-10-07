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
run(frontier)  dispatch queued, unblocked slices to a build session; a claimed green
  │            runs the slice's oracle, and a red run goes back to that session (ADR 0020)
  │            reads:  slices.yaml, slices/<id>.state, questions/*.md
  │            writes: slices/<id>.state, work/<id>.attempt-N.{slice,result}.json,
  │                    journal.ndjson, questions/q-NNN.md, start.<repo>.sha
  │                    (an adopted branch's again at each dispatch until jig has built)
  ▼
gate           re-verification round: oracles (a pass jig recorded on the same tree is
  │            reused, ADR 0021), then a reviewer session's find/route/triage
  │            reads:  brief.md or intent.md (resolveIntent), slices.yaml, journal.ndjson,
  │                    ticket.yaml (branch), gate/round-N/findings.yaml (cumulative fold)
  │            writes: work/gate.round-N.{review,result}.json,
  │                    gate/round-N/{findings.yaml,findings.md,report.yaml,diff-changelog.md},
  │                    evidence/round-N/*, slices.yaml (fix slices, findings, from_gate: N),
  │                    intent.md (--intent/--doc, or inferred),
  │                    ticket.yaml (branch) and start.<repo>.sha (--branch adoption only)
  │            then, after a clean reviewer round only (best effort, never the verdict):
  │            a demo session shows the change working
  │            writes: work/gate.round-N.demo.result.json, gate/round-N/demo.yaml,
  │                    and, under <jig home>/evidence/ (not in the store), the media and
  │                    the session's input, demo.json
  ▼
publish        reconcile, revalidate, docs, squash (unpushed history only)
               → with a GitHub host, open or update the PR and post
               pr/review-notes.md as its first comment; with no host, push only
               reads:  gate/round-N/*, journal.ndjson, ticket.yaml
               writes: changelog/{<ws>.md,consolidated.md},
                       pr/{evidence.md,<repo>.md,review-notes.md},
                       ledger.md, platform/contract-index.md
```

A branch built outside jig enters at the gate instead of at the brief: the
first `jig gate <ticket> --branch <name>` adopts it as the ticket's own,
`run` then builds the fixes the round queued on that branch, the next
`gate` reviews it again, and `publish` pushes it as it is. See
[A ticket's branch](#a-tickets-branch) and [Publish](#publish).

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
| one repo's own change | that repo's `.claude/` + `jig.yaml` | conventions, oracle commands, env classes, invariant-sensitive paths - knowledge specific to that repo |
| platform or ticket history | the truth repo | tickets at root, `platform/`, `ledger.md` |
| session end | nowhere, except receipts | `evidence/` - a session's reasoning dies with it; only its artifacts persist |

Knowledge flows down (repo → session), truth flows sideways (ticket →
ticket, repo → repo), learning flows up (session → ledger).

One kind of file lives in none of the four, deliberately: a gate demo's media
(screenshots, GIFs, videos), under `<jig home>/evidence/<store id>/<ticket>/<head sha>/`.
It is a fifth home, justified by size: the store is a long-lived repo every
clone carries whole, so a 100 MiB video does not belong in its history, and a
lease is rewound between rounds. What invalidates a demo is a new reviewed
head and what can lose it is the machine, so it lives with the machine, like
the pool; only its small manifest, `gate/round-N/demo.yaml`, lives in the
store. The session's input, `demo.json`, lives beside the media
(`<head sha>.demo.json`, in the ticket's evidence directory): it holds the
absolute `media_dir`, a path that names the operator's jig home, and nothing
jig itself writes to the store for a demo, which is committed and pushed, does.
The session's own words (its result file, and the summary and captions
`demo.yaml` copies from it) are recorded as written, like the reviewer's
`result.json` summary: jig does not filter or rewrite model prose. See
[ADR 0014](docs/adr/0014-demo-session-at-the-gate.md).

## Manifest and invariants

A repo's `.claude/jig.yaml` can declare an optional `invariants:` list of
repo-relative, `/`-separated paths that are sensitive to change: schema
migrations, numeric precision code, or any other area where a structural
change merits a more capable (dearer) model. Each entry is either a directory
(ending in `/`), which covers its whole subtree, or a `path.Match` glob
pattern matched against each changed file. Every entry is validated and
matched in its `path.Clean`ed form, so a leading `./`, a doubled separator or
a redundant `..` segment still matches the paths git reports; `./` (the repo
root) covers every path in the repo.

When `frontier` measures a dispatch's staircase signals, it checks whether any
file changed in the ticket's lease diff matches a declared invariant. If any
match, the invariant signal floors the rung selection to the dearest model,
overriding everything else. Otherwise a dispatch opens on the first rung
(Sonnet by default; a project lists its own rungs in `project.yaml`'s
`staircase`) and climbs one rung for each earlier attempt of the slice that
failed at the work, as the journal records it. A question, a flawed brief or
a blocked environment is not such a failure
([ADR 0019](docs/adr/0019-builders-open-on-sonnet-and-climb-on-failure.md)).

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
  ticket.yaml       # optional: this ticket's own record - title, body, blockers, adopted branch
  start.<repo>.sha  # the sha the ticket's branch started from
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
    gate.round-N.demo.result.json
    intent.json        # intent inference, when attempted
    intent.result.json # kept only when jig accepted it
  gate/
    round-N/
      findings.yaml
      findings.md
      diff-changelog.md
      report.yaml
      demo.yaml         # a clean reviewer round's demo: recorded (per-file sha256, size, caption) or refused
  evidence/
    round-N/
  changelog/
    <workspace>.md
    consolidated.md
  pr/
    evidence.md
    <repo>.md
    review-notes.md
```

`work/` is store-side, not lease-side, on purpose: a build session's `git add
-A` runs inside its worktree lease, and must never sweep dispatch plumbing
into a slice's commit. `project.yaml`'s `schema_version` is the compatibility
contract - a store written by one jig version declares the layout a later
version must still read. A demo's media are not in this tree at all (see the
fifth home above): `demo.yaml` names them and holds their hashes.

## Project configuration

`project.yaml` is the project's configuration file. Beyond the required fields
(`name`, `ticket_format`, `repos`, `platform`), it declares `trackers:`, a list
of mirrors of the store's tickets: absent or `[]` means none, and an entry is
refused when the config loads (`VALIDATION_ERROR`) until T-24 builds the
tracker tree and T-22 the GitHub mirror. The old `tracker:` key still reads
`tracker: local` as no mirrors, until L3's migration rewrites `project.yaml`;
any other `tracker:` value, `tracker:` and `trackers:` together, and `routes:`
(which only ever fed publish's now-gone route step) are all refused at load,
each with help naming the fix. `jig init` writes `trackers: []` and no
`tracker:` key. It may also contain an optional `gate` block configuring the
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

Sessions also get a reasoning effort, passed to `claude` as `--effort`: the
gate reviewer's by round scope, a builder's by attempt
([ADR 0023](docs/adr/0023-the-reviewer-runs-on-the-dearest-rung-and-builds-and-reviews-get-an-effort.md)):

```yaml
gate:
  review_effort:
    full: high                 # the first review, or one not on top of the last
    delta: medium              # re-checking fixes since the last reviewed head
builder_effort:
  first: medium                # a slice's first attempt
  retry: high                  # any attempt after a failed one
```

The values shown are the defaults, also for a key with no value; `""` passes
no effort. The gate reviewer runs on the staircase's dearest rung on every
round.

Invalid configuration values are refused when `project.yaml` loads:
a negative `fix_rounds`, a `fix_risks` entry that is not `high`, `medium` or
`low`, a `fix_slice_findings` below 1, or an effort that is not one of `low`,
`medium`, `high`, `xhigh`, `max` or empty.

## Module responsibilities

The dir column is exact; a lint test parses this table and asserts every dir
exists.

| Package | Entry points | Input → Output |
|---|---|---|
| `cmd/jig/` | `main`, `cmdScreen` | CLI args, or a PreToolUse hook payload on stdin → subcommand dispatch, or a screen allow/deny |
| `demo/fixture/` | `main` | `-out <dir>` + `-scenario <name>` → a fixture built via `internal/fixture`, and shell `export` lines (JIG_HOME, store dir, scenario dir, the fixture repo's working clone, ticket id) for a VHS tape to eval |
| `e2e/` | (tests only) | the fixture + fake backend → asserts the full brief→publish chain twice; with `JIG_LIVE_CLAUDE`, README's Quickstart through the real `claude` CLI; `JIG_E2E_BINARY` → the same against an installed jig |
| `internal/axi/` | `Render`, `Table`, `KV`, `Help`, `RenderError`, `ExitCode` | labelled data → jig's plain-text output register and process exit codes |
| `internal/claudetest/` | `API`, `Session`, `Serve` | scripted sessions (tool calls in order) → a stand-in Messages API on loopback that the real `claude` CLI runs against, for the live CLI tests |
| `internal/envrun/` | `Up`, `Shell`, `ShellOutput`, `KillTree` | a `manifest.EnvClass` + ticket/dir → a running `Handle`, or `Unavailable`; a command with a time limit → its output (an oracle run); a process tree ended whole |
| `internal/fixture/` | `Build`, `Generate`, `RepoRoot` | a dir + `Opts` → a fixture repo, its store, and a scripted attempt scenario (plus the machine mapping under `Opts.Home`, by default the jig home `home.Root` resolves); `Generate` builds into a `t.TempDir()`; `RepoRoot`: a caller's source file → the module root |
| `internal/frontier/` | `Run`, `Requeue`, `RequeueSlice`, `Schedule` | `Deps` + `RunOpts` → a `RunReport` (slices driven to green, parked, env-blocked, or stalled) |
| `internal/gittest/` | `Run`, `AtExit` | `*testing.M` → a hermetic git config for the whole test binary, then its exit code |
| `internal/gitx/` | `Run`, `RunEnv`, `RunRaw`, `MaintenanceAuto`, `RevParse`, `MergeBase`, `CommitsIn`, `IsAncestor`, `Missing`, `DiffNameOnly`, `FileExistsAtRev`, `PathExistsAtRev`, `IsLocalRemote`, `GuardedPush`, `CommonDir`, `SameDir`, `TopLevel`, `CommitTime`, `OpenRepo` (`Repo`: `State`, `CommitAll`, `Push`, `Fetch`) | argv + a working dir → git plumbing output, or a refused push; a store's directory → the same store operations in process (go-git), or `ErrUseCLI` for the caller's git-program path |
| `internal/graphify/` | `Detect`, `DetectWith`, `Plane` | `project.Config` → a `Plane` (real or `Noop`) that keeps a lease's code graph current and finds the code linked to a slice's goal ([ADR 0026](docs/adr/0026-a-code-graph-gives-the-builder-its-starting-points.md)) or affected by a seed |
| `internal/home/` | `Root`, `MachinePath`, `PoolDir`, `IntentExcerptDir`, `IntentScratchDir`, `EvidenceDir` | `JIG_HOME` (or the real home dir) → the jig home root, which `cmd/jig` resolves once and passes down; a root → per-machine paths, including the directories intent excerpts and summarizer scratch directories go under, and where one reviewed head's demo media live |
| `internal/intent/` | `NewClaudeReader`, `Best`, `RenderExcerpt` | a repo's git common dir + a time window → matching local agent `Session`s; a scope diff's files → the `Match` a model then summarizes |
| `internal/journal/` | `Append`, `Read`, `BuiltCommits`, `GreenClaims`, `FailedAttempts`, `LastOracleSeconds`, `VerifiedSlices`, `RenderChangelog`, `RenderConsolidated`, `RenderDiffChangelog` | journal `Line` events → `journal.ndjson` and rendered changelogs; a ticket's journal → the commits jig built and verified |
| `internal/manifest/` | `Resolve`, `MatchesInvariant` | a repo dir → a `Manifest` of workspaces, oracle commands, env classes, and invariant-sensitive paths; a file path → whether it matches a declared invariant |
| `internal/outcome/` | `ParseJSON`, `ParseText`, `Signature`, `StallCounter` | a session result (JSON or text) → a typed `Result`, and a stall signature |
| `internal/pool/` | `Acquire`, `Dir`, `Usable`, `CheckTicket`, `Compare`, `DivergedError`, `RequireBuilt`, `HoldsUnpushedBuilt`, `MustExistOnOrigin`, `RecutUnlessBuilt` | the jig home root + repo/remote/target/branch + a ticket and its role (build, gate, publish) → a `Lease` (a full clone, re-pointed to its start point, and synced with its branch when origin has it; anything git shows is not a repository of its own is moved aside and cloned afresh) |
| `internal/project/` | `Load`, `Resolve`, `InitStandalone`, `InitProject` | `project.yaml` + the machine mapping under the jig home root → a `Config` |
| `internal/repohost/` | `New`, `Host.CreatePR`, `Host.CreatePRWithMedia`, `Host.FindOpenPR`, `Host.UpdatePR`, `Host.UpdatePRWithMedia`, `Host.CommentPR`, `Host.ReadPRBody` | a repo's own `remote:` → a `Host` (a GitHub host, for a remote on github.com over ssh or https; refused up front, `GH_NOT_INSTALLED`, when `gh` is not on PATH) or `nil` (any other remote: a local path, or another host); a branch, base, title and a body file → an opened or updated pull request, with or without the gate's demo media attached, found by its qualified head into a base, or read back |
| `internal/revieweval/` | `LoadCorpus`, `RunCorpus`, `MatchRound`, `ScoreRound`, `RenderReport` | a labeled corpus (`testdata/revieweval`) + a session backend → a `CaseScore` per case, matched structurally against seeded gold through the real reviewer contract |
| `internal/screen/` | `Command`, `SecretPath`, `ToolCall`, `Granted`, `Grants` | a shell command, path, or tool-call input → allow, or deny with a reason; a tool name → whether a passing screen grants it |
| `internal/session/` | `New`, `Backend.Run` | a `Dispatch` (paths to `slice.json`/`result.json`, and for a gate demo one extra directory the session may write in) → `result.json` written to disk |
| `internal/staircase/` | `Select`, `Dearest`, `Default` | build `Signals` (the slice's failed attempts, invariant match) + `Config` → a builder's model rung: invariant floored to the dearest rung, one rung up per failed attempt, otherwise the first rung; `Dearest` is the gate reviewer's rung on every round |
| `internal/store/` | `Open`, `Lock`, `AtomicWrite`, `BriefSectionHashes`, `ReadSlices`, `ReadChart`, `WriteChart`, `ReadTicket`, `Ticket.Adopted`, `ReadTicketDeps`, `CreateTicketRecord`, `WriteTicketBranch`, `CheckAdoptableBranch`, `TicketBranch`, `ResolveTicketBranch`, `TicketFilePath`, `StartSHAPath`, `WriteStartSHA`, `Store.ID`, `Mint`, `Claim` | ticket-folder and chart-folder reads/writes → the truth-repo tree described above; a store clone → the stable id its machine-local files are keyed by; `ticket_format` + a ticket's own record → the next id, its folder created and its `ticket.yaml` written whole (refused, before anything is written, when jig cannot use the id it computed); a mint (or other write) + a commit message → that id landed on the store's origin, retried after a push the origin rejects, undone and refused (`ID_NOT_CLAIMED`) after too many or any other failed push, committed with no push on a store with no origin |
| `internal/termrec/` | `Cast`, `Event`, `ReadAsciicast`, `Cast.WriteAsciicast`, `Cast.Validate`, `Cast.SVG`, `SVGOptions`, `Cast.FinalText`, `NewRecorder`, `Recorder.Stream`, `Recorder.Cast` | a program's writes to its pipes, each stream teed into a `Recorder.Stream` → a terminal recording (its size and each write with its time; asciicast v2); a recording → an animated SVG of the screen as it changed, within `MaxSVGBytes`, or the last frame as text ([ADR 0029](docs/adr/0029-demos-are-recordings-of-the-builds-end-to-end-scenarios.md)) |
| `internal/verifydeliver/` | `Gate`, `Publish`, `RebaseOnto`, `ParseDemoResult` | `Deps` + `GateOpts`/`PublishOpts` → a `GateReport` (a clean reviewer round also carries its demo: the session's media verified and recorded, or refused), or a `PublishReport` with an opened or updated PR (its body carrying a `## Demo` section, and its media attached, when the shipped head has one) |

## Session backends

The contract between jig and any backend is pure disk: jig writes
`slice.json` (goal, oracle, workspace, prior attempt log, any answered
question, how long jig's last run of the oracle took on this ticket,
[ADR 0024](docs/adr/0024-builders-are-told-how-long-the-oracle-took.md),
what the ticket's verified slices did and changed,
[ADR 0025](docs/adr/0025-a-builder-reads-what-earlier-slices-built.md), and,
when the project keeps a code graph, the code it links to the goal,
[ADR 0026](docs/adr/0026-a-code-graph-gives-the-builder-its-starting-points.md)),
the backend runs a session in the lease worktree, and jig reads
back `result.json` (outcome, summary, commit, and - for `needs-input` - a
question). Nothing crosses in memory.

Three backends implement that same narrow interface:

- **fake** - replays a scripted scenario directory; no session, no network. The CI and fixture path.
- **headless** - runs a local `claude -p` subprocess in `dontAsk` permission
  mode. The command/secret screens attach as a PreToolUse hook
  (`jig _screen`); its allow is jig's own only grant for the session's shell
  and read tools, though the operator's own user settings (which still
  load) can grant more on top. Edits are granted only inside the lease, on
  the dispatch's own `result.json`, and - for a gate demo - inside its one
  `ExtraWriteDir` (see Safety). Its shell waits up to 30 minutes on a
  command and runs nothing in the background: a command past that bound is
  ended, since nothing collects a background command after the session's
  turn ends ([ADR 0018](docs/adr/0018-builders-test-narrowly.md)). The shell and reads it
  grants are the operator's own and are not confined to the lease, so this
  backend is not a security boundary.
- **herdr** - drives a remote agent through herdr, exec'd natively off Windows and, on Windows, inside a WSL login shell (`JIG_WSL_DISTRO` picks the distro; unset uses WSL's default); it has no PreToolUse hook to attach a screen to, so herdr sessions are not screened. It scopes no edits, so a dispatch's `ExtraWriteDir` needs no grant there. On Windows it creates the workspace at the worktree's WSL mount and rewrites the prompt's own mentions of every path of the dispatch (worktree, input and result files, `ExtraWriteDir`) to their mounts, as headless does its long spelling; the files jig wrote keep the host spelling of the paths they hold. A failed herdr command is named in an error by its subcommand and herdr's stderr, never by its operands (the prompt, the worktree).

A `Dispatch` may name one `ExtraWriteDir`: a single absolute directory outside
the worktree that the session may also write files in, which is where a gate
demo's media go. The fake backend copies a scenario's demo media into it, and
a demo's shell tools are governed by the screen wherever one attaches
(headless, as any session's are), and a session on herdr has none.

Screens attach only where the backend's tool-call surface allows a
PreToolUse hook, which today is `headless` alone; `fake` has no tool calls
to screen, and `herdr`'s tool calls run inside the remote agent it drives,
outside jig's own process.

**Resuming a session.** A backend that can continue a session it ran
implements `session.Resumer`: `RunResumable` is `Run` that also returns the
session id, and `Resume` hands that session one more turn. Only headless does,
through `claude -p --resume <id>`, reading the id from the CLI's final result
object. The frontier uses it when its own oracle run at a claimed green comes
back red, and falls back to a failed attempt with any other backend
([ADR 0020](docs/adr/0020-jig-runs-the-slice-oracle-at-green.md)).

## Gate reviewer contract

A gate round's reviewer dispatch (`internal/verifydeliver/review.go`) is the
same disk-only contract as a build session, narrowed to read-only: jig
writes `work/gate.round-N.review.json` (the ticket, round, scope, base and
head sha, the round's resolved intent - source and path, `Gate`'s own
`resolveIntent`, precedence brief.md then intent.md then none - plus
slices/journal paths, manifest oracles, `oracles_passed` - every oracle run
the gate made on this head before the review, all passed, or reused with
`reused_from` from a pass of the same command, under the same env classes,
that jig recorded on a commit with the same tree
([ADR 0021](docs/adr/0021-the-gate-reuses-an-oracle-pass-on-the-same-tree.md)) - and the
cumulative `open`/`dismissed` findings folded from every earlier round), the
backend runs a session against `must_review` - every file the scope diff
touched plus every still-open finding's file - and jig reads back
`work/gate.round-N.result.json` (findings, `still_present` - unchanged
earlier findings confirmed by id and current line, which jig expands into
the earlier finding before folding the round
([ADR 0022](docs/adr/0022-the-reviewer-confirms-an-unchanged-finding-by-id.md))
- `reviewed_paths`, a summary). The
review is a read: test evidence is the oracles' job, and they ran on this
head before the review, so the reviewer runs no tests, with an empty
`oracles_passed` too
([ADR 0017](docs/adr/0017-the-reviewer-reads-the-gate-tests.md)). The
reviewer edits, commits, and pushes nothing; jig checks this itself (HEAD
and the tracked tree unchanged after dispatch) rather than trusting the
session, and rejects the round (`REVIEW_INVALID`) if either moved, or if
`result.json` fails strict structural validation - an unknown `action` or
`risk`, a finding whose file is neither present at head nor deleted in the
scope diff, an oracle that isn't a manifest oracle, a `prior` naming no
known finding, a `still_present` id naming no known finding, repeated, or
also a finding's `prior`, a `still_present` finding whose file is gone
without a deletion, or `reviewed_paths` missing a `must_review` path all
fail the round loudly rather than falling back to a partial result.

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
regardless of the reviewer's own label. A would-be open fix that clears
every other check (recurrence bound, a missing build target, the risk
floor) is parked for a human the same way once the ticket's fix budget
(`gate.fix_rounds`, compared against `UsedFixBudget`'s count of earlier
rounds whose own `report.yaml` lists a fix slice it appended) is reached.
Each of these three forced-ask cases records which one it was in
`findings.yaml`'s additive `routed_why` field (`recurrence`,
`build-target`, or `budget`), empty for an `ask` the reviewer reported as
such itself. Routing (`route.go`) first runs
triage over this round's `fix` batch and `ask` findings - a human at a
terminal decides the batch (accept all, or list ids to dismiss) and each
`ask` (keep, with an optional decision, or dismiss), a budget-parked one no
differently, since only this terminal path may ever keep one; `--yes` or a
non-terminal stdin runs `DefaultTriage` instead (every fix kept, every
`ask` with a full build target - a derived workspace and a resolvable
oracle - kept, one left undecided when either part is missing, and a
budget-parked `ask` always left undecided whatever its build target) - then
turns every kept finding into a fix slice: one per workspace and oracle
for the kept fixes, one each for a kept `ask`. Routing and triage both
finish, and every fix slice from them is appended, before `Gate` pushes
the store at the end of a successful round. A command that fails partway
is the exception, and a deliberate one: `Gate` and `Publish` both commit
and push best-effort on any error after their own first journal line -
`Gate`'s `gate-open` line, `Publish`'s own `reconcile` line - under a
subject naming the ticket and the failure, rather than leaving it for
the next command's own `Sync` to sweep up anonymously.

## Gate demo contract

After a reviewer round whose verdict is clean, and once everything the round
wrote is committed and pushed, `Gate` dispatches a demo session
(`internal/verifydeliver/demo.go`, `demogate.go`) and records what it
produced, unless a demo is already recorded for that reviewed head or the
gate ran with `--no-demo` (`jig solve` passes the flag through). Only the
reviewer source has a `Demo` method, so a scripted round never dispatches
one. The contract is disk only, like the reviewer's: jig writes `demo.json`
beside the media, under the jig home (ticket, round, the merge base and the
head sha, the round's intent `{source, path}` - the very one the reviewer was
handed, an inferred one included - an absolute `media_dir`, and the
`limits` - the image and video extensions `gh --attach` accepts, images at
most 10 MiB, videos at most 100 MiB, at most 50 files; it is not in the store,
since `media_dir` names the operator's jig home), the session writes
`work/gate.round-N.demo.result.json` (`{"media": [{"file", "caption"}],
"summary"}`, where an empty list with a summary saying why nothing is visible
is valid), and jig reads it back strictly. The prompt states the job, the
contract and what jig verifies, and says what each intent source means in the
reviewer prompt's own words (`intentSourcesPrompt`); it names no tool, since a
repo documents its own demo tooling in its own `CLAUDE.md`.

jig verifies rather than trusts, and refuses a result whole on any failure:
the gate lease's HEAD and tracked tree are unchanged (the reviewer's own
guard); the result has a non-empty summary and a non-empty caption on every file;
`media_dir` is still a plain directory and the very one jig made (a directory
above it swapped for a link is refused, as is an attempt whose store id or
ticket directory is already a link); every listed file is a non-empty
regular file directly inside it (`Lstat`, so a link, a subdirectory or a
Windows junction is refused; gh refuses an empty file), with an allowed
extension and within its size limit; at most 50 files. It then renames the
accepted files `demo-<n>.<ext>` in the order listed and removes everything
else from `media_dir`, so the directory holds exactly what `demo.yaml` lists. A
passing result becomes `gate/round-N/demo.yaml`
(`status: recorded`, `head_sha`, `summary`, and per file `name`, `sha256`,
`size`, `caption`); anything else becomes `status: refused` with a reason, and
what the reason holds depends on who refused the demo, since it is committed to
the store. When the demo session or its backend failed (the dispatch errored,
or the session wrote no demo result) it is `the demo session failed:` and the
failure's code (`failureCode`, the code a store commit subject carries for a
failed gate round), and never the failure's text, which only the gate report
prints (`demo_detail`) and nothing records; a backend's own stand-in for a
missing result (a file with the slice result's `outcome` field) is removed from
`work/` before the demo's own store push. When jig refused what the session wrote, the
reason is jig's own words: the media directory, the jig home and the store are
named `media_dir`, `<jig home>` and `<store>` in any operating system or git
message it quotes, and a file the session listed is named by its index and the
last element of the string it gave, never by the string, which may be a path
in any spelling. The `summary` and captions are the session's own words,
recorded as written.
A demo is best effort: it never makes a clean round unclean, its refusal is
recorded and shown in the gate report (`demo: refused`, `demo_reason`, and for
a failure `demo_detail`), and a
refused demo does not count as the head's demo, so the next clean round on
that head tries again. The media themselves live under
`<jig home>/evidence/<store id>/<ticket>/<head sha>/`, never in the store.

Known v1 limit: env classes are torn down after the oracles, before any
session runs, so a demo that needs one reports that it cannot record. See
[ADR 0014](docs/adr/0014-demo-session-at-the-gate.md) for this half of the
contract, and [Publish](#publish) below for the other: what `jig publish`
does with a recorded demo when it ships the head it belongs to.

## A ticket's branch

A ticket's working branch is recorded in its `ticket.yaml`, not derived
from its id ([ADR 0013](docs/adr/0013-a-ticket-branch-is-recorded.md)).
`store.TicketBranch` resolves it (the recorded `branch:`, else `jig/<ticket>`)
and every command that names the branch resolves it there, once. A ticket
that recorded one is an *adopted* ticket: the branch was built outside jig and
is on origin, and the ticket needs no brief and no slices to be gated, built
and solved.

**Adoption** happens in `Gate` (`resolveGateBranch`), on the first `jig
gate <ticket> --branch <name>`. A later `--branch` naming another branch
is `BRANCH_MISMATCH`; naming the recorded one changes nothing. Adoption is
refused for a name `TicketBranch` would refuse (`store.CheckAdoptableBranch`:
the target, a name git rejects or expands), for a branch missing on origin
(`BRANCH_NOT_FOUND`), and for a ticket jig already built on (`jigBuilt`,
`TICKET_ALREADY_BUILT`): a `verified` journal line, a green `result` line
naming a commit (`journal.GreenClaims`, the one fact every jig version journals
and nothing removes, so a v0.1.x journal, or one whose slices a requeue
set back to queued, is refused too), or a slice in state `green`. Judged
from the store, never from the machine's lease. Which commits jig built is
the `verified` lines alone (`journal.BuiltCommits`), and an adopted ticket
always has them. `Gate` writes the record and the start sha (the branch's tip,
not the target's) only after every precondition has passed, right before its
`gate-open` journal line, so a refusal leaves nothing behind.

**Intent.** An adopted ticket's rounds resolve their intent as every ticket's
do: the brief, else `intent.md` (`--intent`, `--doc` or inferred), else none.
Inference needs nothing special for it: a branch built outside jig is the case
it exists for, the scope diff is the branch against its merge base with the
target, and the matching session is the author's own.

**The start sha** is what a build's commits must descend from (`verifyGreen`)
and where a gate round's scope falls back when the branch has no merge base
with the target; squash and reconcile do not read it. Adoption records the
branch's tip. The author owns the branch until jig builds on it, so until the
journal records commits jig built, every dispatch records origin's tip again
(`ensureStartSHA`), following an author who pushed or rewrote the branch; after
that it stays. An ordinary ticket's never follows origin. A build on an adopted
branch refuses with `BRANCH_NOT_FOUND` when origin no longer has the branch
(`pool.MustExistOnOrigin`), instead of cutting the branch from the target, and
with `BUILD_LEASE_MISSING` when its lease's branch lacks a commit the journal
records jig built (`pool.RequireBuilt`, below), instead of building on a branch
that leaves them out.

**Sync rule.** `pool.Acquire` fetches with `--prune` and compares a local
branch with `origin/<branch>` when origin has one (`syncWithOrigin`, on
`pool.Compare`), for every role: no commits of its own, it fast-forwards; ahead
of origin (jig's own unpushed commits), it is kept; commits on both sides,
`BRANCH_DIVERGED` (`pool.DivergedError`), with the lease untouched - except a
build of an adopted branch whose lease holds no commit jig built that origin
lacks (`pool.RecutUnlessBuilt`, `pool.HoldsUnpushedBuilt`), which follows the
author and is re-cut from origin's tip. A branch origin lacks, the ordinary
`jig/<ticket>` until a publish pushes it, is left as it is; once a publish
has squashed and pushed it, the build lease's unsquashed commits stand
diverged from the squash, and a build, a gate round or a publish after it stops
with `BRANCH_DIVERGED` until origin's branch is merged into the build lease,
after which a publish pushes as it is (see [Publish](#publish)). The gate and
publish leases are disposable, so `Gate` and `Publish` drop their own local
copy of the branch before acquiring (`restoreLeaseBeforeAcquire`,
`dropLeaseBranch`).

**What a round reviews.** For an adopted ticket the gate lease holds origin's
copy of the branch while the journal records no commits jig built on it. Once
it does, and for a ticket's own `jig/<ticket>` from the start, jig's commits
stay in the build lease until someone pushes them, and `chooseBuiltCopy` picks
the copy that holds them by the same sync rule, the build lease's copy against
origin's, once origin has a copy at all (until it does, the build lease's is the
only one): in step with or behind origin's, origin holds them all (and whatever
was added since, by the author or by a publish), so the round reviews origin's;
ahead of it, the round reviews the lease's; diverged, the round refuses with
`BRANCH_DIVERGED`, naming the build lease, unless the lease's copy holds no
commit jig built that origin lacks (`pool.HoldsUnpushedBuilt`, the rule the
build's re-cut applies), which makes it no build lease holding the branch;
and with no build lease holding the branch here, the round reviews origin's.
Whichever copy that is must hold every commit the journal records jig built, or
the round refuses (`BUILD_LEASE_MISSING`, `pool.RequireBuilt`) - one rule for
every copy, which the frontier applies to its build lease before it dispatches
on an adopted branch. The rule is the branch's, not the kind of ticket's: a
ticket's own `jig/<ticket>` is judged the same way once a publish has put it on
origin, so the round after a publish that pushed it as it is reviews origin's
copy, and after the first publish, which squashed it, the round refuses until
origin's branch is merged into the build lease. Publish ships the copy the round
reviewed: `pointAtTicketBranch` is the one function that picks it, for the gate
and for publish alike.

## Publish

`Publish` (`internal/verifydeliver/publish.go`) ships the ticket's branch, in
this order. Steps 1 to 5 only read and refuse, and come before its first store
write, so a refusal there leaves the store untouched; from the `reconcile`
journal line (step 6) on, a failure commits and pushes the store best-effort
under a subject naming it (see the store push above).

1. **Preconditions.** The ticket's record is read once and names the branch
   (`store.ResolveTicketBranch`); every slice is green; the latest gate round
   is clean (`PUBLISH_NOT_CLEAN`).
2. **The publish lease.** An existing publish lease is restored pristine and
   drops its own copy of the branch (`restoreLeaseBeforeAcquire`), then is
   acquired (`pool.MustExistOnOrigin` for an adopted branch, which is on origin
   by definition: `BRANCH_NOT_FOUND`) and pointed at the copy the gate reviewed
   by the function the gate uses, `pointAtTicketBranch`: an adopted branch as
   origin has it while jig built nothing on it (no build lease needed), or
   whichever copy holds the commits jig built (`chooseBuiltCopy`: the build
   lease's while origin has no copy of the branch, else the two compared;
   `BRANCH_DIVERGED`, `BUILD_LEASE_MISSING`).
3. **Ship what was reviewed.** The head at that point must be the last clean
   round's `reviewed_sha` for the repo, when a reviewer round recorded heads
   (`PUBLISH_UNREVIEWED_HEAD`, `checkReviewedHead`); a round that recorded heads
   for other repos only is refused too, and a scripted round records none at
   all and is let through. The same holds for a binding intent, brief or
   explicit: re-reading brief.md or intent.md now and hashing it must match
   the sha256 that round's `report.yaml` recorded (`PUBLISH_UNREVIEWED_INTENT`,
   `checkReviewedIntent`), so an edit after the round cannot be published as
   the reviewed intent, silently. An inferred or absent intent has nothing
   pinned to check.
4. **A fast-forward or nothing.** A branch already on origin must be a
   descendant of origin's copy (`PUBLISH_NOT_FAST_FORWARD`,
   `requireFastForward`); publish never forces. The copy was compared with
   origin's when the lease was pointed at it, so what this catches is a push
   since, between the acquire's fetch and the one publish makes right after. The
   refusal says what to do about it by whose copy publish would ship
   (`pointAtTicketBranch` reports it): the build lease's, where origin's is
   merged in, or origin's own, which a round over the branch as it is now
   catches up with. It comes before reconcile, whose merge of the target could
   conflict in a branch that cannot be pushed and hide it behind `CONFLICT`.
5. **The pull request.** The repo host is built here, not after the push, from
   the repo's own `remote:` (`repohost.New`): a GitHub host for a remote on
   github.com over ssh or https, with any user, refused up front
   (`GH_NOT_INSTALLED`) when `gh` is not on PATH; `nil` for any other remote - a
   local path, as this store's own is, or another host. With a host, it is
   asked for the branch's open pull request into the target (`FindOpenPR`,
   exact: the pull requests endpoint with the qualified head, the base and the
   open state, which GitHub applies, so no page of other forks' pull requests
   can hide it); a lookup that fails refuses the publish. A closed or merged
   pull request from the branch, or an open one into another base, is not that
   one: none is found, and a new one is opened. With no host, nothing is
   looked up: publish pushes the branch and opens nothing.
6. **Reconcile** merges the target into a branch that is on origin and rebases
   one that is not (`reconcile`'s own `ls-remote`, whose answer is the rest of
   publish's too); the `reconcile` journal line is the first store write.
   `PUBLISH_NO_DIVERGENCE` and `NOTHING_TO_PUBLISH` refuse an empty change.
7. **Revalidate**: the gate's recorded target sha against `origin/<target>`;
   moved, every oracle runs again in the publish lease.
8. **Docs.** The memorize commit (`.claude/retrieval/<ticket>.md`) is made on
   the branch in the publish lease, like the merge of the target: jig's own
   commits, on top of the author's on an adopted branch. The lease is put back
   at its head first, so whatever an oracle left in it stays out and the commit
   holds the notes only. Then the changelogs, the ledger entry (titled by the
   first slice that did not come from a gate round, else the recorded title, so
   an adopted ticket, whose slices are only the gate's fixes, keeps the name it
   was minted with), the contract index and the evidence.
9. **Squash, unpushed history only, then the pull request's own docs.** A
   branch that was not on origin is squashed into one commit (refusing a range
   whose commits reached a remote under another name, `PUSHED_RANGE`); one
   that was is left as it is, and the journal's `squash` line records
   `none:branch-on-origin`. `pr/<repo>.md` is then rendered: `## What
   changed` and `## Verification` always, `## Intent` first when the round's
   intent is a binding source with text of its own, and `## Demo` between
   `## What changed` and `## Verification` when the shipped head has a
   recorded demo - never more than these four `## ` sections, in this order,
   and never another. `## Intent` comes from the last clean
   round's intent provenance and is left out entirely for `inferred` or
   `none` - an inferred intent summarizes the author's own private agent
   session and never reaches a pull request (`docs/adr/0012-intent-provenance.md`,
   `.github/SECURITY.md`) - and for a binding source whose file yields no
   text of its own, rather than publishing a heading stating no intent: a
   brief with no first `## ` section with text under it, whether it has no
   such heading or one with nothing below it (`firstBriefSection`, and never
   the whole brief in its place), an `intent.md` that is front matter and
   nothing else. A brief that yielded nothing is the one omission publish
   warns about (`warn`, before the confirmation prompt, naming the brief):
   nothing else in a run says the body went out with no statement of why the
   change exists - not the report, and not `jig validate`, whose section count
   (`store.BriefSectionHashes`) counts the `## ` lines a code fence quotes
   too. The intent text itself is rendered intact, with its own headings
   demoted below the section level (`demoteHeadings`, which shifts them all
   by the least that puts the shallowest at `### `, leaving a fenced code
   block's own lines alone), so a whole design doc recorded by `jig gate --doc`
   cannot add an extra `## ` section of its own or outrank the body's with a
   `# ` title.
   A fenced code block the text leaves open is closed before it is embedded
   (`closeOpenFence`), since an unclosed fence renders the two sections
   after it as the inside of a code block; a `## ` line inside a fence does
   not end the brief's first section either (`firstBriefSection`), so a
   brief that quotes the body's own sections publishes its whole example.
   `## What changed` is, for an adopted ticket, the author's own pre-adoption
   commits first (subject and short sha, from the merge base with the target
   up to the start sha recorded at adoption, `adoptedAuthorCommits`); then
   one bullet per green slice in slice order (the first line of its goal,
   `bulletGoal`, and its short commit sha, omitted when a slice recorded
   none), fix slices grouped after the others as fixes from review. One
   line per bullet is what keeps a fix slice's goal - the builder prompt
   `buildFixSlices` wrote out of the gate's findings - from dumping those
   findings' own detail into the body, which is what the comment below is for.
   `## Demo` (`renderDemoSection`) comes from the gate round that recorded
   one for the shipped head, checked against `checkReviewedHead`'s own: the
   latest round when its own `gate/round-N/demo.yaml` is `status: recorded`
   and names that exact head (checked again, not trusted), or - when the
   latest round ran no demo at all because an earlier one on the same head
   already had (a later clean round on an unchanged head runs none of its
   own, ADR 0014's `DemoExisting`) - the earliest earlier round that did.
   No demo recorded for the head at all (none ever, or one recorded for a
   different head) and a demo refused outright (`status: refused`) both
   leave the body with no section, but publish's own output says which:
   `DemoRenderResult.NoDemo` for the first, `DemoRenderResult.DemoRefused`
   (carrying demo.yaml's own reason) for the second. Each listed file of a
   recorded demo is checked again before it is trusted - present in the
   evidence directory for that head (`demoMediaDir`), with the manifest's
   own sha256 and size - and a file that fails is left out of the section
   and named in publish's output rather than rendered as if it were still
   there (`DemoRenderResult.Omitted`); every listed file failing leaves no
   section either, which is `DemoRenderResult.AllMediaFailed`, neither of
   the other two. What renders, when anything does, is the demo's own
   `summary` and then every verified file with its caption, in
   `demo.yaml`'s own order and under its own recorded name - never
   renumbered from the verified files alone - in the one reference form
   `gh` actually rewrites for its kind (gate finding r1-f3, DECISIONS.md):
   an image (`demoKind`'s own extension list) as markdown image syntax,
   `![caption](./<name>)`, which `gh ... --attach` rewrites to the uploaded
   URL in place; a video as the plain bullet `./<name>: caption` it has
   never reliably rewritten, which a later step (below) reads back and
   patches itself where the host can. Neither this function nor
   `pr/<repo>.md` itself ever names the evidence directory.
   `## Verification` names the oracles green at the last clean round
   (`SortedOracleNames`, the manifest Publish itself resolved) and the
   reviewed head, the revalidation tier, and one line counting the review's
   findings by how they ended (fixed, dismissed, noted, asked), pointing at
   the pull request's first comment for the detail - never a store path.
   `pr/review-notes.md` is rendered beside it: the last round's own summary
   (`findings.yaml`'s `summary`, not its verdict), every finding across every
   round ordered by risk with how it ended (fixed by slice X and cleared at
   round N, kept with the human's decision, dismissed by a human, noted, or
   asked and still open), and coverage - the files the change touched
   (Publish's own diff of the ship range, read before the memorize commit
   adds anything on top of it) against the files the reviewer read (every
   round's own reviewed paths). Both render from the one pass
   `collectFindingsWithOutcomes` makes over every round's `findings.yaml`,
   plus what the store already has (slices, the journal, the gate rounds),
   with no model call (`writePRBody`, `writeReviewNotes`,
   `internal/verifydeliver/render.go`).
10. **Confirm, push, pull request.** `--yes` or an accepted question (which
    says whether it would push and open a pull request, push and update the
    open one, or - with no host - only push the branch), then a plain `git
    push` of the branch, then, with a host, the pull request: the open one is
    updated (`UpdatePR`, `gh pr edit --body-file`) with `pr/<repo>.md` as its
    body, otherwise one is opened (`CreatePR`) with it. With no host nothing is
    opened; `pr/<repo>.md` and `pr/review-notes.md` still land in the store,
    for the operator to post by hand.
    A pull request with a `## Demo` section's media
    (`DemoRenderResult.MediaFiles`) goes through `CreatePRWithMedia`/
    `UpdatePRWithMedia` instead: both add `--attach <file>` once per
    verified file to the same `gh pr create`/`gh pr edit` call, run with the
    evidence directory `renderDemoSection` itself resolved and verified those
    files against (`DemoRenderResult.MediaDir`, never recomputed from the
    branch's own head, which by this point is past the squash) as `gh`'s own
    working directory, after checking that the installed `gh` supports
    `--attach` on the very subcommand about to run (`gh pr create --help` or
    `gh pr edit --help`, cached per subcommand, since the two do not
    necessarily agree) - without support on that subcommand, the pull request
    is opened or updated with no media, and `CreatePRWithMedia`/
    `UpdatePRWithMedia`'s own `attached` return tells publish to say why. Once
    the call returns, publish reads the pull request's body back (`ReadPRBody`,
    `gh pr view <url> --json body`) - a failed read-back is a warning, not
    silence - and checks it for any of the demo's own `./<name>` references
    still sitting in it unrewritten (`checkUnrewrittenReferences`), rather than
    parsing `gh`'s own stderr for the same fact. `gh` rewrites a recognized
    image reference in place but not a video's bare path (`gh`'s own "Videos"
    behavior, DECISIONS.md), so a reference still unrewritten is patched, not
    merely reported: `rewriteUnrewrittenReferences` finds the upload URL `gh`
    appended for that file in the very body just read, moves it to where the
    reference stands, and publish edits the pull request again with the fix
    (`UpdatePR`); only a file `gh` appended no URL for at all is left as it was
    and named on stderr. With no host there is no pull request to attach media
    to or read back, so none of this runs.
    Once that pull request exists, its first comment is `pr/review-notes.md`,
    posted through `CommentPR` (the GitHub host's `gh pr comment`). A post that
    fails is a warning, never a publish failure: the pull request stands, the
    file stays for a manual post, and nothing about the failure keeps the
    store's deferred push from carrying what publish already wrote. The `pr`
    journal line records which (`updated`, `opened`, or `none:no-host`) and the
    pushed head. `publish-done` follows, and the store is pushed.

The report names the head pushed and, per repo, whether the branch was squashed
or was "not squashed (branch already on origin)".

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
granted by path-scoped permission rules for the lease worktree, the
dispatch's `result.json`, and - for a gate demo, and only then - the one
media directory it names, nothing else in the store. Web access, subagents,
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

Two tests in package `verifydeliver` (`identity_test.go`) prove where
`Publish`'s own identity resolution looks: a mapped clone's distinct
identity, or nowhere at all. Both need an environment without the identity
`gittest.PinIdentity()` pins process-wide for every other verifydeliver
test's commits, which `Deps.GitEnv` provides without touching the process
environment itself - nil by default (inherit it, as production always
does), set by these two tests to the ambient environment with that pin
stripped out, so `Publish`'s own "git var" calls see the mapped clone's
config, or its absence, instead.
