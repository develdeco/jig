# DECISIONS - jig v0.1 build log

A log of judgment calls made while building v0.1: one entry per decision, covering what
was ambiguous, what was chosen, and why.

## Testing

- No test in `internal/` edits process-global state (environment, working directory,
  or package-level variables): dependencies come through `Deps` fields or function
  arguments, TestMain may set the process once before tests run, and
  `lint.TestNoGlobalStateEditInInternalTests` enforces this as a ratchet. A debt list
  in the lint names today's offenders outside `internal/verifydeliver`, which has none;
  the list only shrinks as packages fix their own entries. This allows every test in
  package `verifydeliver` to run in parallel, cutting its wall time from 763 s toward
  400 s on the dev machine. The two git identity tests are the one exception the ratchet
  still has to make room for: they must prove identity resolves from somewhere other
  than the ambient identity every other verifydeliver test's `TestMain` pins process-wide
  for determinism. A first pass gave them their own test binary
  (`internal/verifydeliver/identitytest`), whose `TestMain` pinned no identity at all, but
  that duplicated package `verifydeliver`'s own build/gate test harness verbatim - a second
  copy only one of which the package's other tests exercised, free to drift from frontier's
  contract unnoticed. They live in package `verifydeliver` instead, on that one harness:
  `Deps.GitEnv`, nil by default, replaces the environment `Publish`'s own identity
  resolution runs under when a test sets it, so the two tests hand it the ambient
  environment with the pinned identity stripped out, rather than unsetting that pin with
  `t.Setenv`/`os.Unsetenv` for their own span - which would also rule out `t.Parallel()`
  for them, since `t.Setenv` panics in a parallel test.

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
  `REVERT_HEAD`, `sequencer` or `BISECT_LOG`, looked up in the store's git
  directory in process, or with one `git rev-parse --git-path` call when
  the store is on the git program), when HEAD is detached (a bisect, say, so
  a commit would land where `git bisect reset` drops it), or when the index
  has unmerged entries, which is what a conflicted `git stash pop` leaves
  behind on its own. An unconditional `git add -A`
  would stage unresolved conflict markers as ordinary content, and a later
  commit (or `rebase --continue`) would finalize them onto the store branch,
  corrupting whatever file conflicted for every later reader. Both `Sync` and
  `Push` run the check first, so it also guards a command that only ever
  `Push`es, such as `jig requeue`, not only the ones that `Sync` first. It runs
  once per call: their shared `stageAndCommit` step ran it again with only
  reads in between, two more git processes per store write, 1,408 of the
  suite's 17,776 git calls in a traced Linux run.
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
- `<ticket>/ticket.yaml` is now the ticket's one record, not only its blockers:
  `title`, `branch` and `blocked_by`, all optional, under `schema_version: 1`.
  A minted ticket's record is created whole, in one write, by
  `Store.CreateTicketRecord`, which refuses an id that already has one
  (`ErrTicketRecordExists`) instead of merging into whatever ticket claimed
  it. Every later update goes through one read-modify-write under the store
  lock (`Store.mutateTicket`, reached through `Store.WriteTicketBranch`), so
  setting one field never drops another that was already recorded - the
  full-file replace the old `WriteTicketDeps` did would otherwise silently
  lose whichever field the caller was not writing. That writer, and the
  `WriteTicketTitle` that would have joined it, are gone: a title and
  blockers are written once, whole, when the ticket is minted, nothing
  updates them afterwards, and an update no command calls is not kept for a
  later change to find. Decoding is strict (`yaml.Decoder.KnownFields`): an
  unknown key is refused rather than silently dropped the next time anything
  on the ticket is written. The refusal is an `*axi.Error`
  (`TICKET_RECORD_INVALID`) whose help names both causes, since the read
  cannot tell them apart: upgrade jig if a newer version wrote the key, fix
  it if it is a typo. Only keys are guarded: a rewrite drops YAML comments
  and any second document, neither of which a file jig wrote has.
  `ReadTicket` also refuses a `schema_version` greater
  than `ticketSchemaVersion` (`TICKET_SCHEMA_UNSUPPORTED`, help: upgrade
  jig): an unknown field alone would not catch a future jig repurposing an
  existing key's meaning under a new version, and since every write reads the
  file first, refusing here also stops a write from re-marshaling such a file
  back out at today's `schema_version`, silently downgrading it. Both
  refusals reach a build lease, gate round or publish unchanged through
  `Store.TicketBranch`, with their own code and next step.
- `Store.TicketBranch` is the one place that resolves a ticket's working
  branch: the recorded `branch`, or `jig/<ticket>` when none is recorded. It
  replaces every hardcoded `"jig/"+ticket` across frontier, gate, reconcile
  and publish (`internal/verifydeliver/verifydeliver.go`'s old `ticketBranch`
  helper is gone; test files keep a same-named literal-only helper that names
  the default branch in test setup and assertions). `Publish` and `Gate`
  resolve it once, at the top, and hand the name to the lease, the fetch from
  the build lease and `reconcile` (which takes the branch, not the store): a
  `ticket.yaml` that changes while a command runs cannot make it reconcile
  one branch and push another. `frontier.Run` does the same on its first
  slice attempt (not up front, so a run with nothing on its frontier never
  reads the record): every slice of one run, across its concurrent repo
  groups, builds on that one branch. `Store.TicketBranch` also takes `target`
  and refuses a recorded branch equal to it (a comparison of names:
  `refs/heads/main` passes it, and git refuses the push later as ambiguous),
  or one git itself would reject as a ref name (`git check-ref-format
  --branch`), or one git only expands: `@{-1}` names the store's previously
  checked-out branch, so the name `check-ref-format` prints must equal the
  recorded one, or the same store content would pass on one machine and fail
  on another. Both checks are of names, so a spelling git resolves on its own
  terms passes them: `refs/heads/main` for the target, and `@`, which git
  reads as `HEAD` wherever it parses a revision (a checkout of it stays
  where it is), for the name check. Reconcile's merge and publish's
  guardedPush both trust whatever this resolves, and landing straight on
  target - skipping the PR - is the one failure mode worth refusing at the one
  place every caller already goes through, rather than trusting it silently.
  `jig validate` runs the same check against the primary repo's target, whose
  "main" default now lives in one place, `project.Repo.TargetBranch`, instead
  of the copies in frontier and verifydeliver and a third `validate` would
  have added. A `ticket.yaml` it cannot read is reported once, with the
  refusal's own next steps listed under it (`ReadTicket`'s help names the fix,
  which the message alone does not). Nothing writes `branch` yet through this
  path - `WriteTicketBranch` has no caller in v0.1, branch adoption is a later
  change - so today this only guards a hand-edited or otherwise externally
  written `ticket.yaml`.
- `jig ticket new` creates the ticket's `<ticket>/ticket.yaml`, with its
  title, for every tracker, right after minting, through
  `Store.CreateTicketRecord`. Creating it also creates the ticket's store
  folder (the write's lock and `Store.AtomicWrite` both make a file's parent
  directory), so a tracker whose `Mint` creates no store folder of its own
  (github, command) gets one: a GitHub-tracked ticket used to fail
  `requireTicket` until its brief was written by hand; `ticket new` alone is
  now enough. Because a tracker's id is sometimes known only once the ticket
  already exists there (github, command), a refusal or a store write that
  fails after that point can't be retried by minting again without creating
  a duplicate tracker ticket - so an id jig cannot use (`tracker.CheckMinted`,
  a reserved suffix or a path-like id), a record-write failure, and a mint
  that collides with a `ticket.yaml` some earlier, unrelated write already
  left for that id (`ErrTicketRecordExists`) are each reported with the
  tracker and id named and what to do about the ticket that now exists there
  ("close it there", "do not mint again"). The collision is keyed on the
  record, not on the ticket folder: a folder with no `ticket.yaml` is merged
  into, its record created beside whatever it holds (a `brief.md`, a
  `slices.yaml`), nothing there changed or removed. The record is what claims
  an id; a folder alone claims nothing, since the intake skill writes one for
  a ticket that exists only in its tracker and every ticket minted before jig
  recorded titles has one without a record. Refusing every existing folder
  instead would need to know which trackers' `Mint` create the folder
  themselves (the local one does, before the record is written), a
  per-tracker case this avoids.
