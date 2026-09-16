# DECISIONS - jig v0.1 build log

A log of judgment calls made while building v0.1: one entry per decision, covering what
was ambiguous, what was chosen, and why.

## Scope and deferrals

- Boards deferred: structural rounds render as markdown tables in v0.1; the `board`
  package is an interface stub.
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
  `jig gate`; the conflict-resolution fix-slice loop is deferred, and the DoD only
  asserts the oracles-only tier. The pushed-branch publish flow beyond the reconcile
  policy is unreachable in v0.1 because squash refusal blocks it first - a known,
  documented limit.
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
  excluded), newline-normalized, with trailing whitespace trimmed on each line -
  chosen because the spec's "##-delimited section bodies" reads most naturally that
  way.
- The standalone store's default `ticket_format` is `T-{n}`; the tracker is local by
  default. The design names no default, so this was chosen as the simplest workable
  one.
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

- Result-text parsing requires exactly one fenced json block; the v0.1 contract chose
  this over the predecessor's last-block-wins behavior. Zero or multiple blocks parse
  as a failed result, and this port-fidelity tradeoff is recorded in the outcome
  tests.
- The stall signature strips digits and path segments before comparison - the v0.1
  contract chose this normalization over the predecessor's signature, which had
  neither.
- The staircase invariant regex is applied case-insensitively, matching the
  predecessor's IGNORECASE behavior where v0.1 left the choice open.
- Attempt-cap exhaustion has two candidate behaviors: setting a `stalled` state, or
  failing and surfacing the run. Both were kept: on-disk state uses the `stalled`
  vocabulary with reason `attempt-cap`, while the run report surfaces it as a failure.
  This satisfies both readings at once.
- A slice's `oracle` field resolves through the manifest's oracle names (with `{path}`
  substituted); an unknown name is treated as a literal command. This was left
  unconstrained by design and chosen to keep both the fixture and real repos working.
- Requeue keeps attempt counts, so a brief amendment does not erase attempt history;
  the fake backend's attempt numbering advances past the flawed-brief attempt rather
  than resetting.
- The invariant-floor regex keeps its verbatim v0.1 form (no word boundaries, literal
  single spaces, a reduced alternative set) rather than a richer regex considered
  during review, because the v0.1 contract for this pattern was intentional, not an
  oversight.
- The headless backend's exactly-one-fenced-block text parse is the v0.1 rule; the
  known risk is a transcript containing a stray fence, which is documented rather than
  guarded against in v0.1.
- Staircase signals measure the cumulative lease diff, matching how the predecessor
  measured diff-vs-master (also cumulative), so no behavior change was needed here.
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

## CLI

- `jig init` takes `--store <path>` for the project form, while the DoD scenario passes
  explicit `--clone` flags; the flag surface was not fixed by design, so both entry
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

- The fixture envtool is pre-built once per Generate rather than invoked via
  `go run`, because it is a cross-platform Go program and the `go run` form would
  hang on PATH-less shells and recompile on every lifecycle call.
- Status goldens: state (a) ("A green, B blocked") is constructed directly via store
  state snapshots plus `jig status`; states (b) and (c) are natural pauses that occur
  during the end-to-end chain. In all cases the CLI's rendering is what the goldens
  pin.
- The herdr backend computes `/mnt/<drive>` paths mechanically instead of shelling out
  to `wslpath`, after herdr was probed to behave as expected via a WSL login shell
  (herdr 0.8.2); the backend is implemented against that probed JSON surface but is
  not exercised by automated tests.
- Blocked-by relationships on the github adapter use the REST issue-dependencies
  endpoint via `gh api`; sub-issues use the GraphQL `addSubIssue` mutation. Tests
  assert the resulting argv against a stub rather than hitting the network.
- ARCHITECTURE.md's module table is drift-guarded by a lint test that asserts every
  named package directory actually exists.
- CLAUDE.md consists of a single `@AGENTS.md` import line, so the two files share one
  content; AGENTS.md itself stays navigation pointers only.
