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
