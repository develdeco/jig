# DECISIONS - jig v0.1 build log

A log of judgment calls made while building v0.1: one entry per decision, covering what
was ambiguous, what was chosen, and why.

## Scope and deferrals

- Structural rounds render as markdown tables in v0.1.
- jira/linear trackers are compile-checked stubs that return a structured
  not-implemented error.
- `jig gate` pr-mode parses its flags and returns not-implemented.
- Design axis/oracles: the facet is captured in brief/slices data but not enforced.
- fleet/retro binary verbs are deferred; their skills drive the shipped verbs instead.
- Cross-repo fixtures, publish, and wire-contract end-to-end coverage are deferred; a
  scheduler unit test covers the concurrency policy instead.
- graphify ships as an interface plus a CLI shell-out with a no-op fallback; no test
  requires it in v0.1.
- Windows is the v0.1 platform; a WSL-native substrate and nix packaging are deferred.
  The env-class up/check/down commands are the one sanctioned shell-string exception
  (`cmd /C` on Windows, `sh -c` under WSL).
- Reconcile conflicts in v0.1 abort publish with a structured error pointing at
  `jig gate`; the conflict-resolution fix-slice loop is deferred, and v0.1's
  acceptance criteria only assert the oracles-only tier. The pushed-branch publish
  flow beyond the reconcile policy is unreachable in v0.1 because squash refusal
  blocks it first - a known, documented limit.
- Publish re-validate treats "affected oracles" as all manifest oracles in v0.1;
  graphify-based scoping is deferred.
- Divergence checking lands as a lite version: reconcile journals the integrated-diff
  file count, and publish refuses an empty integration diff as a stale-overwrite
  signal. The symbol-grep half of the check stays deferred.
- `Store.Sync` and `Store.Push` do not serialize against a concurrent Sync or Push
  from another process on the same store: `refuseIfMidRebaseOrMerge` refuses a rebase
  or merge it finds already in progress, but two processes can still race between that
  check and the pull/push that follows, and `abortFailedPull`'s best-effort
  `rebase --abort` can then abort a rebase the other process is mid-resolving rather
  than one this process itself started. `store.Lock` (`internal/store/lock.go`) exists
  and already serializes single-file writes (journal, tracker, render output), but
  wiring it around Sync/Push's whole pull/push sequence is its own change, deferred by
  the owner rather than folded into this one.

## Safety

- The secret-read screen uses a pattern list (`.env*`, `*_key*`, `id_rsa*`, `*.pem`,
  `~/.aws/**`, `~/.config/gh/**`) chosen to cover common credential locations broadly,
  rather than a narrower set tied to one project's layout. The pattern rule needed no
  glob engine: literal substring/prefix checks already catch every token, verified by
  test cases.
- The `--yes` flag on publish is treated as the actual publish confirmation: the fix
  threads the real confirm state through to the push guard instead of leaving it
  hardcoded true.

## Store

- `from_brief` hashes are computed as the sha256 of a section's body (the heading line
  excluded), newline-normalized, with trailing whitespace trimmed on each line, so
  editing one `## ` section changes only that section's hash.
- The standalone store's default `ticket_format` is `T-{n}`; the tracker is local by
  default. No default was specified beforehand, so this was chosen as the simplest
  workable one.
- The journal event vocabulary is fixed as: dispatch, result, question, answer,
  requeue, stall, env-*, gate-*, fix-slice, reconcile, revalidate, memorize,
  changelog, squash, pr, route, publish-done. The graduated-revalidate reason is
  encoded in the outcome field to keep the journal line schema exact.
- Store commits happen even without a configured remote, because the commit history is
  useful on its own; only push/pull are skipped silently. "Skips sync silently" was
  read as skipping network sync specifically, not local commits.
- Slice work files (`slice.json`, `result.json`) live under the store's ticket
  directory (`work/`), not the build lease, so that the lease's `git add -A` never
  sweeps dispatch plumbing into slice commits.
- `Store.Sync` stages and commits any uncommitted leftovers with jig's
  identity before it pulls: a gate-open journal line from a gate that then
  failed at an oracle (no round directory is ever written in that case -
  the oracle suite runs before the round is computed), a partial gate round
  directory from a SLICE_ID_DUPLICATE failure after the round itself was
  written, or in-progress `work/` files from a run interrupted mid-dispatch.
  Without this, the next command's `pull --rebase` fails on
  the dirty tree with "cannot pull with rebase: You have unstaged changes.",
  wedging the store until someone commits by hand. This does not retry the
  failed command's round: the leftovers become an ordinary jig commit and
  the next command proceeds, but a partial gate round directory still counts
  as a round, so the next `jig gate` opens round N+1 rather than replaying
  the failed one. `Push` shares the same stage-and-commit step.
- `Sync` and `Push` both refuse with `STORE_CONFLICT`, without touching the
  index, when the store already has an unfinished rebase or merge in progress
  (`rebase-merge`, `rebase-apply`, `MERGE_HEAD`, `CHERRY_PICK_HEAD`,
  `REVERT_HEAD`, `sequencer` or `BISECT_LOG`, all read with one
  `git rev-parse --git-path` call), when HEAD is detached (a bisect, say, so
  a commit would land where `git bisect reset` drops it), or when the index
  has unmerged entries, which is what a conflicted `git stash pop` leaves
  behind on its own. An unconditional `git add -A`
  would stage unresolved conflict markers as ordinary content, and a later
  commit (or `rebase --continue`) would finalize them onto the store branch,
  corrupting whatever file conflicted for every later reader. The check
  lives in the shared `stageAndCommit` step, so it also guards a command that
  only ever `Push`es, such as `jig requeue`, not only the ones that `Sync` first.
