# Builders test narrowly, and the shell waits for the oracle

A build session was told its oracle and nothing about how to use it. In one measured ticket, its builders made 66 `go test` calls, most of them over whole packages, and about 107 of 271 working minutes went to builders waiting on tests. 13 of those calls hit Claude Code's 2-minute default shell timeout. The CLI moved each one to the background, and the builder then reran it or polled it with a loop. jig's gate runs every oracle again after the build, so the builder's own runs are a check, not the proof.

## The dispatch prompt

`dispatchPromptTemplate` (`internal/frontier/dispatch.go`) gains one line after the oracle:

> While you work, run only the tests that cover your change. Run the oracle after your last change, before you report green.

It says how the oracle is used: the narrowest tests drive the work, and the oracle is the final check. A final run that comes back red means another change, and the oracle runs again after it. It names no language or test runner, since jig builds any repo.

## The shell's command timeout

Every headless session's `--settings` sets `BASH_DEFAULT_TIMEOUT_MS` and `BASH_MAX_TIMEOUT_MS` to 30 minutes (`shellCommandTimeout`, `internal/session/headless.go`). The CLI's own defaults are 2 and 10 minutes. It applies the `--settings` env over the operator's user settings.

- **Why one value for both.** An unattended session cannot do anything useful with a command the CLI has backgrounded, except poll it. The default has to fit the oracle, so that a call that names no timeout finishes in the foreground. The max is the same value, so the session has one number to know.
- **Why 30 minutes.** jig's own slowest package takes about 9 minutes on a Windows dev machine, and jig's runs already bound a `go test` binary at 30 minutes (`-timeout=30m`). A command that hangs costs at most that, inside a session bounded by `JIG_HEADLESS_TIMEOUT`; since the amendment below it is then ended, not moved to the background.
- **What it does not cover.** herdr sessions are interactive agents that jig does not configure, so they keep the CLI's defaults. A repo whose oracle runs longer than 30 minutes needs its builders to run narrower tests: the shell ends such a call (amendment below), and jig runs the oracle itself at green.

## Amendment: no background shell runs (2026-10-06)

A headless session (`claude -p`) ends with its turn, so a command it runs in the background is never collected. On the store-layout run's T-23, a Sonnet builder ran the full suite with the shell's background option and ended its turn "waiting for the notification": nothing was committed and the attempt was lost (T-34). The owner had chosen to measure before guarding shell calls (ADR 0024); this is the recurrence that decision waited for.

Every headless session's `--settings` env now also sets `CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, which removes the background option from the session's shell and turns off moving a long command to the background. A command runs in the foreground within the 30-minute bound above, or is ended. jig already runs a slice's oracle itself at green (ADR 0020), so a builder never needs the full suite in its own shell. One consequence: a session can no longer keep a process running across shell calls, such as a dev server for a demo or an end-to-end check; it starts and stops it within one command.

## Test seams

- **The rendered build prompt,** through `TestRenderDispatchPromptMatchesGolden`.
- **The session settings jig hands the CLI,** through `TestHeadlessSettings`.
- **The real CLI's shell,** through `TestHeadlessLiveCLI` (`JIG_LIVE_CLAUDE=1`), which reads both values back from inside a session.
