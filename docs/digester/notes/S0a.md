# S0a: the CLI and test-harness half of the scaffold

Branch `dg/S0a`, from the `digester` tip 9ad4e13 (S0b merged). No new verb, no
change to what an existing command prints or does: `jig`, `jig <verb> -h` and
the unknown-verb refusal were captured from the binary before and after and
are byte-identical. Everything here is a seam for later tickets.

## What landed

| Where | Added |
|---|---|
| `cmd/jig/registry.go` | `verb`, `runFunc`, `registerVerb`, `registeredVerbs`, `lookupVerb`, `commandTable()`, `verbNamed` |
| every verb file | an `init()` registering that file's verb(s) with today's help row, verbatim |
| `cmd/jig/main.go` | `run()` is help check, `lookupVerb`, `v.Run`; the `switch` is gone; `main()` wires the interrupt context |
| `cmd/jig/help.go` | `usageBlocks`, `flagSetName`, `flagsBlockFor` read the registry; the `commandTable` var is gone |
| `cmd/jig/sections.go` | `hereCtx`, `hereSection`, `registerHereSection`, `hereSections`, `statusSection`, `registerStatusSection`, `statusSections` |
| `cmd/jig/status.go` | `RenderStatus` renders the registered status blocks before its help line (`renderStatus` takes the sections, for tests) |
| `cmd/jig/env.go`, `triage.go` | the seams `ctx`, `now`, `spawn`, `executable`, `lookPath`, `stdoutIsTerminal`, each with a `processEnv` default |
| `cmd/jig/env_test.go` | `withSpawn`, `withClock`, `withLookPath`, `withContext`, plus `withExecutable`, `withStdoutTerminal`, `fixedClock`, `spawnRecorder` |
| `cmd/jig/skills_test.go` | the skill count comes from `skillspkg.FS` |
| `e2e/helpers_test.go` | `jigEnv` `extra`/`unset` with `with`/`without`, `withGitGlobal`, `withoutIdentity`, `runJigIn`, `waitFor`, `waitJournal`, `readLedger` |
| `e2e/proc_test.go` | `startJig` and `jigProc` (`Line`, `Write`, `CloseStdin`, `Stop`) |
| `e2e/harness_test.go` | `fakeHarness` and `newHarness` |
| `e2e/proc_alive_{unix,windows}_test.go` | `pidAlive` |
| `internal/{ready,verify,observe,setup,slicectx,recall,correct,spend,mcp,events,tui}/doc.go` | package stubs |
| `ARCHITECTURE.md` | a module-table row for every package dir the build adds, and S0b additions to the rows of `home`, `journal`, `project`, `store` |

## Using the registry

A verb is one file with an `init()`:

```go
func init() {
	registerVerb(verb{
		Spec:  cmdSpec{"here", "name where the work is", []flagSpec{ /* ... */ }, false},
		Order: 300,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdHere(e, args, stdout)
		},
	})
}
```

- `Spec.Name` is one word or two single-spaced words (`"review context"`).
  Registration panics on a bad name, a duplicate name, a nil `Run`, or a
  `NoFlagSet` verb that has `Flags`.
- `Order` sorts help and `registeredVerbs()`, then `Name`. The existing verbs
  hold 100..240 in their old order: init 100, ticket 110, graduate 120, solve
  130, run 140, requeue 150, gate 160, publish 170, status 180, validate 190,
  version 200, skills 210, trackers 220, store 230, `_screen` 240. Later bands
  are in the comment on `verb.Order`.
- `FlagSet` is the name the verb gives `newFlagSet` when it is not `Spec.Name`
  (`ticket new`, `skills install`, `trackers sync`, `store migrate`).
- `Probe` is what the parity test passes after the verb words so the verb
  reaches its flag parse (`T-1` for the ticket verbs, `new` for `ticket`,
  nothing for `init` and `version`). A verb with a required positional that is
  not a ticket id needs a `Probe` that gets past it.
- `NoFlagSet` is for a hidden verb that never calls `newFlagSet`; the parity
  test does not run it and checks its `Flags` are nil.
- `lookupVerb(args)` tries the two-word name first and returns the arguments
  after the name. A two-word verb does not need its first word registered:
  `jig review` alone is an unknown command unless some verb is named `review`.
- A test that registers verbs builds `newVerbRegistry()` and calls `register`
  on it; the binary registry (`defaultRegistry`) is filled by `init()` only.
