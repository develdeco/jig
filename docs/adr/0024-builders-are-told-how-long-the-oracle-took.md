# Builders are told how long the oracle took

Builders ran the full suite with their own short timeouts, or pushed it to the background and waited on it, because nothing told them how long it takes. On jig's own repo the suite takes 10 to 15 minutes on the Windows dev machine. ADR 0018 raised the shell's bound and asked builders to test narrowly, and since ADR 0020 jig runs the slice's oracle itself at green. What a builder still lacks is the fact: how long that oracle actually takes. kun's guidance is timeouts as configuration and better context over bolt-on guards.

The owner chose to measure before enforcing anything: BS-3's Sonnet builder made 59 shell calls without setting its own timeout or backgrounding one, so no guard on shell calls is added here. The duration ships as context. (The guard came later, when a Sonnet builder on another run backgrounded the full suite and lost its attempt: ADR 0018's amendment turns background shell runs off in headless sessions.)

## The rule

- **jig times each oracle run at green.** The `oracle` journal line gains `seconds`, the run's wall time rounded up to whole seconds, so any run records at least 1.
- **The next builder of the same command reads it.** `slice.json` gains `oracle_seconds`: the wall time of jig's latest run of this slice's exact oracle command, with the same env class up, on this ticket, or 0 before the first (`journal.LastOracleSeconds`). The latest run counts whatever its outcome, a red one or one cut off at the 30-minute bound included.
- **A red run's fix turn says how long it took,** so the session knows what running it again costs. A green with uncommitted changes to tracked files gets its own fix turn, which says jig did not run the oracle.
- **The dispatch prompt names the field** in its line about slice.json, so a builder knows what a run of its oracle costs before it starts one.

## Test seams

- `journal.LastOracleSeconds`, through `TestLastOracleSeconds`.
- `frontier.Run` through the fake backend with a timed oracle, through `TestRunTellsTheNextBuilderTheOraclesLastRunTime`: slice a's oracle line records its time, slice a reads 0, and slice b, which shares a's oracle and waits for it, reads a's time.
- The dispatch prompt, through its goldens.
