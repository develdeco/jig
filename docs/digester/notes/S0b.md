# S0b: the internal half of the scaffold

Branch `dg/S0b`, from the `digester` tip 74c3a93. No verbs, no `cmd/jig`, no e2e.
Everything here is a seam for later tickets; nothing changes what an existing
command does.

## What landed

| Package | Added |
|---|---|
| `internal/spawn` | `Spec`, `Detached` (new package, build-tagged `spawn_unix.go` / `spawn_windows.go`) |
| `internal/journal` | the new `Line` fields and `Spend`; `events.go` constants; the default `TS` is `RFC3339Nano`; `Append` takes the checkpoint lock |
| `internal/home` | `digester.go`: `LedgerPath`, `HooksDir`, `LogsDir`, `VerifyWorktreeDir`, `VerifyLogPath` |
| `internal/ledger` | `Append`, `Read`, `ReadFrom` (new package) |
| `internal/locate` | `Where`, `Opts`, `ErrNotImplemented`, stub `Locate` (new package) |
| `internal/project` | `marker.go` (`Marker`, `FindMarker`, `LoadMarker`, `WriteMarker`, kinds); `MachineProject.Root`; locked and atomic `SaveMachine`; `UpdateMachine` |
| `internal/store` | `LockCheckpoint` (taken by `Sync`, `Push`, `Claim`); `approval.go`; `branchbind.go`; `TryLock` |

## Decisions and details a later ticket needs

**Spawn.** `Spec.Args` are the arguments after the program, not argv[0].
`Spec.Env` nil gives the child the caller's environment; non-nil is the child's
whole environment (the `exec.Cmd` rule), so a caller that wants "mine plus
these" passes `append(os.Environ(), "K=V")`. An empty `LogPath` discards output.
The `TestMain` child-mode intercept uses `JIG_SPAWN_CHILD`, passed through
`Spec.Env`. Windows tries `DETACHED_PROCESS|CREATE_NEW_PROCESS_GROUP|CREATE_BREAKAWAY_FROM_JOB`
with `HideWindow`, and retries once without breakaway on `ERROR_ACCESS_DENIED`
(a job that forbids breakaway): the child then dies with that job, the
documented limit.

**Journal.** New `Line` fields, all `omitempty`: `session`, `agent`, `harness`,
`source`, `signature`, `summary`, `provenance`, `unattended`, `branch`, `url`,
`ref`, `tool`, `cwd`, `exit` (a pointer: `0` is written, none is not), `files`,
`pid`, `spend`. `Spend` is `{input, output, cache_read, cache_write, usd}`, each
omitempty. `Append` default `ts` is `time.RFC3339Nano`, which trims trailing
zeros: two stamps of one journal can differ in length, so a reader orders by
parsing, never by comparing strings. Event constants are `Event<CamelCase>` of
the kebab-case value (`EventPr`, `EventPrBody`, `EventVerifyPending`);
`events.go` also names every literal the existing call sites write, with no
call-site rewrite. `graph` is both a frontier journal event and a ledger event,
one constant. `TestEventConstants` enforces the naming and uniqueness.

**Ledger.** One file, `home.LedgerPath(root)`. Lines are `journal.Line`.
`Append` takes `store.TryLock(path, 10s)` and refuses with `LEDGER_LOCKED` when
it is not held; it writes the line with one `O_APPEND` write, preceded by a
newline when the file ends mid-line (a torn write costs only itself). `Read`
skips blank and malformed lines and returns a final unterminated line if it
parses. `ReadFrom(root, off)` returns the lines after byte `off` and the offset
past the last newline-terminated line it consumed, so a follower never sees a
half-written line; it reopens by path each call; `off` past the end restarts
from 0 (the file was replaced); an absent ledger is `(nil, 0, nil)`.

**Home paths.** `VerifyWorktreeDir` is `<root>/verify/<storeID>/<ticket>/<sha12>-<slice>`
and `VerifyLogPath` is `<root>/logs/verify/<storeID>/<ticket>/<sha12>-<slice>.log`,
`sha12` being the first twelve characters (a shorter sha is used whole). Every
part must be one plain directory name (the evidence tree rule).

**Marker.** `FindMarker` walks up from a directory (or a file directory),
nearest wins, and skips any directory named `.claude` so the per-repo manifest
`<repo>/.claude/jig.yaml` is never read as a marker. Decoding ignores unknown
keys; a file that is not YAML is an error from both `FindMarker` and
`LoadMarker`. `WriteMarker` replaces the file whole and takes **no lock**: the
marker sits in a person repo and a `.lock` sidecar would show up as an
untracked file. Kind values are `project`, `orchestrator`, `member`. Every
marker key is `omitempty`; `Jig` is a string (a bare `jig: 1` reads as `"1"`).

**Machine map.** `SaveMachine` and `UpdateMachine` hold `store.Lock(MachinePath, 10s)`
and write with `AtomicWrite`; `UpdateMachine` reads, calls `fn`, writes, all
under one hold, and writes nothing if `fn` errors. An empty `Root` is not
written, so an old `projects.yaml` round-trips unchanged.

**Checkpoint lock.** `Store.LockCheckpoint()` locks `<git common dir>/jig-checkpoint`
(sidecar `jig-checkpoint.lock`), 2 minutes, on timeout proceeds and reports
through `Store.Warn` (stderr `warning: ...` when unset). The common dir is
`<Root>/.git` when that is a directory (a stat), else `gitx.CommonDir` once per
Root (linked worktrees); a Root with no `.git` has an in-process hold only (the
journal tests use such roots). Re-entrant per clone within the process, keyed by
the canonical lock path: a mutex plus a depth counter. Consequence: goroutines
of one process share a hold (they always shared the store); the lock orders
processes. The returned release is idempotent. `Sync`, `Push` and `Claim` take
it before `begin()`; `Push` releases before `RunCheckpointHook` on both exit
paths so the mirror `Claim` takes a fresh hold and a slow hook does not keep
other processes out; `journal.Append` takes it outside its own file lock (order:
checkpoint, then file locks). `lockCheckpoint(timeout)` exists for tests.