- A failed `pull --rebase` is aborted and wrapped as `STORE_CONFLICT` only when
  it actually left a rebase in progress; jig's own conflicts never leave the
  store mid-rebase for the guard above to catch on the next command, unless
  the best-effort `rebase --abort` itself fails, in which case the message
  says so plainly (naming the abort's own error) instead of falsely claiming
  the rebase was aborted. It then points the operator at the store to
  resolve directly there: as still mid-rebase, when the rebase state was
  read successfully first; as an unknown state to check with `git status`,
  when that read itself had failed - a failed read never lets the message
  assert mid-rebase, since that was never confirmed. A pull that failed
  before rebasing (an unreachable or moved remote, an auth failure) left
  nothing to abort, so its error is returned unchanged rather than
  misreported as a conflict. When the rebase state itself can't be read,
  the abort is still attempted, best effort, rather than trusting a read
  that just failed.

## Build loop

- Result-text parsing requires exactly one fenced json block; zero or multiple blocks
  both parse as a failed result rather than falling back to a last-block-wins
  heuristic. This stricter rule is recorded in the outcome tests.
- The stall signature strips digits and path segments before comparison, so the
  same failure at a different line number or path still counts as a repeat.
- The staircase invariant regex is applied case-insensitively; v0.1 left this choice
  open and case-insensitive was picked.
- Attempt-cap exhaustion has two candidate behaviors: setting a `stalled` state, or
  failing and surfacing the run. Both were kept: on-disk state uses the `stalled`
  vocabulary with reason `attempt-cap`, while the run report surfaces it as a failure.
  This satisfies both readings at once.
- A slice's `oracle` field resolves through the manifest's oracle names (with `{path}`
  substituted); an unknown name is treated as a literal command. This was left open
  and chosen to keep both the fixture and real repos working.
- Requeue keeps attempt counts, so a brief amendment does not erase attempt history;
  the fake backend's attempt numbering advances past the flawed-brief attempt rather
  than resetting.
- The invariant-floor regex keeps its verbatim v0.1 form (no word boundaries, literal
  single spaces, a reduced alternative set) rather than a richer regex considered
  during review, because that verbatim v0.1 form was intentional, not an oversight.
- The headless backend's exactly-one-fenced-block text parse runs over the session's
  final message only (the CLI result object's `result`), so a fence in tool output
  elsewhere in the transcript can no longer count as the result block.
- Staircase signals measure the lease's whole diff against its start point, not
  just the latest attempt's changes.
- graphify's `Plane.Affected` derives `--graph <repo>/graphify-out/graph.json --depth
  2` itself, since the interface carries no graph/depth parameters and these are
  reasonable defaults.

## Headless backend

- The permission model itself is ADR 0008. Found on Claude Code CLI 2.1.232: print mode
  rejected `--output-format stream-json` without `--verbose`, so every headless dispatch
  failed before a session ran; and even with it, print mode denied the edits, the commit,
  and the result.json write outside the lease that the disk contract needs.
- `--output-format json`, not `stream-json --verbose`: jig reads only the final result
  object (the session's final message, `is_error`, the denied tool calls), so the event
  stream bought nothing but a whole transcript buffered in memory. Claude Code keeps the
  transcript in its own session store anyway.
- A CLI that ran no session (a rejected flag), or whose session ended in error (expired
  credentials, an API failure), is an infrastructure error carrying the CLI's own message.
  It used to become a synthesized "no result block" result, which hid the cause. A
  completed session that wrote no result.json still gets one from its final message, and
  when that parse fails the summary names the denied tool calls.
- The tool surface leaves out Skill, subagents, and web access even though some would be
  harmless. The session's instructions are its prompt, the dispatch inputs, and the
  repo's CLAUDE.md; skills and subagents would pull in unrelated user-level skills and
  models the staircase never chose, and fetched pages are an injection path.
- The operator's own Claude Code settings still load (project and local sources are
  dropped, the user source is kept: `--setting-sources user`). Dropping user settings too
  would also drop their deny rules and hooks, widening the session as often as narrowing
  it; the `--permission-mode` flag already beats any `defaultMode` there, checked against
  a user-level `bypassPermissions`.
- Granting through the screen is not fail-closed on its own, so jig proves its own
  binary's screen before every screened dispatch: one `jig _screen` run directly, not
  through the hook wiring Claude Code itself launches, with a push, which must come back
  denied. Claude Code skips a hook it cannot launch and its own read-only classifier
  still allows `echo`, `ls`, `git show` and the like, so a missing, failing, silent or
  wrong-answering hook would otherwise leave a session reading the machine with nothing
  saying the screen was gone. The probe binds the start of a session, not its whole life.
- The credential screen judges both how a path is spelled and where a symlink lands: a
  symlink in the lease pointing at `~/.aws` and a search root that is a credential
  directory rather than a file are the same read by another name. A credential directory
  counts as much as a file in it, since a tool given a root reads everything under it.
  What this is not is confinement: a content search over an ordinary directory holding a
  `.env` still returns it, and only a sandbox would change that.
- The lease's `CLAUDE.md` is passed with `--append-system-prompt-file`, because dropping
  the project setting source drops that file too (checked against the installed CLI: the
  marker appears without the flag and disappears with it). Capability and instructions
  part company here - a settings file says what a session may do and must not come from
  the code under review, while `CLAUDE.md` says how the repo works, which is the repo's
  to say. Imports inside it are not resolved.
- The session's bound kills the process tree and sets `WaitDelay`, because killing the
  CLI alone left `Wait` blocked on pipes a surviving grandchild still held: the bound
  did not bound the call. A child the CLI leaves behind after exiting normally still
  outlives it; what jig guarantees is that it stops waiting, not that it kills that
  child too - that is not jig's to kill on any OS.
- A session that wrote its result before the bound is honored, since the disk contract
  is what decides an attempt, not how the process ended.
- A `JIG_HEADLESS_TIMEOUT` that does not parse is refused rather than ignored: an
  operator who set a bound and silently got the default would find out by waiting.
- The screen takes a tool's file-selecting arguments from the tool, not from one shared
  key list: `Grep` filters with `glob` and `Glob` selects with `pattern`, and a
  credential named in either came back in full while a `Read` of the same path was
  denied. A tool the screen has no entry for is denied rather than guessed at, which is
  also what a tool a future CLI adds should get until it is considered.
- `--setting-sources user`: project and local settings live in the lease, which is the
  code under review. A `.claude/settings.json` on the ticket branch ran its own
  PreToolUse hook on this machine, and a `.claude/settings.local.json` granted writes
  outside the lease. The operator's own user settings still load, for the reason above.
- `PowerShell` left the granted surface: the CLI this backend drives has no such tool, so
  naming it in `--tools` and in `screen.Granted` described a grant that never existed.
- A session runs under `JIG_HEADLESS_TIMEOUT` (90 minutes by default). The bound is for a
  session or hook that has stopped making progress at all; a real slice can legitimately
  take a long time, so it is deliberately generous rather than tuned.
- The live contract test asserts the structured `is_error` flag for a refusal the CLI
  words itself, and matches text only where the text is jig's own (the screen's reason).
- The gate reviewer's dispatch (Slice "gate") gets the same grants as a build, worktree
  edits included: main's read-only guard already rejects a round that moved HEAD or
  changed a tracked file. A read-only dispatch flag would turn such an edit into a
  denial the reviewer can work around instead of a failed round; it was left out of this
  change, which does not touch the reviewer's own code.
- The screen hook runs this process's own executable only when build info says it is the
  jig binary. Inside `go test` the executable is the test binary, which `_screen` would
  rerun tests in on every tool call, so a test or another program running screened
  dispatches must pass `Options.ScreenBinary`. The check runs per screened dispatch,
  not in `New`: an unscreened dispatch runs no hook and needs no jig binary.
- Rule paths take the POSIX drive form Claude Code matches Windows paths in
  (`C:\a` is `//c/a`), with gitignore characters escaped, plus the symlink-resolved form
  when it differs. Checked against the CLI: native backslash paths, lowercased paths, and
  a directory named `w [1] (x) y` all match, and a look-alike sibling does not.
- The CLI contract test is opt-in (`JIG_LIVE_CLAUDE=1`), not part of `go test ./...`: it
  runs whichever CLI version is installed, so its result is not reproducible run to run,
  and CI has no `claude` binary.
- Direction taken after three adversarial review rounds each patched around the same
  shape of hole (a glob, then a junction, then a parent search root): the `headless`
  backend is stated as not a security boundary, and no further denylist patch is made
  for that class. The screen stays for what it is good at - an accident guard, a push
  blocker, a plainly spelled credential deny - and real confinement is separate future
  work, not a bigger denylist. See ADR 0008.
- The lease's `CLAUDE.md` moved from a working-tree read to `git ls-tree`/`cat-file`
  through `internal/gitx`, so a symlink or hard link a screened session leaves in the
  lease can no longer carry a file from outside it into the next dispatch's system
  prompt; a blob over 64 KiB refuses the dispatch instead of forwarding it uncapped.
- The secret screen's resolution step (`secretTarget`) never follows a network share or
  a device path (`\\host\share\...`, `\\?\...`): `filepath.EvalSymlinks` on an unroutable
  UNC address was blocking the whole screen for tens of seconds per fresh address. Such
  a token is judged by spelling alone, which the screen already checks first.
- `ToolCall` denies a call it cannot read outright - a missing or non-string Bash
  `command`, a required path argument that is absent, or any path argument shaped as
  something `SecretPath` can't compare (a number, an object, null, a list holding a
  non-string) - instead of the previous fail-open default on an unreadable argument.
- The denylist grew nine exact credential files that sit beside ordinary config in the
  same directory (`.git-credentials`, `.claude/.credentials.json`, `.claude.json`,
  `.config/git/credentials`, `.azure/msal_token_cache.json`,
  `.config/gcloud/credentials.db`, `.gem/credentials`, `.pypirc`,
  `.terraform.d/credentials.tfrc.json`), matched by exact trailing path segments so a
  lease's own `.claude/settings.json` and skills stay readable.
- `killTree` on Windows now runs `%SystemRoot%\System32\taskkill.exe` by absolute path
  under its own 5s deadline, falling back to `Process.Kill` on failure or timeout,
  instead of a bare `"taskkill"` resolved through PATH - which a session's own commands
  can shadow - with no deadline of its own.
- `SecretTarget` was exported with no caller outside `internal/screen`; unexported to
  `secretTarget`, with a test that parses the package's own source and pins its
  exported surface against ARCHITECTURE.md's row, so the two cannot drift apart silently
  again.
- `SESSION_TIMEOUT`'s message now names both halves of the ceiling jig actually
  enforces - the bound and the drain `WaitDelay` can still spend - instead of only the
  shorter number.
- `JIG_HEADLESS_TIMEOUT` is now parsed before the screen probe runs, so a bad bound
  fails as `BAD_TIMEOUT` immediately instead of first paying for a screen check that was
  never going to matter.

## Pool leases

- Lease naming moved into the pool: callers pass a ticket and a role (`Build`,
  `Gate`, `Publish`), and `pool.Dir` derives `<ticket>`, `<ticket>-gate` or
  `<ticket>-publish`. Ticket ids were never validated, so ticket `X-gate`'s build
  lease was ticket X's gate lease, which the gate resets and cleans, and
  `X-publish` collided with X's publish lease the same way. `pool.CheckTicket`
  now reserves both suffixes, ignoring case and trailing dots and spaces (a
  case-insensitive filesystem, the Windows and macOS default, resolves `X-GATE`
  to X's gate lease, and Windows drops trailing dots and spaces). Reserving the
  suffixes won over moving the gate lease to a separator ticket ids cannot
  contain: no character is absent from ids that are never validated, so that
  option needs the same validation, and it would also orphan every existing
  gate and publish clone. The same check requires a single path component
  without a leading dot, since the id names a directory in both the store and
  the pool: `../x` escapes both, `.` and `..` name the repo or pool directory
  itself, and `.git` is the store's own git directory.
- `pool.CheckTicket` runs at every entry point: the local tracker's mint,
  before it writes anything, so a `ticket_format` that yields an unusable id
  leaves nothing behind (`TestLocalMintRefusesUnusableID`); `jig ticket new`
  after any other tracker mints, since that id is known only once the tracker
  has created the ticket, so the refusal names the ticket to close there
  (`TestTicketNewRefusesReservedIDFromCommandTracker`); `jig validate`
  (reported as the only problem, since every other check reads paths derived
  from the id); every ticket command through `requireTicket`/`requireSlices`;
  and `pool.Dir`, so no caller can reach a lease path with a bad id.
  `TestCheckTicket`, `TestAcquireRefusesReservedTicket`,
  `TestTicketNewRefusesReservedID` and the end-to-end
  `TestReservedLeaseSuffixTicketRefused` pin it. Ticket ids that
  differ only in case still share one store folder, and so one set of leases,
  on a case-insensitive filesystem; that predates this and is unchanged.
- `pool.Acquire` reuses a lease only when git opens it as its own repository:
  `.git` is a directory, and `git rev-parse --is-inside-work-tree
  --show-prefix` prints exactly `true` (inside a working tree, at its top). It
  used to trust any `.git` entry, so with `JIG_HOME` inside another working
  copy (the default `~/.config/jig` inside a dotfiles checkout) a `.git` git
  cannot open - a lease deleted by hand and stopped by a locked pack file, a
  clone killed mid-write - sent its `fetch` and `checkout -B jig/<ticket>` to
  the enclosing repository. A `.git` file is refused as well: it can name any
  repository's git dir, and one naming the enclosing repository moved that
  repository's `HEAD` the same way. The prefix test compares no paths, so no
  spelling of the lease path (8.3 names, forward slashes, a POSIX-style git)
  can make a healthy lease look broken. The gate's pre-Acquire restore
  (`internal/verifydeliver/gate.go`) uses the same check through
  `pool.Usable`, which adds `HEAD^{commit}` for its `reset --hard HEAD`,
  instead of keeping its own private copy of it.
  `TestAcquireRecoversBrokenLease`, `TestUsable` and the end-to-end
  `TestRunRecoversBrokenLeaseInsideEnclosingRepo` pin it.
- A lease is moved aside only when git shows it is not a repository of its
  own: `.git` is not a directory, git resolved an enclosing working copy (a
  non-empty prefix) or bare repository (`false`), or git found no repository
  and `.git` lacks `HEAD`, `objects/` or `refs/`, which git requires of one. A
  build lease holds committed but unpushed slice work, and moving it aside
  lets the run continue on a fresh clone without that work while the store
  still calls those slices green, so anything git can still open, however
  oddly, is left alone. When git fails on a `.git` that has all three (an
  extension this git does not know, a corrupt config, git itself missing),
  `Acquire` stops with git's error and leaves the lease alone
  (`TestAcquireRefusesLeaseGitCannotOpen`). The probe runs with `-c
  safe.directory=*`, so a healthy lease git refuses as another user's (a
  `JIG_HOME` on exFAT or a network share, or one left behind by `sudo`) is
  kept and its fetch fails with git's own explanation
  (`TestAcquireKeepsLeaseGitRefusesByOwner`); ownership stays git's own check
  on every real command. An unborn `HEAD` does not count against a lease
  either: the checkout in `Acquire` repairs it, and an orphan checkout can
  leave one in front of a ticket branch that still holds work
  (`TestAcquireReusesLeaseWithUnbornHEAD`).
- What is moved aside is renamed to a timestamped sibling,
  `<key>.broken-<UTC time>`, and cloned afresh, with one stderr line naming
  both paths; a missing lease or an empty directory is simply cloned into.
  The pool still deletes nothing: what a hand deletion left behind may be
  worth inspecting, and a rename never reaches outside the lease's own repo
  directory. The rename stops `Acquire` with an error, touching neither
  directory, when the aside name is already taken or a process still holds a
  file inside it (Windows) (`TestAcquireNeverOverwritesAnAside`).
  `GIT_CEILING_DIRECTORIES` was considered instead and not used: it stops the
  upward walk but not a `.git` file naming another repository, and it
  protects only the git calls it is threaded through, while every git call in
  a lease runs after one check.
- `pool.Dir` returns an absolute lease path. `Acquire` runs the clone from the
  lease's parent directory, so a relative `JIG_HOME` used to resolve the lease
  path twice and fail every Acquire (`TestAcquireRelativeJIGHome`).
- `ownRepo`'s prefix check trusted any probe that did not fail outright. A
  `.git` with everything a repository needs but a HEAD git refuses to read
  (a crash can truncate it) makes the probe still succeed: git walks past
  the broken `.git` and answers for an enclosing repository instead, prefix
  and all, which read as "not a repository of its own" and moved committed,
  unpushed slice work aside while the run reported it green. The check now
  requires the probe to answer exactly `true` with an empty prefix; anything
  else - a failure, a non-empty prefix, a bare repository's `false` - falls
  to the same HEAD/objects/refs shape check a genuine git failure already
  took, so a lease git merely disagrees with, rather than refuses outright,
  still stops `Acquire` with git's error instead of being discarded
  (`TestAcquireRefusesCorruptHEADLeaseInsideEnclosingRepo`, e2e
  `TestRunRefusesCorruptHEADBuildLeaseInsideEnclosingRepo`). Separately,
  `prepare` Lstat'd the lease path: a symlink or Windows junction to a
  healthy lease Lstats as its own mode, never a directory, so it went
  straight to the move-aside branch without ever asking git. It now Stats
  the path first, resolving a link the way git itself would, so `ownRepo`
  decides (`TestAcquireReusesSymlinkedLease`).
- The ticket-id and slice-id "gate" reservations are reported as two
  independent problems: `validateTicket`'s doc comment states once that
  both are reserved and points to `pool.CheckTicket` for the ticket
  suffixes, and each problem message names only its own id and why it is
  reserved, so the slice-id message no longer repeats `pool.Role`'s suffix
  literals where they could drift from it.

## Gate and publish

- Gate writes a machine-readable report (`gate/round-N/report.yaml`) with verdict,
  target sha, and model, making concrete the requirement that a gate report record the
  target sha it ran against.
- Fix slices append to the ticket's `slices.yaml` with a `from_gate: N` marker, keeping
  a single slices map as the one file that represents "the frontier."
- The squash base is computed as `merge-base(origin/<target>, HEAD)` in the publish
  lease at squash time. This equals the recorded start sha while the target is
  unmoved; after a reconcile rebase, the fork point has moved and merge-base reflects
  the moved start correctly. The pushed-range refusal check runs on that same range.
- Gate and publish leases fetch `jig/<ticket>` from the build lease directory (a
  local-path fetch); the branch only reaches origin at publish step 5, after the
  confirm. This keeps the never-push-before-confirm rule intact across separate pool
  clones.
- PR creation is an optional tracker capability: the github adapter shells out to
  `gh pr create`, while local/command trackers keep the PR body file as the artifact
  instead.

## Gate reviewer

This section records the rework that replaced PR #8's session-dispatched
reviewer: see `docs/adr/0007-gate-reviewer-owns-bookkeeping-not-judgment.md`
for why. None of PR #8's `class`, `Closure` accounting, jig-side
carry-forward, `normalizeTitle` auto-dismiss, `rung` pin, or silent oracle
fallback survived into this design; `action` (fix/ask/note), `prior`, and
`reviewed_paths` replace them respectively.

Design questions the code raised, and their resolution:

- "Lies in no manifest workspace" is decided after normalizing the
  manifest's own workspace path to the form a finding's file already has
  (backslashes to `/`, no trailing `/`, no leading `./`), and matched by
  path segment rather than raw string prefix. A workspace path is written
  by hand, where `./billing`, `billing\` and `billing` all name the same
  directory; comparing them literally would put every finding in that
  workspace in the no-build-target case below, turning ordinary fixes into
  questions for a human over a manifest's punctuation.
- A kept `ask` whose file lies in no manifest workspace has no build
  target, so the workspace it needs is the human's judgment, never a silent
  default. At a terminal, keeping such an `ask` prompts for a workspace id.
  With `--yes` or no terminal, it cannot be kept without that judgment and
  stays `asked`: the round is not clean, and `jig gate` lists it under
  "needs a human" and exits 2 (the same code `jig solve` already uses for a
  builder's pending question). `jig solve`'s own gate/fix-slice loop checks
  the same `NeedsHuman` list after every round and stops the same way,
  rather than re-dispatching the reviewer on a decision nothing in that
  loop can make: before this, `jig solve` read only the round's verdict, so
  an undecided ask kept the loop re-dispatching a full reviewer session
  every round up to `maxSolveRounds`, each one repeating the same
  unresolved ask, before failing `GATE_ROUNDS_EXHAUSTED` without ever
  printing what needed a human.
- The same missing-build-target rule covers a missing or stale oracle, not
  only a missing workspace: a `fix` or kept `ask` finding whose recorded
  oracle is empty or no longer one of the manifest's current oracle names
  (the manifest changed between rounds) has no build target jig can
  derive, and is routed as an ask the same way, whichever part is missing
  (`routed_as: ask` when the reviewer called it a fix). A manifest with
  exactly one oracle leaves no choice to make, so jig resolves to that
  oracle itself whether the finding names none or names an oracle the
  manifest no longer has, and a fix or ask finding never records an empty
  oracle when the manifest has any oracle at all (a note, which is never
  routed and needs no build target, may); a recurrence keeps its earlier
  occurrence's resolved oracle when this round names none. Only a manifest
  with several oracles and a finding with no usable name among them leaves
  the choice to a human. A manifest with zero oracles can never build any fix
  slice at all, so that case is checked once, before triage, whenever the
  round has a fix or ask to route (a human is never asked to triage
  findings that were already going to fail regardless of the answer); its
  error text says exactly that ("the manifest has no oracles"), and a
  different message, naming the finding and the manifest's current
  oracles, covers a manifest that does have oracles but none this finding
  can use. At a terminal, keeping a fix or ask missing a workspace, an
  oracle, or both is prompted for each missing part in turn - workspace
  from the manifest's workspace ids, oracle from its oracle names - and the
  answer is recorded on the finding and used for its fix slice; with
  `--yes`, no terminal, or stdin closing before every missing part is
  answered, it stays `asked` and is listed under `needs_a_human`. One
  exported check (`verifydeliver.BuildTargetGaps`) decides which parts are
  missing for `DefaultTriage`, `routeRound`'s kept-ask handling, and
  `cmd/jig`'s terminal prompt and its stdin-closed count alike, so the four
  can never drift apart on what counts as a full build target.
- `findings.yaml` gains two additive fields beyond a finding's core ones
  (id, file, line, title, detail, action, risk, risk_rationale, oracle,
  workspace, status, recurrences):
  `triage: human|auto` (who decided - a person at a terminal, or `--yes`/no
  terminal - absent for notes, dismissed repeats, and undecided asks) and
  `decision` (the human's text for a kept ask). `triage` is decided afresh
  every round, from that round's own routing; `decision` is the human's
  judgment about the finding itself, so it persists on every later
  occurrence of the same finding regardless of what else changes about
  it - including its `file`, when the reviewer reports the same finding
  (by `prior`) as having moved. `routed_as` is written only when
  jig routed a finding as `ask` although the reviewer's own `action` said
  otherwise (the recurrence bound, or a missing build target); the
  persisted `action` always stays the reviewer's label. A round's
  `summary` is persisted too. A repeat of an already-dismissed finding
  carries none of this forward: `triage`, `decision` and `routed_as` reset
  to empty, since that finding is not routed or triaged this round at
  all. These fields exist so a later eval rework's gold can derive from
  jig's own records instead of matching model prose.
- The e2e case "an ask kept with a decision" needs a real terminal, which a
  subprocess pipe correctly is not. The decisive e2e test instead runs the
  full chain in-process through `cmd/jig`'s own `Main`, with the
  package-level `stdinIsTerminal` var overridden and a scripted stdin
  standing in for one; a smaller, genuinely subprocess-driven test in
  `e2e/` covers the actual non-terminal path (`DefaultTriage`).
- Result validation: a `fix` or kept `ask` finding must name a
  manifest oracle when the manifest declares more than one; with exactly
  one, an omitted oracle is that oracle; any oracle a finding does name
  must be a real manifest oracle. The parsed result itself decodes
  strictly: a bare JSON `null`, any other non-object top level, or more
  than one JSON value all fail the round outright. Every key, at the top
  level and inside each finding, must be an exact, case-sensitive match of
  a recognized field name: an unknown key fails the round the same as a
  key that repeats an earlier key of the same object, exactly or only by
  case (`"FINDINGS"` alongside `"findings"`), and so does a lone case
  variant with no correctly-cased duplicate to catch (`"Oracle"` with no
  plain `"oracle"` beside it) - encoding/json's own struct decode matches a
  key to a field case-insensitively when no exact match exists, so without
  this check that lone variant would decode silently instead of being
  rejected. The `findings` and `reviewed_paths` keys must be present and
  must not be JSON `null`; a genuinely absent key and an explicit `null`
  are both rejected the same way, since only an actual empty list (`[]`)
  means "reviewed nothing here." Every test fixture that builds a
  `ReviewResult` as a struct literal has to fill a nil `Findings` or
  `ReviewedPaths` with `[]` itself before marshaling it, for the same
  reason `MarshalReviewRequest` fills its own nil slices: a struct
  literal's zero-value slice marshals as `null`, which this validation now
  rejects.
- Coverage lists (`must_review`, and the scope diff feeding it) come from
  `git diff --name-only -z --no-renames --diff-filter=AMT|D`, so a rename
  counts as its new path under "changed" and its old path under "deleted".
  `must_review` is the sorted, deduplicated union of the changed files and
  the files of open findings still present at head. Every path is
  repo-relative with forward slashes. A finding's own `file` is checked
  strictly: comparison normalizes a backslash separator and a leading
  `./`, and rejects an empty path, an absolute path (leading `/` or a
  drive letter), and any `..` segment. Checking whether `reviewed_paths`
  covers `must_review` is more forgiving, since it reads the reviewer's
  own words rather than validating a finding's own field: an absolute
  path that happens to fall inside the lease worktree is relativized to
  it and counted as covering that file; any other entry that can't be
  normalized this way (an absolute path outside the worktree, a `..`
  segment, a path to something outside the diff entirely, such as the
  brief) is ignored rather than failing the round - only a `must_review`
  path left genuinely uncovered fails it.
- Clearing (an open finding whose file was reviewed and not reported
  again): only this round's *routed* findings, in their final post-triage
  status (`open` or `asked` - a finding the human dismisses at triage no
  longer blocks anything), in the same file block it from clearing. A
  dismissed repeat and a note never block, so a dismissed finding
  re-reported in the same file as an unrelated open finding cannot keep
  that open finding alive. An unreported open finding also clears outright
  when its file no longer exists at head at all - checked directly against
  the lease, not merely inferred from this round's own scope-diff deleted
  list, so a file deleted in an earlier round still clears a finding
  reported in a later delta round that never mentions it. None of this
  applies to an `asked` finding: only an `open` one clears this way. An ask
  is a question put to a person, and nothing the reviewer reports - or
  declines to report - answers it, so neither coverage nor the file's
  disappearance resolves one. An ask leaves the outstanding set exactly two
  ways, a human keeping it or a human dismissing it, and it is offered
  again every round until then. Clearing one automatically would let an
  unattended run drop the question, call the round clean and point at
  publish, shipping the ticket with the decision never made.
- The same rule governs a re-reported ask, and for the same reason it
  governs a re-reported dismissal (rule 2 below): once a finding's status
  is `asked`, a later occurrence replaces its file, line, title, detail,
  action and risk, but only a person changes its status. Deciding it from
  this round's label instead retired the question without an answer - the
  same finding re-reported as `note` became a record, the round went clean
  and publish unlocked; re-reported as `fix` it became queued work with no
  decision recorded anywhere. `routed_as` records the disagreement whenever
  the reviewer's label is not `ask`.
- What an undecided ask does to an unattended run at this stage is hold it:
  the round is not clean, `jig gate` exits 2, and the ask is listed under
  needs-a-human and in `jig status`. It is deliberately not yet a parked
  question in the builder's sense - no `questions.yaml` entry, no `--answer`
  to clear it - so an unattended run stops rather than parking in the way a
  builder's question parks. Turning an ask into a real parked question is
  its own change, and the vocabulary here should not be read as claiming
  that already ships.
- A finding recurs only through the reviewer's own `prior`, never by title
  matching. A recurrence is counted - its recurrence count goes up, and
  the bound below can trigger - only once a fix slice that already
  records the finding's id (the structural `findings:` link, not a parsed
  slice id) has gone green; a finding re-reported while its fix slice is
  still queued or building (or while none exists for it yet at all, as an
  undecided ask re-reported, or a round run with `--early`, can both
  produce) updates in place at its current count instead. The first
  counted recurrence routes like a new finding, with the previous fix
  slice named in the new slice's goal (by id, plus its last attempt's own
  recorded summary when one exists). The second counted recurrence always
  routes to a human as `ask`, whatever the reviewer's label - the same
  failure surviving one fix slice already is jig's stall concept applied
  to review. A `note` label on a recurring finding does not exempt it
  from the bound: a finding whose earlier occurrence was `open` or `asked`
  can legitimately recur as a `note` ("still there but harmless now") and
  still be forced to `ask` on its second counted recurrence, so rule 1
  always carries the earlier occurrence's `oracle` forward when this
  round's own report names none - otherwise a recurrence forced to `ask`
  by the bound could have nothing to build a fix slice with even after a
  human keeps it. `prior` legitimately names a *noted* finding too: a
  noted finding leaves the open set (it is not outstanding work - it
  never blocks clean, and its file is not forced into `must_review`) but
  review.json's `open` list still carries it (`Action: "note"`,
  `openAndNotedFindingsList`), so it stays a citable `prior` target and
  its recurrence count keeps climbing across a note occurrence exactly as
  it would across a fix or ask one. Without this, the bound above would
  be evadable: a reviewer alternating `fix` and `note` on the same
  problem would get a fresh id at `recurrences: 0` every time the label
  flips back, since the noted occurrence would otherwise be unreachable
  by any later round's `prior`.
- Clean without dispatch: when the scope diff changes no file at all (none
  added, modified, or deleted) and no finding is outstanding, jig writes a
  clean round without ever dispatching a reviewer session, since the
  previous review already covers head. A deletion-only diff is not this
  case: it still dispatches, since a deleted file can itself be worth
  reviewing (whether removing it broke something that depended on it, for
  instance).
- Slice ids: a fix batch is `fix-<round>-<workspace>-<oracle>`, a kept ask
  is `fix-<round>-<finding id>`; both are sanitized to
  `[A-Za-z0-9._-]` and, on a collision, disambiguated with the lowest
  unused `-2`, `-3`, ... suffix.
- A kept ask's fix-slice goal states plainly whether a person actually
  decided it: "kept by the human" only when the finding's own recorded
  `triage` field says a person at a terminal did, and "kept with no human
  decision (--yes or no terminal)" otherwise - read from that field rather
  than hardcoded, so an auto-kept ask never tells the builder session a
  human made a call that nobody actually made.
- A reviewer-reported `action` outside `fix`/`ask`/`note` is a programming
  error, surfaced as an error up the call chain, the same as any other
  malformed result; it is never silently treated as `note`. Likewise, a
  finding `file` jig cannot normalize to a repo-relative path is an
  error, never kept with its raw, unvalidated value.
- `gitx.FileExistsAtRev` resolves the rev first, so a bad rev is reported
  as an error rather than folded into "the path doesn't exist"; only then
  does it check the path, structurally rather than by matching git's
  message text: `git --literal-pathspecs ls-tree -z --full-tree` for the
  path, which exits 0 whether or not the path exists there and never
  consults the working tree. Only an entry whose own path is exactly the
  path asked about counts, and only when it is a `blob`: a directory lists
  its children instead of itself, so `alpha` or `alpha/` is `false` rather
  than the type of whichever child git happens to print first, and
  `--literal-pathspecs` keeps a name like `a*b.go` or `:/x` a plain path
  rather than a glob or pathspec magic. No matching entry is absent
  (`false, nil`); any other failure of the `ls-tree` call itself is an
  error. A finding `file` that ends in `/` is rejected at validation as a
  directory rather than a file. Because the
  check never looks at the working tree, an ignored or untracked file that
  happens to sit on disk at that path (for example an oracle regenerating
  a build artifact in the gate lease) cannot make an absent path look
  present.
- Compatibility with the old scripted (`--scenario`) path: `jig gate` had
  no `--backend` flag before this rework, so its scripted source still
  runs exactly as before iff `--scenario` is set and `--backend` is not -
  every existing invocation is unchanged. `jig solve` already had
  `--backend`, so its own rule differs: the scripted source runs iff
  `--scenario` is set, whatever `--backend` says.

Deviations recorded during the build, beyond what is already described
above:

- `Gate`'s `RoundInput.BriefPath` is the ticket's own `brief.md`, except in
  `--branch --doc` mode, where it is the `--doc` file itself, absolute,
  matching PR #8's own recorded reasoning: pointing at this round's own
  `gate/round-N/spec-input.md` instead would leave a partial round dir on
  disk if the reviewer then failed, since that file is written only once
  the round succeeds.
- `findings.md` never prints "clean" for a round that is not clean: it
  prints the round's own recorded verdict, and "nothing new this round"
  when that verdict isn't clean but nothing was reported this round -
  never derived from `len(findings) == 0` alone, since a round can have
  nothing new to report while something still open or asked from an
  earlier round keeps it from being clean. The exact wording here is this
  build's own choice; only that "clean" never appears for a non-clean
  round is required.
- The interactive triage prompt's exact wording is this build's own
  choice: the behavior described above in this document (batch accept or
  dismiss by id, an ask's keep-or-dismiss with an optional decision, the
  workspace and oracle prompts, EOF semantics) is fixed; the literal
  prompt strings are not. Each ask's
  answer syntax is explicit rather than inferred from free text:
  `n`/`no`/`d`/`dismiss` dismisses, and only `k`/`keep`/Enter keeps - any
  other answer reprompts rather than being read as an implicit keep, so
  free text typed for something else can never accidentally become the
  kept decision. A kept ask is then asked for its decision text on a
  second, separate prompt (Enter skips it).
- Every place a finding reaches a human shows it with file:line and risk
  rationale, sorted by risk high first, not the title alone: the ask
  prompt, the notes table, the fix batch table, and the gate report's own
  findings and needs_a_human tables (already sorted by risk then id).
  `detail` reaches a human at the per-ask prompt only; the four tables
  carry file:line, title, risk and risk rationale, not detail.
- Triage is offered every outstanding ask each round, not just the asks
  this round's reviewer happened to report. Routing takes the cumulative
  fold's still-`asked` findings, so an ask left undecided in round N is
  put to the human again in round N+1 whether or not the reviewer
  mentions it; a decision taken this round updates that finding and is
  recorded in this round's own `findings.yaml`, which is why a
  `findings.yaml` can carry a decided finding no reviewer reported that
  round. Without this an ask nobody re-reports sat under `needs_a_human`
  with nothing ever prompting for it - the only ways out were the
  reviewer coincidentally raising it again or the moot-ask clearing
  above, neither of which is a decision.
- `jig status` lists those outstanding asks between rounds, in an
  `outstanding_asks` block (id, risk, file:line, title) read from the same
  cumulative fold: a waiting decision is jig's own pending state, so it is
  visible without running a gate round, exactly as an open question is.
  What decides them - `jig gate <ticket>` at a terminal - is named once in
  the help rather than repeated in a per-row cell, because it is the same
  command for every ask and a table cell cannot carry the ordering below.
- Neither that help nor `jig gate`'s own report hint ever prints `jig gate
  <ticket>` as a step to run while the frontier check would refuse it. Both
  decide that by reading the frontier itself (`frontierGreen`, the same
  condition the check applies), never a proxy for it: a round's own fix
  slices are one way to be short of green, and a fix-slice count read as
  the whole story printed a refused command after `jig gate --early`, which
  reviews an unfinished frontier and can leave an ask undecided having
  queued nothing. Short of green, both surfaces name the order instead,
  and the first step is the ticket's own next-step hint rather than a
  fixed `jig run <ticket>`: a frontier parked on an unanswered question
  does not advance on that command at all, only on the answer, and the
  two surfaces share one hint so they cannot disagree about it. The gate
  at a terminal follows as the second step. This is the same rule the
  printed resume commands follow - a command jig prints as a next step
  runs as printed.
- `--yes`/non-terminal triage's one-line note (`DefaultTriage`, run by
  `triageFor`) is printed only when this round actually had a fix or ask
  to triage; a note is never triaged, so a notes-only round prints none
  either, and a round that routed nothing at all (e.g. a dispatched
  reviewer round with nothing new to report) prints none. It never claims
  an ask was kept when it was actually left undecided: the count of how
  many are left for a human is read straight from what `DefaultTriage`'s
  own result leaves undecided (an ask id absent from its `Asks` map), not
  re-derived from the ask's `Workspace` field alone, so the message can
  never drift out of step with what `DefaultTriage` itself decides.
- `TestGateReviewerRoundsThroughMain`'s round 2 assertions read round 2's
  own `findings.yaml` `cleared` list directly and require the kept ask
  `r1-f3` in it, rather than relying on its absence from round 2's report
  and findings table alone: that table lists only findings reported that
  round, so it cannot on its own distinguish a finding that cleared from
  one that simply went unmentioned. A mutant that lets a dismissed repeat
  block clearing fails this assertion.
- Unit tests on `gateSourceFor` and `gateSourceForSolve` assert the
  returned `GateSource`'s concrete type (scripted vs. reviewer) for each
  flag combination, via `%T` rather than reaching into verifydeliver's
  unexported types, pinning the `jig gate`/`jig solve` compatibility rule
  above.
- The scope base anchor for a full-scope round prefers `merge-base(origin/
  <target>, HEAD)` over the ticket's recorded start sha, falling back to
  the start sha only when the merge-base lookup itself fails (no such
  ref) - merge-base first, ahead of PR #8's own order (the start sha
  first): the recorded start sha can predate a rebase that moved the
  target branch, while merge-base always anchors at the ticket branch's
  actual point of divergence.
- `Gate` appends its `gate-open` journal line before dispatching the round
  at all, and writes `work/gate.round-N.*.json` to the store's working
  copy before validating a result, both before its own end-of-round
  `Store.Push`. A round that fails after that journal line (`REVIEW_INVALID`,
  `REVIEW_FAILED`, `GATE_NO_ORACLE`, an oracle failure, a routing error)
  used to return before that `Push`, leaving the journal line tracked but
  uncommitted - the store's next `Sync` (`git pull --rebase`) then refused
  over it once a remote existed, and only a manual `git checkout -- .` /
  `git clean -fd` on the store recovered it. `Gate` now runs a deferred,
  best-effort `Store.Push` (a message naming the ticket, the round and the
  failure) on any error once the `gate-open` journal line has been
  appended, whatever the failure, so the store is always clean and pushed
  by the time the error reaches the caller and a plain rerun works with no
  manual cleanup. The decisive e2e test (three real gate rounds through
  the fake backend, in-process through `cmd/jig`'s `Main`) asserts the
  store is clean right after its deliberately broken round 1 attempt,
  instead of discarding leftovers by hand before retrying.
- Every error a reviewer round can return after the `gate-open` journal
  line (`REVIEW_INVALID`, `REVIEW_FAILED`, `GATE_NO_ORACLE`) carries a
  `Help` line naming the recovery (fix the input, or add an oracle, then
  rerun `jig gate` for the ticket), on top of the best-effort push above.
- Known gap, deliberately left for its own change: `Publish` journals in
  several places and pushes once at the end, with no equivalent deferred
  push, so any exit after its first journal line leaves the store dirty
  and the next `Sync` refuses - reachable simply by declining at publish's
  own confirmation prompt, not only by an outage. The fix belongs with
  `Publish`'s own error paths rather than widened into the reviewer's
  change, and until it lands the manual recovery is the one named above.
- That failure commit's subject is built from the error's `axi` code plus
  the ticket and round, never the error text: the message is permanent
  store history and gets pushed, and a raw error carries whatever the
  failure happened to contain, including absolute paths on the machine
  that ran it. The full error still goes to stdout, where it is read once
  and not kept. The round number is resolved before the `gate-open`
  journal line for the same reason: a failure between journaling and
  resolving it used to record "round 0", a round that never existed.

## Review eval

- `internal/verifydeliver` gains one type and one function beyond the
  reviewer contract itself: `Fold` and `FoldBefore(st, ticket, round)`,
  wrapping - not replacing - the four unexported helpers `Gate` wired
  together inline (`cumulativeFindings`, `openAndNotedFindingsList`,
  `dismissedFindingsList`, `toDismissedFindingList`). `Gate`'s own behavior
  is otherwise unchanged: a fold error now carries one more wrap
  (`FoldBefore`'s own, under `Gate`'s existing one); the eval package
  calls the same `FoldBefore` a real gate round does, so the two can
  never quietly disagree about what a round's history is.
- The eval never calls jig's routing or triage step (`route.go`'s
  `routeRound`, `DefaultTriage`): a round's score comes straight from
  `verifydeliver.ApplyRound`'s own status assignment and
  `verifydeliver.ClearingAfterTriage`, the same fate a freshly reported
  finding carries before any human, or `--yes`, decides it. Calling triage
  too would fold jig's own default-approval policy into a review-quality
  number, which is a property of jig's config, not of the reviewer.
- `ApplyRound`'s `sliceGreen` callback always answers false for every eval
  round: the eval never builds a fix slice, so every re-reported finding
  is scored as a fresh occurrence rather than a counted recurrence a
  finished fix slice would explain.
- Every case runs under an opaque id, `runID(name)` ("c-" plus the first 8
  hex characters of a sha256 of the case name), never the case name
  itself: the store ticket, `RoundInput.Ticket` (which the reviewer's own
  prompt embeds verbatim), the judge's own dispatch ticket, and every path
  under a run's work root are named after the id, and the case repo's
  commit messages stay neutral literals ("base", "round N"), never the id
  either, for the same reason - nothing a live reviewer or judge session
  reads may say which case it is looking at, or that it is a case at all.
  `CaseScore.Name`, `JudgeQuery.Case` and every error message still carry
  the real name, since only what a live dispatch itself reads or is keyed
  by may never see it. The eval repo's own git identity and its store-side
  `project.yaml` comment are neutral for the same reason (a live session
  can run `git log` in its worktree, so an author of "jig-fixture" would
  say "fixture" to it as plainly as the case's own name would), and the
  base commit now carries a neutral `go.mod` (`module example.com/project`,
  `go 1.22`) next to `.claude/jig.yaml`, so every case repo actually builds
  and a live reviewer's own `go test ./...` can run; `loopvar-trap` gets
  the Go version its premise needs from that base commit like every other
  case. A dedicated test,
  `TestRunCaseNeverLeaksTheCorpusVocabulary` (`leak_test.go`), runs the
  whole corpus through a capturing backend and judge and checks the prompt,
  the dispatch paths, the dispatched slice file, every worktree file
  (tracked or not), the store's own ticket-dir files and the worktree's
  git log against both the case's own name and a fixed vocabulary
  (`eval`, `revieweval`, `fixture`, `trap`, `gold`, `seeded`), tokenized
  with `strings.FieldsFunc`, never `regexp`.
- A finding whose own `prior` names a fold point in the same file, open,
  asked, noted or dismissed, is a structural candidate for that point
  regardless of its line,
  line 0 included: citing the id is itself location evidence, so that one
  candidate survives on anything but an explicit judge Different, without
  the extra burden of an explicit Same a line-0 finding otherwise needs.
  A prior naming a point in another file, or one the judge rejects, is
  simply no edge, so the existing wrong-prior rule (below) still catches
  it.
- Seeded gold findings are matched to a round's reported findings by
  `bestMatching`, the Hungarian algorithm run over lexicographic score
  vectors rather than one packed integer (lexicographic order is a total
  order addition preserves, so the potentials work unchanged and nothing
  can overflow). It finds the exact maximum-weight matching among
  maximum-cardinality matchings in polynomial time; an exhaustive search
  would also be exact, but a captured case with a dozen seeded findings
  would already take billions of steps. A test checks it against an
  exhaustive reference on two thousand random instances, and another runs
  a round no exhaustive search could finish (40 gold entries, 60 findings)
  and pins the exact matching it must return, not only that it returns a
  complete one: each gold entry's own same-indexed finding carries the
  strongest possible edge and every other edge is strictly weaker, so the
  diagonal is the unique optimum. Two matchings
  are compared by a lexicographic tuple - cardinality first, then how many
  edges the judge confirmed Same, then how many cite the gold's own
  prior, then summed line closeness - and never by a finding's status or
  action, since those are exactly what the score then measures. The next
  field, the summed earliness of the paired findings, only decides between
  matchings the evidence cannot tell apart; a tie surviving even that (more
  than one matching using the same set of findings) falls to the
  assignment algorithm's own row order - deterministic for a given input,
  so the same result every run, but not itself a ranking field - a tie
  only the evidence cannot break, which a live judge's own Same/Different
  verdicts normally do resolve.
- A dismissed fold point's span and description ordinarily come from its
  one recorded line and its recorded title and detail; when a decision
  names it (`decisions.yaml`'s optional `recorded: <id>`) and that
  decision's own file still matches the fold's current one, the point
  instead takes the union of the decision's own span with the record's
  own line as the loader captured it (`Decision.RecordedLine`), described
  by the decision itself - never the fold's latest occurrence of that id,
  which a re-report could have moved. A file mismatch skips the link
  rather than trust a pairing the decision never actually judged. The
  loader itself rejects a `recorded` line outside the decision's own span
  widened by `lineWindow`, and a second decision anywhere in the case, not
  only the same round, naming a record another decision already claimed.
- Rule 1 of `classifyUnmatched`: an unmatched finding whose `prior`
  names any fold point, whatever its status, needs a surviving structural
  edge to that point; without one it is `FateWrongPrior`
  (`RoundScore.WrongPriors`) and fails the round. `ApplyRound`'s own
  bookkeeping trusts a reported `prior` id without checking what it
  structurally points at, and would carry that point's identity onto
  unrelated code. Only a well-cited repeat of a dismissed point is silent
  (jig keeps it dismissed); any other well-cited repeat goes on through
  the rules like any finding, so citing a prior never excuses a false
  alarm.
- `checkJudgeReadOnly` returns a violation and an error as two separate
  results, not one: a `git rev-parse`/`git status` command itself failing
  during the read-only check is ordinary infrastructure trouble, returned
  as a real error, never folded into "the judge changed the case repo" -
  only an actual difference from the round's own head is that. Either
  way, the runner hard-resets and cleans the repo (`git reset --hard`,
  `git clean -fd`) before returning, since the next round's `git apply`
  must start from a pristine head whatever a judge dispatch left behind.
- The judge's scratch lives in its own temp root (`os.MkdirTemp`), outside
  the case work root and removed when the case ends, so nothing beside
  the reviewer's worktree says a judge exists. Once a round is fully
  scored, the runner deletes, rather than moves, the case's store-side
  `work` dir and that round's own judge scratch dir (`retireRoundWork`): the JSON report already keeps every finding a
  round produced, so nothing is lost, and deleting them means no later
  round's dispatch, and no later session poking around under the work
  root, can find an earlier round's live
  `review.json`/`result.json`/`judge.json`/`verdicts.json` anywhere and
  read a result that disagrees with the case's own recorded history. This
  claim is scoped to the work root: a later session free to read wherever
  it likes could still find the standing text/JSON report on disk, or the
  CLI's own session transcripts, and this deletion does nothing about
  either - the report's own default location moves outside the work tree
  for exactly that reason (below).
- `live_test.go` fails the Go test itself on a round that comes back
  Failed (a dispatch failure, a judge error, the judge changing the case
  repo, or a reviewer that wrote no `result.json`): the eval has nothing
  to score there, and burying it in a text report nobody reads would let
  it go unnoticed. The last case is the reviewer's own behavior, not
  infrastructure, and a failed round's gold counts as missed. A Refused round stays a measurement, and a judge error still
  keeps that round's `Findings` (status set, no gold match, no fate).
  Cases run one at a time through `RunCase` rather than `RunCorpus`,
  rewriting the text report and JSON after every case, and the documented
  command adds `-timeout 0`, so a long unattended run keeps whatever
  finished on disk if it times out or panics partway through the corpus.
  When `JIG_REVIEWEVAL_REPORT` is unset, the report now falls back to
  `defaultReportDir()` (`os.UserCacheDir()/jig/revieweval`, falling back
  to `os.TempDir()` only when `UserCacheDir` itself fails), never
  `t.TempDir()` (which would vanish with the test itself) and never the
  work root either (`report.txt` and `report.txt.json`), and logs that
  path once at the start; each case's own report lines are logged as it
  finishes, and the whole corpus's cumulative report once more after the
  loop. The work root itself is now its own `os.MkdirTemp("", "jig-")`,
  removed when the test ends, rather than `t.TempDir()`: `t.TempDir()`
  names its own directory after the running test
  (`TestEvalLive.../...`), which a live session reading its own worktree
  path could otherwise see.
- The eval repo's per-round git identity and commit date (pinned in
  `runner.go`'s `identityEnv`) are fixed, not the operator's own: two runs
  of the same corpus produce the same shas, which nothing yet depends on
  but which makes one run reproducible to compare byte for byte against
  another. The identity itself is neutral (no "fixture"), since a live
  session's own `git log` can read it.
- The live path's default model is derived, not hardcoded:
  `staircase.Disjoint(staircase.Default(), []string{cfg.Rungs[0]})`, the
  rung after the cheapest - the pick `Gate` itself makes
  (`staircase.Disjoint(d.Rungs, journal.BuilderModels(lines))`) once an
  unattended ticket's builders have already used the cheapest rung, the
  common case an unattended eval run matches. The cheapest rung itself is
  only what `Gate` picks before any builder has dispatched at all, which
  is never the case for a reviewer round that runs after a ticket's
  slices.
- `RoundScore` carries `FalsePositiveGold` (whether a round's own gold
  could produce a false alarm at all: a trap, a dismissed decision, or an
  exhaustive round) and every reported finding as a `ScoredFinding`, so
  `RenderJSON` is enough for a person to label a pending finding without
  the round's work dir.
- `RenderReport`'s recall, like its other rates, is `n/a` with zero seeded
  gold rather than a bare `0.00`.
- A round's verdict is PASS, PROVISIONAL or FAIL. PROVISIONAL means nothing
  failed but some findings are still pending a label: a round that reads
  PASS with unjudged findings in it would overstate what was measured.
  The alternative, marking more rounds exhaustive, was not taken: it would
  turn every unforeseen but correct remark into a false alarm.
- `bestMatching` bounds each augmenting search at m+1 steps, one per
  column it can mark used, and returns an error past that, so a broken
  comparator fails fast instead of hanging a test run.
- `session.Options.Env` lets a caller fix a headless child's environment;
  the live eval passes its own environment minus every `JIG_` variable,
  `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_NOSYSTEM`, `PWD` and `OLDPWD`, and any
  launch-context entry (an empty name, which is how a Windows per-drive
  working-directory entry such as `=C:=C:\work` reads when cut at its
  first `=`, plus `_` and `GOCOVERDIR`), and
  the backend sets `PWD` to the worktree. Setting `Env` on a backend that
  cannot apply it (fake, herdr) fails at construction instead of being
  silently ignored. It is a denylist of what jig and its
  test scaffolding own, not an allowlist of what the CLI needs, so the CLI
  keeps whatever else it relies on. The claude stub records its
  environment and surroundings, and a test runs a case through the real
  headless backend to check what each child actually saw. That test feeds
  the scrub a synthetic parent environment, one planted value of every
  kind it must drop, plus every variable the test harness added or
  changed after launch, and scans the child's environment strictly.
  PATH, the one list the host fills, is not fed whole: each entry the
  harness added to it is scanned directly instead. Scanning a
  real environment would judge whatever the machine running the test
  happens to carry, such as a CI runner's branch name, and make the result
  depend on the host. The operator's ambient environment, including the
  temp root every path sits under, is outside what the eval scrubs; what
  the harness adds is not. TestMain snapshots the launch environment and
  temp root first thing, so harness setup belongs after the snapshot,
  never in an `init` or a package-level initializer, which run before
  TestMain. The leak checks leave out only the launch temp root and the
  hostile one each leak test makes under it, so a temp root the harness
  chose is still scanned, and each root is replaced with a space, never
  removed outright, so the text on either side cannot merge into one
  token that hides a leak word.
- The five cases ported from the earlier corpus kept their code, but
  `clean`, `loopvar-trap` and `tenant-leak` lost sentences that gave the
  reviewer the verdict (a brief saying the diff is correct, a brief
  explaining the rule the trap tests, a comment arguing the trap is safe).
  That is a content change, not only a translation to the new gold shape.

## CLI

- `jig init` takes `--store <path>` for the project form, while the end-to-end fixture
  passes explicit `--clone` flags; the flag surface was left open, so both entry
  points were kept.
- `jig solve` pauses by exiting with code 2 at a needs-input state; a second
  `jig solve --yes --answer <qid> "<text>"` resumes the chain. "One process" is
  interpreted as one process per invocation of the chain, not one process end to end.
- Model ids are verified against the current API reference (haiku, sonnet, and opus
  aliases) as defaults; the actual rung used is project configuration.
- `jig validate` prints a brief section-hash table so a calling skill can fill
  `from_brief` without needing a new CLI verb, keeping the CLI surface exhaustive
  without growing it.
- `jig status` distinguishes a slice parked on an open human question from one
  that is genuinely stalled, replacing the old single `paused` state value,
  and further from one merely blocked on its env coming up. The state: line
  precedence is stalled > parked > env-blocked > green > building: a stuck
  slice outranks one merely waiting, so it is reported the moment it is
  found. (The help hint's own priority is the opposite: an open question
  comes first there, since answering it is the one action that can also
  unblock other slices queued behind it.) A needs-input slice renders in
  its own parked table with a resume column: the exact resume command for
  the common case, or, for a slice parked on a flawed brief with brief
  sections to amend, prose telling the operator to amend the brief first
  (the command alone would requeue nothing); a stalled slice renders in
  its own stalled table with a human-readable summary of what tripped it.
- The resume command a parked slice is offered is the one that can actually
  clear it. Amending the brief and requeuing with `--from-brief-diff` is
  offered only when the slice was parked for a flawed brief AND has brief
  sections for requeue to notice (`FromBrief` non-empty): requeue keys off
  those hashes, so it does nothing for a slice without them, and a plain
  question is not a brief problem even on a slice that has them. Every
  other parked slice, which is the common case, is resumed by answering its
  question. A stalled slice is resumed by `jig requeue <ticket> --slice <id>`,
  unless it has brief sections, where amending the brief and
  `--from-brief-diff` is offered instead. An env-blocked slice is always
  resumed by `jig requeue <ticket> --slice <id>`, with no brief-sections
  check: that branch exists only in the stalled loop, deliberately, since
  bringing an env back up is never a brief problem. `store.SliceState`
  gains two additive fields: `Signature` (the stall-matching key, not shown)
  and `StallSummary` (the human-readable text the stalled table shows,
  `-` when absent), both set on both stall paths (repeat-failure stall and
  attempt-cap) and cleared on every route out of a stalled or needs-input
  state - green, both requeue forms, and answering - so neither survives a
  slice's eventual recovery. A printed command never carries the invocation's own
  `--store`/`--project`: a path with a space breaks the command as printed,
  and quoting it differs between shells, so a store selected by flag is the
  caller's to repeat.

## Fixture and tests

- The fixture envtool is pre-built rather than invoked via `go run`, because it is a
  cross-platform Go program and the `go run` form would hang on PATH-less shells and
  recompile on every lifecycle call. It is built once per test binary and shared by
  every Generate call, since nothing about it varies between fixtures.
- e2e fails the whole run when the jig binary does not build, instead of skipping
  every test: a skipped suite reads as green.
- Status goldens: state (a) ("A green, B blocked") is constructed directly via store
  state snapshots plus `jig status`; states (b) and (c) are natural pauses that occur
  during the end-to-end chain. In all cases the CLI's rendering is what the goldens
  pin.
- The herdr backend computes `/mnt/<drive>` paths mechanically instead of shelling out
  to `wslpath`, after herdr was probed to behave as expected via a WSL login shell
  (herdr 0.8.2); the backend is implemented against that probed JSON surface. Off
  Windows it runs herdr directly; on Windows it goes through a WSL login shell in the
  default distro, or `JIG_WSL_DISTRO`. A stub `herdr` binary checks the full command
  sequence of a run on every OS; the WSL command line and its quoting are unit-tested
  as pure functions, without spawning `wsl`.
- Blocked-by relationships on the github adapter use the REST issue-dependencies
  endpoint via `gh api`; sub-issues use the GraphQL `addSubIssue` mutation. Tests
  assert the resulting argv against a stub rather than hitting the network.
- ARCHITECTURE.md's module table is drift-guarded by a lint test that asserts every
  named package directory actually exists.
- CLAUDE.md consists of a single `@AGENTS.md` import line, so the two files share one
  content; AGENTS.md itself stays navigation pointers only.

## Git execution and CI

- Commits that land on the user's PR branch (reconcile, memorize, squash) use the
  user's git identity and the current time. The identity is resolved with `git var` in
  the user's mapped clone, so repo-local and `includeIf` identities apply even though
  the commits are made in a pool lease. A missing identity stops `solve` and `publish`
  before any work, with the `git config` commands to fix it. Store bookkeeping commits
  keep jig's own identity: they are jig's commits, not the user's. Tests pin identity
  through the environment (`gittest.PinIdentity`).
- git's detached auto-maintenance, spawned after commits, fetches and on the receiving
  side of local pushes, was still writing when a test's `TempDir` cleanup ran, so
  cleanup failed with "directory not empty" (on Linux, under load, 8 in 3,200 runs of
  one e2e test; 0 in 3,200 with maintenance off). Tests run git under a generated global
  config (`maintenance.auto=false`, `receive.autogc=false`, `gc.autoDetach=false`) with
  no system config. `-c` flags and `GIT_CONFIG_COUNT` would not do: git clears them for
  local transport, so they never reach `git-receive-pack`. A trace2 capture shows
  `GIT_CONFIG_GLOBAL` does.
- gitx is the single owner of git execution, enforced by a lint test. Every call passes
  `-c maintenance.auto=false` on its own argv instead of persisting config, so a
  user's own git keeps maintaining their repos.
- gitx drops an inherited `GIT_DIR`, `GIT_WORK_TREE`, `GIT_COMMON_DIR`,
  `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY` and `GIT_ALTERNATE_OBJECT_DIRECTORIES`
  from every call (names matched case-insensitively, as Windows resolves them),
  so git always finds its repository from the working directory jig names. A git
  hook exports some of these (`GIT_INDEX_FILE`, and `GIT_DIR` in a bare or
  server-side repository) and a user can export any of them; a jig started with
  one set ran every call, a pool lease's `checkout -B` included, against that
  other repository. A caller's own env entries still apply, and `GIT_CONFIG_*`
  is kept, since users and CI set it on purpose. `cmd/jig` also clears the same
  variables from its own process at startup (`gitx.ClearRepoEnv`), so a
  session, an oracle or an env class command it starts inherits none of them:
  a slice agent's own commits would otherwise land in the other repository and
  the slice would fail with its commit missing from the lease. The per-call
  filter stays for any caller that does not start from `main`, tests included.
  `TestRunIgnoresInheritedRepoEnv`, `TestClearRepoEnv` and
  `TestAcquireIgnoresInheritedGitDir` pin it.
- Long-lived repos (the store after a push, a pool lease after a reuse fetch) get a
  foreground, best-effort `git maintenance run --auto`. The per-call flag only stops
  commands from spawning detached maintenance, not this explicit run;
  `gc.autoDetach=false` keeps its gc child in the foreground on git older than 2.54.
- Measured effect. The same 3,200-run load test passes 3,200 of 3,200 with no
  environment overrides, in 171 s instead of 292 s. A traced local verifydeliver run on
  Windows spawns 3,809 git processes instead of 4,331, none of them detached maintenance
  (580 before), and takes 163 s instead of 191 s. Windows CI's test step did not change
  measurably: 656 s median over eight runs before (564-807 s), 664 s and 691 s after;
  the 15% saved on one package is inside that runner's run-to-run spread. The Linux
  test step went from 44-48 s to 38-42 s, and macOS takes 52 s.
- CI actions are on v7; the test matrix runs Windows, Linux, and macOS on
  every push to main and every pull request; govulncheck runs on the Linux leg.
- `lint/workflow_test.go` parses `.github/workflows/{ci,release,smoke}.yml` with
  `yaml.v3` and asserts the invariants that have already bitten or must hold,
  checking shape (triggers, job dependencies, matrix legs, key steps present)
  rather than exact string content, so a routine workflow edit does not churn
  the test.
- Windows Defender exclusions were considered for Windows CI time and dropped: GitHub's
  Windows runner images already turn real-time scanning off and exclude the C: and D: drives.

## Release and install

- The version `jig version` prints comes from Go's build-info VCS stamping, not
  ldflags: the exact tag when HEAD sits at that tag with a clean tree, otherwise a
  Go pseudo-version (`v0.0.0-<timestamp>-<commit>` before any tag exists,
  `vX.Y.(Z+1)-0.<timestamp>-<commit>` once one does), with `+dirty` appended by
  Go itself when the tree carried local modifications, and `(devel)` when build
  info is missing (a `-buildvcs=false` build, or `go run`).
- GoReleaser archives are named `jig_<os>_<arch>` with no version segment, so a
  "latest" download URL stays stable release over release instead of changing with
  every tag.
- Both install scripts verify the downloaded archive against `checksums.txt`
  (SHA-256) before extracting, and install to a user directory with no sudo or
  admin rights. `JIG_RELEASE_URL` overrides the base URL for mirrors and for
  testing against a local or snapshot build; `JIG_VERSION` pins a release tag.
- `release.yml` runs the full test matrix through `ci.yml`'s `workflow_call`
  trigger, checks the built binary reports the tag exactly, publishes with
  GoReleaser, attests build provenance, then smoke-tests the installers and
  `go install` as a `needs:` job - a release published with the default
  `GITHUB_TOKEN` does not fire `release: published`, so the smoke test cannot be
  a separate trigger on that event.
- The GoReleaser snapshot dry run and the installer checks against it run only on
  manual dispatch of `ci.yml`, keeping every push and pull request fast while still
  giving a way to validate the release pipeline before tagging.
