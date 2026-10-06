# The reviewer runs on the dearest rung, and every session gets an effort

The gate picked its reviewer's model with `staircase.Disjoint`: the first rung no builder of the ticket used. With Haiku builders that gave BS-1 a Sonnet reviewer on round 1 and Opus on rounds 2 and 3, weaker on the full review and stronger on the small re-checks. Since ADR 0019 the default staircase has two rungs, so `Disjoint` gave an invariant-floored Opus build a Sonnet reviewer.

jig also passed no reasoning effort to `claude`, so every session ran at the CLI's default whatever its job. Most of a builder's time is the model thinking, not tools: BS-3's builder spent 863 s generating and 28 s in tools in its first 15 minutes, with 112k output tokens, mostly thinking, before its first test run. Claude Code takes `--effort low|medium|high|xhigh|max`.

## The rule

- **The reviewer runs on the staircase's dearest rung on every round** (`staircase.Dearest`). Its independence comes from a fresh, read-only session that never saw the build, not from a model the builders did not use. This supersedes the reviewer-model paragraph of ADR 0019.
- **The reviewer's effort follows the round's scope.** A `full` round (the first review, or one whose earlier reviewed head is not an ancestor) and a `delta` round (re-checking fixes since the last reviewed head) each have a level.
- **A builder's effort follows its attempt.** The first attempt of a slice and a retry each have a level. An attempt is a retry when `journal.FailedAttempts` counts a failed attempt of the slice, the same count that climbs the staircase, so a question, a flawed brief or a blocked environment raises neither. A fix turn that resumes the session after a red oracle run (ADR 0020) keeps its attempt's effort.
- **All four levels are project configuration** (kun: model choice is the user's):

      gate:
        review_effort:
          full: high
          delta: medium
      builder_effort:
        first: medium
        retry: high

  The values shown are the defaults. An empty value passes no effort, so the CLI's default applies, and a value `--effort` does not accept is refused when `project.yaml` loads, naming the key.
- **Every choice is in the journal.** A builder's `dispatch` line carries its effort beside its model. `gate-open` carries the reviewer's model, and the reviewer round's own line (`gate-round` or `gate-clean`) carries its effort, since the scope is known only once the round has started.

`session.Dispatch` carries the effort, and the headless backend passes it as `--effort`. Backends that do not run the `claude` CLI ignore it, as they ignore the model.

## Test seams

- `project.yaml` parsing, defaults and validation, through `TestLoadEffortConfig`.
- The headless backend's argv with and without an effort and on a resumed turn, through `TestHeadlessArgsEffort`.
- `frontier.Run` through the fake backend, through `TestRunClimbsARungPerFailedAttemptButNotForAQuestion`: the first attempt and a resumed one at the first effort, each retry at the retry effort, on the session dispatch and the journal line.
- A gate round as `Gate` runs it, through `TestGateDispatchesTheReviewerOnTheDearestRungWithEffortByScope`: builders on the dearest rung still get a reviewer on it, with the full effort on round 1 and the delta effort on round 2, each journaled on the round's line.