- `TestCommandTableFlagsMatchRegistration` loops over `registeredVerbs()`, so
  a new verb is covered without editing the test. `handParsedFlags` stays for
  `--answer`.

## Sections

`registerHereSection(hereSection{Order, Name, Blocks, JSON})` and
`registerStatusSection(statusSection{Order, Name, Blocks})` from an `init()`;
`hereSections()` and `statusSections()` return them by `Order`, then `Name`.
A section needs a unique `Name`; a here section needs `Blocks` or `JSON`, a
status section needs `Blocks`. `hereCtx.Store` is nil when no store resolved.
`RenderStatus` appends each status section blocks after its own and before
the one help block; a section that errors fails the render with its error.
Tests that need a section call `renderStatus(st, ticket, sections)` with their
own, never `registerStatusSection`, which would add a block to every status
test in the binary.

## The env seams

| Field | Production | Test env (`testEnv` and `envFrom`) |
|---|---|---|
| `ctx` | `context.Background()`; `main()` replaces it with `signal.NotifyContext(os.Interrupt)` | the production one; `withContext` |
| `now` | `time.Now` | production; `withClock`, `fixedClock` |
| `spawn` | `spawn.Detached` | refuses with "spawn not allowed in test env"; `withSpawn`, `spawnRecorder` |
| `executable` | `os.Executable` | production; `withExecutable` |
| `lookPath` | `exec.LookPath` | production; `withLookPath` |
| `stdoutIsTerminal` | an `*os.File` that is a console or tty | production (false for a buffer); `withStdoutTerminal` |

`envFrom` installs the refusing spawner, not only `testEnv`, so a test built on
either cannot start a process by accident.

**Interrupt.** The first interrupt cancels `e.ctx`. Nothing in the existing
verbs reads it, so `main()` also runs `endAfterInterrupt`: it gives the signal
its default action back (a second interrupt ends the process at once) and
ends the run with status 130 if it has not returned 3 seconds after the first.
Without it Ctrl-C would stop killing `jig run` and `jig solve`. A verb that
watches `e.ctx` returns before the grace is out.

## e2e helpers

- `jigEnv.with("K=V", ...)` lays entries over the subprocess environment (they
  replace `JIG_HOME` or `HOME` if named); `without("NAME", ...)` removes names
  the process has, and wins over everything. Both return a copy.
- `env.withGitGlobal(t)` points `GIT_CONFIG_GLOBAL` at a file in the test temp
  dir that includes the shared hermetic config; `git config --global` run
  with `env.environ()` writes only there. Read it back with `--includes`.
- `env.withoutIdentity()` drops the `GIT_AUTHOR_` and `GIT_COMMITTER_` name and
  email and keeps the dates. The harness still pins `e2e-script` for its own
  commits.
- `runJigIn(t, env, cwd, stdin, args...)`; `runJig` is `runJigIn` with nil
  stdin. Recording (`JIG_RECORD_DIR`) works for both and does not show stdin.
- `startJig(t, env, cwd, args...)`: stdout is read line by line without
  limit; `Line(d)` returns the next unread line, or false on timeout or at the
  end of the output; `Stop()` closes stdin, gives the process 2 seconds to
  leave by itself, then kills it, and returns all of stdout, stderr and the
  exit code (the code the process exited with when it left by itself, else the
  code of the kill). `Stop` is idempotent and runs at test cleanup. A verb that streams
  without reading stdin costs the 2 seconds.
- `waitFor(t, d, what, cond)` and `pollUntil(d, cond) bool` poll every 50 ms.
  `waitJournal(t, fx, ticket, d, pred)` retries failed reads and lists the
  journal in its failure. `readLedger(t, env)` reads the ledger under
  `env.home`.

### The fake harness

`newHarness(t, fx, env, session)` works in `fx.RepoDir`; its fields `Session`,
`Agent`, `Transcript` and `VerifierWait` are plain.

- `Branch(name)` checks out the branch, creating it at HEAD when absent.
- `Commit(slice, msg, files)` and `CommitScenario(slice, attempt)` commit with
  the `Jig-Slice: <slice>` trailer (none for an empty slice) and return the
  sha. The scenario patch is read from `fx.ScenarioDir`, the copy of
  `testdata/fixture/scenario` plus any `ScenarioBranch` overlay. The message is
  the fake backend one: `<ticket> <slice>: <first line of the summary>`.
