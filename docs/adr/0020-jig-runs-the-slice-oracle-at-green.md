# jig runs the slice's oracle at green, and a red run goes back to the same session

A builder reported green and jig checked only that the commit existed and descended from the start sha (`verifyGreen`). Nothing ran the oracle until the gate. So builders ran it themselves, often several times. BS-1's builders ran a whole package 4 to 5 times per slice, and `go test` took 143 of the build's 200 minutes. A prompt line asking for one run at the end (ADR 0018) was not followed. Meanwhile a green that was not green reached the gate, and came back as a fix round.

Matt Pocock's `implement` runs the full suite once, at the end. no-mistakes treats executed tests as the evidence, not the agent's confidence, and lets a step iterate on its own findings inside a bounded budget before the run moves on.

## The rule

- **jig runs the slice's oracle when a builder reports green.** The run happens once the green verifies, in the lease, with the slice's env class still up. jig journals it as an `oracle` line (pass or fail), with the lease's HEAD when its tree is clean, so later evidence reuse knows exactly which commit it covers.
- **A red run goes back to the builder's own session.** The session gets the oracle command and the end of its output, as its next turn (`session.Resumer`, `claude -p --resume <id>`), up to `maxOracleFixes` (2) times. The session fixes the cause, commits, and writes `result.json` again, and jig runs the oracle again.
- **After the budget, the attempt fails.** jig writes a code-bug result carrying the oracle's output over `result.json`, so the next attempt's log shows it. The same happens at once with a backend that cannot resume a session (fake, herdr). The failure climbs the staircase (ADR 0019).
- **The build prompt changes.** Builders run only the tests that cover their change, and are told that jig runs the oracle when they report green and hands them its output if it fails.

A fix turn keeps the builder's context warm: a small miss costs one more turn, not a cold session on a dearer model. The budget is the step's own bounded loop, so a builder that cannot get to green still stops.

## Cost

Every green slice now costs one oracle run by jig, where builders used to spend several. jig's own tests drive the fake backend over the fixture's real oracle, so each green slice there runs `go test` once more. The gate reusing a pass recorded for the same tree, instead of rerunning its oracles, is the follow-up that pays that back.

## Test seams

- **The two prompts,** through `TestRenderDispatchPromptMatchesGolden` and `TestRenderOracleFixPromptMatchesGolden`.
- **The headless backend's resume,** through `TestHeadlessRunResumableReportsTheSessionID` and `TestHeadlessResumeContinuesTheSession`, against the stub CLI.
- **The frontier's dispatch,** through `TestRunHandsARedOracleBackToTheSameSession`, over the fixture's real oracle:
  - a red run fixed in the same session ends green on the first attempt;
  - a session that never fixes it fails the attempt after two fix turns, with the output in its result;
  - a backend that cannot resume fails the attempt at once.
