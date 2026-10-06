# jig runs the slice's oracle at green, and a red run goes back to the same session

A builder reported green and jig checked only that the commit existed and descended from the start sha (`verifyGreen`). Nothing ran the oracle until the gate. So builders ran it themselves, often several times. BS-1's builders ran a whole package 4 to 5 times per slice, and `go test` took 143 of the build's 200 minutes. A prompt line asking for one run at the end (ADR 0018) was not followed. Meanwhile a green that was not green reached the gate, and came back as a fix round.

Matt Pocock's `implement` runs the full suite once, at the end. no-mistakes treats executed tests as the evidence, not the agent's confidence, and lets a step iterate on its own findings inside a bounded budget before the run moves on.

## The rule

- **jig runs the slice's oracle when a builder reports green.** The run happens once the green verifies, in the lease, with the slice's env class still up, and within the 30-minute bound a session's shell gives one command (ADR 0018). Past it, jig kills the run's whole process tree and counts it red. jig journals each run as an `oracle` line (pass or fail) with the exact command and the env class that was up, and the lease's HEAD when its tree was clean before the run, so later evidence reuse knows exactly which run on which commit it covers (ADR 0021).
- **Uncommitted edits to tracked files go back first.** When the tracked files are not the reported commit, an oracle run could not vouch for that commit. So jig hands the session the uncommitted changes as a fix turn, without running the oracle.
- **A red run goes back to the builder's own session.** The session gets the oracle command and the end of its output, as its next turn (`session.Resumer`, `claude -p --resume <id>`), up to `maxOracleFixes` (2) times. The session fixes the cause, commits, and writes `result.json` again, and jig runs the oracle again.
- **After the budget, the attempt fails.** jig writes a code-bug result carrying the oracle's output over `result.json`, so the next attempt's log shows it. The same happens at once with a backend that cannot resume a session (fake, herdr). The failure climbs the staircase (ADR 0019).
- **The build prompt says what happens to a red run.** Builders run only the tests that cover their change. Where the backend can resume a session, they are told jig hands them the oracle's output if it fails. Elsewhere they are told a red run fails the attempt, so they run the oracle once themselves.
- **Changelogs list the commits that verified.** A builder can claim green more than once in an attempt, so the changelogs list each slice by its `verified` commit, once. A journal that predates `verified` lines still renders from its green results.

A fix turn keeps the builder's context warm: a small miss costs one more turn, not a cold session on a dearer model. The budget is the step's own bounded loop, so a builder that cannot get to green still stops.

## Cost

Every green slice now costs one oracle run by jig, where builders used to spend several. In jig's own suite, the frontier's tests take the oracle through `frontier.Deps.Oracle`. Tests that are not about the oracle hand in one that passes, which is what they had before this change. The oracle tests use the real one. Measured alone on the Windows dev machine, the frontier package went from 226 s to 357 s with the real oracle everywhere, and to 265 s with the stub (about 25 s of that is the new oracle tests). The CLI's end-to-end tests run the real binary, so each green slice there runs the fixture's oracle once more. The gate reusing a pass recorded for the same tree, instead of rerunning its oracles, is the follow-up that pays that back.

## Test seams

- **The prompts,** through `TestRenderDispatchPromptMatchesGolden` (both oracle lines) and `TestRenderOracleFixPromptMatchesGolden`.
- **The bounded run,** through `TestShellOutputReturnsOutputAndHonorsItsLimit`: output returned, and a command past its limit stopped, its child included.
- **The changelogs,** through `TestRenderChangelogListsOnlyVerifiedCommits`.
- **The headless backend's resume,** through `TestHeadlessRunResumableReportsTheSessionID` and `TestHeadlessResumeContinuesTheSession`, against the stub CLI.
- **The frontier's dispatch,** through `TestRunHandsARedOracleBackToTheSameSession`, over the fixture's real oracle:
  - a red run fixed in the same session ends green on the first attempt;
  - a session that never fixes it fails the attempt after two fix turns, with the output in its result;
  - uncommitted edits to tracked files go back to the session before any oracle run;
  - a backend that cannot resume fails the attempt at once.
