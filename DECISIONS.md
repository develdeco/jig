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

This section records the rework that replaced an earlier branch's (PR #8's)
session-dispatched reviewer: see `docs/adr/0007-gate-reviewer-owns-bookkeeping-not-judgment.md`
for why. None of that branch's `class`, `Closure` accounting, jig-side
carry-forward, `normalizeTitle` auto-dismiss, `rung` pin, or silent oracle
fallback survived into this design; `action` (fix/ask/note), `prior`, and
`reviewed_paths` replace them respectively.

Design questions the code raised, and their resolution:

- A kept `ask` whose file lies in no manifest workspace has no build
  target, so the workspace it needs is the human's judgment, never a silent
  default. At a terminal, keeping such an `ask` prompts for a workspace id.
  With `--yes` or no terminal, it cannot be kept without that judgment and
  stays `asked`: the round is not clean, and `jig gate` lists it under
  "needs a human" and exits 2 (the same code `jig solve` already uses for a
  builder's pending question). `jig solve`'s own gate/fix-slice loop checks
  the same `NeedsHuman` list after every round and stops the same way,
  rather than re-dispatching the reviewer on a decision nothing in that
  loop can make - fixed in fix round 1 (F3): before the fix, `jig solve`
  read only the round's verdict, so an undecided ask kept the loop
  re-dispatching a full reviewer session every round up to
  `maxSolveRounds`, each one repeating the same unresolved ask, before
  failing `GATE_ROUNDS_EXHAUSTED` without ever printing what needed a
  human.
- `findings.yaml` gains two additive fields beyond the design's base shape:
  `triage: human|auto` (who decided - a person at a terminal, or `--yes`/no
  terminal - absent for notes, dismissed repeats, and undecided asks) and
  `decision` (the human's text for a kept ask). `routed_as` is written only
  when jig routed a finding as `ask` although the reviewer's own `action`
  said otherwise (the recurrence bound, or a no-workspace fix); the
  persisted `action` always stays the reviewer's label. A round's `summary`
  is persisted too. These fields exist so a later PR's eval gold can derive
  from jig's own records instead of matching model prose.
- The e2e case "an ask kept with a decision" needs a real terminal, which a
  subprocess pipe correctly is not. The decisive e2e test instead runs the
  full chain in-process through `cmd/jig`'s own `Main`, with the
  package-level `stdinIsTerminal` var overridden and a scripted stdin
  standing in for one; a smaller, genuinely subprocess-driven test in
  `e2e/` covers the actual non-terminal path (`DefaultTriage`).
- Oracle rules: a `fix` or kept `ask` finding must name a manifest oracle
  when the manifest declares more than one; with exactly one, an omitted
  oracle is that oracle, since there is no choice to make; any oracle a
  finding does name must be a real manifest oracle; a manifest with none
  can never build a fix slice (`GATE_NO_ORACLE`) - there is no fallback.
- Coverage lists (`must_review`, and the scope diff feeding it) come from
  `git diff --name-only -z --no-renames --diff-filter=AMT|D`, so a rename
  counts as its new path under "changed" and its old path under "deleted".
  `must_review` is the sorted, deduplicated union of the changed files and
  the files of open findings still present at head. Every path is
  repo-relative with forward slashes; comparison normalizes a backslash
  separator and a leading `./`, and rejects an empty path, an absolute
  path (leading `/` or a drive letter), and any `..` segment.
- Clearing (an open finding whose file was reviewed and not reported
  again): only this round's *routed* findings (status `open` or `asked`)
  in the same file block it from clearing. A dismissed repeat and a note
  never block, so a dismissed finding re-reported in the same file as an
  unrelated open finding cannot keep that open finding alive.
- A finding recurs only through the reviewer's own `prior`, never by title
  matching. The first recurrence routes like a new finding, with the
  previous fix slice named in the new slice's goal (by id, plus its last
  attempt's own recorded summary when one exists). The second recurrence
  always routes to a human as `ask`, whatever the reviewer's label - the
  same failure surviving one fix slice already is jig's stall concept
  applied to review.
- Clean without dispatch: when the scope diff touches nothing and no
  finding is outstanding, jig writes a clean round without ever
  dispatching a reviewer session, since the previous review already
  covers head.
- Slice ids: a fix batch is `fix-<round>-<workspace>-<oracle>`, a kept ask
  is `fix-<round>-<finding id>`; both are sanitized to
  `[A-Za-z0-9._-]` and, on a collision, disambiguated with the lowest
  unused `-2`, `-3`, ... suffix.
- Compatibility with the old scripted (`--scenario`) path: `jig gate` had
  no `--backend` flag before this rework, so its scripted source still
  runs exactly as before iff `--scenario` is set and `--backend` is not -
  every existing invocation is unchanged. `jig solve` already had
  `--backend`, so its own rule differs: the scripted source runs iff
  `--scenario` is set, whatever `--backend` says.

Deviations recorded during the build, beyond what the design and digest
already called out (their own rationale is in `reports/B1-S*.md` in the
run's own records):

- `Gate`'s `RoundInput.BriefPath` was, for one stage, always the ticket's
  own `brief.md`; a `--branch --doc` round needs the `--doc` file itself,
  absolute, matching PR #8's own recorded reasoning (pointing at this
  round's own `gate/round-N/spec-input.md` would leave a partial round dir
  if the reviewer then failed, since that file is written only once the
  round succeeds). Caught by a main-loop review between stages and fixed
  the same stage `routed_as` and the `findings.md` verdict wording were.
- `findings.md` no longer prints "clean" for a round that is not clean
  (a round with nothing new to report but something still open or asked
  from an earlier round used to hit the same, always-wrong `len(findings)
  == 0` shortcut); it now prints the round's own recorded verdict, and
  "nothing new this round" when that verdict isn't clean but nothing was
  reported. The exact wording is this build's own choice; the design and
  digest specify only that "clean" must never appear for a non-clean
  round.
- The recurrence bound (second recurrence forces `ask`) is applied
  unconditionally once `recurrences >= 2`, whatever this round's own label
  is (design 5.3: "whatever the reviewer's label"), including `note`: a
  finding whose earlier occurrence was `open` or `asked` can legitimately
  recur as a `note` ("still there but harmless now") and still be forced to
  `ask` on its second recurrence. What can never happen is `prior` naming a
  *noted* finding as its target: a note leaves the open set on its very
  first occurrence, and `prior` may only legitimately name an id under
  `open`, `asked`, or `dismissed` (validated). Rule 1 carries the earlier
  occurrence's `oracle` forward when this round's finding names none, so a
  recurrence forced to `ask` by the bound still has one to build a fix
  slice with even when the reviewer's own report this round is a bare
  `note`.
- `Gate`'s clean-vs-fix-slices verdict for a reviewer round is derived
  from findings bookkeeping's own post-round fold, not from whether a
  reviewer session actually dispatched. This is required for the "clean
  without dispatch" shortcut to report correctly (that shortcut always
  succeeds, so a verdict keyed on "did a session run" would call every
  such round fix-slices).
- The interactive triage prompt's exact wording is this build's own design:
  the design and digest specify the *behavior* (batch accept or dismiss by
  id, an ask's keep-or-dismiss with an optional decision, the no-workspace
  prompt, EOF semantics) but not literal strings. Each ask's answer syntax
  is explicit rather than inferred from free text: `n`/`no`/`d`/`dismiss`
  dismisses, and only `k`/`keep`/Enter keeps - an answer that is none of
  these reprompts rather than being read as an implicit keep. A kept ask
  is then asked for its decision text on a second, separate prompt (Enter
  skips it). An earlier draft treated any answer that wasn't exactly
  `d`/`dismiss` as an implicit keep, with the typed text becoming the
  decision - so "no" kept the ask, with "no" itself recorded as the
  human's decision. Fixed in fix round 1 (F17): keep must now be said
  explicitly.
- Design 6.4 ("Findings are always shown sorted by risk, high first, each
  with its rationale") applies to every place a finding reaches a human,
  not only the fix batch table: fixed in fix round 1 (F10b) to also cover
  the notes table, the ask prompt (file:line, detail and risk rationale,
  not the title alone), and the gate report's own findings and
  needs_a_human tables (already sorted by risk then id; F10b adds the
  missing file:line and risk_rationale columns).
- `--yes`/non-terminal triage's one-line note (`DefaultTriage`, run by
  `triageFor`) printed unconditionally, including for a dispatched reviewer
  round that routed nothing at all, and always claimed "kept every fix and
  workspace ask" even when a no-workspace ask (Q1) was left undecided -
  fixed in fix round 1 (F16): the note is now printed only when there was
  something to triage, and states how many no-workspace asks were left for
  a human instead of claiming they were kept.
- `TestGateReviewerRoundsThroughMain`'s round 2 previously only asserted
  that the kept ask `r1-f3` was absent from round 2's own report and
  findings table - true whether or not it actually cleared, since that
  table lists only findings reported that round. Fixed in fix round 1
  (F6): the test now reads round 2's own `findings.yaml` `cleared` list
  directly and requires `r1-f3` in it; confirmed to fail against a Q6
  mutant (a dismissed repeat blocking clearing) that the old assertion let
  through.
- Q10's `jig gate`/`jig solve` compatibility rule (above) had no test
  pinning either half - fixed in fix round 1 (F18) with unit tests on
  `gateSourceFor` and `gateSourceForSolve` asserting the returned
  `GateSource`'s concrete type (scripted vs. reviewer) for each flag
  combination, via `%T` rather than reaching into verifydeliver's
  unexported types.
- The scope base anchor for a full-scope round prefers `merge-base(origin/
  <target>, HEAD)` over the ticket's recorded start sha, falling back to
  the start sha only when the merge-base lookup itself fails (no such
  ref). PR #8 tried the start sha first; this design's own wording puts
  merge-base first, so the carried order was inverted rather than reused
  as is.
- The decisive e2e test (three real gate rounds through the fake backend,
  in-process through `cmd/jig`'s `Main`) discards a failed round's leftover
  store changes with an explicit `git checkout -- .` / `git clean -fd` on
  the store before retrying that round. The actual blocker is the tracked
  `journal.ndjson`: `Gate` appends its `gate-open` line before dispatching
  the round at all, and a `REVIEW_INVALID` result returns before `Gate`'s
  own `Store.Push`, so that line is left committed to the working tree but
  not pushed - the store's next `Sync` (`git pull --rebase`) then refuses
  over it once a remote exists. `Gate` also writes `work/gate.round-N.*.json`
  to the store's working copy before validating a result and leaves those
  uncommitted the same way, but they are untracked cruft under the store's
  `work/` tree and do not by themselves block a rebase pull; a store whose
  only leftover was untracked files would pull cleanly. The underlying gap -
  `Store.Sync` not committing its own leftovers before it pulls - is a
  separate branch's fix (`gate-split`), not this one's, and applies equally
  to any oracle failure after `gate-open` on `main` today, not only to a
  reviewer round; the test's workaround matches what an operator would do
  by hand and becomes a no-op, not wrong, once that fix reaches this
  branch.

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
