# DECISIONS — jig v0.1 build log

One line per judgment call: what was ambiguous, what was chosen, why. Precedence per the
build authority order: spec semantics; build-decision deltas win on v0.1 scope and packaging.

## Phase A (understand)
- Sources digested once by parallel readers (first run); this run consumed digests only. Token note: phase A re-run cost 0 subagents (digests preserved); main loop read spec.md + 10 digests.
- One reader (kun-concepts) died on a usage limit in the first run; its digest was completed before this run resumed — no gap.

## Standing deviations (from the build-decision delta log, logged once)
- Boards deferred: structural rounds render as markdown tables in v0.1; `board` package is an interface stub. (Delta log §4.8 overrides spec §04.)
- jira/linear trackers are compile-checked stubs returning a structured not-implemented error. (§4.2/§5.)
- gate pr-mode parses and returns not-implemented. (§4.14/§5.)
- Design axis/oracles: facet captured in brief/slices data, not enforced. (§5.)
- fleet/retro binary verbs deferred; their skills drive shipped verbs. (§5.)
- Cross-repo fixtures/publish/wire-contract E2E deferred; scheduler unit test covers concurrency policy. (§5.)
- graphify: interface + CLI shell-out + no-op fallback only; no test requires it. (§4.7/§5.)
- Windows is the v0.1 platform; WSL-native substrate + nix packaging deferred. Env-class up/check/down are the one sanctioned shell-string exception (cmd /C | sh -c). (§4.9.)

## Phase A/B judgment calls
- Result-text parsing requires EXACTLY ONE fenced json block (delta log §3 row) — deviates from the predecessor's last-block-wins; zero or multiple blocks parse as failed. Port-fidelity note recorded in outcome tests.
- Secret-read screen uses the delta-log §3 pattern list (.env*, *_key*, id_rsa*, *.pem, ~/.aws/**, ~/.config/gh/**), replacing the predecessor's three client-specific patterns; predecessor test cases for dropped patterns not ported.
- Stall signature uses the delta-log §4.13 normalization (digits and path segments stripped) — the predecessor signature had no digit/path stripping; kickoff contract wins.
- Staircase invariant regex applied case-insensitively (predecessor was IGNORECASE; delta log silent).
- Attempt-cap exhaustion: spec §05 says the cap sets `stalled`; delta log §4.12 says exhaustion → `failed` + surfaced. Resolution: on-disk state uses the spec vocabulary (stalled, reason attempt-cap); the run REPORTS it as a surfaced failure. Both authorities satisfied.
- Slice `oracle` field resolves through the manifest oracle names ({path} substituted); an unknown name is treated as a literal command. Spec silent on resolution; keeps fixture + real repos working.
- from_brief hashes: sha256 of section BODY (heading line excluded), \n-normalized, trailing-whitespace-trimmed. "##-delimited section bodies" read literally.
- Gate writes a machine report (gate/round-N/report.yaml with verdict + target sha + model) — spec's publish step 2 requires "the gate report records the target sha it ran against"; a dedicated file makes that concrete.
- Fix slices append to the ticket's slices.yaml with a from_gate: N marker; spec says findings become "new slices on the frontier" and slices.yaml is "the map" — one map, one file.
- Reconcile conflicts in v0.1 abort publish with a structured error pointing at `jig gate` (the conflict-resolution fix-slice loop is deferred); tier asserted by the DoD is the oracles-only tier.
- Publish re-validate "affected oracles" = all manifest oracles in v0.1 (graphify scoping deferred).
- Standalone store default ticket_format "T-{n}"; tracker local. Spec names no default.
- `jig init` takes --store <path> for the project form; the DoD passes explicit --clone flags. Spec names no flag surface.
- `jig solve` pauses by exiting 2 at needs-input; a second `jig solve --yes --answer <qid> "<text>"` resumes the chain. "One process" = the chain is one process per invocation, not five.
- Journal event vocabulary fixed in the contract map (dispatch/result/question/answer/requeue/stall/env-*/gate-*/fix-slice/reconcile/revalidate/memorize/changelog/squash/pr/route/publish-done); graduated-revalidate reason encoded in the outcome field to keep the §4.13 line schema exact.
- Store commits happen even without a remote (history is useful); only push/pull are skipped silently. "Skips sync silently" read as network sync.
- Model ids verified against the current API reference: haiku=claude-haiku-4-5, sonnet=claude-sonnet-5, opus=claude-opus-5 (defaults; rungs are project config).
- herdr probed OK via WSL login shell (herdr 0.8.2); backend implemented against the probed JSON surface; not exercised by tests.
- Blocked-by on the github adapter uses the REST issue-dependencies endpoint via `gh api`; sub-issues via GraphQL addSubIssue (gh-axi pattern). Tests assert argv against a stub.

## Phase B (foundation)
- Token note: phase B = 4 sonnet agents (~459k subagent tokens); packages store, journal, axi, gitx, project, manifest, screen, outcome, staircase all green.
- Secret glob rule needed no glob engine: literal substring/prefix checks already catch `.env*`, `*.pem`, `*_key*` tokens (screen agent note, verified by test cases).
- Squash base: computed as merge-base(origin/<target>, HEAD) in the publish lease at squash time. Equal to the recorded start sha while the target is unmoved; after a reconcile rebase the fork point HAS moved and merge-base is exactly the moved start ("squash start..HEAD" read as the current fork point; stacked bases survive). Pushed-range refusal runs on that same range.
- Requeue keeps attempt counts (a brief amendment doesn't erase attempt history; the fake backend's attempt numbering advances past the flawed-brief attempt). Kickoff says only "flips to queued".
- Fixture envtool is pre-built once per Generate (still a cross-platform Go program; the literal `go run` form would hang on PATH-less shells and re-compile per lifecycle call).
- Status goldens: state (a) ("A green, B blocked") is constructed via store state snapshots + `jig status`; states (b) and (c) are natural pauses in the e2e chain. The CLI rendering is what the goldens pin.

## Phase C (runtime + fixture)
- Token note: phase C = 4 sonnet agents (~527k subagent tokens); session, pool, envrun, graphify, board, tracker, fixture + full scenario tree green.
- graphify Plane.Affected derives --graph <repo>/graphify-out/graph.json --depth 2 itself (contract signature carries no graph/depth params; digest defaults).
- herdr backend computes /mnt/<drive> paths mechanically instead of shelling wslpath.