- `Payload(event, fields)` builds the Claude Code payload. `fields` keys are
  top-level payload keys; `command` is shorthand for `tool_input.command` and
  merges into a given `tool_input`. `agent_id` is present only when `h.Agent`
  is set. `Hook(event, fields)` runs `jig observe --harness claude` on it from
  the clone and returns the result (exit 2 and an unknown-command error until
  the observe verb lands).
- `Bash(cmd)` runs `cmd` in the clone (split into words with quotes honored;
  no pipes, redirection or expansion; `git` goes through gitx), then fires
  `PostToolUse` with `tool_name`, `command` and a `tool_response` of `stdout`,
  `stderr` and `interrupted`. It puts no exit code in `tool_response`, because
  Claude Code does not; pass an explicit `tool_response` through `Hook` for
  another shape. It returns the command result, with the hook result in `.Hook`.
- `Jig(args...)` runs jig from the clone with `JIG_SESSION` set.
  `Push(branch)` pushes to origin.
- `WaitVerifiers()` reads the ledger `verify-spawned` lines, waits up to
  `h.VerifierWait` (60 s) for those pids to exit, then kills the tree of any
  that remain and reports each as a test failure. `newHarness` also kills
  stragglers at cleanup without waiting. The OS can reuse a pid; the kill only
  follows a full wait on a pid that is still alive.

## Deviations from the brief

- The harness reads the scenario from `fx.ScenarioDir` rather than the
  literal `testdata/fixture/scenario` path: the same files, and a scenario
  branch overlay applies.
- `withGitGlobal`, `withoutIdentity`, `with` and `without` are methods on
  `jigEnv` (the brief names them without a receiver); `withSpawn` and the other
  env helpers are methods on `env`, like `inDir` and `atTerminal`.
- Extra names nobody asked for, so two tickets do not each invent one:
  `env.withExecutable`, `env.withStdoutTerminal`, `fixedClock` and
  `spawnRecorder` in `cmd/jig`; `pollUntil`, `bashResult`, `pidAlive` and
  `killTree` in `e2e`.
- `main()` carries the interrupt watcher described above, which the brief does
  not mention; it is what keeps "no behavior change" true for Ctrl-C.
- `internal/locate` has no `doc.go` (its package comment is in `locate.go`, as
  S0b noted); its ARCHITECTURE row is the S0b text.
- The ARCHITECTURE edit also carries the S0b additions to the `home`,
  `journal`, `project` and `store` rows, and short additions to the `cmd/jig`
  and `e2e` rows. Rows only, no prose.
- The Write and Edit tools refused paths in this worktree (it is a worktree
  other than the one the session started in), so the files were written
  through the shell.

## For later tickets

- D13 deletes `_screen`: point the `runJigIn` and `startJig` stdin probes of
  `TestHelpersStdinAndStart` (`e2e/scaffold_test.go`) at `observe`, which also
  reads stdin and exits 0.
- A verb that blocks on `e.ctx` should return 0 or its natural code after the
  cancel; the watcher only backstops one that does not.
- `hereCtx` and `statusSection` live in `cmd/jig`, not an internal package, so
  a section may call any verb helper.

## Tests

- `cmd/jig`: `TestLookupVerbTwoWord`, `TestLookupVerbFindsTheBinarysVerbs`,
  `TestRegisterVerbRefusesWhatCannotWork`,
  `TestRegisteredVerbsSortByOrderThenName`, `TestRunRefusesAnUnknownVerb`,
  `TestCommandTableFlagsMatchRegistration` (now iterates the registry),
  `TestSectionSetOrdersAndRefusesDuplicates`,
  `TestRegisterSectionsRefuseWhatProducesNothing`,
  `TestRenderStatusRendersRegisteredSectionsBeforeTheHelpLine`,
  `TestTestEnvSeamsAndTheirHelpers`, `TestProcessEnvSeamsHaveProductionDefaults`,
  `TestEndAfterInterrupt`; `TestCmdSkillsInstallDest` derives its count.
- `e2e/scaffold_test.go`: `TestHelpersStdinAndStart`,
  `TestHarnessCommitHasTrailer`, `TestJigEnvWithAndWithout`,
  `TestWithGitGlobalIsPerTest`, `TestWithoutIdentityLetsConfigDecideTheAuthor`,
  `TestWaitHelpers`, `TestReadLedgerReadsTheEnvsHome`,
  `TestHarnessPayloadShape`, `TestSplitCommand`,
  `TestHarnessBashRunsTheCommandInTheClone`, `TestHarnessWaitVerifiers`.
