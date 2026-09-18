# DECISIONS - jig v0.1 build log

A log of judgment calls made while building v0.1: one entry per decision, covering what
was ambiguous, what was chosen, and why.

## Scope and deferrals

- Structural rounds render as markdown tables in v0.1; this described every gate
  round until the reviewer landed, and still describes the old scripted source's
  rounds. A real reviewer round instead persists structured `findings.yaml` and
  renders `findings.md` from it deterministically (no shas, a trailing newline,
  "none" when there are no findings).
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
- CI-after-PR monitoring (tracking which findings a human later found were CI
  misses, and revalidating a round after CI runs against it), `jig gate`'s `--pr`
  mode, and any daemon/TUI/background machinery stay deferred to v0.2+; jig stays
  foreground and disk-only in this run too.

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
- Fix round 3, Push also refuses a mid-rebase or mid-merge store (F2, reverify-2
  Nice 2/3/4): round 2's D3 fix put `refuseIfMidRebaseOrMerge` only in `Sync`,
  so `Push` - the only path `jig requeue` takes, since it never Syncs first -
  could still `add -A` and commit over unresolved conflict markers left by a
  person's own `git pull` or `git rebase`, or blow away an operator's
  in-progress rebase with its own unconditional retry-pull-then-abort. The
  check now runs inside `stageAndCommit`, the one staging/commit step both
  `Sync` and `Push` already shared, so it guards both without duplicating the
  call. Separately, when Push's or Sync's own retry `pull --rebase` conflicts
  and jig aborts it, the returned error is now wrapped as
  `axi.Error{Code: "STORE_CONFLICT"}` with a message that says the pull
  conflicted and was aborted (git's raw output kept in the message text) and
  a Help line pointing at resolving the divergence in the store - git's own
  "run git rebase --continue" hint is stale by the time jig has already run
  the abort. Only a pull that actually stopped mid-rebase is aborted and
  wrapped: one that failed before rebasing (an unreachable or moved remote,
  an auth failure) left nothing to abort, so git's own error is returned
  unchanged rather than misreported as a conflict
  (`TestUnreachableRemoteIsNotReportedAsConflict`). If the rebase state
  cannot be read at all, the abort still runs and the error is wrapped, so
  jig never leaves the store mid-rebase on a detection failure.
  `TestPushRefusesWhileMidMerge` and `TestPushRefusesWhileMidRebase`
  prove the guard now covers `Push`; `TestSyncOwnConflictingPullAbortsAndWraps`
  kills the surviving `store_nosyncabort` mutant (dropping Sync's own abort
  call) by asserting the store is not left mid-rebase after Sync's own
  conflicting pull.

## Build loop

- Result-text parsing requires exactly one fenced json block; zero or multiple blocks
  both parse as a failed result rather than falling back to a last-block-wins
  heuristic. This stricter rule is recorded in the outcome tests.
- The stall signature strips digits and path segments before comparison, so the
  same failure at a different line number or path still counts as a repeat.
- The stall signature is now persisted on the slice state (`signature`, additive,
  empty on states an older jig wrote), set on both paths that land a slice in
  `stalled` (repeat-failure stall and attempt-cap) and cleared on the green route
  alongside the existing `question`/`reason` clears. `jig status`'s stalled table
  renders it, `-` when absent.
- `frontier` extracts a small `modelFor(cfg, sl, sig)` helper wrapping
  `staircase.SelectPinned` at its rung-selection call site, since a real run
  cannot cheaply produce a volume signal for a unit test; the helper itself, not
  a live dispatch, is what the rung-pin wiring is tested against.
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
- The headless backend's exactly-one-fenced-block text parse is the v0.1 rule; the
  known risk is a transcript containing a stray fence, which is documented rather than
  guarded against in v0.1.
- Staircase signals measure the lease's whole diff against its start point, not
  just the latest attempt's changes.
- graphify's `Plane.Affected` derives `--graph <repo>/graphify-out/graph.json --depth
  2` itself, since the interface carries no graph/depth parameters and these are
  reasonable defaults.

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
- Gate/solve scripted-source compatibility rule: `jig gate` never had `--backend`
  before the reviewer landed, so its old scripted source (`NewFakeGateSource`) runs
  iff `--scenario` is set AND `--backend` is not; every other combination, including
  `--backend fake --scenario X`, dispatches a real reviewer session played back by
  whichever backend resolves. `jig solve` always had `--backend`, so its own rule
  ignores it: the scripted source runs whenever `--scenario` is set, the real
  reviewer otherwise, on solve's own already-constructed backend.
- `review.json`'s `brief_path` in `--branch --doc` mode points at the `--doc` file
  itself, not the round's `spec-input.md`: writing into the round directory before
  the round exists would leave a partial round behind whenever the reviewer fails,
  since `spec-input.md` is written only after the round completes.
- Fix-slice oracle fallback: a finding's suggested oracle is used when it names a
  real manifest oracle, else the first manifest oracle name in sorted order (plan's
  "workspace's first manifest oracle" read literally, since manifest oracles are
  global, not per workspace, in this codebase). A manifest with no oracles is
  `GATE_NO_ORACLE`.
- Mechanical bundle id rule: kept mechanical findings become one fix slice per
  workspace, first-seen order. The unqualified id `fix-<round>-mech` is used only
  when every kept mechanical finding in the round shares one workspace; once a
  round has mechanical findings in more than one workspace, every bundle gets the
  qualified `fix-<round>-mech-<workspace>` form instead, so two workspaces never
  collide on the same id.
- `result.json` is parsed strictly (`ParseReviewResult`): exactly one JSON object,
  a known verdict with a consistent finding count, a valid class and non-empty
  title per finding, a valid status per closure - anything else is
  `REVIEW_INVALID` naming the problem, so a malformed or off-contract session
  result fails loudly instead of silently reading as clean. The headless backend's
  failure fallback shape (`{"outcome":"failed",...}`) has no `verdict` and so fails
  here too. Paired with this: a forward-only guard requires the gate lease's HEAD
  and tracked `git status --porcelain` (untracked files ignored) to still match a
  pristine head after the reviewer dispatch, and rejects the round
  (`REVIEW_INVALID`) if either moved - reviewers never edit.
- CONTEXT.md's Intake entry dropped "triage" from its `_Avoid_` list: Triage is
  now its own CONTEXT.md term naming the gate's human seam (keep or dismiss each
  finding before fix slices are synthesized), so it is no longer a synonym to
  steer writers away from near Intake.
- Auto-dismiss compares a finding's normalized title (lowercased, whitespace
  collapsed, trimmed) against every prior round's cumulative dismissed titles; a
  match is dismissed before triage ever sees it. This is permanent only for a
  title identical after normalization - a genuinely reworded re-raise is a
  different string and reaches triage again, defended only by the prompt telling
  the reviewer not to raise a dismissed finding again and by `review.json` sending
  it the cumulative dismissed list every round. Fix round 1 corrected ADR 0007,
  CONTEXT.md, and this file's own earlier wording, which had overclaimed that
  rewording could never resurface a dismissal.
- The fake session backend's gate playback (`d.Slice == "gate"`) errors loudly on
  missing scenario coverage for a round (`session/fake: scenario has no gate round
  <n> review-result.json`) rather than defaulting to a silent clean result, since a
  scenario author forgetting to script a round should fail the test, not pass it
  by accident.
- No `schema_version` bump for this run: every store-schema change (slice `rung`,
  slice state `signature`, `report.yaml`'s `reviewed_sha`, the new `gate/round-N/
  findings.yaml` and `work/gate.round-N.*` files) is additive - an older jig reading
  a newer store's ticket folder still parses every field it knows about, and a
  field absent on write leaves no key on disk.
- `report.yaml` gained `reviewed_sha` (repo → sha for that round, `omitempty`); the
  design digest named this field on `reportYAML` explicitly. What the digest left
  unlisted was `GateReport.ReviewedSHA`, the in-memory field that carries the
  reviewer source's `Review.ReviewedSHA` through `Gate` to `writeReportYAML`; both
  are kept additive so a scripted round's `report.yaml` bytes are unchanged.
- Right after a gate round appends fix slices, `jig status`'s next-step hint still
  falls through to "work the frontier" rather than "work the fix-slice round",
  because its `allGreen` check counts every slice including the newly-appended,
  still-queued fix slices - unchanged behavior, shared with the old scripted path,
  not something this run needed to fix.
- Fix round 1, store wedge (A1): `Store.Sync` used to run `pull --rebase`
  straight away, so any uncommitted leftover in the store (a gate-open journal
  line, `work/gate.round-N.*` from a reviewer attempt that failed before the
  round finished, or an operator's Ctrl-C at the triage prompt) failed every
  later command's `Sync`, including the retry the design relies on. `Sync` now
  stages and commits any uncommitted leftovers with jig's identity before it
  pulls, reusing the same stage+commit helper `Push` already had (factored out
  rather than duplicated).
- Fix round 1, lease hygiene (A2): the forward-only guard alone left a reviewer's
  edit, untracked file, or commit sitting in the gate lease after a rejected
  round, poisoning every later round and, in `--branch` mode, moving the branch
  ref onto the reviewer's own commit. `reviewerGateSource.Round` now force-resets
  the lease to a pristine head (`git reset --hard` + `git clean -fd`, never
  `-x`, so ignored build caches survive) both right before dispatch - which also
  wipes dirt a manifest oracle left behind moments earlier, so it is never
  blamed on the reviewer - and unconditionally after dispatch, on every return
  path (`defer`).
- Fix round 1, full-scope base (A3): full scope's base is `gitx.MergeBase(lease,
  "origin/"+target, "HEAD")`, with the ticket's `start.<repo>.sha` used only as a
  fallback when that lookup itself fails (and only if it is still an ancestor of
  HEAD) - a deviation from the design digest's literal "start sha first" text.
  The start sha stays an ancestor of HEAD through a rebase onto an advanced
  target, or a `--branch` branch cut from a newer main, which is exactly when it
  stops being the right fork point; merge-base already gets this right for the
  publish-time squash base, so the reviewer's full-scope base now matches it.
- Fix round 1, closure enforcement and carry-forward (A4): a round's result must
  close every one of `review.json`'s `prior_findings` exactly once - a missing or
  duplicate closure is `REVIEW_INVALID`, same as an unknown id already was, so a
  clean verdict can no longer coexist with an unresolved prior finding. A
  `still-open` closure is not re-raised by the reviewer (the prompt says not to);
  jig itself appends a new finding in the closing round, continuing the id
  sequence after the reviewer's own findings, copying class/title/workspace/
  detail from the original (read back from its own round's `findings.yaml`) and
  extending detail with the closure's note, inheriting the oracle of the fix
  slice the original finding produced. `priorFindingsAndDismissed` now removes an
  id from `prior_findings` on any closure, closed or still-open, since a
  still-open id is superseded by its carried successor and must not also linger
  as open forever under its old id.
- Fix round 1, dismissals through Gate (A5): the flagship two-round
  `TestGateReviewerTwoRounds` now also re-raises a round-1-dismissed title in
  round 2 with case/spacing drift, alongside its closures, and asserts the
  Triage hook never even sees it. This is a test-only addition - A2's lease
  restore and A4's closure enforcement did not change the auto-dismiss code
  path itself, which already existed via `normalizeTitle`; the gap was that
  nothing drove it through `Gate` end to end.
- Fix round 1, title and id sanitizing (A6, A7): a reviewer-supplied title with
  embedded newlines or irregular spacing is collapsed to single spaces
  (`collapseTitle`) before it becomes a jig `Finding.Title`, so it cannot break
  `findings.md`'s rendered list or a mechanical bundle's goal bullet. A
  mechanical bundle id's workspace component is sanitized
  (`sanitizeWorkspaceID`: anything outside `[A-Za-z0-9._-]` becomes `-`) before
  it is embedded in `fix-<n>-mech-<workspace>`, since a manifest workspace id
  may legally contain `/`, `\`, or `:`, which would otherwise nest directories
  or break on Windows in `slices/<id>.state` and `work/<id>.attempt-N.*`.
- Fix round 1, known limit (`--early`): a `jig gate --early` round before an
  earlier round's fix slices have landed can carry a still-open finding forward
  while its first fix slice is still queued, producing a duplicate fix slice,
  because the reviewer is reviewing code the earlier fix has not touched yet.
  `--early` is a deliberate mid-ticket review of unfinished work, so this is
  accepted rather than solved in this run.
- Fix round 1, known limit (stalled gate fix slice): `jig status`'s stalled hint
  always suggests `jig requeue <ticket> --from-brief-diff`, but `Requeue` only
  requeues a slice whose `from_brief` names a changed brief-section hash.
  Synthesized fix slices have no `from_brief`, so the hint is a no-op for a
  stalled `fix-N-mech` or `fix-N-k` - and mechanical bundles, pinned to the
  cheapest rung, are the most likely to stall. Recorded, not solved in this run;
  a `from_gate`-aware resume path is the fix.
- Fix round 2, gate lease restore before oracles (D2): fix round 1's A2 restore
  (`resetLeasePristine`) ran only inside `reviewerGateSource.Round`, as a
  `defer`, so it never ran at all when jig was killed before or during a
  reviewer dispatch (no signal handler) or when the reviewer itself outlived
  its dispatch (the herdr backend leaves a failed reviewer's workspace open
  "for jump-in"). The next `jig gate` on the same lease then ran
  `runGateOracles` - and in `--branch` mode, the reviewer flow itself - over
  whatever the reviewer had left behind: an untracked repro file failed every
  later gate's oracles until someone deleted it by hand, or a reviewer commit
  became the lease HEAD and was reviewed and passed clean, breaking
  forward-only. `Gate` now calls `resetLeasePristine` itself, right after the
  lease is acquired and the branch is in place, before any oracle or reviewer
  round runs: normal mode resets to `HEAD` (already force-updated to the
  build lease's copy by `fetchTicketBranchFromBuildLease`); `--branch` mode
  first requires `refs/remotes/origin/<branch>` to exist - `pool.Acquire`
  fetches origin but never resets an existing local branch, so without this
  check a branch deleted or renamed on origin would silently validate
  whatever stale local copy the lease still had - then resets to
  `origin/<branch>`, since the gate lease never commits and so must always
  equal origin's branch tip exactly, never a stale local copy or a killed
  run's leftover commit. Existing `--branch` tests already pushed the branch
  to origin before gating it, so none relied on the old
  local-branch/origin-fallback behavior and none needed changing.
- Fix round 2, store conflict safety (D3): `Store.Sync` used to run
  `git add -A` and commit unconditionally before pulling, with no check for
  whether the store was already mid-rebase (left there by a previous `Push`
  whose retry `pull --rebase` itself conflicted). `add -A` stages unresolved
  conflict markers as ordinary content, and a `commit` (or later
  `rebase --continue`) finalizes them onto the store branch, corrupting
  whatever file conflicted - `journal.ndjson` for every later reader in the
  worst case. `Sync` now checks `.git/rebase-merge`, `.git/rebase-apply` and
  `MERGE_HEAD` first (via `git rev-parse --git-path`, resolved against the
  store root) and refuses with `axi.Error{Code: "STORE_CONFLICT"}` without
  touching the index when any exists, pointing the operator at `git status`
  and a rerun once they resolve it by hand. Separately, both `Push` and
  `Sync` now run `git rebase --abort` (best effort, its own error ignored)
  whenever their own `pull --rebase` fails, so a conflict jig's own retry
  causes never leaves the store mid-rebase for STORE_CONFLICT to catch on
  the next command - the check and the abort are complementary, not
  redundant: the abort covers jig's own failed retries, the check covers a
  rebase left by anything else (a person, a different tool, a crash between
  the failed pull and the abort).
- Fix round 2, synthesized fix-slice id collisions (D4): two mechanical
  bundles in different workspaces can sanitize to the same id
  (`sanitizeWorkspaceID` maps both `svc/a` and `svc:a` to `svc-a`, giving
  both `fix-N-mech-svc-a`); `AppendSlices` refuses a duplicate id outright,
  which would otherwise leave the round half-applied - some fix slices
  already appended, the rest rejected mid-round. `synthesizeFixSlices` now
  ends with `disambiguateFixSliceIDs`, run before any round file is written:
  the first occurrence of an id, in synthesis order, keeps it; each later
  duplicate gets the lowest `-2`, `-3`, ... suffix not already used by any
  id in the batch (original or already disambiguated), so disambiguation
  itself can never produce a new collision.
- Fix round 3, restore an existing gate lease before Acquire (F1, a residual
  of NM2/D2): D2's post-acquire restore still ran after
  `fetchTicketBranchFromBuildLease`'s own checkout back onto the ticket
  branch. Leftover tracked dirt in the lease (a reviewer that outlived a
  killed jig, or an oracle rewrite) does not matter by itself - the next
  gate's oracles still see a restored tree - but once the ticket branch
  later advances past the same file, that checkout refuses ("local changes
  ... would be overwritten") before D2's restore ever runs, and `Gate`
  returns with the lease detached and still dirty. Every later attempt then
  fails the same way, one step earlier, inside `pool.Acquire`'s own
  `git checkout` - the same hand-cleaning wedge NM2 was accepted for. `Gate`
  now restores an existing lease pristine at its current `HEAD` before
  `pool.Acquire` runs at all (best-effort: if the pool dir cannot be
  resolved, or the lease is not yet its own git working copy with a commit
  checked out, `Acquire` runs unchanged and surfaces its own error).
  `TestGateRecoversLeftoverTrackedDirtOnceBranchAdvances`
  proves both the direct case (branch advances once, the very next gate
  succeeds) and recovery from a lease already left wedged by an earlier
  failed attempt (detached, still dirty, its local branch ref already
  fast-forwarded past the dirty file).
- Fix round 3, the pre-Acquire restore runs only in the lease's own
  repository (NM6, a regression in F1 found by the round-3 re-verify): F1
  first ran `git reset --hard HEAD` and `git clean -fd` whenever the lease
  directory had any `.git` entry. When that entry is not a repository git
  can open (a lease deleted by hand and stopped by a locked pack file, a
  clone killed mid-write) and `JIG_HOME` sits inside another working copy,
  git's upward discovery resolved the enclosing repository and the reset
  discarded its uncommitted work, silently; and a clone killed before its
  first checkout has an unborn `HEAD`, so the reset failed and wedged every
  later gate. The restore now runs only when `git rev-parse --show-toplevel`
  is the lease directory itself (compared with `os.SameFile`) and
  `HEAD^{commit}` resolves; otherwise `pool.Acquire` handles the lease as it
  did before F1. `TestIsOwnGitRepoWithHead`,
  `TestGateBrokenLeaseNeverResetsEnclosingRepo` and
  `TestGateRecoversFromUnbornLease` pin it (the last two fail on F1's
  original condition). `pool.Acquire`'s own `.git` check has the same
  enclosing-repository weakness for its fetch and checkout; that predates
  this work and is recorded as a follow-up, not fixed here.
- Fix round 3, `--branch` detects a branch deleted on origin (F3, reverify-2
  Nice 1; corrects D2's overclaim): `pool.Acquire`'s own fetch has no
  `--prune`, so a branch deleted on origin after an earlier gate on the same
  lease left `refs/remotes/origin/<branch>` stale, and the
  `refs/remotes/origin/<branch>` existence check D2 added passed against
  that stale ref instead of catching the deletion - the gate then silently
  reviewed the last-fetched tip. D2's own DECISIONS entry and ADR 0007
  claimed this check covered a branch "deleted or renamed on origin"; it
  did not, until now. `Gate`'s `--branch` mode now runs
  `git fetch --prune origin` in the gate lease before the existence check,
  and `BRANCH_NOT_FOUND` gained a Help line ("push the branch to origin,
  then rerun"). `TestGateBranchDeletedOnOriginAfterEarlierGate` gates a
  branch, deletes it on origin, then gates it again and asserts
  `BRANCH_NOT_FOUND` with a non-empty Help.
- Fix round 3, `carriedFindingOracle` matches mechanical bundles by
  workspace (F4, reverify-2 Nice 7): it used to rebuild the mechanical
  bundle id it expected (`fix-<round>-mech` or
  `fix-<round>-mech-<sanitizeWorkspaceID(workspace)>`) and match on that
  string. `disambiguateFixSliceIDs` (D4) can push a colliding bundle's id
  to a `-2` (or higher) suffix that `sanitizeWorkspaceID` alone never
  produces, so a still-open mechanical finding carried from the *second* of
  two colliding workspaces resolved the *first* workspace's bundle's
  oracle instead of its own. It now matches by `FromGate == round`,
  `Workspace == prior.Workspace`, and an `ID` `fix-<round>-mech` prefix
  (to exclude intent slices, which share the round but not the prefix),
  so the match is independent of whatever suffix disambiguation gave the
  id. `TestCarriedFindingOracleMatchesByWorkspaceNotUndisambiguatedID`
  proves both bundles resolve their own oracle, not each other's.

## Review eval

- A corpus case's `review.json` is a template the runner partially overwrites, not
  a throwaway file: `RunCase` keeps its authored fields (ticket, round, scope,
  prior findings, dismissed) and overwrites only the sha/path fields it computes,
  then marshals the same struct through `verifydeliver.MarshalReviewRequest` - the
  same function the real gate calls. This lets a case author adjust a case's
  static fields without the runner needing per-case Go code, while still
  guaranteeing the exact wire shape.
- The eval dispatch sets `Dispatch.Screen: false`, unlike the real gate's `true`:
  the `_screen` PreToolUse hook re-execs `os.Executable()`, which inside `go
  test` is the test binary, not `jig`, and the eval repo is a throwaway temp
  dir with no remote, so screening would buy nothing and would break the
  headless backend's hook wiring.
- The eval dispatch identifies a case by `session.Dispatch.Ticket` (set to the
  case name), since `Dispatch` has no dedicated case-name field; the structural
  tests' scripted backend keys its scenario lookup on that field.
- Eval repo commits are identity-pinned via `gitx.RunEnv` with the same
  `GIT_AUTHOR_*`/`GIT_COMMITTER_*` values `internal/fixture` and the fake session
  backend already use, not a `.git/config` write, so the throwaway repo's commits
  hash the same on every run without a new pinning convention.
- Gold `title_pattern`s must not overlap within a case: `mechanical-batch`'s first
  draft matched a doc-typo finding's title against its own missing-doc-comment
  gold entry too, since both titles contained the phrase "doc comment". Corpus
  authors writing `gold.yaml` need to check a new pattern against every other
  finding's title in the same case, not just its own gold file.
- `RenderReport`'s exact text format is a plain, greppable line
  (`<case>: PASS|FAIL found=a/b missed=N fp=N unmatched=N[ reason: ...]`) plus a
  `totals:` line; tests assert on substrings, not the exact format, so it can be
  reformatted later without touching test expectations.
- Fix round 1, one-to-one gold matching (B1): `scoreCase` used to check a result
  finding against every gold entry independently, so one lumped finding could
  satisfy every gold entry in a case at once (a gamed 3-for-1 on
  `mechanical-batch`). It now runs a maximum bipartite matching (an
  augmenting-path matcher, cheap at 1-3 gold entries per case) over the
  compatible (gold, finding) pairs, so each side pairs at most once; a finding
  left unmatched still goes through the trap/false-positive/unmatched checks as
  before.
- Fix round 1, tightened gold patterns (B2): `tenant-leak` and `nil-deref`'s
  `title_pattern`s matched code vocabulary rather than the defect
  (`(?i)tenant|filter` matched any finding merely naming the function
  `ForTenant`; `(?i)nil` matched a finding that argued values are "never
  nil"). Patterns now require defect wording (leak/cross-tenant phrasing; nil
  pointer/deref/map/value or panic phrasing). `mechanical-batch`'s three
  patterns were already mutually exclusive by wording; B1's one-to-one
  matching is what stops a single lumped finding from claiming all three.
- Fix round 2, gold patterns widened to accept plausible correct wording
  (E1): round 1's tightened patterns (B2) still rejected several one-
  sentence correct reviews. `nil-deref`'s `(?i)nil (pointer|deref|map|value)
  |panic` missed "Missing nil check in Lookup" (no "nil pointer/deref"
  phrase), "Lookup dereferences a nil *User..." (no contiguous "nil deref"
  phrase, just "dereferences" and "nil" apart), and a review whose only
  defect wording was "crashes". `tenant-leak`'s pattern missed "Missing
  tenant filter in ForTenant", "ForTenant does not filter by tenantID", and
  "ForTenant ignores its tenantID parameter" (the old `ignor\w+ (the
  )?tenant` required the literal word "the" right after "ignores", so "its"
  fell through). Patterns now add `derefer|nil check|check (for|against)
  nil|crash` (nil-deref) and `for all tenants|missing (a )?(tenant
  )?filter|does(n't| not) filter|ignor\w+ (its |the )?tenant` (tenant-leak).
  Both still reject the wrong-direction reviews B2 was written to catch
  ("the map never holds a nil value..."; "values are never nil"; an
  unrelated ForTenant aliasing finding that never names the tenant filter),
  proven by a table-driven test per gold entry
  (`TestGoldPatternsAcceptCorrectAndRejectWrongReviews`) plus
  `testdata/results/variants/` fixtures that PASS through the full
  `RunCase` pipeline, not just `scoreCase` in isolation
  (`TestScorerPassesCorrectVariantsThroughFullScorer`). `mechanical-batch`'s
  three patterns needed no change; a direct test
  (`TestMechanicalBatchPatternsStayMutuallyExclusive`) confirms each
  positive phrasing still matches exactly one entry.
- Fix round 1, trap coverage (B3): the corpus's only trap lived in
  `loopvar-trap`, a case with zero gold findings, where every finding is a
  false positive by the no-gold rule regardless of whether it matches the trap
  - so `traps:` never actually decided a case's pass/fail, and a broken trap
  check or a dropped file check would not fail any test. Added a trap to
  `tenant-leak` (a correct, tenant-filtered `Summarize` helper that tempts a
  false "this leaks too" flag) alongside its gold finding, plus direct
  `scoreCase` unit tests exercising the trap match and the per-entry file check
  in isolation on a synthetic case.
- Fix round 1, failed cases count as missed (B4): `RunCase` used to return a
  `CaseScore` with an empty `Missed` on a dispatch error or an invalid
  `result.json`, so `RenderReport`'s `recall = found/(found+missed)` silently
  dropped that case's gold findings from the denominator instead of counting
  them as misses - a run where most dispatches failed could still report a
  high recall. A `failedScore` helper now fills `Missed` with every one of the
  failed case's gold finding ids.
- Fix round 3, documented as a known limit, not fixed (F6; reverify-2 Nice
  6): `title_pattern` regex matching is a heuristic, and reverify-2
  demonstrated both directions still slip through after E1 - a negated or
  reworded wrong review ("no nil check is needed in Lookup"; a trap's own
  wording spilled into an unrelated finding's detail) can score a false
  PASS, and a correct review phrased outside the pattern's alternatives
  ("ForTenant never uses tenantID") can score a false FAIL. This is stated
  plainly in ADR 0008 now: the accept/reject phrasing tables each fix
  round records here are the contract a pattern must satisfy, not a claim
  of completeness. No pattern change in this round; widening again on a
  specific gap (as B2 and E1 did) is the fix when a real gap surfaces.

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
- `jig status`'s old combined `paused` state (needs-input OR env-blocked) is gone.
  `state:` now has four precedence tiers, worst first: `stalled` (any slice
  stalled) > `parked` (any slice needs-input specifically) > `env-blocked` (any
  slice env-blocked) > `green` (every slice green), falling back to `building`.
  A `parked[N]{slice,question,resume}` table and a `stalled[N]{slice,reason,
  signature}` table render whenever the ticket has one, in that order, alongside
  the existing questions table - independent of the `state:` line's own
  precedence, which only picks the one summary word.
- `resumeCommand`'s flawed-brief branch compares `SliceState.Reason` against the
  literal string `"flawed-brief"` rather than a shared constant, matching
  `internal/frontier`'s own call site, which already sets that value as a literal;
  this avoids a new cross-package import into `cmd/jig` for one string.
- The e2e reviewer test drives `jig gate`'s stdin through an explicit empty pipe
  (`strings.NewReader("")`) rather than leaving `exec.Cmd.Stdin` unset, so
  triage's non-terminal path (keep all, print the note) is what the test
  exercises, deterministically, regardless of how stdin is decided.
- `stdinIsTerminal`'s mode-bit heuristic (`os.ModeCharDevice` plus an
  `os.SameFile` compare against `os.Stat(os.DevNull)`) could not tell a real
  Windows console apart from `NUL`: `os/stat_windows.go`'s `statHandle`
  returns an empty path and zero volume/index ids for any `FILE_TYPE_CHAR`
  handle, so `os.SameFile` came out true for both, and a real console was
  misread as non-terminal - interactive triage never ran on Windows, this
  project's own development platform. `stdinIsTerminal` now decides with a
  real OS terminal query instead of any Stat heuristic: `isTerminalFile`
  (build-tagged per GOOS in `cmd/jig/tty_*.go`) calls `GetConsoleMode` on
  windows, a termios ioctl (`TCGETS` on linux, `TIOCGETA` on
  darwin/freebsd/netbsd/openbsd/dragonfly, via `golang.org/x/sys/unix`) on
  the unix family, and returns false unconditionally on every other GOOS (the
  non-interactive path, which keeps every finding, so an unsupported OS never
  loses findings silently). A pipe, a regular file, and the null device all
  fail these queries the same way a heuristic would, without needing a
  dedicated null-device comparison.
- Fix round 3, guard the NM1 regression class (F5, reverify-2 Nice 5): every
  existing `stdinIsTerminal` test asserted a negative (non-file, regular
  file, the null device, a pipe), so a mutant that makes `isTerminalFile`
  always return `false` - exactly the shape of the NM1 regression, a real
  console misread as non-terminal - passed the whole suite. A new
  windows-tagged `TestStdinIsTerminalRealConsole` opens `CONIN$` (the
  process's own console input) and asserts `stdinIsTerminal` is true for it;
  it skips, rather than failing, when the open itself fails (some CI runners
  have no console attached). Opening `CONIN$` works under `go test` on this
  box, so the test runs for real here, not skipped.
- Fix round 3, documented as a known limit, not fixed (F6; reverify-2 Nice
  8): on Windows, Git Bash's default mintty terminal gives jig's process a
  pipe for stdin, not a console handle, so `stdinIsTerminal` reads it as
  non-terminal and triage runs non-interactively there - this matches
  `golang.org/x/term`'s own behavior, so it is not specific to jig's
  `GetConsoleMode` query. PowerShell, cmd, and Windows Terminal (ConPTY) are
  all detected correctly.

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
- Windows Defender exclusions were considered for Windows CI time and dropped: GitHub's
  Windows runner images already turn real-time scanning off and exclude the C: and D: drives.
- `lint/workflow_test.go`'s "release reuses ci before publishing" check verifies an
  actual dependency, not just that both jobs exist: it finds the job whose `uses`
  names `ci.yml` and the job whose steps use `goreleaser-action` (the real
  publishing step), then asserts the second depends on the first through a
  transitive `needs` walk. A weaker version asserting only that both jobs exist
  would not catch a release pipeline that publishes before ci passes, which is the
  bug the invariant exists to prevent.
- The same test's tag-pattern check also rejects an empty `push.tags` list, beyond
  the literal "every tag pattern starts with v" wording, since an empty list would
  vacuously satisfy that wording while defeating the trigger's point.

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