**Approval.** `tickets/<id>/approval.yaml`, keys `by`, `email`, `at`,
`brief_sha256`, `provenance`. `BriefSHA256` is the hex sha256 of `brief.md`
exactly as on disk (no normalization) and errors when there is no brief.

**Branch binding.** `BranchLeadID` strips one `jig/`, takes the leading `KEY-N`
when it is the whole name or followed by `-` (`JIG-1x`, `feature/JIG-1`,
`JIG-0-x` lead with nothing). `TicketForBranch` resolves the lead with the alias
scan: `how` is `id` (a ticket own id), `alias`, or `""` with a nil error for a
branch that leads with no id or an id no ticket claims; a collision is the
`TICKET_ALIAS_COLLISION` that `ResolveTicket` returns. D18 extends both.

## Deviations from the brief

- **`store.TryLock`** (exported, new): `Lock` without the "proceeding without
  lock" stderr line. `Lock` is now `TryLock` plus that line, so its behavior is
  unchanged. The ledger refuses on contention, and a warning that says it is
  proceeding unlocked before a refusal would lie; the checkpoint lock words its
  own warning. The brief said the ledger uses `store.Lock`.
- **`internal/locate` has its package comment in `locate.go`**, not a `doc.go`.
  S0a must not add `internal/locate/doc.go` (only the ARCHITECTURE row).
- Extra exported names in `locate`: the `How*` constants (`HowFlag`, `HowMarker`,
  `HowMapPath`, `HowMapRemote`, `HowStore`, `HowNone`) for `Where.How`.
- The ledger concurrency test uses minimal lines: with 400-byte lines the
  2x200 run took 9 s on this Windows host (Defender scanning the growing file);
  with short lines it takes 2.4 s. A separate test covers a 300 KB line.

## ARCHITECTURE.md row text (for S0a / D1)

New rows:

| Dir | Exports | Responsibility |
|---|---|---|
| `internal/spawn/` | `Spec`, `Detached` | a program, its arguments, directory, environment and log path → a detached process (new session on Unix; detached, new process group, job breakaway on Windows) whose pid is returned and which outlives the caller |
| `internal/ledger/` | `Append`, `Read`, `ReadFrom` | the jig home root and a `journal.Line` → one `O_APPEND` line in `ledger/ledger.ndjson` under the ledger lock (`LEDGER_LOCKED` on contention); the file → its lines, skipping malformed ones, or the lines after a byte offset and the next offset |
| `internal/locate/` | `Where`, `Opts`, `Locate`, `ErrNotImplemented` | a jig home root and a directory → where it is (project, store, repo, branch, ticket, slice, session, and how each was found); `Locate` is a stub until D3 |

Additions to existing rows:

- `internal/home/`: `LedgerPath`, `HooksDir`, `LogsDir`, `VerifyWorktreeDir`, `VerifyLogPath`.
- `internal/journal/`: the digester `Line` fields and `Spend`, the `Event*` constants; the default `ts` is nanosecond RFC3339; `Append` takes the store checkpoint lock.
- `internal/project/`: `FindMarker`, `LoadMarker`, `WriteMarker`, `Marker` and its parts, `UpdateMachine`; `MachineProject.Root`; `SaveMachine` is locked and atomic.
- `internal/store/`: `Store.LockCheckpoint`, `TryLock`, `ReadApproval`, `WriteApproval`, `BriefSHA256`, `TicketForBranch`, `BranchLeadID`; `Sync`, `Push` and `Claim` hold the checkpoint lock.

## Tests

- `internal/spawn`: `TestDetachedOutlivesParent`, `TestDetachedAppendsToTheLog`, `TestDetachedRefusesWhatCannotStart`.
- `internal/ledger`: `TestAppendConcurrentProcesses` (re-exec, 2x200), `TestAppendInProcessGoroutines`, `TestAppendStampsNanosecondTime`, `TestReadSkipsMalformedLines`, `TestAppendAfterATornLine`, `TestReadFromFollowsByOffset`, `TestAppendAndReadALongLine`.
- `internal/project`: `TestFindMarkerWalksUp`, `TestFindMarkerIgnoresTheManifest`, `TestMarkerRoundTripAndLenientRead`, `TestUpdateMachineConcurrent`, `TestUpdateMachineErrorWritesNothing`, `TestUpdateMachineCreatesAndRefusesNoHome`, `TestSaveMachineIsAtomicAndKeepsRoot`.
- `internal/store`: `TestLockCheckpointReentrantAndExclusive`, `TestLockCheckpointOnAStoreWithNoGit`, `TestSyncPushAndClaimWaitForTheCheckpointLock`, `TestPushLetsGoOfTheCheckpointLockBeforeTheHook`, `TestClaimHoldsTheCheckpointLockWhileItWrites`, `TestTicketForBranch`, `TestTicketForBranchRefusesAnAmbiguousLead`, `TestBranchLeadID`, `TestApprovalRoundTrip`, `TestBriefSHA256NeedsABrief`, `TestTryLockReportsContentionWithoutProceeding`.
- `internal/journal`: `TestDigesterFieldsRoundTripUnderTheirWireKeys`, `TestAppendDefaultsToNanosecondTime`, `TestAppendWaitsForTheCheckpointLock`, `TestEventConstants`.
- `internal/home`: `TestDigesterPathsDeriveFromTheRootGiven`, `TestVerifyPaths`, `TestVerifyPathsRefuseWhatClimbsOut`.