- `jig graduate` mints into the store under the same two rules, so a minted
  ticket gets its record under one rule whichever command minted it.
  `tracker.Graduate` runs `tracker.CheckMinted` (the check `jig ticket new`
  runs) right after each mint, before anything is written under the id: a
  reserved suffix or a path-like id used to get a record too, the latter
  outside the store. It then calls `Store.CreateTicketRecord` once, with the
  title and the resolved blockers, and an id that already has a `ticket.yaml`
  is refused, not merged into, which would overwrite the other ticket's title
  and hand the new one its blockers. Both refusals give the operator the
  recovery `jig ticket new` gives: the collision has the same message and help
  (`mintedIDCollision`: the tracker ticket exists, the record belongs to
  another ticket and must not be deleted, resolve it by hand), not the orphan
  help written for a folder graduate created itself ("put the id on the
  entry, or delete that ticket folder"), which here would delete another
  ticket's brief or bind the entry to it. The record also lets
  `consolidatedTitle` (`internal/verifydeliver`) find a title for a graduated
  ticket instead of falling all the way back to the bare id. A blocker-less
  entry now gets a `ticket.yaml` too (title only, no `blocked_by`), which the
  two tests pinning it absent were updated to expect. A graduate failure that
  already carries its own next steps (an id jig cannot use, an id that could
  not be written into the chart) keeps its code and help and names the
  entries this run already created, as every other graduate failure does, so
  the operator sees what a re-run continues after. The drift advisory for an
  existing entry whose `ticket.yaml` cannot be read carries the refusal's own
  next step, as `jig validate` does (upgrade jig for a newer schema), instead
  of always saying to edit the file, which would be the wrong step for a
  record a newer jig wrote.
- `Publish`'s PR title (`consolidatedTitle`) falls back to the ticket's own
  recorded title, then the ticket id, when the first slice has no goal to
  offer - no slices yet, or a first slice with an empty goal. Previously it
  fell straight to the ticket id. `Publish` reads the record once, before it
  writes anything to the store, and takes both the branch
  (`Store.ResolveTicketBranch`, the resolution `TicketBranch` runs after its
  own read) and this title from that one read: a record it cannot read fails
  there, rather than after the memorize commit and changelog writes, and one
  that changes while publish runs cannot give the branch and the title two
  different snapshots.

## Chart handover

- `schema_version` stays 1 for both new files (`charts/<name>/tickets.yaml`,
  `<ticket>/ticket.yaml`): each is optional and purely additive, so a store
  written with them still reads exactly as before on an older jig, which
  never looks for either file. `ReadTicket` now checks `ticket.yaml`'s
  version and refuses a newer one (see the ticket record entry under
  "Store"); nothing checks `tickets.yaml`'s yet - that check is left for
  whichever later change actually needs it to gate compatibility.
- Duplicate-ref detection (`resolveChartEntryRefs`, "one entry must not list
  the same ticket twice") compares by resolved target identity, not by a
  ref's literal spelling: a direct ticket id and a `#k` that already
  resolves to that same id are the same target and both fail the check, the
  same as two literal `#k`s repeating each other or two entries still
  pending graduation that would end up pointing at each other. The brief's
  own wording ("the same ticket") reads as identity, not text.
- Every ref in an entry is checked for a bad `kind` before its shape (`#k`
  range, self-reference, cross-chart existence, or a missing ticket id): a
  ref that is both malformed and carries a bad kind is refused for the kind
  first. The brief left the precedence open; kind is a property of the ref
  regardless of what it resolves to, so it is the cheapest and most
  ref-independent thing to check first.
- The post-graduation drift advisory (comparing a chart entry's resolved
  blockers against that ticket's own `ticket.yaml`) treats the two lists as
  sets of (ticket, kind) pairs, order-insensitive: reordering `blocked_by`
  entries in the chart file, or in `ticket.yaml` by hand, is not itself a
  drift. The brief specifies "when they differ" without saying whether
  order counts; nothing in either file's semantics makes blocker order
  meaningful, so it was left out of the comparison.

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
- A Windows 8.3 short-name spelling (a runner's `RUNNER~1`) matches no rule, however
  the rule itself is spelled: the CLI checks an edit against the rules as the session
  spells its target, and denies every edit to such a path (2.1.232 locally, 2.1.284 on
  GitHub's Windows runners), while it allows the same file spelled long and matches a
  long name holding a literal `~` as usual (2.1.232). So the backend hands a session the
  long spelling of what it grants (`sessionView`): its working directory, its input and
  result paths, and the prompt's mentions of them. GitHub's Windows runners reach every
  test through such a temp dir, and a machine whose store, jig home or working directory
  goes through a short name would reach its sessions the same way. Beyond spelling out
  8.3 names, GetLongPathName only corrects the case of a name short enough to be one,
  which rule matching ignores; a relative path is left alone, and a symlink or junction
  keeps both of its spellings in the rules, as above. The gate reads the paths a
  reviewer reports back through either spelling of its lease (`relativizeReviewedPath`
  compares them resolved when they do not match as spelled), since the reviewer now
  sees the long one.
- The CLI contract test is opt-in (`JIG_LIVE_CLAUDE=1`), not part of `go test ./...`: it
  runs whichever CLI version is installed, so its result is not reproducible run to run.
  CI runs it in a job of its own, which installs the CLI (under "Git execution and CI").
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
  the round succeeds. (Superseded: see Intent provenance below -
  `RoundInput.BriefPath` is retired for `RoundInput.Intent`.)
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
  uncommitted until the next command's own `Store.Sync` swept it into an
  anonymous commit (`"jig: record uncommitted store state"`) - not lost
  (`Sync` already stages and commits any uncommitted store state before it
  pulls), but unpushed and unattributed to this round's failure until
  whatever command happened to run `Sync` next, on this ticket or any
  other. `Gate` now runs a deferred, best-effort `Store.Push` (a message
  naming the ticket, the round and the failure) on any error once the
  `gate-open` journal line has been appended, whatever the failure, so the
  store is committed and pushed under that failure's own name immediately,
  with no dependence on a later command's `Sync` sweep. The decisive e2e
  test (three real gate rounds through the fake backend, in-process
  through `cmd/jig`'s `Main`) asserts the store is clean and pushed right
  after its deliberately broken round 1 attempt.
- Every error a reviewer round can return after the `gate-open` journal
  line (`REVIEW_INVALID`, `REVIEW_FAILED`, `GATE_NO_ORACLE`) carries a
  `Help` line naming the recovery (fix the input, or add an oracle, then
  rerun `jig gate` for the ticket), on top of the best-effort push above.
- `Publish` had the same gap `Gate` once did: it journals its reconcile
  outcome, then revalidate, memorize, changelog, squash, pr, route and
  publish-done, and pushed once at the end with no equivalent deferred
  push. Before this fix, any exit after that first journal line -
  reachable simply by declining at `Publish`'s own confirmation prompt,
  not only by an outage - left those writes uncommitted until the next
  command's own `Store.Sync` swept them into that same anonymous,
  unattributed commit, unpushed until whatever later command happened to
  call `Store.Push`. `Publish` now runs the same deferred, best-effort
  `Store.Push` `Gate` does, once its own earliest tracked write to the
  store - `recordAndCheckDivergence`'s `reconcile` line, appended before
  its `PUBLISH_NO_DIVERGENCE` check runs, so that error path counts as
  journaled too - has landed, committing and pushing under this failure's
  own name at the point of failure instead of leaving it for a later
  `Sync` to sweep anonymously. `journaled` is set once, at that `reconcile`
  line, and never cleared, so pinning it true at the earliest point it can
  matter, a middle point, and the tail fixes it true everywhere in
  between: the in-package tests cover `PUBLISH_NO_DIVERGENCE` from
  `recordAndCheckDivergence` itself (the earliest point), `PUSHED_RANGE`
  from `squash` (in between), and a declined prompt and a `guardedPush`
  failure (both well after the `reconcile` line, at the tail) - each
  asserting the code-only subject and the pushed tip. `failureCode`'s own
  branches (a plain error, an `*axi.Error` with no code, one wrapped by
  `fmt.Errorf`) are pinned separately, as a pure function, by its own
  table test rather than paying for another fixture and gate rounds here.
  The decisive e2e test runs `jig publish` through the real binary with no
  `--yes` and no scripted stdin - the same empty answer a declining
  operator's Enter would give - then asserts the store is clean, pushed,
  and carries that exact subject.
- `Gate`'s failure commit's subject is built from the error's `axi` code
  plus the ticket and round, never the error text: the message is
  permanent store history and gets pushed, and a raw error carries
  whatever the failure happened to contain, including absolute paths on
  the machine that ran it. The full error still goes to stdout, where it
  is read once and not kept. The round number is resolved before the
  `gate-open` journal line for the same reason: a failure between
  journaling and resolving it used to record "round 0", a round that
  never existed. Both `Gate` and `Publish` extract that code the same
  way, through the shared `failureCode(err)` (`verifydeliver.go`) -
  the error's own `axi.Error` code, or `INTERNAL` when it has none -
  so the axi-code-only rule can't quietly drift between the two.
- `Publish`'s own failure commit subject is `<ticket>: publish failed:
  <code>` - the same `axi`-code-only rule as `Gate`'s (via the shared
  `failureCode`), minus the round: `Publish` is not round-scoped the way
  `Gate` is.

Intent provenance (see [ADR 0012](docs/adr/0012-intent-provenance.md)):

- `gate/round-N/spec-input.md` is retired outright, and with it `--doc`'s
  old behavior of copying its file's content there: the round-scoped spec
  axis input it existed for is gone now that intent is resolved once per
  ticket (`intent.md`, ticket-scoped like `brief.md` already is) rather
  than reconstructed per round. `--doc` on a `--branch` ticket now does what
  `--intent` does - writes an explicit `intent.md` - never a round-local
  copy.
- `--doc` on a ticket that already has a `brief.md` used to be silently
  accepted and change what the reviewer read, in `--branch` mode; it is
  now refused with `INTENT_CONFLICT`, the same as `--intent`, since a
  ticket's intent resolution always prefers `brief.md` when one exists -
  writing an `intent.md` beside it would record a provenance jig would
  never actually read.
- `writeExplicitIntent` checks `brief.md`'s existence, not its content,
  before ever writing `intent.md` or reading `--doc`'s file: the conflict
  check is structural (a file present or absent), never a comparison of
  what either file says.
- `report.yaml`'s `intent` block records the sha256 of the exact bytes of
  the file at `intent.path`, not the text itself - the text already lives
  in `brief.md` or `intent.md`, both already in the store, and
  duplicating it into every round's own report bought nothing. The hash
  is empty for source `"none"` rather than the hash of an empty string,
  since `"none"` means no text was read at all, not that empty text was.
- The prompt names what `"brief"`, `"explicit"` and `"none"` each mean
  once, generically, rather than branching on this round's own resolved
  source: it already points the reviewer at `review.json`'s own `intent`
  block for the actual value, so the prompt's job is only to say what
  each label means.
- `resolveIntent` reads `brief.md`'s or `intent.md`'s exact bytes once
  and returns them alongside the resolved `{source, path}`, so `Gate`
  never reads the same file twice to compute the report's sha256
  separately from what it handed the round's source.
- `resolveIntent` reads `intent.md` as strictly as `writeExplicitIntent`
  writes it. A body with no text in it (empty or whitespace only, one
  shared rule) is `INTENT_EMPTY` on read too, and a front matter that does
  not parse is `INTENT_INVALID`.
  These two and `INTENT_INVALID_SOURCE` all carry the same help, to rerun
  `jig gate` with `--intent` or `--doc`, which replaces the file. Only a
  hand edit of `intent.md` reaches any of them. The CLI's refusal of a
  set-but-empty `--intent` or `--doc` uses the same `INTENT_EMPTY`, so an
  empty flag and a whitespace-only one are one mistake with one code.

Intent inference (see [ADR 0012](docs/adr/0012-intent-provenance.md)'s own
amendment):

- `internal/intent` knows nothing about the store, sessions, or dispatch: a
  `Reader` discovers `Session`s (Claude Code today, `NewClaudeReader`),
  `Best` scores and picks one against a diff file list, `RenderExcerpt`
  renders its text. `internal/verifydeliver/intent_infer.go` is the only
  caller that wires those into a live dispatch, so the matching algorithm
  stays testable with plain structs and no store fixture at all.
- Repo identity is a git common-dir comparison
  (`internal/gitx.CommonDir`, a new function on the package that already
  owns every git call - `internal/intent` imports it directly rather than
  shelling out itself, same as every other package does), never a remote
  URL: a remote-based comparison would let a jig lease clone of the
  operator's own remote match, which is exactly the false positive this
  design refuses. The two common dirs are compared with `gitx.SameDir`
  (file identity, `os.SameFile`), not as strings: `CommonDir`'s symlink
  resolution leaves a Windows junction or a subst drive as a different
  spelling of the same directory.
- The matcher's `mentionMatches` treats a bare-basename diff file (a
  repo-root file, no directory component) specially: only an equally bare
  mention can match it, never a mention merely ending in `/<basename>`,
  which would otherwise let a nested file of the same name (a different
  file entirely) stand in for the repo-root one.
- Every `*.jsonl` under a session's own `<session>/subagents/`, at any
  depth (a workflow's own subagents nest one level deeper, under
  `subagents/workflows/<wf-id>/`), is read and folded into its parent
  `Session` inside the reader, not returned as a session of its own: an
  implementer subagent does the actual editing the matcher needs to see,
  and the fold means the matcher and excerpt renderer need no special
  case for it at all.
- `RoundInput.OperatorClone` is resolved through the same lookup
  `identityDir` already used (`operatorClone`, extracted from it rather
  than duplicated), returning `""` with no leaseDir fallback: unlike
  `identityDir`, which needs some directory to commit from either way,
  inference has a real "there is nothing to compare a session against"
  case, and must fail open to it rather than silently comparing against
  the gate lease.
- `Gate` resolves intent once, before the round's source runs (so the
  reviewer source knows whether to even attempt inference), and hands the
  source the intent and the exact bytes of its file (`RoundInput.Intent`,
  `IntentText`). A reviewer round's `Review` carries back the intent it
  dispatched with (`Review.Intent`, `IntentText`: the resolved one, or the
  one `inferIntent` just recorded), and that is what Gate reports and
  hashes, so a freshly inferred `intent.md` reaches this round's own
  `report.yaml` and the report can never disagree with `review.json`. An
  earlier draft re-ran `resolveIntent` after the round instead; that let
  anything writing `brief.md` or `intent.md` during the round (a session's
  screened shell is not a boundary) change the report away from what the
  reviewer was given. The scripted source runs no reviewer, so its round
  keeps the resolution made before it.
- A user record in a Claude Code transcript is the developer's own words
  when its `origin` says the human wrote it or it has none (older
  versions), and never when it is `isMeta`, a compaction recap
  (`isCompactSummary`), transcript-only (`isVisibleInTranscriptOnly`) or
  attributed to someone else (a task notification, a peer session, a
  workflow coordinator). Those are fields of the record, so no list of tags
  or phrases is kept. Counted over one machine's transcripts, the records
  with no origin still include slash-command echoes and command output,
  which no field the reader uses distinguishes from a typed prompt; they
  stay in the excerpt and the summarizing model judges them, since telling
  them apart by their text is the enumerated filtering this design
  refuses. The filter runs on a subagent's own file as well: a
  coordinator's message to a subagent is dropped, and the parent's prompt
  to it, which has no origin, is kept and labelled. Keeping every prompt a
  subagent file holds would need the loader told which kind of file it
  reads, so one rule applies to every file.
- The summarizer does not run in the lease. It has to read the excerpt and
  write its result and nothing else, so its session's working directory (the
  dispatch's `Worktree`) is a fresh, empty scratch directory under the jig
  home (`home.IntentScratchDir`), and under the headless backend its edit
  grant, which follows the working directory, covers that directory and the
  dispatch's own `result.json`. The alternative, running it in the lease and
  cleaning up after it (reset to head, then list the lease's ignored paths
  before and after the dispatch and remove every new one), cannot be made
  safe. Git for Windows lists what lies behind a junction as the lease's own
  files, so removing the new ones follows a junction out of the lease -
  deleting a developer's files, or tracked files of the lease reached
  through a link an install had made - and the walk still misses what `git
  clean -fd` skips, an untracked nested repository. Cleaning up what a
  session leaves in the code under review is a walk jig cannot make
  complete or safe, so the session is kept out of that directory instead,
  and there is nothing of its to clean out. This is not confinement: the
  headless backend is not a security boundary
  ([ADR 0008](docs/adr/0008-headless-permission-model.md)), so a session can
  still reach the lease through its shell, and jig does not chase what it
  leaves there.
- The scratch directory is one per dispatch (`os.MkdirTemp` under
  `IntentScratchDir`, owner-only), so two gates never share one and another
  dispatch's directory, or a crashed run's, is neither seen nor removed (a
  test holds two dispatches inside their sessions at once and checks that
  neither sees the other's file). It is removed by a deferred
  `os.RemoveAll`, so every way out of the attempt removes it. `RemoveAll`
  is used because it removes a symlink or junction
  itself and never follows it, whatever the session put in the directory (a
  session's shell can plant a link there however it was granted its edits):
  probed on Windows with junctions and symlinks, it removed a link at any
  depth, a link to a directory that holds a link, a link to the lease, and
  a link standing in the directory's own place, each as the link itself,
  and left everything they point at. Tests pin that, with junctions on
  Windows and symlinks elsewhere, so a removal that followed links would
  fail them. Best effort, like removing the result file: a directory that
  cannot be removed does not fail a round that failed open.
- The dispatch sets `NoSessionPersistence` (a field of `session.Dispatch`
  that no other dispatch sets), which the headless backend turns into
  `claude -p --no-session-persistence`. `claude -p` saves every session
  under `~/.claude/projects/<encoded cwd>`, and the summarizer's working
  directory is a fresh random one per dispatch that jig then deletes: each
  dispatch would leave a new project directory in the operator's Claude
  Code data, named after a path that no longer exists and holding the
  excerpt as the session read it, and nothing would ever remove it. The
  excerpt stays under the jig home, where a failed or disputed inference
  can be checked against it. The fake backend runs no session and herdr's
  agent is an interactive session started without flags, so both accept the
  field and ignore it (each says so where it does, and a test pins that
  neither changes what it does for it). The headless argv is pinned with the
  field set and unset, and tests pin that the reviewer's dispatch and a
  build dispatch leave it unset.
- The lease is still checked around the summarizer, with the reviewer's own
  check (HEAD and the tracked tree; untracked files are not counted). A
  change, or a check that could not be made, puts the lease back with
  `resetLeasePristine` (`git reset --hard`, then `git clean -fd`), the
  recovery `Gate` and the reviewer's own round use, and fails the inference
  open: a summarizer that broke its read-only rule has no summary worth
  trusting, and the reviewer is not blamed for a change that was not its
  own. The reason reported is the dispatch's own error when there was one,
  else that the summarizer changed the lease. A lease the check confirms
  unchanged is left alone, exactly as it is: nothing of the summarizer's is
  in it, and a reset there would only be jig's own `git clean -fd` walking a
  lease no one had a reason to change - removing an untracked file or link a
  session's shell planted there and, on Windows, possibly following such a
  junction out of the lease. A test pins that with a summarizer that only
  plants an untracked file and a link. That junction behavior belongs to
  `resetLeasePristine`, the restore every round shares: a known gap of it,
  tracked separately from inference, which the inference's restore neither
  adds to nor closes. The restore failing is the one hard error, since a
  round cannot safely dispatch a reviewer onto a lease that might still be
  dirty.
- `intent.result.json` is removed on every return that does not record it
  as `intent.md` (a deferred remove, cleared by the one success path)
  rather than on each rejection path in turn: the store's push commits
  whatever is under `work/`, and enumerating the paths that reject a
  result is how one of them was missed.
- Every fail-open reason that comes from an error is built by one helper
  (`noIntentFor`: what failed, then the cause), never by formatting the
  error at its own call site. Eighteen steps can fail that way and a test
  can provoke only some of them (a commit time git cannot read, or an
  `intent.md` that cannot be read back, has no cheap way in), so a note
  that drops its cause would go unseen at exactly those; one helper with
  one test holds the rule for all of them.
- The summary a summarizer may return is capped (4 KiB) and refused when
  over, as every other malformed result is, not truncated: a result far
  past the few sentences asked for is the excerpt echoed back, and
  truncating it would still put transcript text in a store file.
- Inference reads and writes under two roots, and takes both as explicit
  inputs rather than reading the environment: `RoundInput.Home` (the jig
  home, where the excerpt is written) and `RoundInput.UserHome` (the
  operator's own home, where their local agent transcripts are read), from
  `Deps.Home` and `Deps.UserHome`. `cmd/jig` resolves the operator's home
  once, beside the jig home. One that could not be resolved is a named
  reason to skip inference, never a search somewhere else, and no
  inference test sets an environment variable, so all of them run in
  parallel.
- `work/intent.json`/`work/intent.result.json` are not round-numbered
  (unlike `gate.round-N.review.json`): inference succeeds at most once per
  ticket - it re-attempts on every brief-less round until one succeeds,
  since a success writes `intent.md` and every later round's
  `resolveIntent` then short-circuits before ever reaching the reviewer
  source's own inference code, but a failed attempt leaves nothing to
  reuse and so is retried - so a fixed pair of scratch names is enough.
- `parseIntentInferResult` is `ParseReviewResult`'s own strictness -
  exactly one object, no duplicate key, no unrecognized key, `"summary"`
  present and non-null - reused only for `duplicateObjectKey`, rebuilt
  for the rest, since a one-field schema does not need
  `ParseReviewResult`'s case-variant-key machinery (a lone `"Summary"`
  with no `"summary"` beside it is already caught by the manual key-name
  loop before the strict decode ever runs).
- The fake backend distinguishes an intent dispatch from a review one by
  `Dispatch.Slice` (`"intent"` vs `"gate"`), plays back
  `gate/round-N/intent-result.json` verbatim - the same shape
  `review-result.json` already has - and errors loudly on a scenario with
  no coverage for it, exactly like a missing `review-result.json` does.

## Gate demo

See [ADR 0014](docs/adr/0014-demo-session-at-the-gate.md) for the contract, the
best-effort semantics, and why the media live under the jig home. What the code
raised beyond it:

- The demo is not part of `GateSource.Round`. `Gate` asks the source for a
  `Demo` method by interface (`DemoSource`) after the round's final store push,
  so the scripted source is excluded by having none, not by a flag or a check on
  its type, and the round's own writes are already pushed when the session
  starts.
- `demo.yaml` has two statuses, `recorded` and `refused`. A dispatch that
  errored or wrote no result is `refused` with a failure code as its reason
  (see below), and a malformed result is `refused` with jig's own words, not a
  third `failed` status: one vocabulary for "no demo", one reason field to
  read. `existing` is only a `GateReport` status,
  for a round that ran no demo because an earlier round holds one.
- Only a `recorded` demo ends the retries for a head; a refused one does not.
  The alternative, any `demo.yaml` naming the head, would strand a head with no
  demo after one transient failure (a timed-out session, a backend that was
  down), with no way to try again short of a new commit. As chosen, gating again
  is the retry, and each retry is an operator's own command, so it cannot loop.
- The demo's `base_sha` is the merge base with the target (`resolveFullBase`,
  the same base a full review starts from), never the round's delta base: a demo
  shows the whole change, and a delta round's base is only the previous review's
  head.
- A refusal reason is one line of at most 400 characters (`demoReason`): an
  operating system or git message it quotes can run long, and `demo.yaml` is a
  manifest, not a log. What the demo session or its backend said is not in it
  at all (a bullet below).
- The demo is not journaled. The journal records a gate round's own events
  (`gate-open`, `gate-round`, `gate-clean`), not the reviewer session's dispatch,
  and `demo.yaml` is the demo's own record, written per round beside `report.yaml`.
- A result must give every file a non-empty caption and the demo a non-empty
  summary, even with no media: the summary is what says why nothing is visible,
  and a caption is what a reader sees beside a file. Both are structure jig can
  check without judging what either says.
- An attempt starts by clearing `media_dir` and making it fresh, notes which
  directory that is, and checks afterwards that `media_dir` is a plain
  directory and the same one (`os.SameFile`). The plain-directory check alone
  follows every parent, so a directory above `media_dir` that a session swapped
  for a link left the demo, and the rename after it, outside the evidence tree
  (and, with a link into the store's working copy, committed into the store).
  `os.RemoveAll` does not follow a link, so a link or junction a session left
  is removed as itself and never what it pointed at; a test pins that the
  target survives.
- The identity check only sees a swap made during its own attempt. A swap that
  stays would send the next attempt's `RemoveAll`, `MkdirAll` and media through
  the link (`RemoveAll` on a path below a junction deletes what the junction
  points at), so an attempt first requires the store id and ticket directories
  above the head's to be plain directories (`plainEvidenceParents`) and refuses
  the demo, dispatching no session, when one is not. The head directory is left
  out on purpose, since clearing removes a link there as itself, and
  `<jig home>/evidence` itself is the operator's own to link. The refusal names
  the directory by its own name.
- Nothing jig itself writes for a demo in the store names the jig home (the
  session's own words are another matter, next bullets, and so is a result
  key jig does not recognize, which a refusal quotes as the session wrote it). `demo.json` holds the
  absolute `media_dir`, which by default sits under `<user home>/.config/jig`
  and so holds the user name, and the store's git is committed, pushed and
  often shared, so `demo.json` is written beside the media, at
  `<jig home>/evidence/<store id>/<ticket>/<head sha>.demo.json`, and never in
  the store's `work/`. It is machine-local like the media, nothing reads it
  back, and being a sibling of `media_dir` it survives an attempt's clearing
  and is never seen by the prune. Ignoring it in the store's `.gitignore`
  would not cover a store created before the entry, and would leave a machine
  path one missed pattern away from history. The result stays in `work/`: it
  holds names, captions and a summary. A test walks the store's working tree
  after a recorded demo whose session writes no path and fails on any file that
  spells the jig home, raw, with forward slashes or JSON-escaped.
- The session's own words are recorded as written, and jig does not filter or
  rewrite them. A demo's result file, and the summary and captions `demo.yaml`
  copies from it, are model prose, like the reviewer's `result.json` summary,
  and the session was told `media_dir`'s absolute path, so a summary such as
  "saved two screenshots in <media_dir>" is ordinary output and names the jig
  home in the store. Two ways to make the claim "nothing in the store names the
  jig home" true were weighed and set aside: keeping the result file out of the
  store like `demo.json` (`demo.yaml` carries its content anyway), and passing
  the summary and captions through the role-naming reasons get. Both rewrite
  or hide what a model chose to say, on a guess about which strings are paths,
  and a path in prose has no fixed spelling to find (raw, forward-slash,
  Go-quoted, a WSL mount). So the guarantee is scoped to what jig writes: its
  own fields and messages. The session's words, unlike jig's capped reasons,
  are recorded unbounded, the way the reviewer's are. A test has a session
  name `media_dir` in a summary and a caption
  that are not tidy (edge whitespace, a line break, runs of spaces, more runes
  than a reason is capped at) and asserts both are recorded byte for byte in the
  result file, `demo.yaml` and the report, so that neither trimming, collapsing
  nor capping can be applied to them unseen, while every other field of
  `demo.yaml` still names no host path (`.github/SECURITY.md` says the same).
- A refusal jig composes names its own directories by role. A reason is
  committed to `demo.yaml` and printed, and the operating system and git
  messages it can quote (`GetFileAttributesEx <path>: ...`, `mkdir <path>: ...`, a
  failed rename) each name the path they failed on. `gateDemo` replaces the
  media directory, the jig home and the store's own directory with `media_dir`,
  `<jig home>` and `<store>` in every such reason before it is recorded, printed
  or capped (`leaveOutHostPaths`). It is one step at the end, so a message added
  later is covered, where wrapping each error at its own site would leave the
  next one out. It replaces absolute spellings only (as given, cleaned, with
  forward slashes, and each Go-quoted, since a message that quotes a path with
  `%q` doubles a Windows path's backslashes), never a relative path or a
  filesystem root, and only jig's own directories, so it is for the paths jig
  chose. What it cannot cover is text jig did not write and cannot enumerate the
  spellings of, which is why the demo session's and its backend's text is not
  put through it: it is not recorded (next bullet).
- A demo that failed because the demo session or its backend did (the dispatch
  returned an error, or the session wrote no demo result) records a failure code
  and never the failure's text. That text is not jig's: a backend's error can
  echo the arguments it ran with (herdr's echoed the whole prompt, and on Windows
  every path of the dispatch spelled as a WSL mount, a spelling
  `leaveOutHostPaths` does not know), a stderr tail, or a path of its own, and a
  committed reason cannot be cleaned of what jig cannot enumerate. So `demo.yaml`
  gets `the demo session failed: <code>`, the code being `failureCode`'s, the
  helper `Gate` and `Publish` use for a store commit subject for the same
  reason (the error's own code where it has one, `INTERNAL` where it has none,
  `DEMO_NO_RESULT` for a session that wrote no result), and the full text goes
  to the gate report only (`demo_detail`, beside `demo_reason`), where it is read
  once and not kept. The two kinds are told apart by a type (`demoFailure`), not
  by the wording of an error: an error is a failure when the dispatch or the
  missing result made it, and a refusal, jig's own words about a result it read,
  when anything else did, so a refusal added later cannot carry a backend's text
  by accident. Weighed and set aside: keeping the text in the reason and adding
  the WSL spelling to `leaveOutHostPaths`, which is the route of extending the
  list one spelling per backend, and would leave the whole prompt in a reason
  that says nothing to an operator.
- When a session wrote no demo result, headless and herdr each write a result of
  their own into `demo.result.json` in its place, the slice result's shape (an
  `outcome` field and a `summary`, which for a failed outcome headless extends with
  the denied tool calls and their paths). It is not a demo result that broke the
  contract: a result file whose top-level object has an `outcome` field is taken
  as "the demo session wrote no demo result", a failure with the code
  `DEMO_NO_RESULT`, and the file is removed from `work/` (`demoFailed`) before
  the round's push, since it is the backend's text and the store would commit it.
  The field decides, structurally (`backendFallback`), and no wording is read, so
  a demo result whose summary says "outcome" is recorded as any other, and a
  result a session wrote with such a field is taken the same way, being
  indistinguishable from the backend's. What the backend recorded (its outcome
  and summary) goes to the report's detail. A file a dispatch that failed left in
  `work/` is removed the same way. The backends' fallback itself is not changed
  here: it stays what it is for every dispatch, and what a backend writes for a
  dispatch that is not a slice is a change to the backends of its own. A test
  pins that what the backends write is the marshaled `outcome.Result` and has the
  field, so a change of that shape fails before a demo stops recognizing it.
- herdr's control commands are named by their subcommand in an error, never by
  their operands: `runHerdr(sub, args...)` reports `herdr agent prompt: exit
  status 1: <stderr tail>` and, for a response that is not JSON, `parse the
  response of herdr agent prompt`. The operands of `agent prompt` are the whole
  prompt and every path in it, and those of `workspace create` the worktree; the
  errors quoted them, and they reach the terminal and whatever records an error.
  The subcommand is a parameter of its own, so no caller can echo operands by
  joining them. herdr's own stderr is kept, which is what says why.
- A file the session listed is named in a reason by its index and the last
  element of the string it gave (`demoEntry`: `media entry 1 ("shot.png") is not
  a plain file name directly inside media_dir`), never by the string. The
  session is told `media_dir`'s absolute path and may list a file by it, in
  whatever spelling its backend gave it (raw, forward-slash, a herdr session's
  WSL mount), and `leaveOutHostPaths` only knows the spellings jig chose. Not
  echoing a path jig did not choose is the one rule that holds for every
  spelling; extending the list of spellings would not, and quoting the string
  with `%q` leaks a Windows jig home, its backslashes doubled. The same naming
  applies to the empty-caption refusal, which is raised before the file is
  looked at. A name that passed the plain-name check has no separator or
  colon, so the refusals after it keep quoting it whole.
- Both identity checks (the directory's, and a file's between `Lstat` and the
  read) use `lstatPinned`, which reads the file id when the `Lstat` is taken. On
  Windows a `FileInfo` from `Lstat` holds no id: `os.SameFile` opens its path to
  read one the first time it compares, after any swap, and both sides then name
  what now sits at the path. Tests swap a parent for a real junction and a file
  for another, and fail on the plain `Lstat`.
- A listed file that is empty is refused (`file %q is empty`). `gh --attach`
  refuses an empty file, and the limits exist so that a file jig records is one
  a later publish can attach.
- After the rename, every entry in `media_dir` that is not a recorded file is
  removed (an unlisted file, a subdirectory, a link, none followed), so the
  directory holds exactly what `demo.yaml` lists. The 50-file limit counts
  listed files, so without this the directory could hold more than the manifest
  says, and a link could sit in the evidence tree pointing anywhere.
- A refused demo "records none of your files": the files a session wrote stay in
  `media_dir` until the next attempt clears it, and a rename refused part of the
  way can leave staged `.jig-demo-N.tmp` names there. Nothing reads them, since
  no `demo.yaml` lists them.
- The rename is two-phase (every file to a temporary name, then to
  `demo-<n>.<ext>`) and refuses up front when an unlisted file already holds a
  final name. A session that called its files `demo-2` and `demo-1` in that
  order is ordinary, and a single-phase rename would overwrite one with the
  other.
- A file name is a plain name: no separator in either spelling, no drive or
  stream colon, listed once, and `filepath.IsLocal` (which also refuses a
  Windows reserved device name). Duplicates compare case-insensitively, since
  two spellings that differ only by case are one file on a case-insensitive
  filesystem. Hard links are not detected: one is a regular file, the same as a
  copy the session could make itself.
- The store id is the first 16 hex digits of the sha256 of the store root,
  absolute and symlink-resolved. A project name is not unique or a safe path
  component, a remote URL does not exist for a standalone store, and a stored id
  would travel with a clone that must not carry machine-local state. A Windows
  junction is not a symlink and Go does not resolve it, so a clone reached
  through one has its own id, like a clone that moved; that is documented rather
  than resolved, since resolving it takes a Windows-only call for a spelling
  that only changes where the media are looked for.
- herdr on Windows rewrites the prompt's mentions of every path of the dispatch
  (worktree, input file, result file, `ExtraWriteDir`) to their WSL mount, the
  way headless rewrites them to the long spelling (`respellMentions`, one helper
  for both), and creates the workspace at the worktree's mount as before. This
  applies to every herdr dispatch on Windows, so the reviewer and slice
  sessions there are now told WSL paths too, where they were told host
  paths. jig still reads the result at its host path, and the JSON files it
  wrote keep the host spelling of the paths they hold (`media_dir` in
  `demo.json`, the paths in `review.json`); no backend can rewrite a file jig
  wrote. herdr scopes no edits, so `ExtraWriteDir` needs no grant and is not
  passed to herdr on its own. It does not refuse the dispatch on Windows: a
  refusal there would be a platform case standing in for a mechanism that
  already exists, and would record every demo on the default backend as
  refused.
- The sentence saying what each intent source means is one constant,
  `intentSourcesPrompt`, in the reviewer's prompt and the demo's alike. The demo
  is handed the round's own intent pair, so a source jig gains has to be
  described to it too; two copies of the sentence would let one prompt lag the
  other without a test failing. A test pins that each prompt carries it, and
  another that the demo is dispatched with the very pair the reviewer was
  handed and the report shows, for a brief, an explicit intent and none; the
  inferred-intent test through the fake backend checks the same for an inferred
  one.
- `leaseChanged`, extracted from the reviewer round so the demo shares it, returns
  errors with no package prefix, and each caller adds its own, so the reviewer
  round's error text is what it was before the extraction.
- The fake backend fails on a round with no scripted `demo-result.json`, the
  way it fails on a round with no `review-result.json`, so a scenario that
  forgot its demo shows up as a refused demo instead of a silent "nothing to
  show". A scenario with no media has a result and no `demo-media/` directory.
  The `reviewer` fixture scenario scripts a demo for its clean round 3, so the
  tape and the tests that drive it show a recorded demo, not a refusal; a new
  `demo` scenario has one clean round with its demo, for the demo tape. The
  `inferred-intent` scenario scripts a demo with no media too, so its clean round
  shows a recorded demo and its summary and not a refusal.
- `jig gate --no-demo` and `jig solve --no-demo` are the only switches; there is
  no config to turn demos off, since which repos want one is the repo's own
  `CLAUDE.md` to say. The gate report prints nothing about a demo for a round
  that ran none, `--no-demo` included.
- `cmd/jig`'s `solveGateSource` is a variable only so a test can substitute a
  source that counts demo dispatches: `jig solve` with `--scenario` always
  selects the scripted source, so no scenario can reach a reviewer source through
  solve's own flags.
- The existing findings-bookkeeping tests that drive clean reviewer rounds
  through a scripted reviewer stub pass `NoDemo`: their subject is the review,
  and a demo dispatch would count as one more round on the stub.
## Adopted branches

The design is [ADR 0013](docs/adr/0013-a-ticket-branch-is-recorded.md); these
are the judgment calls the build left open.

- A recorded branch means an adopted one (`Ticket.Adopted`, the one predicate
  gate, run, solve, publish and status all ask). Nothing but adoption writes
  `branch:`, and the record is the fact the store holds; the branch's presence on
  origin is a fact about one moment and one remote, so what needs the branch to be
  there (`pool.MustExistOnOrigin` in the gate, the build and publish) asks origin,
  and what needs to know the ticket adopted one asks the record. Publish refused
  every recorded branch until it could ship one (below); the target check and the
  record's own validation (`TICKET_BRANCH_INVALID`) come first, so a bad record
  says what is wrong with it.
- `--branch` on a ticket jig already built on is refused (`TICKET_ALREADY_BUILT`)
  where it used to review origin's copy of the named branch. That review
  silently skipped the ticket's own commits, which is the mistake the refusal
  exists to make loud; the way to review another branch is another ticket. The
  store decides (`jigBuilt`, below): a `verified` journal line, a green result
  line naming a commit, or a slice in state `green`. `--branch jig/<ticket>`,
  the ticket's own default name, is an adoption like any other and is refused
  the same way once jig has built.
- "Did jig build on this ticket" and "which commits did jig build" are two
  questions, and the store answers each from a different record. The commits are
  the `verified` lines, which only this version journals. The frontier journals
  one (slice, commit, attempt) when a green result's commit passes `verifyGreen`,
  before it marks the slice green, and `journal.BuiltCommits` returns those
  commits, once each. The `result` line, journaled before verification, stays
  what the builder claimed. `BuiltCommits` used to count every `result` line
  that named a commit, on the reasoning that a claimed green whose commit failed
  verification still put a commit on the branch. That holds for a commit that
  exists (a declared artifact missing at it): it stays in the lease, and the
  retry that verifies builds on top of it, so a copy that holds the verified
  commit holds it too. It is false for a sha that is not in the lease and for the
  start sha itself, and counting them refused a ticket for good over a commit
  that never existed: the gate refused the machine with no build lease
  forever while ignoring the same journal on a machine with one. One predicate
  now serves the start sha, both arms of the gate's choice of copy, the build
  and the push-first hint: they need the commits, and only an adopted ticket
  reaches them, which only this version makes. Whether jig built at all is a
  stricter question than which commits, and is answered next.
- Adoption asks whether jig built at all, and reads it from three facts of the
  store (`jigBuilt`), any of which says so: a `verified` line, a green `result`
  line naming a commit (`journal.GreenClaims`), and a slice in state `green`.
  The path this guards is new; the state it guards, commits a released jig
  built on `jig/<ticket>`, is not, and in those versions `--branch` meant
  "review this branch once", so a user carrying an in-flight ticket across the
  upgrade reaches adoption by doing what they did before. Adoption then holds
  for good (`--branch jig/<ticket>` is `BRANCH_MISMATCH` and no verb un-adopts
  it), every later round reviews the author's branch without the ticket's own
  work, and the hint says to open the pull request for a branch that lacks it.
  Two earlier readings each let that through. The `verified` lines alone, on the
  reasoning that "the adoption path this guards is new", missed every ticket
  whose journal predates them (v0.1.x: green result lines, green slices, no
  `verified` line). Adding the slices' green state closed that, and still
  failed open after `jig requeue --from-brief-diff`, which sets every slice
  whose brief section changed back to queued, green ones included: a slice's
  state is not a record of a build, and nothing else of one was left to read.
  The green `result` line is the record. Every jig version journals it before it
  routes the result, and nothing removes it. It is a claim, not proof, so it
  decides only where there is no `verified` line to say better, and that costs
  one shape: a ticket of this version whose green claims all failed
  verification has no `verified` line either, and is now refused
  (`TICKET_ALREADY_BUILT`, with the help to mint a ticket for the branch) where
  it used to adopt. A journal from before the `verified` lines cannot be told
  from it, and a loud refusal the ticket recovers from is better than an
  adoption that is silent and permanent. The `verified` lines and the slices
  still count, each for a shape of its own: this version journals a `verified`
  line before it marks the slice green, so a run that stopped between the two
  writes has built a commit no slice shows yet, and a slice in state `green` is
  what every version writes only after its commit verified. In every journal
  jig writes the claims subsume both, so a test pins each with the other two
  facts gone. The id-based checks read the `verified` lines alone, since they
  need which commits and not whether, and the tickets they guard are adopted
  ones. An old ticket is not migrated: refusing to adopt for it is the whole
  change, and its way to review another branch is a ticket of its own, like any
  other.
- An adoption is recorded after `--intent`/`--doc` is written and the intent
  resolved, and before the round's journal line: the intent flags refuse
  (`INTENT_CONFLICT`, `INTENT_DOC_MISSING`, `INTENT_EMPTY`) far more often than
  anything after them can, and a refusal must leave no adopted branch behind for
  a later `--branch <other>` to hit `BRANCH_MISMATCH` over. The start sha is
  written before the record, so a crash between the two leaves a start sha no
  record points at, which the next adoption replaces. Adoption replaces a start
  sha that already exists: one can only be there from a dispatch that built
  nothing, since the journal has no commits.
- The start sha follows the author until jig builds. While the journal records
  no commits jig built, every dispatch records origin's tip of the branch again
  (`ensureStartSHA`), where the lease was just cut or fast-forwarded to, so a
  branch the author pushed to or rewrote after the adoption is built on as it is
  when jig starts. It used to be fixed at adoption, and a rewrite made every
  build commit fail `verifyGreen`, so the slices stalled with no stated cause.
  Once jig has built, the start sha stays: its commits descend from it, and
  moving it would disown them. It stays for an ordinary ticket from the first
  dispatch, too: the rule is for an adopted branch jig has built nothing on, and
  nothing else. An ordinary ticket's slices are committed on `jig/<ticket>`, cut
  from the target as it was, so a start sha that followed a target which moved
  between two runs would disown every commit of the earlier ones, and the next
  slice would stall on a green that did not verify. The frontier keeps it, not
  the gate, because the frontier is what reads it: a gate round is refused while
  a fix slice is queued (`--early` aside), which is exactly the state after a
  rewrite between the first round and the first build, so a remedy that needed a
  round would be one that could not run. What remains is a branch rewritten
  under commits jig built. A lease that holds them stands diverged from
  the rewritten origin, which `Acquire` refuses (`BRANCH_DIVERGED`); a
  lease cut afresh from the rewritten origin, on a machine that did not
  build them, lacks them, which the build refuses before its first dispatch
  (`BUILD_LEASE_MISSING`, below). A third refusal, `BRANCH_REWRITTEN`, for
  a lease whose branch no longer held the start sha, is gone. Every commit
  jig built descends from the start sha, so a lease that holds them all
  holds it, and its remedy could not work: rebasing jig's commits onto the
  rewritten branch in the lease that built them made new shas the journal
  does not record, and the start sha, frozen once jig built, is not on the
  new history, so the rerun failed the same way forever. The remedy is the
  divergence refusal's: merge origin's branch into the lease, which keeps
  the old tip reachable, so the commits and the start sha stay valid.
- A build lease that has diverged from origin is re-cut from origin's tip, not
  refused, when it holds no commit jig built that origin lacks
  (`pool.RecutUnlessBuilt`, which the build passes for an adopted ticket). Until
  jig has built, the branch is the author's, and a run that parked a question or
  failed leaves a lease holding the old tip: refusing it after the author's
  rewrite sent the human to merge the discarded history back into the rewritten
  branch, against the promise that a build follows the author until jig builds.
  It is the pool's rule, decided by the same sync comparison, and not "drop the
  lease's copy whenever nothing is built" as the gate does for its own lease,
  because an unverified commit in a build lease is not always the author's: a
  green whose declared artifact was missing leaves its commit there for the
  retry to fix, and dropping it would make the retry start over. So an ahead
  lease keeps such a commit, and only a diverged one - origin moved under it -
  is re-cut, discarding the unverified commits (the reflog keeps them). The
  re-cut is `checkout -B`, which like the fast-forward stops with git's own error
  over an uncommitted edit it would overwrite, and a test pins that the re-cut
  discards none. A lease that holds a commit jig built that origin lacks is
  refused as diverged: it holds jig's work, which only it has.
- Whether a diverged build lease is jig's to keep is one predicate,
  `pool.HoldsUnpushedBuilt`: a copy that holds a commit jig built that origin
  lacks is, and one that holds none is not. The build's re-cut and the gate's
  choice of copy both ask it, and the reason a refusal gives is then true at both
  sites: neither copy holds both that commit and the author's. The gate used to
  refuse every divergence, on the reasoning that neither copy holds both jig's
  commits and the author's, which is false for a lease holding none of jig's
  commits: origin holds both, and the lease holds only an attempt's leftover.
  The build went on over that state and the gate refused it, and the gate's help
  (merge origin into the lease) folded the never-verified leftover into the
  branch, which the re-cut exists to avoid. The predicate was then "holds any
  commit jig built", and its reason was still false for a lease whose built
  commits had all been pushed, with a leftover on top of them and the author's
  commits on origin: origin holds jig's commits and the author's, the lease only
  the leftover, and both the gate and the build refused it, with the same help.
  Now such a lease counts, for the gate, as a build lease that does not hold the
  branch: the round reviews origin's copy, the build re-cuts, and the
  requirement that the copy holds every commit jig built applies to it as to any
  (`BUILD_LEASE_MISSING` when jig's commits were built elsewhere and not
  pushed).
- The commits jig built must be in the copy a command works on, for the build as
  for the gate (`pool.RequireBuilt`, `BUILD_LEASE_MISSING`). The frontier checks
  its build lease's branch after acquiring it and before it dispatches, so a
  machine whose lease was cut after another machine built does not put the next
  commits on a branch that leaves the earlier ones out; verification would still
  pass them, since it looks only at the start sha. The check is for adopted
  tickets: a ticket's own `jig/<ticket>` is never on origin before a publish, so
  a second machine could not hold the first's commits by any push, and the gap
  there predates this work and claims nothing of the kind. The help is the same
  for both commands: run it on the machine that built them, or push them from
  there and this machine follows origin. The commits are found by id, not by
  patch: a human who rebases or squashes jig's commits before pushing them gives
  them ids the journal does not know, and every later round and build on that
  branch is refused until they are integrated by a merge instead, which the help
  says. Matching by patch would accept a rebase and still not a squash, and would
  make "holds the commit" a similarity judgment where it is a fact about the
  history; the refusal is loud and names the commits.
- A recorded branch must be on origin, for the build as for the gate. The build's
  `pool.Acquire` takes `pool.MustExistOnOrigin` for an adopted ticket and refuses
  with `BRANCH_NOT_FOUND` where it used to cut the branch from the target, which
  built the fixes on a branch that lacks the author's code with nothing said. The
  gate takes the same option, which replaces its own check after a `--prune`
  fetch: `Acquire` now fetches with `--prune` for every lease, since a lease's
  view of origin that keeps a deleted branch defeats both the option and the sync
  rule. The option is the caller's, not the pool's, because the ordinary
  `jig/<ticket>` is cut from the target on purpose.
- The first dispatch into a repo records the start sha where `pool.Acquire` cut
  the lease's branch: `origin/<branch>` when origin has it, else the target.
  Adoption normally wrote it first; the rule covers a record made any other way
  and keeps the two from disagreeing about where a branch started.
- The gate reviews the copy of an adopted branch that holds the commits jig
  built, judged by the pool's own sync rule on the build lease's copy against
  origin's (`chooseBuiltCopy`). It used to review the build lease's copy whenever
  the journal recorded any commit, and never compared the two: once a publish or
  the human had pushed jig's commits and the author pushed on top, a round said
  clean over a head that was not the branch's, and a machine with no build
  lease refused even when origin already held every commit. Now in step or
  behind, the round reviews origin's; ahead, the lease's; and with no lease
  here, origin's. Diverged, with a lease that holds a commit jig built
  that origin lacks, the round is refused with `BRANCH_DIVERGED`, naming the
  build lease, where the alternative was to review the lease and print that
  origin has commits the round did not review: a notice beside a clean verdict
  is read as clean, and the next build refuses the same state, so the gate
  refuses it too. A diverged lease that holds none is not jig's to keep (the
  predicate above) and is no lease here for the gate: origin's. Whichever copy
  is chosen must then hold every commit the journal records jig built
  (`pool.RequireBuilt`), or the round is refused (`BUILD_LEASE_MISSING`). That
  check used to run only for a machine with no build lease: with one, the
  copy was chosen by comparing the lease with origin and the journal was
  never consulted, so a lease cut after another machine built (any `jig run`
  that acquired one), or one that built commits of its own without the other
  machine's, was reviewed and called clean over less than the ticket built. A
  commit the journal names that this repository has never seen counts as
  not held (`gitx.Missing`), since nothing here can say where it went.
- The gate lease drops its own local copy of the branch before acquiring, since
  the round re-points it at the round's source and Acquire's sync would
  otherwise refuse it over a stale copy in exactly the cases it exists for: a
  branch the author has pushed to since the last round, after jig's commits were
  fetched in. The publish lease is re-pointed right after acquiring too, and
  drops its copy the same way: it used to be that a ticket's own `jig/<ticket>` on
  origin was the publish lease's own last push, but a branch that is published
  again, or that reached origin another way, stands diverged from a copy an
  earlier attempt left, and the stale copy would refuse the acquire before it is
  replaced. Both leases restore pristine and drop the copy in one function
  (`restoreLeaseBeforeAcquire`).
- The sync rule changes one thing for a ticket that is not adopted, after its own
  publish, and so does the choice of copy. Publish pushes a squash of
  `jig/<ticket>`, while the build lease keeps the unsquashed commits, so the
  branch is on origin and the two stand diverged: a build acquire after a publish
  (a `jig run` after a post-publish requeue) stops with `BRANCH_DIVERGED`, where
  it used to build on and fail later, at a second publish's push. The rule is the
  branch's, and `jig/<ticket>` is a branch once it is on origin, so the gate and
  publish judge it as they judge an adopted branch jig built on
  (`chooseBuiltCopy`): the build lease's copy while origin has none, and after a
  publish the build lease's copy against origin's. Diverged, with commits jig
  built that origin lacks, a round or a publish is refused with the same
  `BRANCH_DIVERGED` (the gate used to review the lease's unsquashed copy, a head
  no publish could ship). A branch pushed as it is leaves the lease behind
  origin's, and the round reviews and publish ships origin's copy
  (`TestOrdinaryTicketLoopAfterAnAsIsPublish`): `jig/<ticket>` used to be the
  build lease's copy whatever origin held, so the round after a publish that
  added commits said clean over a head that was not the branch's and the publish
  after it was refused as not a fast-forward. The commits jig built are the
  journal's verified lines for every ticket, adopted or not; a journal that
  predates them records none, so a diverged lease of such a ticket holds none that
  is unpushed and counts as no lease holding the branch: origin's copy. The
  alternative, publish re-pointing the build lease at what it pushed, writes to a
  lease publish does not own, after the push has shipped, where a failure has
  nowhere honest to go; publishing a branch that already reached origin (below)
  left that choice as it was. The way on is the refusal's own: merge origin's
  branch into the build lease, which leaves it ahead of origin's, and the next
  round and publish work from there (`TestPublishRepublishesAPublishedTicket`).
  The `BRANCH_DIVERGED` help names neither side's author: jig made this
  divergence itself.
- The fast-forward is git's `merge --ff-only`, so an uncommitted edit in a lease
  to a file the author's new commits change stops the acquire with git's own
  error. The alternative, a hard reset, would discard the lease's uncommitted
  work to make room, which jig does not do in a build lease.
- `jig status` names an adopted ticket's branch on a `branch:` line under the
  ticket's, and counts the ticket as `green` while it has no slices: nothing
  built is not something building. After a clean round its hint is `jig publish`,
  as for any ticket: publish ships an adopted branch (below). While it could not,
  the hint, the gate's and publish's refusal said one sentence between them - open
  the pull request yourself, after pushing the commits jig built from the build
  lease - kept short for the demo's 160-column terminal; that sentence and the
  function that built it (`PublishByHand`) are gone with the gap they covered. The
  gate report prints the `branch:` line for an adopted ticket only, so every other
  ticket's output is unchanged.
- `jig solve` on an adopted ticket runs the loop and publishes it like any other.
  It stopped at the clean round with the gate report while publish refused an
  adopted branch, since the loop's work was done and the refusal read as failure
  to any script that drove it; with the refusal gone there is nothing to stop for.
- An adopted ticket's intent follows the precedence every ticket's does, the
  brief, else `intent.md` (`--intent`, `--doc`, or inferred), else none
  ([ADR 0012](docs/adr/0012-intent-provenance.md)), and adoption adds nothing to
  it. Inferring an intent from the author's own Claude Code session was built
  for a ticket with no brief, and a branch built outside jig is that ticket in
  its purest form: the scope diff is the adopted branch against its merge base
  with the target (the gate lease holds the branch, `origin/<target>` is the
  base), the session that matches its files is the author's, and what is
  recorded is the same `intent.md` with the same provenance. Nothing in the gate
  treats an adopted ticket differently, since a special case that skipped it
  would have left the ticket that most needs an intent without one.
  `TestGateInfersAnAdoptedTicketsIntent` runs a reviewer source on an adopted,
  brief-less branch with the target moved past its fork point: the summarizer is
  given the branch's files and not the target's own change, the adoption and the
  inferred `intent.md` are recorded, and a second round reads it without
  inferring again. The demo's tape uses the scripted source, which runs no
  reviewer and so no inference, and still reports `intent: none`.
- `jig ticket new` offers both ways to give the new ticket work: a brief and
  slices, or a branch built outside jig for `jig gate --branch`. It used to offer
  only the brief. The refusal of the commands that can work an adopted ticket and
  `jig status` of a ticket with no slices and no branch offer the same pair
  (`getWorkHints`); status used to offer the brief alone.
- `gate`, `run`, `solve` and `publish` share `requireWork`, which passes a ticket
  with slices or an adopted branch; `requeue` keeps the strict check, since a
  ticket with no slices has none to requeue. A refusal for a ticket with neither
  offers both ways to get work (a brief and slices, or `jig gate --branch`), and
  for an adopted ticket with no slices says the gate queues them. A ticket record
  that cannot be read now fails these commands with its own error where they
  used to say "no slices", since deciding whether a ticket adopted a branch reads
  it.
- Whether amending the brief is the remedy for a flawed brief is decided by
  the slice: it cites brief sections to amend or it does not. A flawed-brief
  outcome on a slice with none - on a ticket with no `brief.md`, or a gate
  fix slice on a ticket that has one - parks the slice with the session's
  summary as a plain question and no `flawed-brief` reason, so status offers
  the answer command and never the requeue one. It used to ask whether the
  ticket has a brief, which disagreed with `jig status` on a fix slice: the
  question told the human to amend sections that were not named while status
  said to answer it. The journal's `question` line still carries the outcome
  the builder reported. `jig requeue --from-brief-diff` on a ticket with no
  `brief.md` is refused (`VALIDATION_ERROR`) with the slices' own remedies,
  where it failed on the missing file.
- Tests that adopted the target as a stand-in for "some branch on origin"
  (the intent-flag refusals) now push a real branch, since the target is
  refused before anything else happens, and assert that the refusal recorded
  no branch and no start sha; the two `--branch` tests that built the ticket
  first (a stale gate lease, a branch deleted on origin) adopt a branch nobody
  built on. The two publish and gate tests that recorded a branch by hand
  put it on origin first, since a recorded branch is on origin by definition.

## Publishing a branch that is already on origin

The design is in [ADR 0013](docs/adr/0013-a-ticket-branch-is-recorded.md); these
are the judgment calls the build left open.

- The squash rule is about history, not about which branch it is. One answer
  picks it: reconcile's own `ls-remote` of `refs/heads/<branch>` gives the policy
  (merge for a branch on origin, rebase for one that is not), and the squash
  follows the policy, so the two cannot disagree. Asking git twice would let a
  push between the two answers merge a branch and then squash it. The squash
  itself is unchanged and keeps `PUSHED_RANGE` for commits that reached a remote
  under another name, which the old test pushed under the branch's own name and
  now pushes under another.
- The report's `squashed` table became `pushed`, with the head and a `squash`
  column that says `squashed` or the words "not squashed (branch already on
  origin)" (`verifydeliver.NotSquashed`). `jig solve` prints the same table.
  `PublishReport.Squashed` still holds the squash sha, and only for a squashed
  branch. The `pr_url` table gained an `action` column (`opened` or `updated`).
  The journal records the squash as `none:branch-on-origin`, in the style of
  `none:target-unmoved`, so the store says what happened, and the `pr` line
  carries the pushed head and whether the pull request was opened or updated.
- The gate and publish share the function that points their lease at the copy of
  the branch (`pointAtTicketBranch`, from the gate's own switch), so a head the
  gate reviewed and the head publish ships cannot be chosen by two rules. The
  refusals that come with the copy - `BRANCH_DIVERGED`, `BUILD_LEASE_MISSING` -
  name the command that hit them (`chooseBuiltCopy` takes it), and the help for
  the machine that built the commits says to run publish there.
- The reviewed-head check reads the head after the lease is pointed at the copy
  and before reconcile, which is the head "it would ship", and compares it with
  the latest round's `reviewed_sha` for the repo. The latest round is the last
  clean one (publish refuses any other first). A round that recorded no head at
  all is let through, not refused: a scripted round is a test double, and no
  other rule can say what it reviewed. The alternative, deriving the reviewed
  head from the journal for those rounds, would invent a fact the round did not
  record. A round that recorded heads, but none for this repo, is refused, with
  a message naming both: the map is keyed by the remote's basename, so a remote
  renamed between the round and the publish would otherwise ship a head no
  reviewer saw, with nothing said.
- A push that is not a fast-forward has its own code, `PUBLISH_NOT_FAST_FORWARD`,
  not `BRANCH_DIVERGED`: origin being ahead of the copy is not a divergence, and a
  refusal that says "0 commits of ours" under that name misleads. It counts both
  sides, says publish never forces, and says where to go on by whose copy publish
  would ship (`pointAtTicketBranch` reports it): the build lease's is where
  origin's copy is merged in, while origin's own has nothing to merge into and
  can only be behind through a push between publish's two fetches, so the way on
  is `jig gate`. Naming the build lease for both sent the operator to a lease
  that may not exist. It is checked after the head check and before reconcile,
  from the remote-tracking ref the lease just fetched: reconcile would merge the
  target into a branch that cannot be pushed, and a conflict there would hide the
  real problem behind `CONFLICT` (`TestPublishChecksTheFastForwardBeforeItReconciles`).
  A push between that fetch and the push is git's to refuse, without force. The
  copy was compared with origin's when the lease was pointed at it, and a copy that
  neither holds the other is refused there with `BRANCH_DIVERGED`, so what reaches
  this check is a push since the acquire's fetch, in the window before the fetch
  publish makes right after pointing the lease. The tests move origin in that
  window through `fetchOrigin`, a func var beside `guardedPush` and `confirm`,
  since no state of the leases reaches it, and stay serial for it.
- One `PRUpdater` interface with both halves, beside `PRCreator`, because they
  are one question with two answers (there is a pull request, or there is not).
  The lookup is the pull requests endpoint (`gh api repos/<owner>/<repo>/pulls
  --method GET -f head=<owner>:<branch> -f base=<target> -f state=open`), not `gh
  pr view`, which fails with a message when there is no pull request, and telling
  that from any other failure would be matching prose, and not `gh pr list`. That
  takes `--head` as the bare branch name, which matches it in every fork, so the
  fork's pull requests share its one page of 30 (its default) with the repo's own,
  and filtering by the head repository's owner afterwards read a page the forks
  filled as "none": publish went on to open a second pull request, which GitHub
  refuses after the push, and the rerun met the same lookup and the same failure,
  so the branch could not be published at all. The endpoint takes the qualified
  head and GitHub applies it, and the state and the base, so the answer is the
  pull requests of that head into that base and nothing else, and two are an
  error. (`--method GET` is not optional: gh sends a POST, which opens a pull
  request, when it is given parameters and no method.) A lookup that fails refuses
  the publish. The update replaces the body from the file and nothing else: the title,
  which the author may have edited, is left alone. A pull request the author wrote
  by hand has its body replaced by the one publish generated; there is no merging
  of the two.
- Only an open pull request from the branch into the target is the one publish
  updates. A closed one, a merged one and an open one into another base are not
  found, and publish opens one into the target beside them. The alternatives were
  to refuse or to ask. A closed pull request is a decision about that pull
  request, not about the branch's next delivery, and publish is what the operator
  asked for and its question says it would open a pull request; refusing would
  make every publish depend on the tracker's whole history of the branch, where
  the question here is whether there is a pull request to update. The fake `gh`
  answers the pull requests endpoint as GitHub does - the state, the qualified
  head and the base narrow the list, and one page of it is returned, 30 rows unless
  `per_page` says otherwise, and it refuses a request that is not a GET - so the
  tests show that it is GitHub that leaves them out, and that a repo's pull request
  behind a page of forks' is still found, not only that the arguments were passed.
- The tracker is built, and the lookup made, before the first store write and
  before the push. It used to be built after the push, so a missing `gh` stranded
  a pushed branch with no pull request. `Deps.Tracker` lets a test hand its own
  adapter without touching the process environment, so those tests stay parallel;
  the ones that run the real github adapter against the fake `gh` put it on `PATH`
  and are serial. They wrap the local adapter for everything but the pull request
  (a fixture ticket is `JIG-1`, not the issue number the github adapter projects
  onto) and let the github adapter make the pull request calls.
- The question publish asks names the pull request it would update, when there is
  one (`confirm` takes it), rather than say "open a PR" over an edit.
- The pull request and the ledger entry are titled by the goal of the first slice
  that did not come from a gate round (`consolidatedTitle`), else the recorded
  title, else the ticket id. A gate round's fix slice records its round
  (`FromGate`) and its goal names a batch of findings, not the work, so it never
  heads a ticket: an adopted ticket's slices are all fixes, and it keeps the name
  it was minted with. The slices say what they are, so nothing asks whether the
  ticket was adopted. An adopted ticket with no recorded title is titled with
  its id, not with a fix's multi-line goal.
- Publish does not re-point the build lease at what it pushed (see the sync rule
  above). A branch pushed as it is needs nothing: jig's commits are on origin under
  their own shas, the build lease is behind origin's copy, the gate and publish
  work from origin's meanwhile, and the next build acquire fast-forwards the lease.
  A branch squashed by the first publish leaves the build lease diverged from the
  squash, and the documented way on is a merge in the build lease.
- A publish that fails after its push (a pull request that could not be opened
  or updated) is not resumable by running it again: the branch on origin is the
  squash, the build lease is diverged from it, and the rerun stops at
  `BRANCH_DIVERGED` with the merge to do, and then at `PUBLISH_UNREVIEWED_HEAD`,
  since the merge moved the head the last round reviewed, until a round has
  reviewed the merged branch. Before this change the rerun squashed a second time
  and failed at git's refusal of the push, later. It is a known limit, not a new
  one, and an adopted branch, which is on origin from the start, is spared the
  merge. The pull request calls are pinned to fail loudly
  (`TestPublishFailsLoudlyWhenThePRCannotBeWritten`): a publish whose pull request
  was not written is not reported or journaled as done.
- The memorize commit lands on an adopted branch as a commit of its own, on top of
  the author's, and is pushed with it. It adds `.claude/retrieval/<ticket>.md` to a
  repo the author may not expect jig files in, the same file `jig/<ticket>` always
  carried inside its squash. Leaving it out of an adopted branch would make publish
  ship a different tree from the one it ships for every other ticket; the docs say
  so.
- The memorize commit holds the notes and nothing else. It used to stage the whole
  working tree, and the publish lease has run every oracle by then when the target
  moved, so a file an oracle left behind, a rewrite of a tracked file or a staged
  change went into a commit that, on an adopted branch, lands on the author's
  branch. The lease is now put back at its head before the notes are written (the
  lease is disposable, and is restored the same way before every acquire); staging
  only the notes' path would have failed the publish of a repo that ignores
  `.claude/`, where staging everything skipped the notes without a word, and that
  is left as it was.

## Lean PR body and review notes

The brief's own user-confirmed decisions (reviewer material in a pull
request comment, body stays lean; an inferred intent is never rendered into
a pull request) and defaults (the body's three parts; no comment capability
on the command tracker; `pr/evidence.md` keeps being written, only the
body's link to it goes) are recorded at the top of `brief.md`'s own
"Decisions" section, not repeated here. These are the judgment calls the
build left open beyond those.

- `firstBriefSection` reads `## Intent` by position, not by heading text: the
  body of `brief.md`'s first `## ` section, whatever it is titled. Every
  brief this repo has (its own included) opens with `## Intent`, so matching
  by position tracks the convention without hard-coding the literal word
  "Intent" into the parser. It matches the first line outside a fenced code
  block that starts with `## `, wherever it falls - never a fixed "skip line
  0" - and normalizes CRLF first, the same as `store.BriefSectionHashes`, so
  a brief whose first section header is line 0 and a CRLF-authored brief both
  parse the same way a LF brief with a leading title line does.
- An explicit `intent.md` that fails to parse, or whose `text` field comes
  back empty, falls back to the file's own bytes with the YAML front matter
  stripped (`stripIntentFrontMatter`) rather than refusing the render: a
  hand-edited or malformed `intent.md` still produces an `## Intent` section
  instead of silently dropping it the way an inferred or absent intent does,
  but never with the source/agent/session/score keys a hand edit's front
  matter can carry.
- Owner decision: a brief with no `## ` section - which nothing upstream
  refuses - has no first section to publish, so no `## Intent` section is
  published at all, exactly as for an inferred or absent intent. A brief
  whose first section is merely empty reads the same way
  (`firstBriefSection` returns `""` for both), and so does one whose only
  `## ` lines sit inside a fenced code block, since those are a code
  sample's own text wherever the body is built. The brief's own bytes are
  never the fallback: a brief can be short and hand-written, but it can just
  as well be long and sectioned with `### ` headings or bold run-in ones, and
  publishing it whole put its Out of scope, Tests and Decisions in the body
  as the intent they are not, against the brief's own "Nothing else belongs
  there" - with those `### ` headings rendered as subsections of an Intent
  they are no part of (`demoteHeadings` shifts nothing when the shallowest
  heading is already below the section level). A bare `## Intent` heading
  stays the one outcome no path allows: a binding source with no text to
  show renders no section whatsoever. The hand-edited `intent.md` above
  keeps its own fallback, front matter stripped, because there the whole
  file is the intent text; a brief's later sections are not.
- Owner decision: that omission is kept, and made visible. `publish` warns on
  stderr that the body has no `## Intent` section and that the brief has no
  `## ` section with text to publish as one (`renderIntentSection` reports the
  omission, `Publish` warns), prefixed `jig:` like every other warning in the
  tree, and before the confirmation prompt, where editing the brief and gating
  again is still cheaper than editing a published pull request. A brief binds
  whatever bytes it has - `resolveIntent` checks neither emptiness nor sections,
  while an explicit intent with no text is refused `INTENT_EMPTY` - so a
  brief-sourced ticket is the one way a publish ships a body that never says
  why the change exists, and nothing else in a run records that it did: the
  report says nothing about the body's sections, and `jig validate`'s
  `brief.md has no "## " sections` counts with `store.BriefSectionHashes`,
  which counts a `## ` line a fenced code block quotes too, so a brief whose
  only headings sit inside a fence passes it. Warned, not refused: the
  omission is the rule above, so an operator who reads the warning can
  publish anyway and edit the pull request after.
- Owner decision: the `## Intent` text is rendered intact, but every ATX
  heading inside it is demoted below the section level (`demoteHeadings`),
  so the body keeps exactly three `## ` sections. `jig gate --doc <path>`
  records an arbitrary file's whole contents as the explicit intent text,
  and a design doc or spec carries its own headings: rendered as they stand
  they added `## ` sections indistinguishable from jig's own three, and a
  `# ` title outranked all of them. The brief says both that the section is
  that text and that the body has three `## ` sections, and demotion is the
  one remedy that keeps both - rewriting or dropping the doc's prose would
  cost the reviewer the text they came for. One shift for the whole text,
  the least that puts the shallowest heading at `### `, so the doc's own
  nesting survives; the shift stops at `######`, the deepest heading
  markdown has, collapsing the last two levels of a doc that already uses
  all six rather than emitting a literal `#######`. A heading inside a
  fenced code block is left alone: it is a code sample's own text, which
  renders literally and is never a section. The same call is made for a
  brief-sourced section, where it is a no-op unless the brief put a `# `
  inside its own first section, since `firstBriefSection` already stops at
  the next `## `. Setext headings (`Title` over `====`) are left as they
  are: the body's promise is about `## ` lines, and rewriting an underline
  risks mistaking a thematic break or a list for one.
- A `## ` line inside a fenced code block is a code sample's own text
  everywhere the body is built, not only in `demoteHeadings`. It does not
  end the brief's first section either (`firstBriefSection` tracks fences
  with the same scanner, `fenceScanner`), so a brief that quotes the body's
  own `## ` sections - what this repository's own briefs do - publishes its
  whole example rather than half of one. And whatever the source hands over
  is closed before it is embedded (`closeOpenFence`): a text ending inside
  an open fence swallows `## What changed`, `## Verification` and the
  findings line into a code block, silently - no error and no warning -
  which `jig gate --doc` reaches with any file that ends mid-fence and a
  brief reaches with a fence it never closes. Closed is judged by cmark-gfm's
  rule, the renderer GitHub uses: a fence line closes a block only when its
  run is the block's own character, at least as long, and followed by nothing
  but spaces - an info string is allowed only on an opening fence, so a
  closing fence that names a language closes nothing at all and leaves the
  block open. `store.BriefSectionHashes` still splits on every `## ` line,
  fenced or not: its hashes are a section's identity in `slices.yaml`
  (`from_brief`), not a rendering, and re-splitting them would unbind the
  slices already recorded against them.
- A `## What changed` bullet prints the first line of a slice's goal, not
  the whole goal (`bulletGoal`), with a trailing `":"` dropped. A goal an
  author wrote is one line already; a fix slice's "goal" is the builder
  prompt `buildFixSlices` wrote, which carries every finding's file:line,
  title, detail, risk rationale and decision. Printed whole it closes the
  list item and renders the gate's finding text as a paragraph with the
  short sha stranded at its end, and it reproduces in the body the detail
  the brief moves to `pr/review-notes.md`. Two fix slices for the same
  workspace and oracle then read alike apart from their sha, which is what
  the brief's own rule ("its goal and short commit sha") asks for; the
  findings themselves, named and ordered by risk, are in the comment.
- The blank lines in `## What changed` are separators between its three
  blocks - the author's pre-adoption commits, jig's slice bullets, the
  fixes from review - with one closing the last block before the next
  `## ` heading. A block with nothing in it contributes no separator, so a
  ticket with only the gate's fixes does not open the section with a blank
  line, and an adopted ticket with no jig bullet at all does not end it
  with two.
- A finding's outcome in `pr/review-notes.md` is "fixed" whenever its id
  appears in any round's `cleared` list, regardless of what its own last
  recorded status says - a finding that was later fixed should never read
  as merely dismissed or asked. The slice credited ("fixed by slice X") is
  the last slice in `slices.yaml`'s own build order whose `Findings` names
  the id, since a recurring finding can have more than one fix slice across
  rounds and the latest is the one that actually cleared it. Findings are
  then sorted by risk and, within a risk, by finding id, so the order is
  stable across renders without needing the round number as a second key.
- A dismissed finding is rendered "dismissed by a human", never an
  auto-triaged variant: nothing but the interactive triage prompt ever
  dismisses a fix or an ask (`DefaultTriage` never does), so the wording
  does not key off the finding's own last-recorded `Triage` field, which
  `ApplyRound`'s rule 2 resets to `""` on a dismissed finding's repeat on
  purpose (nobody decided that round) and would otherwise misreport an
  earlier human dismissal as automatic once it recurs.
- The "Coverage" section's touched-files half is `Publish`'s own
  `git diff --name-only origin/<target>...HEAD`, read once right after
  reconcile and before the memorize commit adds the retrieval notes on top
  of it (so jig's own bookkeeping file never counts as "touched"), not the
  union of files the recorded findings happen to name: a file the change
  touched that drew no finding at all is exactly the gap Coverage exists to
  surface, and the old findings-only union could never show it.
- A failed `CommentPR` is reported the same way any other soft failure in
  `Publish` is: a line on stderr (`internal/verifydeliver/publish.go`'s own
  `warn` func var, the same test seam as `confirm` and `guardedPush`),
  prefixed `jig:` like every other warning in the tree, not a new field on
  `PublishReport` or a journal line of its own. The journal's `pr` line
  already records the pull request itself; a comment that failed to post is
  not a publish outcome worth a line of its own, since the brief requires
  publish to still succeed and the file to stay in the store for a manual
  post either way.
- Owner decision: publish refuses, before writing anything to the store,
  when `brief.md` or `intent.md`'s bytes no longer match the sha256 the
  last clean gate round recorded (`report.yaml`'s `intent.sha256`) - the
  same rule `checkReviewedHead` already gives a reviewed head, extended to
  the intent bytes that field exists to pin. A new code,
  `PUBLISH_UNREVIEWED_INTENT`, with help to run `jig gate` again, rather
  than warning and rendering whatever is on disk: a brief edited after the
  clean round must not be published as the reviewed intent, silently. An
  inferred or absent intent is never checked, the same as it is never
  rendered (`renderIntentSection`): there is nothing pinned to hold it to.

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
  changed after launch, PATH included, and compares each value the child
  sees with exactly what it must be: the synthetic PATH, the stub's knob,
  PWD on the worktree, and on Windows SYSTEMROOT at its launch value. A
  surviving variable fails whatever its value, a harness-changed PATH
  lands after the constant one and fails too, and no value is judged by
  its words, so the result cannot depend on the host (a CI runner's
  branch name, say). The operator's ambient environment, including the
  temp root every path sits under, is outside what the eval scrubs; what
  the harness adds is not. TestMain snapshots the launch environment and
  temp root first thing, so harness setup belongs after the snapshot,
  never in an `init` or a package-level initializer, which run before
  TestMain; a missing snapshot fails the test at once. The corpus leak
  test does judge text by its words, so it leaves out the launch temp
  root and the hostile one it makes under it, and only where the root
  ends a word, so a sibling that merely starts with the root's last
  letters is read as the word it is, and a temp root the harness chose is
  still scanned.
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
  recompile on every lifecycle call. It is built once per process and cached outside
  any fixture dir; each Build call then copies that cached binary into its own
  dir/bin, so a fixture no longer references that shared, longer-lived cache. The
  fixture's oracle command still runs the Go toolchain at `runtime.GOROOT()`, a path
  outside dir.
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
- verifydeliver's tests run in parallel. Its gate and publish tests make thousands of
  git calls, and run one after another they were the Windows test step's wall time.
  Each test now passes its own jig home through `fixture.Opts.Home` instead of setting
  `JIG_HOME`, and calls `t.Parallel`. Three stay serial because they change
  process-wide state: the two identity tests (git config and the identity
  environment) and `TestPublishConfirmWiring` (swaps the package's push and confirm
  hooks); go test runs them before any parallel test resumes. `go test -race` finds
  no data race. On three GitHub Windows runners, test binaries precompiled and the
  order rotated, the whole suite took 644-902 s on main, 453-659 s with the Windows
  CI changes and gitx's PATH cache, and 306-403 s with this as well, with every vCPU
  busy; Linux took 38-40 s for all three.
- The scripted Messages API the live CLI tests run the real `claude` against lives in
  `internal/claudetest`, shared by the headless contract test and the e2e Quickstart
  rather than copied into each. A conversation's session is picked from its prompt,
  so one server can play every session a `jig solve` dispatches, and a scripted
  session takes the paths it writes to from jig's own dispatch prompt, as a real one
  must. The Quickstart's reviewer reads `review.json` and lists its `must_review`
  paths rather than hardcoding the one file the build session adds.

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
- gitx searches PATH for git once per PATH, not once per call. `exec.Command("git")`
  searches PATH on every call, and on Windows that search stats every PATH directory
  once per PATHEXT extension until it reaches git: 31-39 ms a call on a 57-entry
  developer PATH with git's directory 31 entries in, as long as git itself takes to run
  a small command. A syscall profile of `internal/pool`'s tests there put 16 s of the
  package's 41 s in that search. gitx reuses the path it found while PATH, PATHEXT,
  `NoDefaultCurrentDirectoryInExePath` and the working directory are unchanged, as a
  shell's command hash does, so a test that changes PATH is searched again
  (`TestRunSearchesPATHAgainWhenItChanges`). A reused path that no longer exists
  (git removed or moved while jig runs) fails to start; gitx then forgets it and
  tries once more (`TestRunSearchesAgainWhenTheGitItFoundIsGone`), unless the call's
  own working directory is what is missing. On that machine `TestPublishFullChain`
  went from 50-56 s to 27 s. A GitHub Windows runner's search costs 1.6-3.4 ms (git is
  27 entries into its 74-entry PATH), so CI time there does not change measurably.
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
- ci.yml's test step lists `internal/verifydeliver` ahead of `./...`. go test starts
  packages in the order it is given them, four at a time on a hosted runner, so
  verifydeliver, the longest-running package, started among the last from its `./...`
  place and the Windows step then waited on it alone. Run both ways on the same runner, with the
  order swapped on a second runner, the step took 1,004 s and 894 s in `./...` order
  and 948 s and 756 s with verifydeliver first: 10% less on average. Replaying the
  package times through go test's scheduling predicted 24%, but verifydeliver itself
  runs 17-26% slower when it starts beside cmd/jig, e2e and frontier than when it
  starts after them. go test still prints results in the order it was given, so
  nothing prints until verifydeliver finishes.
- What is left of Windows CI time, measured on GitHub's windows-2025 runners (4 vCPUs,
  real-time scanning already off, so a Dev Drive for TEMP measured no faster): an empty
  Go program takes 6-8 ms to start and a trivial git command 13-18 ms, against about
  1 ms for either on Linux. A traced run of the suite starts 17,776 git processes, plus
  the ones git starts itself for a local push, fetch or clone (a push takes about
  300 ms on the runner): 880-1,540 s of git time across five runners, against 61 s on
  Linux. With test binaries already compiled, the whole suite took 808-872 s on three
  runners before gitx's PATH cache, running git's own binary rather than Git for
  Windows' launcher, the single store guard and this order, and 546-564 s after,
  against 51-53 s on Linux. What remains is verifydeliver:
  7,891 of those git calls, one test after another, so with it listed first it is the
  step's wall time. Split across four test processes beside the other packages, the
  same runners took 380-400 s, and the other packages ran 1.5-1.9x slower beside it,
  so every vCPU was busy: about the floor for a 4-vCPU runner. verifydeliver's tests
  now run in parallel, which gets there (under "Fixture and tests"): the pool and
  machine-mapping paths take the jig home root as an argument, so its tests no longer
  set `JIG_HOME` with `t.Setenv`, which `t.Parallel` forbids. Below that takes fewer
  git processes per test, or more vCPUs; the store's writes no longer start any (next
  entry).
- The store runs git in process (ADR 0011): `gitx.Repo` stages and commits from the
  index itself and pushes to and fetches from a bare remote at a local path through
  go-git's object storage, handing every step it would not do as the git program does
  to the git program. One store write (`store.Push` with a change: stage, commit, push
  to a local bare remote, maintenance), timed on GitHub runners in one binary whose two
  stores differ only in the `.gitattributes` that allows the in-process path: Windows
  408-700 ms through the git program and 21-28 ms in process, macOS 141-151 ms and
  6-14 ms, Linux 44-47 ms and 3.5 ms; from 5,000 commits of packed history, Windows
  406-658 ms and 38-62 ms, macOS 150-192 ms and 12-13 ms, Linux 22-25 ms and 9-22 ms.
  go-git's own work tree was the first in-process version and was dropped: its status
  read every HEAD tree from the pack on each write and checked every path it touched
  for symbolic links, so a write grew with the store's history and size (on a Windows
  machine 280 ms with no history and 470 ms with 3,000 commits, against 11 ms and 52 ms
  for the index-based commit on the same machine). The whole suite on the same runners,
  test binaries precompiled and the order rotated: Windows 388, 382 and 470 s on main
  and 307, 370 and 341 s with the store in process; macOS 132 and 140 s, and 111 and
  106 s; Linux 46 and 43 s, and 47 and 47 s. What the Windows suite still spends is git
  work on users' repositories (pool leases, gate resets and diffs, publish), which stays
  on the git program, and test fixtures building their repositories with it.
  Building fixtures with half as many git processes was tried and dropped: `git init`
  and the pinned-identity commits stayed on the git program, while the config, the bare
  clones and the remotes were set up in process (16 git processes per fixture down to
  8). In nine paired Windows runs the whole suite averaged 351 s on main and 331 s with
  it, a mean paired difference of 21 s against a standard error of 19 s, and macOS and
  Linux, two runs each, did not change measurably. That was not worth 320 lines of gitx
  code and 280 of tests.
- The Windows test leg puts `git --exec-path` first on PATH. The runner's first git is
  Git for Windows' `bin\git.exe`, a launcher that starts git's own `git.exe` as a second
  process on every call, the calls git makes itself during a local push included. On
  the same runners (two runners, three rounds each, order rotated), verifydeliver's
  publish tests took 123-170 s (mean 152 s) through the launcher and 96-144 s (mean
  117 s) with git's own binary first, and `internal/store` 40.0 s against 27.5 s.
  jig itself keeps running the first git on PATH. Running git's own binary from gitx
  was tried, and it changes what git starts: Git for Windows' `git.exe` adds its
  `mingw64\bin` and `usr\bin` to their PATH only when MSYSTEM is unset, and behind
  `%HOME%\bin` rather than ahead of it as the launcher does, so with MSYSTEM set and
  Git's directories off PATH a `#!/bin/sh` hook fails with "cannot spawn". A user's
  hooks, credential helper and LFS must get what their own git gives them. The CI leg
  runs pwsh with MSYSTEM unset and no `%HOME%\bin`. Since most Windows users do run
  git through the launcher, `internal/gitx`'s own tests (argv, output, exit codes)
  still run through it first, on the same leg, before the PATH change.
- ci.yml's `claude-cli` job installs the real Claude Code CLI with its official
  installers, on all three platforms, and runs the live CLI tests (`JIG_LIVE_CLAUDE=1`)
  against it. v0.1.1 passed CI and its release smoke test and still shipped a headless
  backend that never started a session: it asked for `--output-format stream-json` in
  print mode without `--verbose`, which the CLI refuses, the test job's stub `claude`
  accepts any argv, and the smoke test checked only `jig version`. The job installs the
  latest CLI release rather than a pinned one, since that is the release a user's CLI
  updates to, and a CLI release can break jig with no jig change at all; a daily
  scheduled run of ci.yml catches that between pushes, until GitHub turns the schedule
  off after 60 days without repository activity. It is a job of its own rather
  than steps of the test job, so the hermetic suite still runs with no `claude` on PATH
  and the Windows leg's wall time does not grow.
- Main's ruleset requires `ci ok` beside the test job's three legs, switched once this
  landed. `ci ok` is ci.yml's gate job: it needs every job that runs on pull requests
  and fails unless each of them succeeded. Before it, only the test legs were required,
  so a pull request that broke the CLI contract showed a red `claude-cli` and could
  still merge; a release could not, since release.yml waits on all of ci.yml. Requiring
  `claude-cli`'s legs by name would have meant a ruleset edit for every further agent
  CLI jig drives, each of which needs its own real-CLI contract job for the reason
  `claude-cli` exists; such a job joins the gate's needs instead. The test legs stay
  required because they run the lint test that keeps the gate honest, and a pull
  request's own ci.yml defines the gate it is judged by: required alone, `ci ok` would
  let a pull request that emptied the gate's needs or its failure merge. The lint test
  fails when a job that runs on pull requests is missing from the needs, and runs the
  gate's step with synthetic results, so it fails on a failed, cancelled or skipped job
  and on no results at all. The gate runs with `if: always()`: GitHub counts a skipped
  required check as passing. `claude-cli` installs the latest CLI from claude.ai, so a
  CLI release that breaks jig, or an outage there, blocks merges until fixed or over. A
  pull request that edits both the gate and its lint test still gets through checks
  alone; review is what stops it.

## Release and install

- The version `jig version` prints comes from Go's build-info VCS stamping, not
  ldflags: the exact tag when HEAD sits at that tag with a clean tree, otherwise a
  Go pseudo-version (`v0.0.0-<timestamp>-<commit>` before any tag exists,
  `vX.Y.(Z+1)-0.<timestamp>-<commit>` once one does), with `+dirty` appended by
  Go itself when the tree carried local modifications, and `(devel)` when build
  info is missing (a `-buildvcs=false` build, or `go run`).
- v0.1.1 is retracted in `go.mod`: its headless backend cannot start a session, since
  the CLI refuses its argv. Go reads retractions from the latest release's `go.mod`, so
  this takes effect with the next release: `go list -m -retracted` marks v0.1.1,
  `go get` and `go list -m -u` warn a module that requires it, and `@latest` never falls
  back to it, even if a later release were retracted too. `go install ...@v0.1.1` still
  installs it without a warning. Its GitHub release stays as published, and the next
  release replaces it as the one the installers fetch.
- GoReleaser archives are named `jig_<os>_<arch>` with no version segment, so a
  "latest" download URL stays stable release over release instead of changing with
  every tag.
- Both install scripts verify the downloaded archive against `checksums.txt`
  (SHA-256) before extracting, and install to a user directory with no sudo or
  admin rights. `JIG_RELEASE_URL` overrides the base URL for mirrors and for
  testing against a local or snapshot build; `JIG_VERSION` pins a release tag.
- The installers install release archives only. `main` has none, so README installs it
  with `go install github.com/develdeco/jig/cmd/jig@main` (or `go install ./cmd/jig`
  from a clone), then `jig skills install`. A `JIG_VERSION=main` would have to build
  from source, which drops both of the installers' promises: no Go toolchain, and an
  archive checked against `checksums.txt`.
- `release.yml` runs the full test matrix through `ci.yml`'s `workflow_call`
  trigger, checks the built binary reports the tag exactly, publishes with
  GoReleaser, attests build provenance, then smoke-tests the installers and
  `go install` as a `needs:` job - a release published with the default
  `GITHUB_TOKEN` does not fire `release: published`, so the smoke test cannot be
  a separate trigger on that event.
- The GoReleaser snapshot dry run and the installer checks against it run on a manual
  dispatch of `ci.yml`, to validate the release pipeline before tagging, and in every
  release before GoReleaser publishes (`release.yml` calls `ci.yml` with `release:
  true`), never on a push or pull request, which they would slow down.
- A release's own binaries run README's Quickstart through the real Claude Code CLI,
  against a scripted Messages API on loopback, both before and after it is published:
  the snapshot archives as the installers put them on disk (`ci.yml`'s installers job),
  then the published ones from the installer and from `go install` (`smoke.yml`).
  v0.1.1 installed and printed its version, the most either job checked, and failed at
  its first dispatch. Checking before publishing is what keeps a broken release from
  becoming the one every documented install path gets; checking after covers what only
  publishing can break. The e2e suite runs against an installed binary through
  `JIG_E2E_BINARY`.
- `smoke.yml` checks out its own commit, not the tag it installs: in a release they are
  the same commit, and a dispatch from a branch runs that branch's Quickstart against an
  older release. Checked out at a tag from before the Quickstart test existed, `go test
  -run` would match no test and pass.

## Publish demo attachment

The brief's own user-confirmed decision (a visual demo shows a pull
request's work best) and defaults (media uploaded through `gh --attach`;
a refused or missing demo adds no section, and publish's output says why)
are recorded at the top of `brief.md`'s own "Decisions" section, not
repeated here. These are the judgment calls the build left open beyond
those.

- `renderDemoSection` reads the gate round that actually recorded a demo for
  the shipped head: the latest round when its own `demo.yaml` names that
  exact head, or - when the latest round ran none at all - the earliest
  earlier round that did (`recordedDemoRound`). A demo is one per reviewed
  head (ADR 0014): a later clean round on an unchanged head runs no demo of
  its own and writes no `demo.yaml` (`DemoExisting`), which is routine, not
  rare, so reading only the latest round would drop a recorded demo from
  the pull request on every re-gate of the same head and tell the operator
  the opposite of what happened. Every candidate's `head_sha` is still
  checked again against the shipped head publish itself resolved, rather
  than trusted - the same "verify, don't trust what was written earlier"
  stance ADR 0014 takes with the gate's own session output - so the
  fallback only ever reaches a round that recorded this exact head, never a
  different change's media.
- `Publish` attaches media through the evidence directory
  `renderDemoSection` itself resolved and verified them against
  (`DemoRenderResult.MediaDir`), never a directory it recomputes from the
  branch's own head: by the time the pull request call runs, that head has
  moved past the one the gate reviewed (the memorize commit, then the
  squash, both land on the lease in between), and recomputing from it
  resolves a directory that was never written to. A demo whose evidence
  directory cannot be resolved at all is an error out of `renderDemoSection`,
  not a silent "no demo": the alternative is a published body whose
  `./<name>` references name media that was never looked for, let alone
  uploaded.
- Every file `demo.yaml` lists is re-verified at publish time against the
  evidence directory - present, same sha256, same size - rather than
  trusting the round's own manifest: the evidence directory is machine-local
  scratch an operator can prune (ARCHITECTURE.md's fifth home), and a
  publish can run long after the round, on the same machine or a different
  one. A file that no longer verifies is left out of the section and named
  in publish's own warning, never silently - the same "never partially
  accept, but always say what's missing" rule ADR 0014 uses for the gate's
  own verification, read the other way around: here it is one file that
  drops, not the whole result.
- When every listed file fails verification, publish reports
  `DemoRenderResult.AllMediaFailed` rather than folding it into "no demo
  recorded": the gate round did record one, and an operator who sees "no
  demo" would look at the wrong half of the pipeline (the gate, not the
  machine serving publish) to explain why. A demo recorded with no media at
  all (a valid, empty-media result, ADR 0014) is not this case: nothing
  failed, there was simply nothing to show, and publish adds no section for
  it without reporting a failure that never happened. Nor is a demo.yaml
  itself recorded as `status: refused` for the shipped head: that is
  `DemoRenderResult.DemoRefused`, reported with demo.yaml's own reason - a
  different half of the pipeline again (the demo session or its backend,
  not publish's own re-verification), so the field that means "every file
  failed re-verification" keeps a name of its own instead of overloading
  demo.yaml's "refused" status, which is what the gate recorded and not
  publish's own finding.
- Each media file renders under the exact name `demo.yaml` already recorded
  it under (`renameDemoMedia`'s own `demo-<n>.<ext>`, in its own order),
  never a fresh counter over the verified files alone: the two can disagree
  as soon as one file fails verification, and a renumbered reference then
  names a file `gh` was never asked to attach while mislabeling the one
  that was. Gate finding r1-f3's owner decision: the reference form is keyed
  on `demoKind`'s own extension, not one form for every kind - an image as
  markdown image syntax, `![caption](./<name>)`, a video as the plain
  bullet, `- ./<name>: caption`, it is not renderable as. The build-time
  call recorded here through v0.1 was the opposite (one bullet form for
  every kind, "never a second code path keyed on file extension"), on the
  reasoning that `gh`'s own rewrite support for a given reference form is
  not guaranteed stable across its versions (ADR 0014's own "Videos" note)
  either way, so keying the renderer on extension would not reliably buy
  anything. r1-f3 found what that reasoning missed: a published body's
  bare-path bullets are the one form `gh` has never rewritten at all, images
  included, so every demo - not only a video one - shipped with dead
  relative links beside `gh`'s own appended URLs. Markdown image syntax is
  the one form `gh` does rewrite, so a second code path keyed on extension
  is exactly what fixes it; see "Publish patches an unrewritten video
  reference" below for the video half gh still leaves to publish.
- Media attachment is two new optional tracker capabilities,
  `PRCreatorWithMedia` and `PRUpdaterWithMedia`, beside `PRCreator` and
  `PRUpdater`, rather than new parameters on those two: an adapter that
  never attaches media (local, command) keeps its existing, narrower
  signature with nothing to ignore, and `Publish` falls back to the plain
  call whenever an adapter lacks the capability or there is no media to
  attach, the same optional-capability shape `PRCommenter` already uses.
- The installed `gh`'s `--attach` support is probed once per subcommand per
  adapter instance (`gh pr create --help` for `CreatePRWithMedia`, `gh pr
  edit --help` for `UpdatePRWithMedia`, each string-matched for the flag)
  and cached separately: `pr create` and `pr edit` are different commands
  with different flag sets, so one's support says nothing about the
  other's, and probing the wrong one can send `--attach` to a subcommand
  that does not take it - on `pr edit`, the common path, since publish
  updates an open pull request on every re-publish. The check itself is
  still cheap and side-effect free, so caching exists only to spare a pull
  request's worth of `gh` invocations the same repeated question, not to
  share an answer across commands. Without support on the subcommand about
  to run, the pull request still opens or updates with `pr/<repo>.md` as
  its body and no `--attach` flags, and `CreatePRWithMedia`/
  `UpdatePRWithMedia`'s own `attached` return (rather than a side channel)
  tells publish to say why - a `gh` an operator cannot upgrade is a reason
  to degrade, never to refuse a publish outright.
- `gh pr create`/`gh pr edit` run with the evidence directory as `cmd.Dir`
  when attaching media, so `--attach <file>` names a file by its bare name:
  the same spelling-safety reasoning ADR 0014 already applies to a demo
  session's own dispatch (a path crossing a process boundary can be spelled
  differently than it was given) applies again here, one hop later, to the
  path `gh` is handed.
- The read-back after an attach (`gh pr view <url> --json body`) checks the
  body for the exact `./<name>` references `writePRBody` rendered
  (`DemoRenderResult.MediaFiles`), not a substring scan for `./demo-` over
  every line: the body can carry that text elsewhere (an Intent section
  quoting a brief, a fenced snippet) without it being a reference `gh` was
  ever asked to rewrite, and reporting a match names the file, never the
  whole matching line, which can be model-written prose rather than
  anything naming a file. Gate finding r1-f3's owner decision: a reference
  still unrewritten is now patched, not left for the operator to fix by
  hand (`rewriteUnrewrittenReferences`, see "Publish patches an unrewritten
  video reference" below) - the build-time call recorded here through v0.1
  was the opposite, never rewriting the pull request a second time, on the
  reasoning that guessing which upload URL belongs to which leftover
  reference is the kind of inference jig avoids elsewhere. r1-f3 found a
  way to patch it that is not a guess: `gh` appends a `[<name>](<url>)`
  link for an attached file it found no in-body reference to rewrite, and
  that link names the file, so the match is read off `gh`'s own record, not
  inferred from position or kind. Only a file with no such appended link at
  all still gets the same soft-failure report, the same shape as the Intent
  omission and a failed `CommentPR`, leaving that one case to the operator,
  who already has the pull request open. A read-back that fails outright
  (auth, rate limit, a `gh` that cannot parse the url) is the same kind of
  soft failure and gets the same warning, not silence: media were just
  uploaded, and the read-back is the one signal that says whether the
  references actually point at them now.
- A demo's `summary` and every file's `caption` are the session's own words,
  recorded in `demo.yaml` as written and unbounded (ADR 0014's own
  "Consequences"), and jig never filters or rewrites them going in: what jig
  guarantees about host paths is about what jig writes, not about what a
  model says, there. Rendering them into a pull request body changes that
  guarantee's reach, since a published body is public on the tracker, so
  gate finding r1-f13's owner decision draws the line at the render step,
  `renderDemoSection`, rather than at the record: before rendering, the
  summary and each verified file's own caption are checked, by exact string
  match only and never a pattern, against the literal paths jig itself
  handed the demo session - `media_dir`, the jig home, the gate lease (where
  the session ran, `DemoInput.LeaseDir`) and the store - in every spelling
  jig may have handed them out in, a WSL mount among them (`hostPathSpellings`,
  `containsHostPath`), the one herdr respells a path to on Windows
  (`session.respellMentions`/`session.WSLPath`). A summary or caption that
  names one is left out of the section whole, not merely edited - rewriting
  only the matched substring would still publish the rest of a sentence that
  describes the operator's own machine - and the omission is named in
  publish's own output (`DemoRenderResult.ScrubbedSummary`,
  `.ScrubbedCaptions`), the same soft-failure shape as an omitted media file.
  A caption that clears the check is still capped at `demoCaptionRenderCap`
  runes: unlike the summary, a caption sits beside a file as a short label,
  and an unbounded one is not that. `demo.yaml` itself is never touched by
  either rule - the record stays exactly what the session wrote, per ADR
  0014 - only what `renderDemoSection` builds from it for the body is.
- Gate finding r4-f3: the host-path check above and `sanitizeDemoCaption`'s
  bracket strip (r2-f6, its own comment in `render.go`) both leave a caption
  or summary free to carry raw HTML, which GitHub renders a sanitized subset
  of inside a pull request body - an `<img src="...">` caption or summary
  becomes a live, camo-proxied remote image; an `<a href="...">` becomes a
  live link. Caught for a non-image caption (`- ./<name>: <caption>`) and
  for the summary, which gets no stripping and, by the r1-f13 decision
  above, no cap either; an image caption was already inert there, landing
  inside `![...](...)`'s alt text, which is never parsed as HTML. The
  owner's decision: escape rather than strip or accept - `escapeDemoHTML`
  renders `&`, `<` and `>` as HTML entities in both the summary and every
  caption, the last transform before either reaches the body, so none of
  it, nor anything demoteHeadings, closeOpenFence or sanitizeDemoCaption
  added ahead of it, is read back as markup. `demo.yaml` itself is still
  untouched, per ADR 0014.

## Publish patches an unrewritten video reference

Gate finding r1-f3 (reviewing this build) caught the two decisions above
("Each media file renders..." and "The read-back after an attach...")
leaving every published demo, images included, with dead `./demo-<n>.<ext>`
links beside `gh`'s own appended URLs: the one reference form every media
file rendered in, the bare bullet, is exactly the form `gh ... --attach`
does not rewrite, and publish never fixed one itself. The two decisions are
corrected in place above; this entry records how the fix is tested, since
that needed its own call.

- `testdata/fixture/ghstub` (the fake `gh` every tracker and publish test
  builds against) is the only place this build can pin "which forms `gh`
  rewrites" against: nothing else in the suite talks to a real `gh`. It now
  reads the `--body-file` a `pr create`/`pr edit` call carries, for every
  file named by that call's own `--attach` flags: a markdown image
  reference to the file, `![alt](./<file>)`, is rewritten in place to a
  minted upload URL, alt text kept; a file with no such reference gets no
  in-place rewrite but is still "uploaded", so the stub appends
  `[<file>](<url>)`, naming it, on its own line - the one way a test (or
  `rewriteUnrewrittenReferences`) can learn which URL an unreferenced
  attach actually got. The result is saved for a later `pr view` to answer
  with ($GH_STUB_BODY_STATE), so a test exercising the full attach-then-
  read-back round trip does not have to hand-write the body `gh` would
  have produced - only a test that wants a body a real `gh` would not
  produce (TestPublishWarnsAboutAnUnrewrittenMediaReference's image gh
  somehow left unrewritten, with no appended URL either) still sets
  `$GH_STUB_BODY` directly, which wins over the saved state.
- The minted upload URL is call-local (`https://.../assets/<n>`, `n` the
  1-based position of that file's own `--attach` flag in the call), never
  a counter persisted across calls: a real upload mints a fresh asset id
  every time a file is attached, even the same bytes attached twice across
  a create and a later edit, so restarting the count per call is not a
  simplification that costs the tests anything, and avoids one more state
  file beside `$GH_STUB_STATE`'s issue counter (a different count; mixing
  the two would make an issue-minting test's own number depend on whether
  a media test ran first).

## Pinning gh's own rewrite behavior against a real gh

Gate finding r2-f4 (reviewing this build): both decisions above ("Each media
file renders..." and "The read-back after an attach...") and the fixture
that tests them were written from reasoning about how `gh ... --attach`
probably behaves, then asserted against that same reasoning encoded as
`testdata/fixture/ghstub`'s own rewrite rule - the brief's "check which
forms gh rewrites" went unrecorded. No test, doc or ADR named a gh version,
so nothing here distinguished a fact from a guess, and a wrong guess would
have had publish's own second edit (`rewriteUnrewrittenReferences`) make a
published body worse than before this feature existed.

The owner ran a real `gh` against a real pull request and recorded what it
did; that run, not reasoning about `--attach`, is the evidence this entry
pins and the only thing the stub now models:

- gh 2.100.0. `gh pr create --help` documents `--attach` as appending the
  attached file to the body as a link; as rewriting a body reference such as
  `![alt](./login.png)` in place to the uploaded asset; and, when some of a
  call's own uploads fail, as still creating the pull request, printing the
  uploaded URL to stdout, and exiting non-zero - the shape `runInDir` already
  keeps (stdout folded into the returned error rather than discarded; see
  "Publish demo attachment" above).
- The same gh 2.100.0, run for real against a bare path reference
  (2026-09-27): a `./x.mp4` line was left exactly as it stood, and the
  video's own upload URL was appended at the end of the body - the
  `[<name>](<url>)` line `appendedAttachmentURL` reads back - confirming the
  video half of the r1-f3 premise by the same run that confirmed the image
  half, rather than by the stub's own say-so.
- Owner decision: a reference form this evidence does not show rewritten - a
  video among them, but not only a video - is modeled, and treated by
  `rewriteUnrewrittenReferences`, exactly like a bare path: left alone by the
  attach call and patched from gh's own appended link afterward, never
  assumed rewritten on the strength of a guess about what some other gh
  might do.

`testdata/fixture/ghstub/main.go`'s rewrite rule does not change for this
entry - it already rewrote only a matched markdown image reference and
appended a link naming the file for everything else - but its own comments
now cite this evidence and name gh 2.100.0, rather than reading as a rule
the fixture invented and then checked itself against.

Gate finding r2-f4's other two risks are not settled by this evidence and
stay open: whether a real gh refuses to run with `cmd.Dir` set to the
evidence directory (not a git work tree) and `--head <branch>` naming no
owner is still unrecorded. A human with a real gh and a real pull request
remains the only oracle for that.

## A real gh resolves `--repo`/`--head` outside a git work tree

Gate finding r2-f4's remaining risk: closed by a second real-`gh` run, on
2026-10-04, from a directory that is not a git repository at all (not just
one lacking a remote) - `gh pr create --dry-run --repo develdeco/jig --head
main --base main`, gh 2.100.0. It resolved the named repository and reached
its own head/base check (head branch "main" is the same as base branch
"main") without ever reading a local git remote, confirming `--repo` and
`--head` together carry what a git checkout would have supplied, cmd.Dir
notwithstanding. `gh pr create`/`gh pr edit` (`internal/tracker/github.go`)
must keep passing both explicitly rather than relying on gh's own
repo/remote inference. `--dry-run` cannot be combined with `--attach`, so
this run is evidence for the repo/head resolution alone; the upload path's
first real check is still the first live publish of a demo.

## Gate risk floor (slice RR-1)

A fix finding whose risk is not in `project.yaml`'s configured `gate.fix_risks` list is routed as a note (status `noted`, `routed_as: note`) rather than an open fix. The default floor is `[high, medium]`: low-risk findings are notes, stopping low-risk loops without blocking delivery. A round with only notes folds clean. The floor is decided before the missing-build-target rule, so a below-floor fix in a file outside every declared workspace is a note too, not an ask: that rule only ever applied to a would-be open fix. See [ADR 0015](docs/adr/0015-gate-fix-budget-and-risk-floor.md).

## Gate fix budget (slice RR-1)

Once a ticket's used budget - its earlier gate rounds that appended at least one fix slice, scripted source included - equals `gate.fix_rounds` (default 3), a round parks each would-be open fix as `asked`/`routed_why: budget` instead of queuing it. Only a person at a terminal can keep a parked finding; `--yes`, a non-terminal stdin and `jig solve` all build on `DefaultTriage`, which leaves it undecided regardless of its build target. `findings.yaml`'s `routed_why` also covers the two other forced-ask cases (`recurrence`, `build-target`), so `jig status`'s new `why` column names every one of them. See [ADR 0015](docs/adr/0015-gate-fix-budget-and-risk-floor.md).

## Gate fix slice sizing (slice RR-1)

A round's kept fixes, grouped by (workspace, oracle), are packed into slices bounded by `gate.fix_slice_findings` (default 5): same-file findings always share a slice, files are taken in path order and packed greedily, and a file over the bound gets a slice of its own. A group that fits in one slice keeps today's id; a split group's slices are numbered `-1`, `-2`, ... from 1. See [ADR 0015](docs/adr/0015-gate-fix-budget-and-risk-floor.md).

## Invariant-sensitive paths (slice RR-2)

The staircase's floor was a keyword regex (`BigDecimal|rounding|migration|...`), where a string match cost 2026-10-04 a BG-1 dispatch extra on Opus because the test string "does not trim surrounding whitespace" matched `rounding` inside "surrounding". Structural invariants (repo-declared paths in `.claude/jig.yaml`) floor to the dearest model instead, measured by file change, not keywords.

## Tests at agreed seams (slice RR-3)

Briefs that list test cases one by one and demand that every new test fail when its rule is removed create large test suites. The intake skill now asks every brief for a `## Seams` section naming public interfaces and critical paths, never test cases; builders and the reviewer hold briefs to their seams. The reviewer's prompt gains one principle sentence after the action definition: "Tests belong at the seams the intent names: a missing test is a problem only at one of those seams or as the proof of a defect you report, and a test elsewhere is at most a note." See [ADR 0016](docs/adr/0016-tests-at-agreed-seams.md).

## The reviewer reads, it doesn't test (build-speed item 1)

The gate's oracles already pass on the head a reviewer gets, so `review.json` now carries them as `oracles_passed` (oracle, workspace, command) and the prompt says the review reads and runs no tests. The list has no result field, since the gate stops at the first failure and every entry it hands on passed. Each entry records the manifest's command, not the short-path spelling the Windows shell workaround runs. The gate now runs oracles in a fixed order, workspaces in manifest order and oracles by name, where it used to follow map order, so `oracles_passed` is deterministic. The review eval runs each case's oracles before the round and hands its reviewer the runs, as `Gate` does; that adds about 30 s to `go test ./internal/revieweval/` on the Windows dev machine, off the suite's critical path. No screen rule refuses test commands: see [ADR 0017](docs/adr/0017-the-reviewer-reads-the-gate-tests.md).

## Builders test narrowly (build-speed item 2)

The dispatch prompt now says to run only the tests that cover the change while working, and the oracle after the last change, before reporting green: not "once", which a builder could read as no rerun after a red final run. It names no test runner, since jig builds any repo. Every headless session's `--settings` env sets the CLI's shell timeouts, `BASH_DEFAULT_TIMEOUT_MS` and `BASH_MAX_TIMEOUT_MS`, both to 30 minutes, so an oracle finishes in the foreground instead of being backgrounded at the CLI's 2-minute default and polled. It lives in `--settings` rather than the child's environment for two reasons: `Options.Env` is an exact environment, and the CLI applies a settings source's env over the inherited one. herdr sessions keep the CLI's defaults. See [ADR 0018](docs/adr/0018-builders-test-narrowly.md).

## Builders open on Sonnet and climb on failure (build-speed item 6g)

The staircase's default rungs are Sonnet then Opus, so Haiku runs only where a project lists it in `project.yaml`'s `staircase`. Each earlier attempt of a slice that failed at the work climbs one rung. That covers a journaled code-bug, oracle-wrong or failed result, and a green that did not verify. A question, a flawed brief or a blocked environment does not climb, so the count comes from the journal (`journal.FailedAttempts`), not from slice state, which counts every attempt. The volume rule, a cumulative lease diff over 400 lines or 10 files, is removed: with Sonnet opening, it would have sent most later slices of a multi-slice ticket to Opus. See [ADR 0019](docs/adr/0019-builders-open-on-sonnet-and-climb-on-failure.md).

## jig runs the slice's oracle at green (build-speed item 6b)

When a builder's green verifies, jig runs the slice's oracle in the lease and journals the run (`oracle`, pass or fail, with HEAD when the tree is clean). A red run goes back to the builder's own session (`session.Resumer`, `claude -p --resume`) with the oracle's output, up to two times. After that, or with a backend that cannot resume, the attempt fails as a code-bug carrying the output, written over `result.json` so the next attempt's log shows it. Uncommitted edits to tracked files go back to the session before any oracle run. jig's run is bounded at 30 minutes and ends its whole process tree when killed; the tree kill moved from `session` to `envrun` (`KillTree`), with the Windows quoted-path workaround (`ShortenQuotedPath`). Builders run only the tests that cover their change; the prompt says a red run comes back to them, or, with a backend that cannot resume, fails the attempt. Changelogs list verified commits, since an attempt can claim green more than once. `frontier.Deps.Oracle` lets tests that are not about the oracle pass it without running. See [ADR 0020](docs/adr/0020-jig-runs-the-slice-oracle-at-green.md).

## The gate reuses an oracle pass on the same tree (build-speed item 6e)

Before each run of its suite, the gate looks for an `oracle` pass line recorded at a slice's green, with the same exact command (frontier now records the command and the env class on the line), a commit with the gate head's tree, the same env class set the gate brings up, and no failed run of that command on the same tree. It reuses such a pass instead of running the oracle again, and brings up env classes only when some run is left. `review.json`'s `oracles_passed` marks each reused run with `reused_from`, and the review prompt now says each command passed on this head's tree (ADR 0017 amended). The gate's own runs and publish's revalidation are now bounded at 30 minutes, with the process tree killed past it, and a failure carries the end of the oracle's output. See [ADR 0021](docs/adr/0021-the-gate-reuses-an-oracle-pass-on-the-same-tree.md).

## jig declares its own test oracle with a longer timeout

`.claude/jig.yaml` declares this repo's `test` oracle as `go test -timeout 25m ./...` in place of the detected `go test ./...`. With verifydeliver's tests parallel (BS-1) and jig's oracle runs in frontier's tests (ADR 0020), `cmd/jig` and `internal/frontier` take 600 to 640 s each inside the full suite on the Windows dev machine, past `go test`'s 10-minute default per package, so the detected command would fail a slice's oracle run and the gate's on timing alone, unless the environment's `GOFLAGS` sets a longer timeout, as the coordination scripts for jig runs on this repo do. 25 minutes stays under jig's 30-minute bound on one oracle run (ADR 0020), so a hung test still fails with `go test`'s own message naming it. The declaration reaches only slices whose oracle names `test`; a literal oracle runs as written. CONTRIBUTING and AGENTS.md give the same timeout for the local suite, as CI does.

## Builders are told how long the oracle took (build-speed item 6a)

jig times each oracle run at green and journals it (`seconds` on the `oracle` line); `slice.json` gains `oracle_seconds`, the latest run time of the slice's exact oracle command on the ticket (0 before the first), and the dispatch prompt names it. The shell-call guard 6a once proposed is not built: the owner chose to measure first, and BS-3's Sonnet builder never set its own timeout or backgrounded a call. See [ADR 0024](docs/adr/0024-builders-are-told-how-long-the-oracle-took.md).

## A builder reads what earlier slices built (build-speed item 6d)

`slice.json` gains `earlier_slices`: every slice of the ticket whose green jig verified before this dispatch, with its builder's summary and the files its verified attempt changed (the union of each attempt's own range: from the lease head at its dispatch, now on the dispatch line, to its verified commit or the head its last turn ended on, now on the result line). The dispatch prompt names it. Sizing slices to a session goes into the intake skill instead (BS-2). See [ADR 0025](docs/adr/0025-a-builder-reads-what-earlier-slices-built.md).

## A code graph gives the builder its starting points

jig's graphify plane gains `Update` and `Query` and a caller: when a project opts in (`context: {graphify: true}` in `project.yaml`) and the binary is on PATH, frontier runs `graphify update .` in the lease and `graphify query` with the slice's goal and earlier slices' files before each dispatch, and writes the linked files and symbols into `slice.json`'s `related` (25 files, 8 symbols each). `graphify-out/` goes in the lease's `info/exclude`; a `graph` journal line records each run, and a failure only empties `related`. `DetectWith` takes the binary lookup, so graphify's test no longer edits PATH and leaves the global-state lint's debt list. See [ADR 0026](docs/adr/0026-a-code-graph-gives-the-builder-its-starting-points.md).
